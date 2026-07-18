// Discordgo - Discord bindings for Go
// Available at https://github.com/bwmarrin/discordgo

// Copyright 2015-2016 Bruce Marriner <bruce@sqls.net>.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains low level functions for interacting with the Discord
// data websocket interface.

package discordgo

import (
	"bytes"
	"compress/zlib"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// ErrWSAlreadyOpen is thrown when you attempt to open
// a websocket that already is open.
var ErrWSAlreadyOpen = errors.New("web socket already opened")

// ErrWSNotFound is thrown when you attempt to use a websocket
// that doesn't exist
var ErrWSNotFound = errors.New("no websocket connection exists")

// ErrWSShardBounds is thrown when you try to use a shard ID that is
// more than the total shard count
var ErrWSShardBounds = errors.New("ShardID must be less than ShardCount")

const websocketWriteTimeout = time.Second

const (
	// Discord allows 120 events per connection every minute. Keep five
	// slots available for Heartbeat, Identify, and Resume payloads.
	gatewaySendRateLimit       = 115
	gatewaySendRateLimitWindow = time.Minute
)

type gatewaySendRateLimiter struct {
	mu         sync.Mutex
	connection *websocket.Conn
	cancel     chan struct{}
	sent       int
	resetAt    time.Time
	now        func() time.Time
	wait       func(time.Duration, <-chan struct{}) bool
}

func (r *gatewaySendRateLimiter) reset(connection *websocket.Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.cancelLocked()
	r.connection = connection
	r.cancel = make(chan struct{})
	r.sent = 0
	r.resetAt = r.timeNow().Add(gatewaySendRateLimitWindow)
}

func (r *gatewaySendRateLimiter) stop(connection *websocket.Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.connection == connection {
		r.cancelLocked()
	}
}

func (r *gatewaySendRateLimiter) acquire(connection *websocket.Conn) bool {
	for {
		r.mu.Lock()
		if r.connection == nil {
			r.connection = connection
			r.cancel = make(chan struct{})
			r.resetAt = r.timeNow().Add(gatewaySendRateLimitWindow)
		}
		if r.connection != connection || isGatewaySendCanceled(r.cancel) {
			r.mu.Unlock()
			return false
		}

		now := r.timeNow()
		if !now.Before(r.resetAt) {
			r.sent = 0
			r.resetAt = now.Add(gatewaySendRateLimitWindow)
		}
		if r.sent < gatewaySendRateLimit {
			r.sent++
			r.mu.Unlock()
			return true
		}

		delay := r.resetAt.Sub(now)
		cancel := r.cancel
		wait := r.wait
		r.mu.Unlock()

		if wait == nil {
			if !waitForGatewaySendLimit(delay, cancel) {
				return false
			}
		} else if !wait(delay, cancel) {
			return false
		}
	}
}

func (r *gatewaySendRateLimiter) timeNow() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

func (r *gatewaySendRateLimiter) cancelLocked() {
	if r.cancel == nil || isGatewaySendCanceled(r.cancel) {
		return
	}
	close(r.cancel)
}

func isGatewaySendCanceled(cancel <-chan struct{}) bool {
	select {
	case <-cancel:
		return true
	default:
		return false
	}
}

func waitForGatewaySendLimit(delay time.Duration, cancel <-chan struct{}) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return true
	case <-cancel:
		return false
	}
}

func gatewayStartupReadTimeout(dialer *websocket.Dialer) time.Duration {
	// A dialer without a handshake timeout must not disable the startup
	// read deadlines; fall back to the same bound used for nil dialers.
	if dialer == nil || dialer.HandshakeTimeout <= 0 {
		return 45 * time.Second
	}
	return dialer.HandshakeTimeout
}

func writeWebsocketCloseFrame(conn *websocket.Conn, closeCode int) error {
	return conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(closeCode, ""),
		time.Now().Add(websocketWriteTimeout),
	)
}

func writeGatewayJSONWithDeadline(conn *websocket.Conn, data interface{}) error {
	if err := conn.SetWriteDeadline(time.Now().Add(websocketWriteTimeout)); err != nil {
		return err
	}
	return conn.WriteJSON(data)
}

type resumePacket struct {
	Op   int `json:"op"`
	Data struct {
		Token     string `json:"token"`
		SessionID string `json:"session_id"`
		Sequence  int64  `json:"seq"`
	} `json:"d"`
}

// Open creates a websocket connection to Discord.
// See: https://discord.com/developers/docs/topics/gateway#connecting
func (s *Session) Open() error {
	s.log(LogInformational, "called")

	var err error

	// Prevent Open or other major Session functions from
	// being called while Open is still running.
	s.Lock()
	defer s.Unlock()

	s.ensureReconnectCancelLocked()

	// If the websock is already open, bail out here.
	if s.wsConn != nil {
		return ErrWSAlreadyOpen
	}

	sequence := atomic.LoadInt64(s.sequence)
	resuming := s.sessionID != "" || sequence != 0

	var gateway string
	// Get the gateway to use for the Websocket connection
	if sequence != 0 && s.sessionID != "" && s.resumeGatewayURL != "" {
		s.log(LogDebug, "using resume gateway %s", s.resumeGatewayURL)
		gateway = s.resumeGatewayURL
	} else {
		if s.gateway == "" {
			s.gateway, err = s.Gateway()
			if err != nil {
				return err
			}
		}

		gateway = s.gateway
	}

	// Add the version and encoding to the URL
	gateway += "?v=" + APIVersion + "&encoding=json"

	// Connect to the Gateway
	s.log(LogInformational, "connecting to gateway %s", gateway)
	header := http.Header{}
	header.Add("accept-encoding", "zlib")
	s.wsConn, _, err = s.Dialer.Dial(gateway, header)
	if err != nil {
		s.log(LogError, "error connecting to gateway %s, %s", s.gateway, err)
		s.gateway = "" // clear cached gateway
		s.wsConn = nil // Just to be safe.
		return err
	}
	wsConn := s.wsConn
	s.gatewaySendRateLimiter.reset(wsConn)

	wsConn.SetCloseHandler(func(code int, text string) error {
		return nil
	})

	defer func() {
		// because of this, all code below must set err to the error
		// when exiting with an error :)  Maybe someone has a better
		// way :)
		if err != nil {
			if s.wsConn == wsConn {
				s.gatewaySendRateLimiter.stop(wsConn)
				if s.listening != nil {
					close(s.listening)
					s.listening = nil
				}
				wsConn.Close()
				s.wsConn = nil
			}
		}
	}()

	// The first response from Discord should be an Op 10 (Hello) Packet.
	// When processed by onEvent the heartbeat goroutine will be started.
	mt, m, err := s.readGatewayHello(wsConn)
	if err != nil {
		if shouldStartNewGatewaySessionOnClose(err) {
			s.resetGatewayResumeStateLocked()
		}
		return err
	}
	err = s.openGatewayControlEvent(mt, m, 10, false)
	if err != nil {
		return err
	}
	e, err := s.onEvent(mt, m)
	if err != nil {
		return err
	}
	if e.Operation != 10 {
		err = fmt.Errorf("expecting Op 10, got Op %d instead", e.Operation)
		return err
	}
	s.log(LogInformational, "Op 10 Hello Packet received from Discord")
	s.LastHeartbeatAck = time.Now().UTC()
	var h helloOp
	if err = json.Unmarshal(e.RawData, &h); err != nil {
		err = fmt.Errorf("error unmarshalling helloOp, %s", err)
		return err
	}
	if h.HeartbeatInterval <= 0 || h.HeartbeatInterval > maxGatewayHeartbeatIntervalMsec {
		err = fmt.Errorf("invalid gateway heartbeat interval %d", h.HeartbeatInterval)
		return err
	}

	// Now we send either an Op 2 Identity if this is a brand new
	// connection or Op 6 Resume if we are resuming an existing connection.
	if s.sessionID == "" && sequence == 0 {

		// Send Op 2 Identity Packet
		err = s.identify(wsConn)
		if err != nil {
			err = fmt.Errorf("error sending identify packet to gateway, %s, %s", s.gateway, err)
			return err
		}

	} else {

		// Send Op 6 Resume Packet
		p := resumePacket{}
		p.Op = 6
		p.Data.Token = s.Token
		p.Data.SessionID = s.sessionID
		p.Data.Sequence = sequence

		s.log(LogInformational, "sending resume packet to gateway")
		err = s.writeGatewayStartupPacket(wsConn, p)
		if err != nil {
			err = fmt.Errorf("error sending gateway resume packet, %s, %s", s.gateway, err)
			return err
		}

	}

	// A basic state is a hard requirement for Voice.
	// We create it here so the below READY/RESUMED packet can populate
	// the state :)
	// XXX: Move to New() func?
	if s.State == nil {
		state := NewState()
		state.TrackChannels = false
		state.TrackEmojis = false
		state.TrackMembers = false
		state.TrackRoles = false
		state.TrackVoice = false
		s.State = state
	}

	// Create listening chan outside of listen, as it needs to happen inside the
	// mutex lock and needs to exist before calling heartbeat and listen
	// go rountines.
	s.listening = make(chan interface{})

	// Start sending heartbeats after Hello while waiting for READY/RESUMED.
	go s.heartbeat(wsConn, s.listening, h.HeartbeatInterval)
	startupReadTimeout := gatewayStartupReadTimeout(s.Dialer)

	s.Unlock()
	e, err = s.waitForGatewayReady(wsConn, resuming, startupReadTimeout)
	s.Lock()
	if err != nil {
		return err
	}
	if s.wsConn == nil {
		err = ErrWSNotFound
		return err
	}
	if s.wsConn != wsConn {
		// A concurrent Close()+Open() replaced the connection; the
		// session belongs to the newer Open now.
		err = ErrWSAlreadyOpen
		return err
	}
	if e.Type != `READY` && e.Type != `RESUMED` {
		// This is not fatal, but it does not follow their API documentation.
		s.log(LogWarning, "Expected READY/RESUMED, instead got:\n%#v\n", e)
	}
	s.log(LogInformational, "First Packet:\n%#v\n", e)

	s.log(LogInformational, "We are now connected to Discord, emitting connect event")
	s.Unlock()
	s.handleEvent(connectEventType, &Connect{})
	s.Lock()
	if s.wsConn == nil {
		err = ErrWSNotFound
		return err
	}
	if s.wsConn != wsConn {
		// A concurrent Close()+Open() replaced the connection; the
		// session belongs to the newer Open now.
		err = ErrWSAlreadyOpen
		return err
	}

	// A VoiceConnections map is a hard requirement for Voice.
	// XXX: can this be moved to when opening a voice connection?
	if s.VoiceConnections == nil {
		s.log(LogInformational, "creating new VoiceConnections map")
		s.VoiceConnections = make(map[string]*VoiceConnection)
	}

	// Start reading messages from Discord.
	go s.listen(wsConn, s.listening)

	s.log(LogInformational, "exiting")
	return nil
}

func (s *Session) waitForGatewayReady(wsConn *websocket.Conn, allowDispatchReplay bool, startupReadTimeout time.Duration) (event *Event, err error) {
	if startupReadTimeout > 0 {
		defer func() {
			if clearErr := wsConn.SetReadDeadline(time.Time{}); err == nil && clearErr != nil {
				err = clearErr
			}
		}()
	}

	for {
		// Refresh the deadline for every message so it bounds the time
		// without data from the gateway rather than the total startup
		// duration; a healthy resume can replay events for longer than
		// the timeout.
		if startupReadTimeout > 0 {
			if err = wsConn.SetReadDeadline(time.Now().Add(startupReadTimeout)); err != nil {
				return nil, err
			}
		}

		mt, m, err := wsConn.ReadMessage()
		if err != nil {
			if shouldStartNewGatewaySessionOnClose(err) {
				s.resetGatewayResumeState()
			}
			return nil, err
		}

		s.Lock()
		err = s.openGatewayControlEvent(mt, m, 0, allowDispatchReplay, "READY", "RESUMED")
		s.Unlock()
		if err != nil {
			return nil, err
		}

		e, err := s.onEvent(mt, m)
		if err != nil {
			return e, err
		}
		if e != nil && e.Operation == 0 && (e.Type == "READY" || e.Type == "RESUMED") {
			return e, nil
		}
	}
}

func (s *Session) readGatewayHello(wsConn *websocket.Conn) (messageType int, message []byte, err error) {
	if timeout := gatewayStartupReadTimeout(s.Dialer); timeout > 0 {
		if err = wsConn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
			return
		}
		defer func() {
			if clearErr := wsConn.SetReadDeadline(time.Time{}); err == nil && clearErr != nil {
				err = clearErr
			}
		}()
	}

	return wsConn.ReadMessage()
}

func (s *Session) openGatewayControlEvent(messageType int, message []byte, expectedOperation int, allowDispatchBeforeExpected bool, expectedEventTypes ...string) error {
	e, err := peekGatewayEvent(messageType, message)
	if err != nil || e == nil {
		return nil
	}

	switch e.Operation {
	case 7:
		return fmt.Errorf("received Op 7 reconnect during open")
	case 9:
		var resumable bool
		if err := json.Unmarshal(e.RawData, &resumable); err != nil {
			// The session cannot safely be resumed when Discord's answer
			// cannot be interpreted, so force the next attempt to identify.
			s.resetGatewayResumeStateLocked()
			return fmt.Errorf("error unmarshalling invalid session event, %s", err)
		}
		if !resumable {
			s.resetGatewayResumeStateLocked()
		}
		return fmt.Errorf("received Op 9 invalid session during open")
	}

	if expectedOperation == 0 && (e.Operation == 1 || e.Operation == 11) {
		return nil
	}
	if e.Operation != expectedOperation {
		return fmt.Errorf("received Op %d during open, expected Op %d", e.Operation, expectedOperation)
	}
	if e.Operation == 0 {
		for _, expected := range expectedEventTypes {
			if e.Type == expected {
				return nil
			}
		}
		if allowDispatchBeforeExpected {
			return nil
		}
		return fmt.Errorf("received %s event during open", e.Type)
	}

	return nil
}

func peekGatewayEvent(messageType int, message []byte) (*Event, error) {
	var reader io.Reader
	reader = bytes.NewBuffer(message)

	if messageType == websocket.BinaryMessage {
		z, err := zlib.NewReader(reader)
		if err != nil {
			return nil, err
		}
		defer z.Close()

		reader = z
	}

	var e *Event
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(&e); err != nil {
		return e, err
	}

	return e, nil
}

// listen polls the websocket connection for events, it will stop when the
// listening channel is closed, or an error occurs.
func (s *Session) listen(wsConn *websocket.Conn, listening <-chan interface{}) {

	s.log(LogInformational, "called")

	for {

		messageType, message, err := wsConn.ReadMessage()

		if err != nil {
			readErr := err

			// Detect if we have been closed manually. If a Close() has already
			// happened, the websocket we are listening on will be different to
			// the current session.
			s.RLock()
			sameConnection := s.wsConn == wsConn
			s.RUnlock()

			if sameConnection {

				s.log(LogWarning, "error reading from gateway %s websocket, %s", s.gateway, readErr)
				closeCode := websocket.CloseNormalClosure
				if shouldReconnectOnGatewayClose(readErr) {
					closeCode = websocket.CloseServiceRestart
				}

				// There has been an error reading, close the websocket so that
				// OnDisconnect event is emitted.
				closed, err := s.closeWithCodeForConnection(wsConn, closeCode, shouldStartNewGatewaySessionOnClose(readErr))
				if err != nil {
					s.log(LogWarning, "error closing session connection, %s", err)
				}
				if !closed {
					return
				}

				if !shouldReconnectOnGatewayClose(readErr) {
					s.log(LogInformational, "not reconnecting after terminal gateway close, %s", readErr)
					return
				}

				s.log(LogInformational, "calling reconnect() now")
				s.reconnect()
			}

			return
		}

		select {

		case <-listening:
			return

		default:
			s.onEvent(messageType, message)

		}
	}
}

func shouldReconnectOnGatewayClose(err error) bool {
	return !websocket.IsCloseError(err, 4004, 4010, 4011, 4012, 4013, 4014)
}

func shouldStartNewGatewaySessionOnClose(err error) bool {
	return websocket.IsCloseError(err, 4003, 4005, 4007, 4009)
}

func shouldResetGatewayResumeStateOnCloseCode(closeCode int) bool {
	return closeCode == websocket.CloseNormalClosure || closeCode == websocket.CloseGoingAway
}

func (s *Session) resetGatewayResumeState() {
	s.Lock()
	defer s.Unlock()
	s.resetGatewayResumeStateLocked()
}

func (s *Session) resetGatewayResumeStateLocked() {
	s.resumeGatewayURL = ""
	s.sessionID = ""
	atomic.StoreInt64(s.sequence, 0)
}

type heartbeatOp struct {
	Op   int   `json:"op"`
	Data int64 `json:"d"`
}

type helloOp struct {
	HeartbeatInterval time.Duration `json:"heartbeat_interval"`
}

// maxGatewayHeartbeatIntervalMsec bounds the raw millisecond interval so
// that its conversion to time.Duration cannot overflow.
const maxGatewayHeartbeatIntervalMsec = time.Duration(1<<63-1) / time.Millisecond

// FailedHeartbeatAcks is the number of heartbeat intervals without an ACK that
// forces a connection restart. The time.Duration type is retained for source
// compatibility.
const FailedHeartbeatAcks time.Duration = time.Millisecond

var gatewayHeartbeatInitialJitter atomic.Value

func init() {
	gatewayHeartbeatInitialJitter.Store(randomGatewayHeartbeatInitialJitter)
}

// HeartbeatLatency returns the latency between heartbeat acknowledgement and heartbeat send.
func (s *Session) HeartbeatLatency() time.Duration {

	s.RLock()
	defer s.RUnlock()

	return s.LastHeartbeatAck.Sub(s.LastHeartbeatSent)

}

// heartbeat sends regular heartbeats to Discord so it knows the client
// is still connected.  If you do not send these heartbeats Discord will
// disconnect the websocket connection after a few seconds.
func (s *Session) heartbeat(wsConn *websocket.Conn, listening <-chan interface{}, heartbeatIntervalMsec time.Duration) {

	s.log(LogInformational, "called")

	if listening == nil || wsConn == nil {
		return
	}

	heartbeatInterval := heartbeatIntervalMsec * time.Millisecond
	if !waitForInitialHeartbeat(listening, heartbeatInterval) {
		return
	}

	var err error
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		if isListeningClosed(listening) {
			return
		}

		s.RLock()
		lastAck := s.LastHeartbeatAck
		lastSent := s.LastHeartbeatSent
		s.RUnlock()
		missedAck := lastAck.Before(lastSent)
		if !missedAck {
			sequence := atomic.LoadInt64(s.sequence)
			s.log(LogDebug, "sending gateway websocket heartbeat seq %d", sequence)
			s.Lock()
			s.LastHeartbeatSent = time.Now().UTC()
			s.Unlock()
			s.wsMutex.Lock()
			err = writeGatewayJSONWithDeadline(wsConn, heartbeatOp{1, sequence})
			s.wsMutex.Unlock()
		}
		if err != nil || missedAck {
			if isListeningClosed(listening) {
				return
			}
			if err != nil {
				s.log(LogError, "error sending heartbeat to gateway %s, %s", s.gateway, err)
			} else {
				s.log(LogError, "haven't gotten a heartbeat ACK in %v, triggering a reconnection", time.Now().UTC().Sub(lastSent))
			}
			closed, _ := s.closeWithCodeForConnection(wsConn, websocket.CloseServiceRestart, false)
			if !closed {
				return
			}
			s.reconnect()
			return
		}
		s.Lock()
		s.DataReady = true
		s.Unlock()

		select {
		case <-ticker.C:
			// continue loop and send heartbeat
		case <-listening:
			return
		}
	}
}

func waitForInitialHeartbeat(listening <-chan interface{}, heartbeatInterval time.Duration) bool {
	jitter := gatewayHeartbeatInitialJitter.Load().(func(time.Duration) time.Duration)
	delay := jitter(heartbeatInterval)
	if delay <= 0 {
		return true
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return true
	case <-listening:
		return false
	}
}

func randomGatewayHeartbeatInitialJitter(heartbeatInterval time.Duration) time.Duration {
	if heartbeatInterval <= 0 {
		return 0
	}

	n, err := rand.Int(rand.Reader, big.NewInt(int64(heartbeatInterval)))
	if err != nil {
		return 0
	}

	return time.Duration(n.Int64())
}

func isListeningClosed(listening <-chan interface{}) bool {
	select {
	case <-listening:
		return true
	default:
		return false
	}
}

// UpdateStatusData is provided to UpdateStatusComplex()
type UpdateStatusData struct {
	IdleSince  *int        `json:"since"`
	Activities []*Activity `json:"activities"`
	AFK        bool        `json:"afk"`
	Status     string      `json:"status"`
}

type updateStatusOp struct {
	Op   int              `json:"op"`
	Data UpdateStatusData `json:"d"`
}

func newUpdateStatusData(idle int, activityType ActivityType, name, url string) *UpdateStatusData {
	usd := &UpdateStatusData{
		Status: "online",
	}

	if idle > 0 {
		usd.IdleSince = &idle
	}

	if name != "" {
		usd.Activities = []*Activity{{
			Name: name,
			Type: activityType,
			URL:  url,
		}}
	}

	return usd
}

// UpdateGameStatus is used to update the user's status.
// If idle>0 then set status to idle.
// If name!="" then set game.
// if otherwise, set status to active, and no activity.
func (s *Session) UpdateGameStatus(idle int, name string) (err error) {
	return s.UpdateStatusComplex(*newUpdateStatusData(idle, ActivityTypeGame, name, ""))
}

// UpdateWatchStatus is used to update the user's watch status.
// If idle>0 then set status to idle.
// If name!="" then set movie/stream.
// if otherwise, set status to active, and no activity.
func (s *Session) UpdateWatchStatus(idle int, name string) (err error) {
	return s.UpdateStatusComplex(*newUpdateStatusData(idle, ActivityTypeWatching, name, ""))
}

// UpdateStreamingStatus is used to update the user's streaming status.
// If idle>0 then set status to idle.
// If name!="" then set game.
// If name!="" and url!="" then set the status type to streaming with the URL set.
// if otherwise, set status to active, and no game.
func (s *Session) UpdateStreamingStatus(idle int, name string, url string) (err error) {
	gameType := ActivityTypeGame
	if url != "" {
		gameType = ActivityTypeStreaming
	}
	return s.UpdateStatusComplex(*newUpdateStatusData(idle, gameType, name, url))
}

// UpdateListeningStatus is used to set the user to "Listening to..."
// If name!="" then set to what user is listening to
// Else, set user to active and no activity.
func (s *Session) UpdateListeningStatus(name string) (err error) {
	return s.UpdateStatusComplex(*newUpdateStatusData(0, ActivityTypeListening, name, ""))
}

// UpdateCustomStatus is used to update the user's custom status.
// If state!="" then set the custom status.
// Else, set user to active and remove the custom status.
func (s *Session) UpdateCustomStatus(state string) (err error) {
	data := UpdateStatusData{
		Status: "online",
	}

	if state != "" {
		// Discord requires a non-empty activity name, therefore we provide "Custom Status" as a placeholder.
		data.Activities = []*Activity{{
			Name:  "Custom Status",
			Type:  ActivityTypeCustom,
			State: state,
		}}
	}

	return s.UpdateStatusComplex(data)
}

// UpdateStatusComplex allows for sending the raw status update data untouched by discordgo.
func (s *Session) UpdateStatusComplex(usd UpdateStatusData) (err error) {
	// The comment does say "untouched by discordgo", but we might need to lie a bit here.
	// The Discord documentation lists `activities` as being nullable, but in practice this
	// doesn't seem to be the case. I had filed an issue about this at
	// https://github.com/discord/discord-api-docs/issues/2559, but as of writing this
	// haven't had any movement on it, so at this point I'm assuming this is an error,
	// and am fixing this bug accordingly. Because sending `null` for `activities` instantly
	// disconnects us, I think that disallowing it from being sent in `UpdateStatusComplex`
	// isn't that big of an issue.
	if usd.Activities == nil {
		usd.Activities = make([]*Activity, 0)
	}

	return s.gatewayWriteJSON(updateStatusOp{3, usd})
}

type requestGuildMembersData struct {
	GuildID   string    `json:"guild_id"`
	Query     *string   `json:"query,omitempty"`
	UserIDs   *[]string `json:"user_ids,omitempty"`
	Limit     int       `json:"limit"`
	Nonce     string    `json:"nonce,omitempty"`
	Presences bool      `json:"presences"`
}

type requestGuildMembersOp struct {
	Op   int                     `json:"op"`
	Data requestGuildMembersData `json:"d"`
}

type requestSoundboardSoundsData struct {
	GuildIDs []string `json:"guild_ids"`
}

type requestSoundboardSoundsOp struct {
	Op   int                         `json:"op"`
	Data requestSoundboardSoundsData `json:"d"`
}

// RequestSoundboardSounds requests soundboard sounds for the given guilds from the gateway.
// The gateway responds with SoundboardSounds events.
// guildIDs : IDs of guilds to request soundboard sounds for.
func (s *Session) RequestSoundboardSounds(guildIDs []string) error {
	data := requestSoundboardSoundsData{GuildIDs: guildIDs}
	return s.gatewayWriteJSON(requestSoundboardSoundsOp{31, data})
}

type requestChannelInfoData struct {
	GuildID string   `json:"guild_id"`
	Fields  []string `json:"fields"`
}

type requestChannelInfoOp struct {
	Op   int                    `json:"op"`
	Data requestChannelInfoData `json:"d"`
}

// RequestChannelInfo requests ephemeral channel data for a guild from the gateway.
// The gateway responds with a ChannelInfo event.
// guildID : ID of guild to request channel info for.
// fields  : Channel info fields to request, such as "status" or "voice_start_time".
func (s *Session) RequestChannelInfo(guildID string, fields []string) error {
	data := requestChannelInfoData{
		GuildID: guildID,
		Fields:  fields,
	}
	return s.gatewayWriteJSON(requestChannelInfoOp{43, data})
}

// RequestGuildMembers requests guild members from the gateway
// The gateway responds with GuildMembersChunk events
// guildID   : Single Guild ID to request members of
// query     : String that username starts with, leave empty to return all members
// limit     : Max number of items to return, or 0 to request all members matched
// nonce     : Nonce to identify the Guild Members Chunk response
// presences : Whether to request presences of guild members
func (s *Session) RequestGuildMembers(guildID, query string, limit int, nonce string, presences bool) error {
	data := requestGuildMembersData{
		GuildID:   guildID,
		Query:     &query,
		Limit:     limit,
		Nonce:     nonce,
		Presences: presences,
	}
	return s.requestGuildMembers(data)
}

// RequestGuildMembersList requests guild members from the gateway
// The gateway responds with GuildMembersChunk events
// guildID   : Single Guild ID to request members of
// userIDs   : IDs of users to fetch
// limit     : Max number of items to return, or 0 to request all members matched
// nonce     : Nonce to identify the Guild Members Chunk response
// presences : Whether to request presences of guild members
func (s *Session) RequestGuildMembersList(guildID string, userIDs []string, limit int, nonce string, presences bool) error {
	data := requestGuildMembersData{
		GuildID:   guildID,
		UserIDs:   &userIDs,
		Limit:     limit,
		Nonce:     nonce,
		Presences: presences,
	}
	return s.requestGuildMembers(data)
}

// RequestGuildMembersBatch requests guild members from the gateway
// The gateway responds with GuildMembersChunk events
// guildID   : Slice of guild IDs to request members of
// query     : String that username starts with, leave empty to return all members
// limit     : Max number of items to return, or 0 to request all members matched
// nonce     : Nonce to identify the Guild Members Chunk response
// presences : Whether to request presences of guild members
//
// NOTE: this function is deprecated, please use RequestGuildMembers instead
func (s *Session) RequestGuildMembersBatch(guildIDs []string, query string, limit int, nonce string, presences bool) (err error) {
	// Discord only accepts one guild_id per opcode 8 request.
	for _, guildID := range guildIDs {
		data := requestGuildMembersData{
			GuildID:   guildID,
			Query:     &query,
			Limit:     limit,
			Nonce:     nonce,
			Presences: presences,
		}
		if err = s.requestGuildMembers(data); err != nil {
			return
		}
	}
	return
}

// RequestGuildMembersBatchList requests guild members from the gateway
// The gateway responds with GuildMembersChunk events
// guildID   : Slice of guild IDs to request members of
// userIDs   : IDs of users to fetch
// limit     : Max number of items to return, or 0 to request all members matched
// nonce     : Nonce to identify the Guild Members Chunk response
// presences : Whether to request presences of guild members
//
// NOTE: this function is deprecated, please use RequestGuildMembersList instead
func (s *Session) RequestGuildMembersBatchList(guildIDs []string, userIDs []string, limit int, nonce string, presences bool) (err error) {
	// Discord only accepts one guild_id per opcode 8 request.
	for _, guildID := range guildIDs {
		data := requestGuildMembersData{
			GuildID:   guildID,
			UserIDs:   &userIDs,
			Limit:     limit,
			Nonce:     nonce,
			Presences: presences,
		}
		if err = s.requestGuildMembers(data); err != nil {
			return
		}
	}
	return
}

// GatewayWriteStruct allows for sending raw gateway structs over the gateway.
func (s *Session) GatewayWriteStruct(data interface{}) (err error) {
	return s.gatewayWriteJSON(data)
}

func (s *Session) gatewayWriteJSON(data interface{}) (err error) {
	s.RLock()
	wsConn := s.wsConn
	s.RUnlock()
	if wsConn == nil {
		return ErrWSNotFound
	}
	if !s.gatewaySendRateLimiter.acquire(wsConn) {
		return ErrWSNotFound
	}

	return s.writeGatewayJSON(wsConn, data)
}

func (s *Session) writeGatewayJSON(wsConn *websocket.Conn, data interface{}) (err error) {
	s.wsMutex.Lock()
	defer s.wsMutex.Unlock()

	s.RLock()
	current := s.wsConn == wsConn
	s.RUnlock()
	if !current {
		return ErrWSNotFound
	}

	return writeGatewayJSONWithDeadline(wsConn, data)
}

func (s *Session) requestGuildMembers(data requestGuildMembersData) (err error) {
	s.log(LogInformational, "called")

	return s.gatewayWriteJSON(requestGuildMembersOp{8, data})
}

// onEvent is the "event handler" for all messages received on the
// Discord Gateway API websocket connection.
//
// If you use the AddHandler() function to register a handler for a
// specific event this function will pass the event along to that handler.
//
// If you use the AddHandler() function to register a handler for the
// "OnEvent" event then all events will be passed to that handler.
func (s *Session) onEvent(messageType int, message []byte) (*Event, error) {

	var err error
	var reader io.Reader
	reader = bytes.NewBuffer(message)

	// If this is a compressed message, uncompress it.
	if messageType == websocket.BinaryMessage {

		z, err2 := zlib.NewReader(reader)
		if err2 != nil {
			s.log(LogError, "error uncompressing websocket message, %s", err)
			return nil, err2
		}

		defer func() {
			err3 := z.Close()
			if err3 != nil {
				s.log(LogWarning, "error closing zlib, %s", err)
			}
		}()

		reader = z
	}

	// Decode the event into an Event struct.
	var e *Event
	decoder := json.NewDecoder(reader)
	if err = decoder.Decode(&e); err != nil {
		s.log(LogError, "error decoding websocket message, %s", err)
		return e, err
	}
	if e == nil {
		err = fmt.Errorf("invalid gateway event")
		s.log(LogError, "error decoding websocket message, %s", err)
		return nil, err
	}

	// Redacting the payload is expensive; skip it unless the message
	// would actually be logged.
	if s.LogLevel >= LogDebug {
		s.log(LogDebug, "Op: %d, Seq: %d, Type: %s, Data: %s\n\n", e.Operation, e.Sequence, e.Type, redactedGatewayData(e.RawData))
	}

	// Ping request.
	// Must respond with a heartbeat packet within 5 seconds
	if e.Operation == 1 {
		s.log(LogInformational, "sending heartbeat in response to Op1")

		sentAt := time.Now().UTC()
		s.RLock()
		wsConn := s.wsConn
		s.RUnlock()
		if wsConn == nil {
			return e, ErrWSNotFound
		}
		err = s.writeGatewayJSON(wsConn, heartbeatOp{1, atomic.LoadInt64(s.sequence)})
		if err != nil {
			s.log(LogError, "error sending heartbeat in response to Op1")
			return e, err
		}
		s.Lock()
		if s.LastHeartbeatSent.Before(sentAt) {
			s.LastHeartbeatSent = sentAt
		}
		s.Unlock()

		return e, nil
	}

	// Reconnect
	// Must immediately disconnect from gateway and reconnect to new gateway.
	if e.Operation == 7 {
		s.log(LogInformational, "Closing and reconnecting in response to Op7")
		s.closeWithCode(websocket.CloseServiceRestart, false)
		s.reconnect()
		return e, nil
	}

	// Invalid Session
	// Must respond with a Identify packet.
	if e.Operation == 9 {
		s.log(LogInformational, "Closing and reconnecting in response to Op9")
		s.closeWithCode(websocket.CloseServiceRestart, false)

		var resumable bool
		err = json.Unmarshal(e.RawData, &resumable)
		if err != nil {
			s.log(LogError, "error unmarshalling invalid session event, %s", err)
		}

		// If Discord sends an invalid payload, discard the session rather
		// than leaving the gateway closed or repeatedly attempting a resume
		// that cannot be known to be valid.
		if err != nil || !resumable {
			s.log(LogInformational, "Gateway session is not resumable, discarding its information")
			s.Lock()
			s.resumeGatewayURL = ""
			s.sessionID = ""
			s.Unlock()
			atomic.StoreInt64(s.sequence, 0)
		}

		s.reconnect()
		return e, err
	}

	if e.Operation == 10 {
		// Op10 is handled by Open()
		return e, nil
	}

	if e.Operation == 11 {
		s.Lock()
		s.LastHeartbeatAck = time.Now().UTC()
		s.Unlock()
		s.log(LogDebug, "got heartbeat ACK")
		return e, nil
	}

	// Do not try to Dispatch a non-Dispatch Message
	if e.Operation != 0 {
		// But we probably should be doing something with them.
		// TEMP
		if s.LogLevel >= LogWarning {
			s.log(LogWarning, "unknown Op: %d, Seq: %d, Type: %s, Data: %s, message: %s", e.Operation, e.Sequence, e.Type, redactedGatewayData(e.RawData), redactedGatewayData(message))
		}
		return e, nil
	}

	// Store the message sequence
	atomic.StoreInt64(s.sequence, e.Sequence)

	// Map event to registered event handlers and pass it along to any registered handlers.
	if eh, ok := registeredInterfaceProviders[e.Type]; ok {
		e.Struct = eh.New()

		// Attempt to unmarshal our event.
		if err = json.Unmarshal(e.RawData, e.Struct); err != nil {
			s.log(LogError, "error unmarshalling %s event, %s", e.Type, err)
		}

		// Send event to any registered event handlers for it's type.
		// Because the above doesn't cancel this, in case of an error
		// the struct could be partially populated or at default values.
		// However, most errors are due to a single field and I feel
		// it's better to pass along what we received than nothing at all.
		// TODO: Think about that decision :)
		// Either way, READY events must fire, even with errors.
		s.handleEvent(e.Type, e.Struct)
	} else if s.LogLevel >= LogWarning {
		s.log(LogWarning, "unknown event: Op: %d, Seq: %d, Type: %s, Data: %s", e.Operation, e.Sequence, e.Type, redactedGatewayData(e.RawData))
	}

	// For legacy reasons, we send the raw event also, this could be useful for handling unknown events.
	s.handleEvent(eventEventType, e)

	return e, nil
}

func redactedGatewayData(data []byte) string {
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return string(data)
	}

	redactGatewayValue(v)

	b, err := json.Marshal(v)
	if err != nil {
		return string(data)
	}

	return string(b)
}

func redactGatewayValue(v interface{}) {
	switch value := v.(type) {
	case map[string]interface{}:
		for k, nested := range value {
			if isGatewaySecretKey(k) {
				value[k] = "[REDACTED]"
				continue
			}
			redactGatewayValue(nested)
		}
	case []interface{}:
		for _, nested := range value {
			redactGatewayValue(nested)
		}
	}
}

func isGatewaySecretKey(key string) bool {
	switch key {
	case "token", "access_token", "refresh_token":
		return true
	}

	return false
}

// ------------------------------------------------------------------------------------------------
// Code related to voice connections that initiate over the data websocket
// ------------------------------------------------------------------------------------------------

type voiceChannelJoinData struct {
	GuildID   *string `json:"guild_id"`
	ChannelID *string `json:"channel_id"`
	SelfMute  bool    `json:"self_mute"`
	SelfDeaf  bool    `json:"self_deaf"`
}

type voiceChannelJoinOp struct {
	Op   int                  `json:"op"`
	Data voiceChannelJoinData `json:"d"`
}

// ChannelVoiceJoin joins the session user to a voice channel.
//
//	gID     : Guild ID of the channel to join.
//	cID     : Channel ID of the channel to join.
//	mute    : If true, you will be set to muted upon joining.
//	deaf    : If true, you will be set to deafened upon joining.
func (s *Session) ChannelVoiceJoin(gID, cID string, mute, deaf bool) (voice *VoiceConnection, err error) {

	s.log(LogInformational, "called")
	s.voiceMutex.Lock()

	// A disconnect unregisters its connection before writing to the
	// gateway. Select and configure a registered connection in a loop
	// so a join racing that teardown switches to a replacement.
	for {
		s.Lock()
		if s.VoiceConnections == nil {
			s.VoiceConnections = make(map[string]*VoiceConnection)
		}
		voice = s.VoiceConnections[gID]
		if voice == nil {
			voice = &VoiceConnection{}
			s.VoiceConnections[gID] = voice
		}
		s.Unlock()

		voice.Lock()
		if voice.disconnecting {
			voice.Unlock()

			s.Lock()
			if s.VoiceConnections[gID] == voice {
				delete(s.VoiceConnections, gID)
			}
			s.Unlock()
			continue
		}
		voice.GuildID = gID
		voice.ChannelID = cID
		voice.deaf = deaf
		voice.mute = mute
		voice.session = s
		voice.Unlock()

		s.RLock()
		registered := s.VoiceConnections[gID] == voice
		s.RUnlock()
		voice.RLock()
		disconnecting := voice.disconnecting
		voice.RUnlock()
		if !registered || disconnecting {
			continue
		}

		err = s.channelVoiceJoinManual(gID, cID, mute, deaf)
		break
	}
	s.voiceMutex.Unlock()

	if err != nil {
		return
	}

	// doesn't exactly work perfect yet.. TODO
	err = voice.waitUntilConnected()
	if err != nil {
		s.log(LogWarning, "error waiting for voice to connect, %s", err)
		voice.Close()
		return
	}

	return
}

// ChannelVoiceJoinManual initiates a voice session to a voice channel, but does not complete it.
//
// This should only be used when the VoiceServerUpdate will be intercepted and used elsewhere.
//
//	gID     : Guild ID of the channel to join.
//	cID     : Channel ID of the channel to join, leave empty to disconnect.
//	mute    : If true, you will be set to muted upon joining.
//	deaf    : If true, you will be set to deafened upon joining.
func (s *Session) ChannelVoiceJoinManual(gID, cID string, mute, deaf bool) (err error) {

	s.log(LogInformational, "called")
	s.voiceMutex.Lock()
	err = s.channelVoiceJoinManual(gID, cID, mute, deaf)
	s.voiceMutex.Unlock()
	return
}

// channelVoiceJoinManual sends a voice state update.
// The caller must hold voiceMutex.
func (s *Session) channelVoiceJoinManual(gID, cID string, mute, deaf bool) (err error) {
	var channelID *string
	if cID == "" {
		channelID = nil
	} else {
		channelID = &cID
	}

	// Send the request to Discord that we want to join the voice channel
	data := voiceChannelJoinOp{4, voiceChannelJoinData{&gID, channelID, mute, deaf}}
	return s.gatewayWriteJSON(data)
}

// onVoiceStateUpdate handles Voice State Update events on the data websocket.
func (s *Session) onVoiceStateUpdate(st *VoiceStateUpdate) {
	if st == nil || st.VoiceState == nil {
		return
	}

	// If we don't have a connection for the channel, don't bother
	if st.ChannelID == "" {
		return
	}

	// Check if we have a voice connection to update
	s.RLock()
	voice, exists := s.VoiceConnections[st.GuildID]
	state := s.State
	s.RUnlock()
	if !exists || voice == nil || state == nil {
		return
	}

	// We only care about events that are about us.
	state.RLock()
	user := state.User
	if user == nil {
		state.RUnlock()
		return
	}
	userID := user.ID
	state.RUnlock()
	if userID != st.UserID {
		return
	}

	// Store the SessionID for later use.
	voice.Lock()
	voice.UserID = st.UserID
	voice.sessionID = st.SessionID
	voice.ChannelID = st.ChannelID
	voice.Unlock()
}

// onVoiceServerUpdate handles the Voice Server Update data websocket event.
//
// This is also fired if the Guild's voice region changes while connected
// to a voice channel.  In that case, need to re-establish connection to
// the new region endpoint.
func (s *Session) onVoiceServerUpdate(st *VoiceServerUpdate) {
	if st == nil {
		return
	}

	s.log(LogInformational, "called")

	s.RLock()
	voice, exists := s.VoiceConnections[st.GuildID]
	s.RUnlock()

	// If no VoiceConnection exists, just skip this
	if !exists || voice == nil {
		return
	}

	// If currently connected to voice ws/udp, then disconnect.
	// Has no effect if not connected.
	voice.Close()

	// Store values for later use
	voice.Lock()
	voice.token = st.Token
	voice.endpoint = st.Endpoint
	voice.GuildID = st.GuildID
	voice.Unlock()

	// Open a connection to the voice server
	err := voice.open()
	if err != nil {
		s.log(LogError, "onVoiceServerUpdate voice.open, %s", err)
	}
}

type identifyOp struct {
	Op   int      `json:"op"`
	Data Identify `json:"d"`
}

// identify sends the identify packet to the gateway
func (s *Session) identify(wsConn *websocket.Conn) error {
	s.log(LogDebug, "called")

	// TODO: This is a temporary block of code to help
	// maintain backwards compatibility
	if s.Compress == false {
		s.Identify.Compress = false
	}

	// TODO: This is a temporary block of code to help
	// maintain backwards compatibility
	if s.Token != "" && s.Identify.Token == "" {
		s.Identify.Token = s.Token
	}

	// TODO: Below block should be refactored so ShardID and ShardCount
	// can be deprecated and their usage moved to the Session.Identify
	// struct
	if s.ShardCount > 1 {

		if s.ShardID >= s.ShardCount {
			return ErrWSShardBounds
		}

		s.Identify.Shard = &[2]int{s.ShardID, s.ShardCount}
	}

	// Send Identify packet to Discord
	op := identifyOp{2, s.Identify}
	s.log(LogDebug, "Identify Packet: \n%#v", redactedIdentify(op))

	return s.writeGatewayStartupPacket(wsConn, op)
}

// writeGatewayStartupPacket writes while Open temporarily drops the session lock.
func (s *Session) writeGatewayStartupPacket(wsConn *websocket.Conn, data interface{}) error {
	s.Unlock()
	s.wsMutex.Lock()
	err := writeGatewayJSONWithDeadline(wsConn, data)
	s.wsMutex.Unlock()
	s.Lock()
	if err != nil {
		return err
	}
	if s.wsConn != wsConn {
		return ErrWSNotFound
	}
	return nil
}

func redactedIdentify(op identifyOp) identifyOp {
	if op.Data.Token != "" {
		op.Data.Token = redactedValue
	}

	return op
}

func (s *Session) reconnectVoiceConnections() {
	s.RLock()
	voiceConnections := make([]*VoiceConnection, 0, len(s.VoiceConnections))
	for _, voice := range s.VoiceConnections {
		if voice != nil {
			voiceConnections = append(voiceConnections, voice)
		}
	}
	s.RUnlock()

	for i, voice := range voiceConnections {
		s.log(LogInformational, "reconnecting voice connection to guild %s", voice.GuildID)
		go voice.reconnect()

		// This is here just to prevent violently spamming the
		// voice reconnects.
		if i < len(voiceConnections)-1 {
			time.Sleep(1 * time.Second)
		}
	}
}

func (s *Session) disconnectVoiceConnections() {
	s.RLock()
	voiceConnections := make([]*VoiceConnection, 0, len(s.VoiceConnections))
	for _, voice := range s.VoiceConnections {
		if voice != nil {
			voiceConnections = append(voiceConnections, voice)
		}
	}
	s.RUnlock()

	for _, voice := range voiceConnections {
		_ = voice.Disconnect()
	}
}

func (s *Session) reconnect() {

	s.log(LogInformational, "called")

	var err error
	cancel := s.reconnectCancelSignal()

	if s.ShouldReconnectOnError {

		wait := time.Duration(1)

		for {
			if reconnectCanceled(cancel) {
				s.log(LogInformational, "reconnect canceled")
				return
			}

			s.log(LogInformational, "trying to reconnect to gateway")

			err = s.Open()
			if err == nil {
				if reconnectCanceled(cancel) {
					// Close() cancels the reconnect and tears down the
					// current connection in one critical section, so
					// the connection this Open created is already
					// closed - and the current one may belong to a
					// newer user-initiated Open() that must survive.
					s.log(LogInformational, "reconnect canceled after opening gateway")
					return
				}

				s.log(LogInformational, "successfully reconnected to gateway")

				// I'm not sure if this is actually needed.
				// if the gw reconnect works properly, voice should stay alive
				// However, there seems to be cases where something "weird"
				// happens.  So we're doing this for now just to improve
				// stability in those edge cases.
				if s.ShouldReconnectVoiceOnSessionError {
					s.reconnectVoiceConnections()
				}
				return
			}

			// Certain race conditions can call reconnect() twice. If this happens, we
			// just break out of the reconnect loop
			if err == ErrWSAlreadyOpen {
				s.log(LogInformational, "Websocket already exists, no need to reconnect")
				return
			}

			if !shouldReconnectOnGatewayClose(err) {
				s.log(LogInformational, "not reconnecting after terminal gateway close, %s", err)
				return
			}

			s.log(LogError, "error reconnecting to gateway, %s", err)

			timer := time.NewTimer(wait * time.Second)
			select {
			case <-timer.C:
			case <-cancel:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				s.log(LogInformational, "reconnect canceled during backoff")
				return
			}
			wait *= 2
			if wait > 600 {
				wait = 600
			}
		}
	}
}

func (s *Session) reconnectCancelSignal() <-chan struct{} {
	s.Lock()
	defer s.Unlock()

	if s.reconnectCancel == nil {
		s.reconnectCancel = make(chan struct{})
	}

	return s.reconnectCancel
}

func (s *Session) ensureReconnectCancelLocked() {
	if s.reconnectCancel == nil || reconnectCanceled(s.reconnectCancel) {
		s.reconnectCancel = make(chan struct{})
	}
}

func reconnectCanceled(cancel <-chan struct{}) bool {
	if cancel == nil {
		return false
	}

	select {
	case <-cancel:
		return true
	default:
		return false
	}
}

func (s *Session) cancelReconnectLocked() {
	if s.reconnectCancel != nil {
		select {
		case <-s.reconnectCancel:
		default:
			close(s.reconnectCancel)
		}
	}
}

// Close closes the gateway and voice connections and stops their goroutines.
func (s *Session) Close() error {
	return s.CloseWithCode(websocket.CloseNormalClosure)
}

// CloseWithCode closes the gateway and voice connections using the provided
// gateway closeCode and stops their goroutines.
func (s *Session) CloseWithCode(closeCode int) (err error) {
	return s.closeWithCode(closeCode, true)
}

func (s *Session) closeWithCode(closeCode int, cancelReconnect bool) (err error) {
	_, err = s.closeGatewayConnection(nil, closeCode, cancelReconnect, false)
	return
}

// closeWithCodeForConnection closes the session only if wsConn is still the
// current gateway connection.
func (s *Session) closeWithCodeForConnection(wsConn *websocket.Conn, closeCode int, resetGatewayResume bool) (closed bool, err error) {
	return s.closeGatewayConnection(wsConn, closeCode, false, resetGatewayResume)
}

func (s *Session) closeGatewayConnection(wsConn *websocket.Conn, closeCode int, cancelReconnect, resetGatewayResume bool) (closed bool, err error) {

	s.log(LogInformational, "called")
	s.Lock()
	if wsConn != nil && s.wsConn != wsConn {
		s.Unlock()
		return false, nil
	}

	if cancelReconnect {
		s.cancelReconnectLocked()
	}

	s.DataReady = false

	if resetGatewayResume || shouldResetGatewayResumeStateOnCloseCode(closeCode) {
		s.resetGatewayResumeStateLocked()
	}

	if s.listening != nil {
		s.log(LogInformational, "closing listening channel")
		close(s.listening)
		s.listening = nil
	}

	if s.wsConn != nil {
		s.gatewaySendRateLimiter.stop(s.wsConn)

		s.log(LogInformational, "sending close frame")
		// To cleanly close a connection, a client should send a close
		// frame and wait for the server to close the connection.
		err := writeWebsocketCloseFrame(s.wsConn, closeCode)
		if err != nil {
			s.log(LogInformational, "error closing websocket, %s", err)
		}

		s.log(LogInformational, "closing gateway websocket")
		err = s.wsConn.Close()
		if err != nil {
			s.log(LogInformational, "error closing websocket, %s", err)
		}

		s.wsConn = nil
	}

	s.Unlock()
	if cancelReconnect {
		s.disconnectVoiceConnections()
	}

	s.log(LogInformational, "emit disconnect event")
	s.handleEvent(disconnectEventType, &Disconnect{})

	return true, err
}
