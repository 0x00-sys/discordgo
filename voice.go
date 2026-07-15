// Discordgo - Discord bindings for Go
// Available at https://github.com/bwmarrin/discordgo

// Copyright 2015-2016 Bruce Marriner <bruce@sqls.net>.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains code related to Discord voice suppport

package discordgo

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/chacha20poly1305"
)

// ------------------------------------------------------------------------------------------------
// Code related to both VoiceConnection Websocket and UDP connections.
// ------------------------------------------------------------------------------------------------

// A VoiceConnection struct holds all the data and functions related to a Discord Voice Connection.
type VoiceConnection struct {
	sync.RWMutex
	lifecycleMu sync.Mutex

	Debug         bool // If true, print extra logging -- DEPRECATED
	LogLevel      int
	Ready         bool // If true, voice is ready to send/receive audio
	UserID        string
	GuildID       string
	ChannelID     string
	deaf          bool
	mute          bool
	speaking      bool
	reconnecting  bool // If true, voice connection is trying to reconnect
	disconnecting bool // If true, voice connection is being permanently torn down

	OpusSend chan []byte  // Chan for sending opus audio
	OpusRecv chan *Packet // Chan for receiving opus audio

	wsConn  *websocket.Conn
	wsMutex sync.Mutex
	udpConn *net.UDPConn
	session *Session

	sessionID string
	token     string
	endpoint  string

	// Used to send a close signal to goroutines
	close chan struct{}

	// Used to allow blocking until connected
	connected chan bool

	// Used to pass the sessionid from onVoiceStateUpdate
	// sessionRecv chan string UNUSED ATM

	aead           cipher.AEAD
	encryptionMode string
	nonceCounter   uint32
	sequence       int64
	sequenceSet    bool

	daveMu                      sync.Mutex
	dave                        VoiceDAVESession
	daveGeneration              uint64
	daveMaxProtocolVersion      int
	daveProtocolVersion         uint16
	davePreparedProtocolVersion uint16
	davePendingTransitions      map[uint16]uint16
	daveUsers                   map[string]struct{}
	daveSSRCToUserID            map[uint32]string

	op4 voiceOP4
	op2 voiceOP2

	voiceSpeakingUpdateHandlers []VoiceSpeakingUpdateHandler
}

// VoiceSpeakingUpdateHandler type provides a function definition for the
// VoiceSpeakingUpdate event
type VoiceSpeakingUpdateHandler func(vc *VoiceConnection, vs *VoiceSpeakingUpdate)

const (
	voiceGatewayVersion               = 8
	voiceModeAES256GCMRTPSize         = "aead_aes256_gcm_rtpsize"
	voiceModeXChaCha20Poly1305RTPSize = "aead_xchacha20_poly1305_rtpsize"
	voiceOpusSilenceFrame             = "\xf8\xff\xfe"
	voiceOpusSilenceFrameCount        = 5
)

// VoiceSpeakingFlags describes the type of audio being transmitted.
type VoiceSpeakingFlags int

// Valid VoiceSpeakingFlags values.
const (
	VoiceSpeakingFlagMicrophone VoiceSpeakingFlags = 1 << iota
	VoiceSpeakingFlagSoundshare
	VoiceSpeakingFlagPriority
)

// Speaking sends a speaking notification to Discord over the voice websocket.
// This must be sent as true prior to sending audio and should be set to false
// once finished sending audio.
// b : Send true if speaking, false if not.
func (v *VoiceConnection) Speaking(b bool) (err error) {
	flags := VoiceSpeakingFlags(0)
	if b {
		flags = VoiceSpeakingFlagMicrophone
	}
	return v.SpeakingFlags(flags)
}

// SpeakingFlags sends a speaking notification with the provided audio flags.
func (v *VoiceConnection) SpeakingFlags(flags VoiceSpeakingFlags) (err error) {

	v.log(LogDebug, "called (%d)", flags)

	type voiceSpeakingData struct {
		Speaking VoiceSpeakingFlags `json:"speaking"`
		Delay    int                `json:"delay"`
		SSRC     uint32             `json:"ssrc"`
	}

	type voiceSpeakingOp struct {
		Op   int               `json:"op"` // Always 5
		Data voiceSpeakingData `json:"d"`
	}

	// Snapshot the connection instead of holding the write lock across
	// the blocking websocket write; a stalled write must not wedge
	// Close(), reconnect() and the heartbeat behind v.Lock.
	v.RLock()
	wsConn := v.wsConn
	ssrc := v.op2.SSRC
	v.RUnlock()
	if wsConn == nil {
		return fmt.Errorf("no VoiceConnection websocket")
	}

	data := voiceSpeakingOp{5, voiceSpeakingData{flags, 0, ssrc}}
	v.wsMutex.Lock()
	err = wsConn.WriteJSON(data)
	v.wsMutex.Unlock()

	v.Lock()
	defer v.Unlock()
	if err != nil {
		v.speaking = false
		v.log(LogError, "Speaking() write json error, %s", err)
		return
	}

	v.speaking = flags != 0

	return
}

// ChangeChannel sends Discord a request to change channels within a Guild
// !!! NOTE !!! This function may be removed in favour of just using ChannelVoiceJoin
func (v *VoiceConnection) ChangeChannel(channelID string, mute, deaf bool) (err error) {

	v.log(LogInformational, "called")

	for {
		v.RLock()
		session := v.session
		v.RUnlock()
		if session == nil {
			return ErrWSNotFound
		}

		session.voiceMutex.Lock()
		v.RLock()
		if v.session != session {
			v.RUnlock()
			session.voiceMutex.Unlock()
			continue
		}
		if v.disconnecting {
			v.RUnlock()
			session.voiceMutex.Unlock()
			return ErrWSNotFound
		}
		guildID := v.GuildID
		v.RUnlock()

		err = session.channelVoiceJoinManual(guildID, channelID, mute, deaf)
		if err == nil {
			v.Lock()
			v.ChannelID = channelID
			v.deaf = deaf
			v.mute = mute
			v.speaking = false
			v.Unlock()
		}
		session.voiceMutex.Unlock()
		return
	}
}

// Disconnect disconnects from this voice channel and closes the websocket
// and udp connections to Discord.
func (v *VoiceConnection) Disconnect() (err error) {

	var session *Session
	var sessionID, guildID string
	for {
		// Wait for the voice state operation slot without holding the voice
		// lock. A stalled voice lock must not block unrelated gateway writes.
		v.RLock()
		session = v.session
		v.RUnlock()
		if session != nil {
			session.voiceMutex.Lock()
		}

		v.Lock()
		if v.session != session {
			v.Unlock()
			if session != nil {
				session.voiceMutex.Unlock()
			}
			continue
		}

		sessionID = v.sessionID
		guildID = v.GuildID
		v.sessionID = ""
		v.disconnecting = true
		v.Unlock()
		break
	}

	// Unregister before the blocking gateway write so a new join uses a
	// replacement connection. Only remove this connection's own entry.
	if session != nil {
		session.Lock()
		if session.VoiceConnections[guildID] == v {
			delete(session.VoiceConnections, guildID)
		}
		session.Unlock()
	}

	// Send an OP4 with a nil channel to disconnect.
	if sessionID != "" {
		if session == nil {
			err = ErrWSNotFound
		} else {
			err = session.channelVoiceJoinManual(guildID, "", true, true)
		}
	}
	if session != nil {
		session.voiceMutex.Unlock()
	}

	// Close websocket and udp connections
	v.Close()

	// The connection is being torn down for good; drop the opus
	// channels so a future join starts fresh. Internal reconnects go
	// through Close() directly and must keep the channels stable for
	// any user pipelines attached to them.
	v.Lock()
	v.OpusSend = nil
	v.OpusRecv = nil
	v.Unlock()

	v.log(LogInformational, "Deleting VoiceConnection %s", guildID)

	return
}

// Close closes the voice ws and udp connections
func (v *VoiceConnection) Close() {
	v.lifecycleMu.Lock()
	defer v.lifecycleMu.Unlock()

	v.log(LogInformational, "called")

	v.Lock()

	v.Ready = false
	v.speaking = false

	if v.close != nil {
		v.log(LogInformational, "closing v.close")
		close(v.close)
		v.close = nil
	}

	if v.udpConn != nil {
		v.log(LogInformational, "closing udp")
		err := v.udpConn.Close()
		if err != nil {
			v.log(LogError, "error closing udp connection, %s", err)
		}
		v.udpConn = nil
	}

	if v.wsConn != nil {
		v.log(LogInformational, "sending close frame")

		err := writeWebsocketCloseFrame(v.wsConn, websocket.CloseNormalClosure)
		if err != nil {
			v.log(LogError, "error closing websocket, %s", err)
		}

		v.log(LogInformational, "closing websocket")
		err = v.wsConn.Close()
		if err != nil {
			v.log(LogError, "error closing websocket, %s", err)
		}

		v.wsConn = nil
	}
	v.Unlock()

	// Media goroutines must be stopped before the DAVE session is cleared;
	// otherwise an in-flight sender could observe protocol version 0 and leak
	// a plaintext frame during teardown.
	v.closeVoiceDAVE()
}

// AddHandler adds a Handler for VoiceSpeakingUpdate events.
func (v *VoiceConnection) AddHandler(h VoiceSpeakingUpdateHandler) {
	v.Lock()
	defer v.Unlock()

	v.voiceSpeakingUpdateHandlers = append(v.voiceSpeakingUpdateHandlers, h)
}

// VoiceSpeakingUpdate is a struct for a VoiceSpeakingUpdate event.
type VoiceSpeakingUpdate struct {
	UserID        string             `json:"user_id"`
	SSRC          int                `json:"ssrc"`
	Speaking      bool               `json:"-"`
	SpeakingFlags VoiceSpeakingFlags `json:"speaking"`
}

// UnmarshalJSON supports current integer speaking flags and legacy boolean payloads.
func (v *VoiceSpeakingUpdate) UnmarshalJSON(data []byte) error {
	var payload struct {
		UserID   string          `json:"user_id"`
		SSRC     int             `json:"ssrc"`
		Speaking json.RawMessage `json:"speaking"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}

	var flags VoiceSpeakingFlags
	if len(payload.Speaking) > 0 {
		if payload.Speaking[0] == 't' || payload.Speaking[0] == 'f' {
			var speaking bool
			if err := json.Unmarshal(payload.Speaking, &speaking); err != nil {
				return err
			}
			if speaking {
				flags = VoiceSpeakingFlagMicrophone
			}
		} else if err := json.Unmarshal(payload.Speaking, &flags); err != nil {
			return err
		}
	}

	v.UserID = payload.UserID
	v.SSRC = payload.SSRC
	v.SpeakingFlags = flags
	v.Speaking = flags != 0
	return nil
}

// ------------------------------------------------------------------------------------------------
// Unexported Internal Functions Below.
// ------------------------------------------------------------------------------------------------

// A voiceOP4 stores the data for the voice operation 4 websocket event
// which provides the negotiated transport encryption key.
type voiceOP4 struct {
	SecretKey           []int  `json:"secret_key"`
	Mode                string `json:"mode"`
	DAVEProtocolVersion uint16 `json:"dave_protocol_version"`
}

// A voiceOP2 stores the data for the voice operation 2 websocket event
// which is sort of like the voice READY packet
type voiceOP2 struct {
	SSRC              uint32        `json:"ssrc"`
	Port              int           `json:"port"`
	Modes             []string      `json:"modes"`
	HeartbeatInterval time.Duration `json:"heartbeat_interval"`
	IP                string        `json:"ip"`
}

type voiceOP8 struct {
	HeartbeatInterval time.Duration `json:"heartbeat_interval"`
}

// WaitUntilConnected waits for the Voice Connection to
// become ready, if it does not become ready it returns an err
func (v *VoiceConnection) waitUntilConnected() error {

	v.log(LogInformational, "called")

	i := 0
	for {
		v.RLock()
		ready := v.Ready
		v.RUnlock()
		if ready {
			return nil
		}

		if i > 10 {
			return fmt.Errorf("timeout waiting for voice")
		}

		time.Sleep(1 * time.Second)
		i++
	}
}

// Open opens a voice connection.  This should be called
// after VoiceChannelJoin is used and the data VOICE websocket events
// are captured.
func (v *VoiceConnection) open() (err error) {
	v.lifecycleMu.Lock()
	defer v.lifecycleMu.Unlock()

	v.log(LogInformational, "called")

	v.Lock()
	// Don't open a websocket if one is already open
	if v.wsConn != nil {
		v.log(LogWarning, "refusing to overwrite non-nil websocket")
		v.Unlock()
		return
	}

	// TODO temp? loop to wait for the SessionID
	i := 0
	for {
		if v.sessionID != "" {
			break
		}

		if i > 20 { // only loop for up to 1 second total
			v.Unlock()
			return fmt.Errorf("did not receive voice Session ID in time")
		}
		// Release the lock, so sessionID can be populated upon receiving a VoiceStateUpdate event.
		v.Unlock()
		time.Sleep(50 * time.Millisecond)
		i++
		v.Lock()
	}
	session := v.session
	userID := v.UserID
	channelID := v.ChannelID
	v.Unlock()
	if session == nil {
		return ErrWSNotFound
	}

	session.RLock()
	factory := session.VoiceDAVESessionFactory
	dialer := session.Dialer
	session.RUnlock()
	if dialer == nil {
		return ErrWSNotFound
	}

	daveSession, daveCallbacks, maxDAVEVersion, err := v.newVoiceDAVESession(factory, userID, channelID)
	if err != nil {
		return err
	}
	closeUninstalledDAVE := func() {
		if daveSession == nil {
			return
		}
		if closeErr := daveSession.Close(); closeErr != nil {
			v.log(LogError, "DAVE close error, %s", closeErr)
		}
		daveSession = nil
	}

	v.Lock()
	if v.wsConn != nil {
		v.log(LogWarning, "refusing to overwrite non-nil websocket")
		v.Unlock()
		closeUninstalledDAVE()
		return nil
	}
	if v.disconnecting || v.session != session || v.UserID != userID || v.ChannelID != channelID {
		v.Unlock()
		closeUninstalledDAVE()
		return ErrWSNotFound
	}

	// Connect to VoiceConnection Websocket
	v.sequence = 0
	v.sequenceSet = false
	vg := "wss://" + strings.TrimSuffix(v.endpoint, ":80") + "?v=" + strconv.Itoa(voiceGatewayVersion)
	v.log(LogInformational, "connecting to voice endpoint %s", vg)
	wsConn, _, err := dialer.Dial(vg, nil)
	if err != nil {
		v.Unlock()
		closeUninstalledDAVE()
		v.log(LogWarning, "error connecting to voice endpoint %s, %s", vg, err)
		v.log(LogDebug, "voice struct: %p (details redacted)\n", v)
		return
	}
	v.wsConn = wsConn
	v.installVoiceDAVESessionLocked(daveSession, daveCallbacks, maxDAVEVersion)

	type voiceHandshakeData struct {
		ServerID               string `json:"server_id"`
		UserID                 string `json:"user_id"`
		SessionID              string `json:"session_id"`
		Token                  string `json:"token"`
		MaxDAVEProtocolVersion int    `json:"max_dave_protocol_version"`
	}
	type voiceHandshakeOp struct {
		Op   int                `json:"op"` // Always 0
		Data voiceHandshakeData `json:"d"`
	}
	data := voiceHandshakeOp{0, voiceHandshakeData{v.GuildID, v.UserID, v.sessionID, v.token, maxDAVEVersion}}

	v.wsMutex.Lock()
	err = wsConn.WriteJSON(data)
	v.wsMutex.Unlock()
	if err != nil {
		v.wsConn = nil
		v.Unlock()
		_ = wsConn.Close()
		v.closeVoiceDAVE()
		v.log(LogWarning, "error sending init packet, %s", err)
		return
	}

	v.close = make(chan struct{})
	closeChan := v.close
	v.Unlock()
	go v.wsListen(wsConn, closeChan)

	// add loop/check for Ready bool here?
	// then return false if not ready?
	// but then wsListen will also err.

	return
}

// wsListen listens on the voice websocket for messages and passes them
// to the voice event handler.  This is automatically called by the Open func
func (v *VoiceConnection) wsListen(wsConn *websocket.Conn, close <-chan struct{}) {

	v.log(LogInformational, "called")

	for {
		messageType, message, err := wsConn.ReadMessage()
		if err != nil {
			select {
			case <-close:
				return
			default:
			}

			closeErr, isCloseErr := err.(*websocket.CloseError)

			// 4014 indicates a manual disconnection by someone in the guild;
			// 4017 indicates that the server requires DAVE support;
			// 4021 indicates that the voice connection was dropped due to rate limiting;
			// 4022 indicates that the call was terminated.
			// we shouldn't reconnect.
			if isCloseErr && (closeErr.Code == 4014 || closeErr.Code == 4017 || closeErr.Code == 4021 || closeErr.Code == 4022) {
				v.log(LogInformational, "received %d manual disconnection", closeErr.Code)

				// Abandon the voice WS connection
				v.Lock()
				if v.wsConn == wsConn {
					v.wsConn = nil
				}
				v.Unlock()

				if closeErr.Code == 4014 {
					// Wait for VOICE_SERVER_UPDATE.
					// When the bot is moved by the user to another voice channel,
					// VOICE_SERVER_UPDATE is received after the code 4014.
					for i := 0; i < 5; i++ { // TODO: temp, wait for VoiceServerUpdate.
						<-time.After(1 * time.Second)

						v.RLock()
						reconnected := v.wsConn != nil
						v.RUnlock()
						if !reconnected {
							continue
						}
						v.log(LogInformational, "successfully reconnected after 4014 manual disconnection")
						return
					}
				}

				// When VOICE_SERVER_UPDATE is not received, disconnect as usual.
				v.log(LogInformational, "disconnect due to %d manual disconnection", closeErr.Code)

				v.session.Lock()
				delete(v.session.VoiceConnections, v.GuildID)
				v.session.Unlock()

				v.Close()

				return
			}

			// Detect if we have been closed manually. If a Close() has already
			// happened, the websocket we are listening on will be different to the
			// current session.
			v.Lock()
			sameConnection := v.wsConn == wsConn
			if sameConnection {
				v.wsConn = nil
			}
			v.Unlock()
			if sameConnection {

				v.log(LogError, "voice endpoint %s websocket closed unexpectedly, %s", v.endpoint, err)

				// Start reconnect goroutine then exit.
				canResume := !isCloseErr || closeErr.Code == 4015 || closeErr.Code < 4000
				go v.reconnectWithResume(canResume)
			}
			return
		}

		// Pass received message to voice event handler
		select {
		case <-close:
			return
		default:
			v.dispatchVoiceMessage(messageType, message)
		}
	}
}

func (v *VoiceConnection) updateSequence(message []byte) {
	var payload struct {
		Sequence *int64 `json:"seq"`
	}
	if err := json.Unmarshal(message, &payload); err != nil || payload.Sequence == nil {
		return
	}
	v.updateVoiceSequence(*payload.Sequence)
}

func (v *VoiceConnection) updateVoiceSequence(sequence int64) {
	v.Lock()
	v.sequence = sequence
	v.sequenceSet = true
	v.Unlock()
}

func (v *VoiceConnection) voiceSequenceAck() int64 {
	v.RLock()
	defer v.RUnlock()
	if !v.sequenceSet {
		return -1
	}
	return v.sequence
}

// wsEvent handles any voice websocket events. This is only called by the
// wsListen() function.
func (v *VoiceConnection) onEvent(message []byte) {

	v.log(LogDebug, "received: %s", redactedVoiceData(message))

	var e Event
	if err := json.Unmarshal(message, &e); err != nil {
		v.log(LogError, "unmarshall error, %s", err)
		return
	}

	switch e.Operation {

	case 2: // READY

		op2 := voiceOP2{}
		if err := json.Unmarshal(e.RawData, &op2); err != nil {
			v.log(LogError, "OP2 unmarshall error, %s, %s", err, redactedVoiceData(e.RawData))
			v.failVoiceEncryption()
			return
		}
		mode, err := selectVoiceEncryptionMode(op2.Modes)
		if err != nil {
			v.log(LogError, "OP2 encryption mode error, %s", err)
			v.failVoiceEncryption()
			return
		}

		v.Lock()
		v.op2 = op2
		v.aead = nil
		v.encryptionMode = mode
		v.nonceCounter = 0
		v.op4 = voiceOP4{}
		v.Ready = false
		v.Unlock()
		v.onDAVELocalSSRC(op2.SSRC)

		// Start the UDP connection
		err = v.udpOpen()
		if err != nil {
			v.log(LogError, "error opening udp connection, %s", err)
			return
		}

		return

	case 3: // HEARTBEAT response
		// add code to use this to track latency?
		// TODO: maybe actually implement this, seems cool
		return

	case 4: // udp encryption secret key
		op4 := voiceOP4{}
		if err := json.Unmarshal(e.RawData, &op4); err != nil {
			v.log(LogError, "OP4 unmarshall error, %s, %s", err, redactedVoiceData(e.RawData))
			v.failVoiceEncryption()
			return
		}

		var udpConn *net.UDPConn
		var closeChan chan struct{}
		var opusSend chan []byte
		var opusRecv chan *Packet
		startTransport := false
		receive := false

		v.Lock()
		if op4.Mode != v.encryptionMode {
			selectedMode := v.encryptionMode
			v.Unlock()
			v.log(LogError, "OP4 encryption mode %q does not match selected mode %q", op4.Mode, selectedMode)
			v.failVoiceEncryption()
			return
		}

		aead, err := newVoiceAEAD(op4.Mode, op4.SecretKey)
		if err != nil {
			v.Unlock()
			v.log(LogError, "OP4 encryption setup error, %s", err)
			v.failVoiceEncryption()
			return
		}

		v.op4 = op4
		v.aead = aead
		if !v.Ready && v.udpConn != nil && v.close != nil {
			if v.OpusSend == nil {
				v.OpusSend = make(chan []byte, 2)
			}
			if !v.deaf && v.OpusRecv == nil {
				v.OpusRecv = make(chan *Packet, 2)
			}

			udpConn = v.udpConn
			closeChan = v.close
			opusSend = v.OpusSend
			opusRecv = v.OpusRecv
			receive = !v.deaf
			startTransport = true
			v.Ready = true
		}
		v.Unlock()
		if err := v.onDAVESessionDescription(op4.DAVEProtocolVersion); err != nil {
			v.log(LogError, "OP4 DAVE setup error, %s", err)
			v.failVoiceEncryption()
			return
		}

		if startTransport {
			go v.opusSender(udpConn, closeChan, opusSend, 48000, 960)
			if receive {
				go v.opusReceiver(udpConn, closeChan, opusRecv)
			}
		}

		return

	case 5:
		voiceSpeakingUpdate := &VoiceSpeakingUpdate{}
		if err := json.Unmarshal(e.RawData, voiceSpeakingUpdate); err != nil {
			v.log(LogError, "OP5 unmarshall error, %s, %s", err, redactedVoiceData(e.RawData))
			return
		}
		v.onDAVESpeakingUpdate(voiceSpeakingUpdate)

		v.RLock()
		handlers := append([]VoiceSpeakingUpdateHandler(nil), v.voiceSpeakingUpdateHandlers...)
		v.RUnlock()

		if len(handlers) == 0 {
			return
		}

		for _, h := range handlers {
			v.callVoiceSpeakingUpdateHandler(h, voiceSpeakingUpdate)
		}

	case 9: // RESUMED
		return

	case 11: // CLIENTS CONNECT
		v.onDAVEClientsConnect(e.RawData)
		return

	case 13: // CLIENT DISCONNECT
		v.onDAVEClientDisconnect(e.RawData)
		return

	case voiceOpDAVEPrepareTransition:
		v.onDAVEPrepareTransition(e.RawData)
		return

	case voiceOpDAVEExecuteTransition:
		v.onDAVEExecuteTransition(e.RawData)
		return

	case voiceOpDAVEPrepareEpoch:
		v.onDAVEPrepareEpoch(e.RawData)
		return

	case 8:
		hello := voiceOP8{}
		if err := json.Unmarshal(e.RawData, &hello); err != nil {
			v.log(LogError, "OP8 unmarshall error, %s, %s", err, redactedVoiceData(e.RawData))
			return
		}

		v.RLock()
		wsConn := v.wsConn
		closeChan := v.close
		v.RUnlock()

		// Start the voice websocket heartbeat to keep the connection alive
		go v.wsHeartbeat(wsConn, closeChan, hello.HeartbeatInterval)
		// TODO monitor a chan/bool to verify this was successful

	default:
		v.log(LogDebug, "unknown voice operation, %d, %s", e.Operation, redactedVoiceData(e.RawData))
	}

	return
}

func (v *VoiceConnection) callVoiceSpeakingUpdateHandler(h VoiceSpeakingUpdateHandler, vs *VoiceSpeakingUpdate) {
	defer func() {
		if err := recover(); err != nil {
			v.log(LogError, "panic in VoiceSpeakingUpdate handler, %v", err)
		}
	}()

	h(v, vs)
}

func (v *VoiceConnection) failVoiceEncryption() {
	v.Lock()
	v.aead = nil
	v.encryptionMode = ""
	v.nonceCounter = 0
	v.op4 = voiceOP4{}
	v.Unlock()
	v.Close()
}

func selectVoiceEncryptionMode(modes []string) (string, error) {
	hasXChaCha := false
	for _, mode := range modes {
		switch mode {
		case voiceModeAES256GCMRTPSize:
			return mode, nil
		case voiceModeXChaCha20Poly1305RTPSize:
			hasXChaCha = true
		}
	}
	if hasXChaCha {
		return voiceModeXChaCha20Poly1305RTPSize, nil
	}
	return "", fmt.Errorf("no supported voice encryption mode")
}

func newVoiceAEAD(mode string, secretKey []int) (cipher.AEAD, error) {
	if len(secretKey) != chacha20poly1305.KeySize {
		return nil, fmt.Errorf("invalid voice secret key length %d", len(secretKey))
	}

	key := make([]byte, len(secretKey))
	for i, value := range secretKey {
		if value < 0 || value > 255 {
			return nil, fmt.Errorf("invalid voice secret key byte at index %d", i)
		}
		key[i] = byte(value)
	}

	switch mode {
	case voiceModeAES256GCMRTPSize:
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, fmt.Errorf("creating aes cipher: %w", err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("creating aes-gcm: %w", err)
		}
		return aead, nil
	case voiceModeXChaCha20Poly1305RTPSize:
		aead, err := chacha20poly1305.NewX(key)
		if err != nil {
			return nil, fmt.Errorf("creating xchacha20-poly1305: %w", err)
		}
		return aead, nil
	default:
		return nil, fmt.Errorf("unsupported voice encryption mode %q", mode)
	}
}

func setVoiceNonce(nonce []byte, counter uint32) {
	binary.BigEndian.PutUint32(nonce[:4], counter)
}

func redactedVoiceData(data []byte) string {
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return string(data)
	}

	redactVoiceValue(v)

	b, err := json.Marshal(v)
	if err != nil {
		return string(data)
	}

	return string(b)
}

func redactVoiceValue(v interface{}) {
	switch value := v.(type) {
	case map[string]interface{}:
		for k, nested := range value {
			if isVoiceSecretKey(k) {
				value[k] = "[REDACTED]"
				continue
			}
			redactVoiceValue(nested)
		}
	case []interface{}:
		for _, nested := range value {
			redactVoiceValue(nested)
		}
	}
}

func isVoiceSecretKey(key string) bool {
	switch key {
	case "token", "secret_key", "access_token", "refresh_token":
		return true
	}

	return false
}

type voiceHeartbeatOp struct {
	Op   int                `json:"op"` // Always 3
	Data voiceHeartbeatData `json:"d"`
}

type voiceHeartbeatData struct {
	Timestamp   int64 `json:"t"`
	SequenceAck int64 `json:"seq_ack"`
}

// NOTE :: When a guild voice server changes how do we shut this down
// properly, so a new connection can be setup without fuss?
//
// wsHeartbeat sends regular heartbeats to voice Discord so it knows the client
// is still connected.  If you do not send these heartbeats Discord will
// disconnect the websocket connection after a few seconds.
func (v *VoiceConnection) wsHeartbeat(wsConn *websocket.Conn, close <-chan struct{}, i time.Duration) {

	if close == nil || wsConn == nil || i <= 0 {
		return
	}

	var err error
	ticker := time.NewTicker(i * time.Millisecond)
	defer ticker.Stop()
	for {
		v.log(LogDebug, "sending heartbeat packet")
		sequenceAck := v.voiceSequenceAck()
		v.wsMutex.Lock()
		err = wsConn.WriteJSON(voiceHeartbeatOp{3, voiceHeartbeatData{
			Timestamp:   time.Now().UnixNano() / int64(time.Millisecond),
			SequenceAck: sequenceAck,
		}})
		v.wsMutex.Unlock()
		if err != nil {
			v.log(LogError, "error sending heartbeat to voice endpoint %s, %s", v.endpoint, err)
			return
		}

		select {
		case <-ticker.C:
			// continue loop and send heartbeat
		case <-close:
			return
		}
	}
}

// ------------------------------------------------------------------------------------------------
// Code related to the VoiceConnection UDP connection
// ------------------------------------------------------------------------------------------------

type voiceUDPData struct {
	Address string `json:"address"` // Public IP of machine running this code
	Port    uint16 `json:"port"`    // UDP Port of machine running this code
	Mode    string `json:"mode"`
}

type voiceUDPD struct {
	Protocol string       `json:"protocol"` // Always "udp" ?
	Data     voiceUDPData `json:"data"`
}

type voiceUDPOp struct {
	Op   int       `json:"op"` // Always 1
	Data voiceUDPD `json:"d"`
}

var voiceUDPReadTimeout = 5 * time.Second

// udpOpen opens a UDP connection to the voice server and completes the
// initial required handshake.  This connection is left open in the session
// and can be used to send or receive audio.  This should only be called
// from voice.wsEvent OP2
func (v *VoiceConnection) udpOpen() (err error) {

	v.Lock()
	defer v.Unlock()

	if v.wsConn == nil {
		return fmt.Errorf("nil voice websocket")
	}

	if v.udpConn != nil {
		return fmt.Errorf("udp connection already open")
	}

	if v.close == nil {
		return fmt.Errorf("nil close channel")
	}

	if v.endpoint == "" {
		return fmt.Errorf("empty endpoint")
	}
	if v.encryptionMode == "" {
		return fmt.Errorf("empty voice encryption mode")
	}

	host := v.op2.IP + ":" + strconv.Itoa(v.op2.Port)
	addr, err := net.ResolveUDPAddr("udp", host)
	if err != nil {
		v.log(LogWarning, "error resolving udp host %s, %s", host, err)
		return
	}

	v.log(LogInformational, "connecting to udp addr %s", addr.String())
	v.udpConn, err = net.DialUDP("udp", nil, addr)
	if err != nil {
		v.log(LogWarning, "error connecting to udp addr %s, %s", addr.String(), err)
		return
	}
	defer func() {
		if err != nil {
			_ = v.udpConn.Close()
			v.udpConn = nil
		}
	}()

	// Create a 74 byte array to store the packet data
	sb := make([]byte, 74)
	binary.BigEndian.PutUint16(sb, 1)              // Packet type (0x1 is request, 0x2 is response)
	binary.BigEndian.PutUint16(sb[2:], 70)         // Packet length (excluding type and length fields)
	binary.BigEndian.PutUint32(sb[4:], v.op2.SSRC) // The SSRC code from the Op 2 VoiceConnection event

	// And send that data over the UDP connection to Discord.
	_, err = v.udpConn.Write(sb)
	if err != nil {
		v.log(LogWarning, "udp write error to %s, %s", addr.String(), err)
		return
	}

	// Create a 74-byte array and listen for the initial handshake response
	// from Discord.  Once we get it parse the IP and PORT information out
	// of the response.  This should be our public IP and PORT as Discord
	// saw us.
	rb := make([]byte, 74)
	err = v.udpConn.SetReadDeadline(time.Now().Add(voiceUDPReadTimeout))
	if err != nil {
		v.log(LogWarning, "udp read deadline error, %s, %s", addr.String(), err)
		return
	}

	rlen, _, err := v.udpConn.ReadFromUDP(rb)
	if deadlineErr := v.udpConn.SetReadDeadline(time.Time{}); deadlineErr != nil && err == nil {
		err = deadlineErr
	}
	if err != nil {
		v.log(LogWarning, "udp read error, %s, %s", addr.String(), err)
		return
	}

	if rlen < 74 {
		v.log(LogWarning, "received udp packet too small")
		return fmt.Errorf("received udp packet too small")
	}

	// Loop over position 8 through 71 to grab the IP address.
	var ip string
	for i := 8; i < len(rb)-2; i++ {
		if rb[i] == 0 {
			break
		}
		ip += string(rb[i])
	}

	// Grab port from position 72 and 73
	port := binary.BigEndian.Uint16(rb[len(rb)-2:])

	// Take the data from above and send it back to Discord to finalize
	// the UDP connection handshake.

	data := voiceUDPOp{1, voiceUDPD{"udp", voiceUDPData{ip, port, v.encryptionMode}}}

	v.wsMutex.Lock()
	err = v.wsConn.WriteJSON(data)
	v.wsMutex.Unlock()
	if err != nil {
		v.log(LogWarning, "udp write error, %#v, %s", data, err)
		return
	}

	// start udpKeepAlive
	go v.udpKeepAlive(v.udpConn, v.close, 5*time.Second)
	// TODO: find a way to check that it fired off okay

	return
}

// udpKeepAlive sends a udp packet to keep the udp connection open
// This is still a bit of a "proof of concept"
func (v *VoiceConnection) udpKeepAlive(udpConn *net.UDPConn, close <-chan struct{}, i time.Duration) {

	if udpConn == nil || close == nil {
		return
	}

	var err error
	var sequence uint64

	packet := make([]byte, 8)

	ticker := time.NewTicker(i)
	defer ticker.Stop()
	for {

		binary.LittleEndian.PutUint64(packet, sequence)
		sequence++

		_, err = udpConn.Write(packet)
		if err != nil {
			v.log(LogError, "write error, %s", err)
			return
		}

		select {
		case <-ticker.C:
			// continue loop and send keepalive
		case <-close:
			return
		}
	}
}

// opusSender will listen on the given channel and send any
// pre-encoded opus audio to Discord.  Supposedly.
func (v *VoiceConnection) opusSender(udpConn *net.UDPConn, close <-chan struct{}, opus <-chan []byte, rate, size int) {

	if udpConn == nil || close == nil || rate <= 0 || size <= 0 {
		return
	}
	frameDuration := time.Second * time.Duration(size) / time.Duration(rate)
	if frameDuration <= 0 {
		return
	}

	defer func() {
		v.Lock()
		v.Ready = false
		v.Unlock()
	}()

	var sequence uint16
	var timestamp uint32
	var recvbuf []byte
	var ok bool
	var err error
	udpHeader := make([]byte, 12)
	var nonce []byte
	var lastPacketAt time.Time
	sentAudio := false
	silenceFrames := 0

	// build the parts that don't change in the udpHeader
	udpHeader[0] = 0x80
	udpHeader[1] = 0x78
	v.RLock()
	ssrc := v.op2.SSRC
	v.RUnlock()
	binary.BigEndian.PutUint32(udpHeader[8:], ssrc)

	// start a send loop that loops until buf chan is closed
	ticker := time.NewTicker(frameDuration)
	defer ticker.Stop()
	for {
		received := false
		paced := false
		trailingSilence := false
		var silenceTick <-chan time.Time
		if sentAudio && silenceFrames < voiceOpusSilenceFrameCount {
			silenceTick = ticker.C
		}

		// Prefer queued audio on each send tick. When the source pauses or
		// closes, send the five silence frames required by Discord before
		// stopping transmission.
		select {
		case <-close:
			return
		case recvbuf, ok = <-opus:
			received = true
		case <-silenceTick:
			paced = true
			select {
			case recvbuf, ok = <-opus:
				received = true
			default:
			}
		}

		if !received || !ok {
			if !sentAudio || silenceFrames >= voiceOpusSilenceFrameCount {
				if received && !ok {
					return
				}
				continue
			}
			recvbuf = []byte(voiceOpusSilenceFrame)
			trailingSilence = true
		}

		v.RLock()
		speaking := v.speaking
		v.RUnlock()
		if !speaking {
			err := v.Speaking(true)
			if err != nil {
				v.log(LogError, "error sending speaking packet, %s", err)
			}
		}

		var daveFrame []byte
		waitedForDAVE := false
		for {
			daveFrame, err = v.encryptDAVEFrame(ssrc, recvbuf)
			if !errors.Is(err, errDAVETransitionPending) {
				break
			}
			waitedForDAVE = true
			select {
			case <-close:
				return
			case <-ticker.C:
			}
		}
		if err != nil {
			v.log(LogDebug, "DAVE encrypt error, %s", err)
			if !waitedForDAVE {
				select {
				case <-close:
					return
				case <-ticker.C:
				}
			}
			continue
		}
		recvbuf = daveFrame

		// Block until this frame's send time unless the ticker or a pending
		// DAVE transition already provided the pacing delay.
		if !paced && !waitedForDAVE {
			select {
			case <-close:
				return
			case <-ticker.C:
			}
		} else {
			select {
			case <-close:
				return
			default:
			}
		}

		packetAt := time.Now()
		if !lastPacketAt.IsZero() {
			timestamp = interpolateVoiceTimestamp(
				timestamp,
				packetAt.Sub(lastPacketAt),
				frameDuration,
				uint32(size),
			)
		}

		// Add sequence and timestamp to udpPacket.
		binary.BigEndian.PutUint16(udpHeader[2:], sequence)
		binary.BigEndian.PutUint32(udpHeader[4:], timestamp)

		// encrypt the opus data
		// add incrementing nonce counter as per discord's requirements
		v.Lock()
		aead := v.aead
		nonceCounter := v.nonceCounter
		if aead != nil {
			v.nonceCounter++
		}
		v.Unlock()
		if aead == nil {
			continue
		}

		if len(nonce) != aead.NonceSize() {
			nonce = make([]byte, aead.NonceSize())
		}
		setVoiceNonce(nonce, nonceCounter)

		sendbuf := aead.Seal(nil, nonce, recvbuf, udpHeader)
		sendbuf = append(sendbuf, nonce[:4]...) // 4 byte nonce to ciphertext appended
		sendbuf = append(udpHeader, sendbuf...) // final

		_, err = udpConn.Write(sendbuf)

		if err != nil {
			v.log(LogError, "udp write error, %s", err)
			v.log(LogDebug, "voice struct: %p (details redacted)\n", v)
			return
		}
		lastPacketAt = packetAt

		sequence++
		timestamp += uint32(size)
		if trailingSilence {
			silenceFrames++
			if silenceFrames == voiceOpusSilenceFrameCount {
				if err := v.Speaking(false); err != nil {
					v.log(LogError, "error sending stop speaking packet, %s", err)
				}
			}
		} else {
			sentAudio = true
			silenceFrames = 0
		}
	}
}

func interpolateVoiceTimestamp(timestamp uint32, elapsed, frameDuration time.Duration, frameSize uint32) uint32 {
	if frameDuration <= 0 {
		return timestamp
	}

	elapsedFrames := elapsed / frameDuration
	if elapsedFrames < 2 {
		return timestamp
	}

	missedFrames := uint64(elapsedFrames - 1)
	return timestamp + uint32(missedFrames*uint64(frameSize))
}

// A Packet contains the headers and content of a received voice packet.
type Packet struct {
	SSRC      uint32
	Sequence  uint16
	Timestamp uint32
	Type      []byte
	Opus      []byte
	PCM       []int16
}

// opusReceiver listens on the UDP socket for incoming packets
// and sends them across the given channel
// NOTE :: This function may change names later.
func (v *VoiceConnection) opusReceiver(udpConn *net.UDPConn, close <-chan struct{}, c chan *Packet) {

	if udpConn == nil || close == nil {
		return
	}

	recvbuf := make([]byte, 2048)
	var nonce []byte

	for {
		rlen, err := udpConn.Read(recvbuf)
		if err != nil {
			// Detect if we have been closed manually. If a Close() has already
			// happened, the udp connection we are listening on will be different
			// to the current session.
			v.RLock()
			sameConnection := v.udpConn == udpConn
			v.RUnlock()
			if sameConnection {

				v.log(LogError, "udp read error, %s, %s", v.endpoint, err)
				v.log(LogDebug, "voice struct: %p (details redacted)\n", v)

				go v.reconnectWithResume(false)
			}
			return
		}

		select {
		case <-close:
			return
		default:
			// continue loop
		}

		// For now, skip anything except RTP v2 packets (audio).
		// RTP v2 => top two bits are 10 (0x80).
		if rlen < 12 || (recvbuf[0]&0xC0) != 0x80 {
			continue
		}

		// build a audio packet struct
		p := Packet{}
		p.Type = recvbuf[0:2]
		p.Sequence = binary.BigEndian.Uint16(recvbuf[2:4])
		p.Timestamp = binary.BigEndian.Uint32(recvbuf[4:8])
		p.SSRC = binary.BigEndian.Uint32(recvbuf[8:12])

		// RTP header parsing for *_rtpsize AEAD modes:
		// - base RTP header is 12 bytes + 4 bytes per CSRC (CC).
		// - if extension bit (X) is set, ONLY the 4-byte extension preamble is unencrypted/AAD;
		//   the extension payload is encrypted and must be stripped after decryption.
		cc := int(recvbuf[0] & 0x0F)
		hasExt := (recvbuf[0] & 0x10) != 0

		baseHeaderLen := 12 + (4 * cc)
		if rlen < baseHeaderLen {
			continue
		}

		aadLen := baseHeaderLen
		extPayloadBytes := 0
		if hasExt {
			if rlen < baseHeaderLen+4 {
				continue
			}
			// Extension length is in 32-bit words at the end of the extension preamble.
			extLenWords := int(binary.BigEndian.Uint16(recvbuf[baseHeaderLen+2 : baseHeaderLen+4]))
			extPayloadBytes = extLenWords * 4
			aadLen = baseHeaderLen + 4
		}

		if rlen < aadLen+4 {
			continue
		}

		// decrypt opus data
		payload := recvbuf[aadLen:rlen]
		if len(payload) < 4 {
			continue
		}
		nonceCounter := payload[len(payload)-4:]
		cipherTextPayload := payload[:len(payload)-4]

		v.RLock()
		aead := v.aead
		v.RUnlock()
		if aead == nil {
			continue
		}
		if len(nonce) != aead.NonceSize() {
			nonce = make([]byte, aead.NonceSize())
		}
		setVoiceNonce(nonce, binary.BigEndian.Uint32(nonceCounter))
		// AAD must cover the unencrypted header portion.
		if plain, err := aead.Open(nil, nonce, cipherTextPayload, recvbuf[:aadLen]); err == nil {
			// If header extensions are present, strip decrypted extension payload to get to Opus.
			if extPayloadBytes > 0 {
				if len(plain) < extPayloadBytes {
					continue
				}
				plain = plain[extPayloadBytes:]
			}
			p.Opus = plain
		} else {
			continue
		}

		p.Opus, err = v.decryptDAVEFrame(p.SSRC, p.Opus)
		if err != nil {
			v.log(LogDebug, "DAVE decrypt error for ssrc %d, %s", p.SSRC, err)
			continue
		}

		if c != nil {
			select {
			case c <- &p:
			case <-close:
				return
			}
		}
	}
}

// Reconnect will close down a voice connection then immediately try to
// reconnect to that session.
// NOTE : This func is messy and a WIP while I find what works.
// It will be cleaned up once a proven stable option is flushed out.
// aka: this is ugly shit code, please don't judge too harshly.
func (v *VoiceConnection) reconnect() {
	v.reconnectWithResume(true)
}

func (v *VoiceConnection) reconnectWithResume(canResume bool) {

	v.log(LogInformational, "called")

	v.Lock()
	if v.reconnecting {
		v.log(LogInformational, "already reconnecting to channel %s, exiting", v.ChannelID)
		v.Unlock()
		return
	}
	v.reconnecting = true
	v.Unlock()

	defer func() {
		v.Lock()
		v.reconnecting = false
		v.Unlock()
	}()

	if canResume && v.canResumeVoice() && v.isSessionVoiceConnection() {
		if err := v.resumeVoice(); err == nil {
			v.log(LogInformational, "successfully resumed voice websocket")
			return
		} else {
			v.log(LogWarning, "could not resume voice websocket, %s", err)
		}
	}

	// Close any currently open connections
	v.Close()

	if !v.isSessionVoiceConnection() {
		return
	}

	wait := time.Duration(1)
	for {

		<-time.After(wait * time.Second)
		wait *= 2
		if wait > 600 {
			wait = 600
		}

		if !v.isSessionVoiceConnection() {
			return
		}

		v.session.RLock()
		dataReady := v.session.DataReady
		wsConn := v.session.wsConn
		v.session.RUnlock()
		if !dataReady || wsConn == nil {
			v.log(LogInformational, "cannot reconnect to channel %s with unready session", v.ChannelID)
			continue
		}

		v.log(LogInformational, "trying to reconnect to channel %s", v.ChannelID)

		_, err := v.session.ChannelVoiceJoin(v.GuildID, v.ChannelID, v.mute, v.deaf)
		if err == nil {
			v.log(LogInformational, "successfully reconnected to channel %s", v.ChannelID)
			return
		}

		v.log(LogInformational, "error reconnecting to channel %s, %s", v.ChannelID, err)

		// if the reconnect above didn't work lets just send a disconnect
		// packet to reset things.
		sent, err := v.sendDisconnectIfCurrent()
		if !sent {
			return
		}
		if err != nil {
			v.log(LogError, "error sending disconnect packet, %s", err)
		}

	}
}

// sendDisconnectIfCurrent sends a voice state reset only while this
// connection still owns its guild entry. The ownership check and OP4
// write are serialized with joins and permanent disconnects.
func (v *VoiceConnection) sendDisconnectIfCurrent() (sent bool, err error) {
	v.RLock()
	session := v.session
	guildID := v.GuildID
	v.RUnlock()
	if session == nil {
		return false, nil
	}

	session.voiceMutex.Lock()
	v.RLock()
	current := v.session == session && v.GuildID == guildID && !v.disconnecting
	v.RUnlock()
	if current {
		session.RLock()
		current = session.VoiceConnections[guildID] == v
		session.RUnlock()
	}
	if current {
		err = session.channelVoiceJoinManual(guildID, "", true, true)
		sent = true
	}
	session.voiceMutex.Unlock()
	return
}

func (v *VoiceConnection) isSessionVoiceConnection() bool {
	v.RLock()
	session := v.session
	guildID := v.GuildID
	v.RUnlock()
	if session == nil {
		return false
	}

	session.RLock()
	defer session.RUnlock()

	return session.VoiceConnections[guildID] == v
}
