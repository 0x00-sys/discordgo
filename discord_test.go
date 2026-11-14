package discordgo

import (
	"fmt"
	"os"
	"runtime"
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
