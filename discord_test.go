package discordgo

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ////////////////////////////////////////////////////////////////////////////
// //////////////////////////////////////////////////// VARS NEEDED FOR TESTING
var (
	dg    *Session // Stores a global discordgo user session
	dgBot *Session // Stores a global discordgo bot session

	envOAuth2Token  = os.Getenv("DG_OAUTH2_TOKEN")  // Token to use when authenticating using OAuth2 token
	envBotToken     = os.Getenv("DGB_TOKEN")        // Token to use when authenticating the bot account
	envGuild        = os.Getenv("DG_GUILD")         // Guild ID to use for tests
	envChannel      = os.Getenv("DG_CHANNEL")       // Channel ID to use for tests
	envVoiceChannel = os.Getenv("DG_VOICE_CHANNEL") // Channel ID to use for tests
	envAdmin        = os.Getenv("DG_ADMIN")         // User ID of admin user to use for tests
)

func TestMain(m *testing.M) {
	fmt.Println("Init is being called.")
	if envBotToken != "" {
		if d, err := New(envBotToken); err == nil {
			dgBot = d
		}
	}

	if envOAuth2Token == "" {
		envOAuth2Token = os.Getenv("DGU_TOKEN")
	}

	if envOAuth2Token != "" {
		if d, err := New(envOAuth2Token); err == nil {
			dg = d
		}
	}

	os.Exit(m.Run())
}

//////////////////////////////////////////////////////////////////////////////
/////////////////////////////////////////////////////////////// START OF TESTS

// TestNewToken tests the New() function with a Token.
func TestNewToken(t *testing.T) {

	if envOAuth2Token == "" {
		t.Skip("Skipping New(token), DGU_TOKEN not set")
	}

	d, err := New(envOAuth2Token)
	if err != nil {
		t.Fatalf("New(envToken) returned error: %+v", err)
	}

	if d == nil {
		t.Fatal("New(envToken), d is nil, should be Session{}")
	}

	if d.Token == "" {
		t.Fatal("New(envToken), d.Token is empty, should be a valid Token.")
	}
}

func TestNewIncludesPollIntents(t *testing.T) {
	d, err := New("Bot token")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	want := IntentGuildMessagePolls | IntentDirectMessagePolls
	if got := d.Identify.Intents & want; got != want {
		t.Fatalf("default intents include poll intents = %d, want %d", got, want)
	}
}

// resetDefaultRESTTransport makes the next New build its shared REST
// transport again, so a test can control http.DefaultTransport and the ping
// timers first. Everything is restored when the test ends.
func resetDefaultRESTTransport(t *testing.T) {
	t.Helper()
	savedDefault := http.DefaultTransport
	savedTransport, savedBase := restTransport, restTransportBase
	savedSendPing, savedPing := restHTTP2SendPingTimeout, restHTTP2PingTimeout
	restTransportOnce = sync.Once{}
	restTransport, restTransportBase = nil, nil
	t.Cleanup(func() {
		http.DefaultTransport = savedDefault
		restHTTP2SendPingTimeout, restHTTP2PingTimeout = savedSendPing, savedPing
		restTransportOnce = sync.Once{}
		restTransport, restTransportBase = savedTransport, savedBase
		if savedTransport != nil {
			restTransportOnce.Do(func() {})
		}
	})
}

// The default client sends all REST traffic for a host over one shared HTTP/2
// connection. If that connection stops answering without closing, requests
// must move to a new connection instead of each timing out on the old one.
func TestNewClientReplacesUnresponsiveHTTP2Connection(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.Proto))
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	// The proxy silently drops traffic on the first connection once stalled,
	// like a stuck middlebox. Later connections pass through.
	proxy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })
	var stalled atomic.Bool
	var dials atomic.Int32
	var mu sync.Mutex
	var conns []net.Conn
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	go func() {
		for {
			client, err := proxy.Accept()
			if err != nil {
				return
			}
			first := dials.Add(1) == 1
			upstream, err := net.Dial("tcp", server.Listener.Addr().String())
			if err != nil {
				_ = client.Close()
				return
			}
			mu.Lock()
			conns = append(conns, client, upstream)
			mu.Unlock()
			pipe := func(dst, src net.Conn) {
				buf := make([]byte, 32<<10)
				for {
					n, err := src.Read(buf)
					if err != nil {
						return
					}
					if first && stalled.Load() {
						continue
					}
					if _, err := dst.Write(buf[:n]); err != nil {
						return
					}
				}
			}
			go pipe(upstream, client)
			go pipe(client, upstream)
		}
	}()

	// New builds its transport from http.DefaultTransport, so trust the test
	// server there and shorten the ping timers before calling it.
	resetDefaultRESTTransport(t)
	restHTTP2SendPingTimeout = 100 * time.Millisecond
	restHTTP2PingTimeout = 100 * time.Millisecond
	trusting := http.DefaultTransport.(*http.Transport).Clone()
	trusting.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	t.Cleanup(trusting.CloseIdleConnections)
	http.DefaultTransport = trusting

	session, err := New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	session.Client.Timeout = 500 * time.Millisecond
	t.Cleanup(session.Client.CloseIdleConnections)
	url := "https://" + proxy.Addr().String() + "/"
	get := func() error {
		body, err := session.RequestWithBucketID(http.MethodGet, url, nil, url)
		if err != nil {
			return err
		}
		if string(body) != "HTTP/2.0" {
			t.Fatalf("protocol = %q, want HTTP/2.0", body)
		}
		return nil
	}

	if err := get(); err != nil {
		t.Fatal(err)
	}
	stalled.Store(true)

	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if lastErr = get(); lastErr == nil {
			return
		}
	}
	t.Fatalf("REST requests still failing after the connection stalled for 5s (dials=%d): %v", dials.Load(), lastErr)
}

type countingTransport struct {
	requests atomic.Int32
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.requests.Add(1)
	return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}")), Request: r}, nil
}

// Sessions share one ping-enabled transport so connections stay pooled. HTTP
// mocks and tracing wrappers installed in http.DefaultTransport before New
// must still see the requests of sessions created after the swap.
func TestNewClientFollowsReplacedDefaultTransport(t *testing.T) {
	resetDefaultRESTTransport(t)
	original := http.DefaultTransport

	first, err := New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	second, err := New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	shared, ok := first.Client.Transport.(*http.Transport)
	if !ok || shared == original || second.Client.Transport != first.Client.Transport {
		t.Fatalf("transports = %T %p and %T %p, want one shared clone of http.DefaultTransport", first.Client.Transport, first.Client.Transport, second.Client.Transport, second.Client.Transport)
	}
	if shared.HTTP2 == nil || shared.HTTP2.SendPingTimeout == 0 || shared.HTTP2.PingTimeout == 0 {
		t.Fatalf("shared transport HTTP2 = %+v, want ping timeouts set", shared.HTTP2)
	}

	mock := &countingTransport{}
	http.DefaultTransport = mock
	third, err := New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	if third.Client.Transport != nil {
		t.Fatalf("Client.Transport after DefaultTransport was replaced = %T, want nil", third.Client.Transport)
	}
	if _, err := third.RequestWithBucketID(http.MethodGet, EndpointGateway, nil, EndpointGateway); err != nil {
		t.Fatal(err)
	}
	if got := mock.requests.Load(); got != 1 {
		t.Fatalf("replaced DefaultTransport got %d requests, want 1", got)
	}

	http.DefaultTransport = original
	fourth, err := New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	if fourth.Client.Transport != first.Client.Transport {
		t.Fatal("New did not return to the shared pinging transport after DefaultTransport was restored")
	}
}

// If something replaced http.DefaultTransport before the first New, there is
// no *http.Transport to clone, so the client keeps using DefaultTransport.
func TestNewClientWithCustomDefaultTransportUsesIt(t *testing.T) {
	resetDefaultRESTTransport(t)
	mock := &countingTransport{}
	http.DefaultTransport = mock

	session, err := New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	if session.Client.Transport != nil {
		t.Fatalf("Client.Transport = %T, want nil", session.Client.Transport)
	}
	if _, err := session.RequestWithBucketID(http.MethodGet, EndpointGateway, nil, EndpointGateway); err != nil {
		t.Fatal(err)
	}
	if got := mock.requests.Load(); got != 1 {
		t.Fatalf("DefaultTransport got %d requests, want 1", got)
	}
}

// HTTP/2 settings an application put on http.DefaultTransport carry over, and
// http.DefaultTransport itself is not modified.
func TestNewClientKeepsDefaultTransportHTTP2Settings(t *testing.T) {
	resetDefaultRESTTransport(t)
	custom := http.DefaultTransport.(*http.Transport).Clone()
	custom.HTTP2 = &http.HTTP2Config{SendPingTimeout: 3 * time.Second, MaxReadFrameSize: 1 << 20}
	http.DefaultTransport = custom

	session, err := New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	got := session.Client.Transport.(*http.Transport).HTTP2
	if got.SendPingTimeout != 3*time.Second || got.PingTimeout != restHTTP2PingTimeout || got.MaxReadFrameSize != 1<<20 {
		t.Fatalf("HTTP2 = %+v, want SendPingTimeout 3s, PingTimeout %v, MaxReadFrameSize 1MiB", *got, restHTTP2PingTimeout)
	}
	if custom.HTTP2.PingTimeout != 0 {
		t.Fatalf("http.DefaultTransport HTTP2 was modified: %+v", *custom.HTTP2)
	}
}

// BenchmarkNewClientRequest measures a REST request through the client New
// sets up, against a loopback HTTP/1.1 server.
func BenchmarkNewClientRequest(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{}"))
	}))
	b.Cleanup(server.Close)
	session, err := New("Bot token")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(session.Client.CloseIdleConnections)
	url := server.URL + "/api/v10/gateway"
	b.ReportAllocs()
	for b.Loop() {
		if _, err := session.RequestWithBucketID(http.MethodGet, url, nil, url); err != nil {
			b.Fatal(err)
		}
	}
}

func TestCurrentAPIVersion(t *testing.T) {
	if APIVersion != "10" {
		t.Fatalf("APIVersion = %q, want %q", APIVersion, "10")
	}
	if EndpointAPI != "https://discord.com/api/v10/" {
		t.Fatalf("EndpointAPI = %q, want current API base URL", EndpointAPI)
	}
}

func TestOpenClose(t *testing.T) {
	if envOAuth2Token == "" {
		t.Skip("Skipping TestClose, DGU_TOKEN not set")
	}

	d, err := New(envOAuth2Token)
	if err != nil {
		t.Fatalf("TestClose, New(envToken) returned error: %+v", err)
	}

	if err = d.Open(); err != nil {
		t.Fatalf("TestClose, d.Open failed: %+v", err)
	}

	// We need a better way to know the session is ready for use,
	// this is totally gross.
	start := time.Now()
	for {
		d.RLock()
		if d.DataReady {
			d.RUnlock()
			break
		}
		d.RUnlock()

		if time.Since(start) > 10*time.Second {
			t.Fatal("DataReady never became true.yy")
		}
		runtime.Gosched()
	}

	// TODO find a better way
	// Add a small sleep here to make sure heartbeat and other events
	// have enough time to get fired.  Need a way to actually check
	// those events.
	time.Sleep(2 * time.Second)

	// UpdateStatus - maybe we move this into wsapi_test.go but the websocket
	// created here is needed.  This helps tests that the websocket was setup
	// and it is working.
	if err = d.UpdateGameStatus(0, time.Now().String()); err != nil {
		t.Errorf("UpdateStatus error: %+v", err)
	}

	if err = d.Close(); err != nil {
		t.Fatalf("TestClose, d.Close failed: %+v", err)
	}
}

func TestAddHandler(t *testing.T) {

	testHandlerCalled := int32(0)
	testHandler := func(s *Session, m *MessageCreate) {
		atomic.AddInt32(&testHandlerCalled, 1)
	}

	interfaceHandlerCalled := int32(0)
	interfaceHandler := func(s *Session, i interface{}) {
		atomic.AddInt32(&interfaceHandlerCalled, 1)
	}

	bogusHandlerCalled := int32(0)
	bogusHandler := func(s *Session, se *Session) {
		atomic.AddInt32(&bogusHandlerCalled, 1)
	}

	d := Session{}
	d.AddHandler(testHandler)
	d.AddHandler(testHandler)

	d.AddHandler(interfaceHandler)
	d.AddHandler(bogusHandler)

	d.handleEvent(messageCreateEventType, &MessageCreate{})
	d.handleEvent(messageDeleteEventType, &MessageDelete{})

	<-time.After(500 * time.Millisecond)

	// testHandler will be called twice because it was added twice.
	if atomic.LoadInt32(&testHandlerCalled) != 2 {
		t.Fatalf("testHandler was not called twice.")
	}

	// interfaceHandler will be called twice, once for each event.
	if atomic.LoadInt32(&interfaceHandlerCalled) != 2 {
		t.Fatalf("interfaceHandler was not called twice.")
	}

	if atomic.LoadInt32(&bogusHandlerCalled) != 0 {
		t.Fatalf("bogusHandler was called.")
	}
}

func TestRemoveHandler(t *testing.T) {

	testHandlerCalled := int32(0)
	testHandler := func(s *Session, m *MessageCreate) {
		atomic.AddInt32(&testHandlerCalled, 1)
	}

	d := Session{}
	r := d.AddHandler(testHandler)

	d.handleEvent(messageCreateEventType, &MessageCreate{})

	r()

	d.handleEvent(messageCreateEventType, &MessageCreate{})

	<-time.After(500 * time.Millisecond)

	// testHandler will be called once, as it was removed in between calls.
	if atomic.LoadInt32(&testHandlerCalled) != 1 {
		t.Fatalf("testHandler was not called once.")
	}
}

func TestRemoveHandlerReleasesReferences(t *testing.T) {
	testHandler := func(*Session, *MessageCreate) {}
	d := Session{}

	removeFirst := d.AddHandler(testHandler)
	removeMiddle := d.AddHandler(testHandler)
	removeLast := d.AddHandler(testHandler)
	removeMiddle()

	handlers := d.handlers[messageCreateEventType]
	for i, handler := range handlers[len(handlers):cap(handlers)] {
		if handler != nil {
			t.Fatalf("handlers backing array entry %d still retains a removed handler", len(handlers)+i)
		}
	}

	removeFirst()
	removeLast()
	if _, ok := d.handlers[messageCreateEventType]; ok {
		t.Fatal("handlers map still retains the empty handler slice")
	}

	removeOnce := d.AddHandlerOnce(testHandler)
	removeOnce()
	if _, ok := d.onceHandlers[messageCreateEventType]; ok {
		t.Fatal("onceHandlers map still retains the empty handler slice")
	}
}

func TestAddHandlerOnceConcurrentDispatch(t *testing.T) {
	handlerCalled := int32(0)
	handler := func(s *Session, event *RateLimit) {
		atomic.AddInt32(&handlerCalled, 1)
	}

	d := Session{SyncEvents: true}
	d.AddHandlerOnce(handler)

	start := make(chan struct{})
	done := make(chan struct{})
	for i := 0; i < 32; i++ {
		go func() {
			<-start
			d.handleEvent(rateLimitEventType, &RateLimit{})
			done <- struct{}{}
		}()
	}
	close(start)

	for i := 0; i < 32; i++ {
		<-done
	}

	if atomic.LoadInt32(&handlerCalled) != 1 {
		t.Fatalf("handlerCalled = %d, want 1", handlerCalled)
	}
}

// Dispatch iterates the published handler slice without copying it, so a
// handler that removes itself or another handler mid-dispatch must not shift
// the entries the dispatch is still walking.
func TestRemoveHandlerDuringDispatch(t *testing.T) {
	var first, second, third int32
	d := Session{SyncEvents: true}
	var removeFirst, removeSecond func()
	removeFirst = d.AddHandler(func(*Session, *MessageCreate) {
		atomic.AddInt32(&first, 1)
		removeFirst()
	})
	removeSecond = d.AddHandler(func(*Session, *MessageCreate) {
		atomic.AddInt32(&second, 1)
	})
	d.AddHandler(func(*Session, *MessageCreate) {
		atomic.AddInt32(&third, 1)
		removeSecond()
	})

	d.handleEvent(messageCreateEventType, &MessageCreate{})
	if first != 1 || second != 1 || third != 1 {
		t.Fatalf("first dispatch calls = %d, %d, %d, want 1, 1, 1", first, second, third)
	}
	d.handleEvent(messageCreateEventType, &MessageCreate{})
	if first != 1 || second != 1 || third != 2 {
		t.Fatalf("calls after removals = %d, %d, %d, want 1, 1, 2", first, second, third)
	}
}

// Handlers added and removed while other goroutines dispatch must not disturb
// a handler that stays registered, and each once handler runs at most once.
func TestHandlersConcurrentDispatchAddRemove(t *testing.T) {
	const (
		dispatchers = 8
		dispatches  = 500
		mutators    = 4
	)
	var stable, once, repeated atomic.Int32
	d := Session{SyncEvents: true}
	d.AddHandler(func(*Session, *MessageCreate) {})
	d.AddHandler(func(*Session, *MessageCreate) { stable.Add(1) })

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var addedOnce atomic.Int32
	for range mutators {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				remove := d.AddHandler(func(*Session, *MessageCreate) {})
				var calls atomic.Int32
				removeOnce := d.AddHandlerOnce(func(*Session, *MessageCreate) {
					once.Add(1)
					if calls.Add(1) > 1 {
						repeated.Add(1)
					}
				})
				addedOnce.Add(1)
				remove()
				if addedOnce.Load()%2 == 0 {
					removeOnce()
				}
			}
		}()
	}
	var dispatchWG sync.WaitGroup
	for range dispatchers {
		dispatchWG.Add(1)
		go func() {
			defer dispatchWG.Done()
			for range dispatches {
				d.handleEvent(messageCreateEventType, &MessageCreate{})
			}
		}()
	}
	dispatchWG.Wait()
	close(stop)
	wg.Wait()

	if got := stable.Load(); got != dispatchers*dispatches {
		t.Fatalf("stable handler calls = %d, want %d", got, dispatchers*dispatches)
	}
	if got := repeated.Load(); got != 0 {
		t.Fatalf("once handlers called more than once %d times", got)
	}
	if got, added := once.Load(), addedOnce.Load(); got > added {
		t.Fatalf("once handler calls = %d, more than the %d once handlers added", got, added)
	}
}

// BenchmarkDispatchToHandlers runs the handler lookups that one gateway
// dispatch performs: interface{} handlers, typed handlers and the legacy
// *Event handlers.
func BenchmarkDispatchToHandlers(b *testing.B) {
	d := Session{SyncEvents: true}
	d.AddHandler(func(*Session, interface{}) {})
	d.AddHandler(func(*Session, *MessageCreate) {})
	d.AddHandler(func(*Session, *MessageCreate) {})
	d.AddHandler(func(*Session, *Event) {})
	m := &MessageCreate{Message: &Message{}}
	e := &Event{Type: messageCreateEventType, Struct: m}

	b.ReportAllocs()
	for b.Loop() {
		d.handle(interfaceEventType, m)
		d.handle(messageCreateEventType, m)
		d.handle(interfaceEventType, e)
		d.handle(eventEventType, e)
	}
}

func TestSyncHandlerPanicDoesNotCrashDispatch(t *testing.T) {
	handlerCalled := int32(0)
	panicHandler := func(s *Session, event *MessageCreate) {
		panic("handler failed")
	}
	nextHandler := func(s *Session, event *MessageCreate) {
		atomic.AddInt32(&handlerCalled, 1)
	}

	d := Session{SyncEvents: true}
	d.AddHandler(panicHandler)
	d.AddHandler(nextHandler)

	d.handleEvent(messageCreateEventType, &MessageCreate{})

	if atomic.LoadInt32(&handlerCalled) != 1 {
		t.Fatalf("handlerCalled = %d, want 1", handlerCalled)
	}
}

func TestAsyncHandlerPanicDoesNotCrashDispatch(t *testing.T) {
	handlerCalled := int32(0)
	panicDone := make(chan struct{})
	nextDone := make(chan struct{})

	panicHandler := func(s *Session, event *MessageCreate) {
		defer close(panicDone)
		panic("handler failed")
	}
	nextHandler := func(s *Session, event *MessageCreate) {
		atomic.AddInt32(&handlerCalled, 1)
		close(nextDone)
	}

	d := Session{}
	d.AddHandler(panicHandler)
	d.AddHandler(nextHandler)

	d.handleEvent(messageCreateEventType, &MessageCreate{})

	select {
	case <-panicDone:
	case <-time.After(time.Second):
		t.Fatal("panic handler was not called")
	}

	select {
	case <-nextDone:
	case <-time.After(time.Second):
		t.Fatal("next handler was not called")
	}

	if atomic.LoadInt32(&handlerCalled) != 1 {
		t.Fatalf("handlerCalled = %d, want 1", handlerCalled)
	}
}

func TestScheduledEvents(t *testing.T) {
	if dgBot == nil {
		t.Skip("Skipping, dgBot not set.")
	}

	beginAt := time.Now().Add(1 * time.Hour)
	endAt := time.Now().Add(2 * time.Hour)
	event, err := dgBot.GuildScheduledEventCreate(envGuild, &GuildScheduledEventParams{
		Name:               "Test Event",
		PrivacyLevel:       GuildScheduledEventPrivacyLevelGuildOnly,
		ScheduledStartTime: &beginAt,
		ScheduledEndTime:   &endAt,
		Description:        "Awesome Test Event created on livestream",
		EntityType:         GuildScheduledEventEntityTypeExternal,
		EntityMetadata: &GuildScheduledEventEntityMetadata{
			Location: "https://discord.com",
		},
	})
	defer dgBot.GuildScheduledEventDelete(envGuild, event.ID)

	if err != nil || event.Name != "Test Event" {
		t.Fatal(err)
	}

	events, err := dgBot.GuildScheduledEvents(envGuild, true)
	if err != nil {
		t.Fatal(err)
	}

	var foundEvent *GuildScheduledEvent
	for _, e := range events {
		if e.ID == event.ID {
			foundEvent = e
			break
		}
	}
	if foundEvent.Name != event.Name {
		t.Fatal("err on GuildScheduledEvents endpoint. Missing Scheduled Event")
	}

	getEvent, err := dgBot.GuildScheduledEvent(envGuild, event.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if getEvent.Name != event.Name {
		t.Fatal("err on GuildScheduledEvent endpoint. Mismatched Scheduled Event")
	}

	eventUpdated, err := dgBot.GuildScheduledEventEdit(envGuild, event.ID, &GuildScheduledEventParams{Name: "Test Event Updated"})
	if err != nil {
		t.Fatal(err)
	}

	if eventUpdated.Name != "Test Event Updated" {
		t.Fatal("err on GuildScheduledEventUpdate endpoint. Scheduled Event Name mismatch")
	}

	// Usage of 1 and 1 is just the pseudo data with the purpose to run all branches in the function without crashes.
	// see https://github.com/bwmarrin/discordgo/pull/1032#discussion_r815438303 for more details.
	users, err := dgBot.GuildScheduledEventUsers(envGuild, event.ID, 1, true, "1", "1")
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 0 {
		t.Fatal("err on GuildScheduledEventUsers. Mismatch of event maybe occurred")
	}

	err = dgBot.GuildScheduledEventDelete(envGuild, event.ID)
	if err != nil {
		t.Fatal(err)
	}
}

func TestComplexScheduledEvents(t *testing.T) {
	if dgBot == nil {
		t.Skip("Skipping, dgBot not set.")
	}

	beginAt := time.Now().Add(1 * time.Hour)
	endAt := time.Now().Add(2 * time.Hour)
	event, err := dgBot.GuildScheduledEventCreate(envGuild, &GuildScheduledEventParams{
		Name:               "Test Voice Event",
		PrivacyLevel:       GuildScheduledEventPrivacyLevelGuildOnly,
		ScheduledStartTime: &beginAt,
		ScheduledEndTime:   &endAt,
		Description:        "Test event on voice channel",
		EntityType:         GuildScheduledEventEntityTypeVoice,
		ChannelID:          envVoiceChannel,
	})
	if err != nil || event.Name != "Test Voice Event" {
		t.Fatal(err)
	}
	defer dgBot.GuildScheduledEventDelete(envGuild, event.ID)

	_, err = dgBot.GuildScheduledEventEdit(envGuild, event.ID, &GuildScheduledEventParams{
		EntityType: GuildScheduledEventEntityTypeExternal,
		EntityMetadata: &GuildScheduledEventEntityMetadata{
			Location: "https://discord.com",
		},
	})

	if err != nil {
		t.Fatal("err on GuildScheduledEventEdit. Change of entity type to external failed")
	}

	_, err = dgBot.GuildScheduledEventEdit(envGuild, event.ID, &GuildScheduledEventParams{
		ChannelID:      envVoiceChannel,
		EntityType:     GuildScheduledEventEntityTypeVoice,
		EntityMetadata: nil,
	})

	if err != nil {
		t.Fatal("err on GuildScheduledEventEdit. Change of entity type to voice failed")
	}
}
