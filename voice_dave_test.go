package discordgo

import (
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type fakeDAVETransition struct {
	id      uint16
	version uint16
}

type fakeDAVEEpoch struct {
	epoch   uint64
	version uint16
}

type fakeDAVEMessage struct {
	transitionID uint16
	payload      []byte
}

type fakeVoiceDAVESession struct {
	mu sync.Mutex

	maxVersion int
	ready      bool
	callbacks  VoiceDAVECallbacks
	closed     int
	closeHook  func()
	localSSRC  uint32

	addedUsers             []string
	removedUsers           []string
	sessionDescriptions    []uint16
	preparedTransitions    []fakeDAVETransition
	executedTransitions    []uint16
	preparedEpochs         []fakeDAVEEpoch
	externalSenderPackages [][]byte
	proposals              [][]byte
	commits                []fakeDAVEMessage
	welcomeMessages        []fakeDAVEMessage
	encryptSSRCs           []uint32
	decryptUsers           []string
	encryptErr             error
	decryptErr             error
}

func (f *fakeVoiceDAVESession) MaxSupportedProtocolVersion() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxVersion
}

func (f *fakeVoiceDAVESession) Ready() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ready
}

func (f *fakeVoiceDAVESession) SetLocalSSRC(ssrc uint32) {
	f.mu.Lock()
	f.localSSRC = ssrc
	f.mu.Unlock()
}

func (f *fakeVoiceDAVESession) EncryptFrame(ssrc uint32, frame []byte) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.encryptSSRCs = append(f.encryptSSRCs, ssrc)
	if f.encryptErr != nil {
		return nil, f.encryptErr
	}
	return append([]byte{0xd0}, frame...), nil
}

func (f *fakeVoiceDAVESession) DecryptFrame(userID string, frame []byte) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.decryptUsers = append(f.decryptUsers, userID)
	if f.decryptErr != nil {
		return nil, f.decryptErr
	}
	if len(frame) == 0 || frame[0] != 0xd1 {
		return nil, errors.New("fake DAVE frame marker is missing")
	}
	return append([]byte(nil), frame[1:]...), nil
}

func (f *fakeVoiceDAVESession) AddUser(userID string) {
	f.mu.Lock()
	f.addedUsers = append(f.addedUsers, userID)
	f.mu.Unlock()
}

func (f *fakeVoiceDAVESession) RemoveUser(userID string) {
	f.mu.Lock()
	f.removedUsers = append(f.removedUsers, userID)
	f.mu.Unlock()
}

func (f *fakeVoiceDAVESession) OnSessionDescription(protocolVersion uint16) {
	f.mu.Lock()
	f.sessionDescriptions = append(f.sessionDescriptions, protocolVersion)
	f.mu.Unlock()
}

func (f *fakeVoiceDAVESession) OnDAVEPrepareTransition(transitionID, protocolVersion uint16) {
	f.mu.Lock()
	f.preparedTransitions = append(f.preparedTransitions, fakeDAVETransition{id: transitionID, version: protocolVersion})
	f.mu.Unlock()
}

func (f *fakeVoiceDAVESession) OnDAVEExecuteTransition(transitionID uint16) {
	f.mu.Lock()
	f.executedTransitions = append(f.executedTransitions, transitionID)
	f.mu.Unlock()
}

func (f *fakeVoiceDAVESession) OnDAVEPrepareEpoch(epoch uint64, protocolVersion uint16) {
	f.mu.Lock()
	f.preparedEpochs = append(f.preparedEpochs, fakeDAVEEpoch{epoch: epoch, version: protocolVersion})
	f.mu.Unlock()
}

func (f *fakeVoiceDAVESession) OnDAVEMLSExternalSenderPackage(externalSender []byte) {
	f.mu.Lock()
	f.externalSenderPackages = append(f.externalSenderPackages, append([]byte(nil), externalSender...))
	f.mu.Unlock()
}

func (f *fakeVoiceDAVESession) OnDAVEMLSProposals(proposals []byte) {
	f.mu.Lock()
	f.proposals = append(f.proposals, append([]byte(nil), proposals...))
	f.mu.Unlock()
}

func (f *fakeVoiceDAVESession) OnDAVEMLSAnnounceCommitTransition(transitionID uint16, commit []byte) {
	f.mu.Lock()
	f.commits = append(f.commits, fakeDAVEMessage{transitionID: transitionID, payload: append([]byte(nil), commit...)})
	f.mu.Unlock()
}

func (f *fakeVoiceDAVESession) OnDAVEMLSWelcome(transitionID uint16, welcome []byte) {
	f.mu.Lock()
	f.welcomeMessages = append(f.welcomeMessages, fakeDAVEMessage{transitionID: transitionID, payload: append([]byte(nil), welcome...)})
	f.mu.Unlock()
}

func (f *fakeVoiceDAVESession) Close() error {
	f.mu.Lock()
	f.closed++
	hook := f.closeHook
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return nil
}

func installFakeDAVE(v *VoiceConnection, session *fakeVoiceDAVESession, protocolVersion uint16) {
	v.Lock()
	v.installVoiceDAVESessionLocked(session, nil, session.maxVersion)
	v.daveProtocolVersion = protocolVersion
	v.davePreparedProtocolVersion = protocolVersion
	v.Unlock()
}

type voiceTestWebsocketMessage struct {
	messageType int
	payload     []byte
}

func TestVoiceDAVEIdentifyAndCallbacks(t *testing.T) {
	messages := make(chan voiceTestWebsocketMessage, 5)
	upgrader := websocket.Upgrader{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			messageType, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			messages <- voiceTestWebsocketMessage{messageType: messageType, payload: payload}
		}
	}))
	defer server.Close()

	fake := &fakeVoiceDAVESession{maxVersion: 1, ready: true}
	session := &Session{
		Dialer: &websocket.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		VoiceDAVESessionFactory: func(userID string, channelID uint64, callbacks VoiceDAVECallbacks) (VoiceDAVESession, error) {
			if userID != "200" || channelID != 300 {
				return nil, fmt.Errorf("factory arguments = %q/%d", userID, channelID)
			}
			fake.mu.Lock()
			fake.callbacks = callbacks
			fake.mu.Unlock()
			return fake, nil
		},
	}
	voice := &VoiceConnection{
		LogLevel:  -1,
		GuildID:   "100",
		UserID:    "200",
		ChannelID: "300",
		sessionID: "session",
		token:     "token",
		endpoint:  strings.TrimPrefix(server.URL, "https://"),
		session:   session,
	}
	if err := voice.open(); err != nil {
		t.Fatalf("open() error = %v", err)
	}

	identify := <-messages
	if identify.messageType != websocket.TextMessage {
		t.Fatalf("identify message type = %d", identify.messageType)
	}
	var identifyPayload struct {
		Op   int `json:"op"`
		Data struct {
			MaxDAVEProtocolVersion int `json:"max_dave_protocol_version"`
		} `json:"d"`
	}
	if err := json.Unmarshal(identify.payload, &identifyPayload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if identifyPayload.Op != 0 || identifyPayload.Data.MaxDAVEProtocolVersion != 1 {
		t.Fatalf("identify payload = %#v", identifyPayload)
	}

	fake.mu.Lock()
	callbacks := fake.callbacks
	fake.mu.Unlock()
	if callbacks == nil {
		t.Fatal("factory did not receive callbacks")
	}
	if err := callbacks.SendMLSKeyPackage(nil); err == nil {
		t.Fatal("SendMLSKeyPackage(nil) error = nil")
	}
	if err := callbacks.SendMLSKeyPackage([]byte{1, 2}); err != nil {
		t.Fatalf("SendMLSKeyPackage() error = %v", err)
	}
	if err := callbacks.SendMLSCommitWelcome([]byte{3, 4}); err != nil {
		t.Fatalf("SendMLSCommitWelcome() error = %v", err)
	}
	if err := callbacks.SendReadyForTransition(7); err != nil {
		t.Fatalf("SendReadyForTransition() error = %v", err)
	}
	if err := callbacks.SendInvalidCommitWelcome(8); err != nil {
		t.Fatalf("SendInvalidCommitWelcome() error = %v", err)
	}

	keyPackage := <-messages
	commitWelcome := <-messages
	ready := <-messages
	invalid := <-messages
	if keyPackage.messageType != websocket.BinaryMessage || !reflect.DeepEqual(keyPackage.payload, []byte{26, 1, 2}) {
		t.Fatalf("key package frame = %d/%v", keyPackage.messageType, keyPackage.payload)
	}
	if commitWelcome.messageType != websocket.BinaryMessage || !reflect.DeepEqual(commitWelcome.payload, []byte{28, 3, 4}) {
		t.Fatalf("commit/welcome frame = %d/%v", commitWelcome.messageType, commitWelcome.payload)
	}
	for _, test := range []struct {
		name         string
		message      voiceTestWebsocketMessage
		opcode       int
		transitionID uint16
	}{
		{name: "ready", message: ready, opcode: 23, transitionID: 7},
		{name: "invalid", message: invalid, opcode: 31, transitionID: 8},
	} {
		var payload struct {
			Op   int `json:"op"`
			Data struct {
				TransitionID uint16 `json:"transition_id"`
			} `json:"d"`
		}
		if err := json.Unmarshal(test.message.payload, &payload); err != nil {
			t.Fatalf("%s json.Unmarshal() error = %v", test.name, err)
		}
		if test.message.messageType != websocket.TextMessage || payload.Op != test.opcode || payload.Data.TransitionID != test.transitionID {
			t.Fatalf("%s payload = %d/%#v", test.name, test.message.messageType, payload)
		}
	}

	voice.Close()
	fake.mu.Lock()
	closed := fake.closed
	fake.mu.Unlock()
	if closed != 1 {
		t.Fatalf("DAVE Close calls = %d, want 1", closed)
	}
	if err := callbacks.SendReadyForTransition(9); err == nil {
		t.Fatal("stale callbacks remained active after Close")
	}
}

func TestVoiceDAVESessionFactoryValidation(t *testing.T) {
	voice := &VoiceConnection{}
	factoryErr := errors.New("factory failed")

	tests := []struct {
		name      string
		channelID string
		factory   VoiceDAVESessionFactory
	}{
		{
			name:      "invalid channel id",
			channelID: "invalid",
			factory: func(string, uint64, VoiceDAVECallbacks) (VoiceDAVESession, error) {
				t.Fatal("factory called with invalid channel id")
				return nil, nil
			},
		},
		{
			name:      "factory error",
			channelID: "1",
			factory: func(string, uint64, VoiceDAVECallbacks) (VoiceDAVESession, error) {
				return nil, factoryErr
			},
		},
		{
			name:      "nil session",
			channelID: "1",
			factory: func(string, uint64, VoiceDAVECallbacks) (VoiceDAVESession, error) {
				return nil, nil
			},
		},
		{
			name:      "negative protocol version",
			channelID: "1",
			factory: func(string, uint64, VoiceDAVECallbacks) (VoiceDAVESession, error) {
				return &fakeVoiceDAVESession{maxVersion: -1}, nil
			},
		},
		{
			name:      "overflowing protocol version",
			channelID: "1",
			factory: func(string, uint64, VoiceDAVECallbacks) (VoiceDAVESession, error) {
				return &fakeVoiceDAVESession{maxVersion: 0x10000}, nil
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, _, err := voice.newVoiceDAVESession(test.factory, "2", test.channelID); err == nil {
				t.Fatal("newVoiceDAVESession() error = nil")
			}
		})
	}
}

func TestVoiceDAVESessionDescriptionValidation(t *testing.T) {
	fake := &fakeVoiceDAVESession{maxVersion: 1, ready: true}
	voice := &VoiceConnection{LogLevel: -1}
	installFakeDAVE(voice, fake, 0)

	if err := voice.onDAVESessionDescription(2); err == nil {
		t.Fatal("unsupported protocol version error = nil")
	}
	voice.RLock()
	protocolVersion := voice.daveProtocolVersion
	voice.RUnlock()
	if protocolVersion != 0 {
		t.Fatalf("protocol version = %d after rejected selection", protocolVersion)
	}
	fake.mu.Lock()
	descriptions := append([]uint16(nil), fake.sessionDescriptions...)
	fake.mu.Unlock()
	if len(descriptions) != 0 {
		t.Fatalf("rejected session descriptions = %v", descriptions)
	}

	if err := (&VoiceConnection{}).onDAVESessionDescription(1); err == nil {
		t.Fatal("selected DAVE without a backend error = nil")
	}
}

func TestVoiceDAVERejectsUnsupportedSessionDescription(t *testing.T) {
	fake := &fakeVoiceDAVESession{maxVersion: 1, ready: true}
	voice := &VoiceConnection{
		LogLevel:       -1,
		encryptionMode: voiceModeAES256GCMRTPSize,
	}
	installFakeDAVE(voice, fake, 0)

	key := make([]int, 32)
	description, err := json.Marshal(struct {
		Op   int      `json:"op"`
		Data voiceOP4 `json:"d"`
	}{
		Op: 4,
		Data: voiceOP4{
			Mode:                voiceModeAES256GCMRTPSize,
			SecretKey:           key,
			DAVEProtocolVersion: 2,
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	voice.onEvent(description)

	voice.RLock()
	aead := voice.aead
	session := voice.dave
	voice.RUnlock()
	if aead != nil || session != nil {
		t.Fatalf("rejected DAVE selection left encryption state: aead=%t session=%t", aead != nil, session != nil)
	}
	fake.mu.Lock()
	closed := fake.closed
	descriptions := append([]uint16(nil), fake.sessionDescriptions...)
	fake.mu.Unlock()
	if closed != 1 || len(descriptions) != 0 {
		t.Fatalf("rejected DAVE backend state: closed=%d descriptions=%v", closed, descriptions)
	}
}

func daveBinaryFrame(sequence uint16, opcode byte, payload ...byte) []byte {
	frame := make([]byte, 3+len(payload))
	binary.BigEndian.PutUint16(frame, sequence)
	frame[2] = opcode
	copy(frame[3:], payload)
	return frame
}

func TestVoiceDAVEInboundProtocolLifecycle(t *testing.T) {
	fake := &fakeVoiceDAVESession{maxVersion: 1, ready: true}
	voice := &VoiceConnection{
		LogLevel:       -1,
		UserID:         "self",
		encryptionMode: voiceModeAES256GCMRTPSize,
	}
	installFakeDAVE(voice, fake, 0)

	key := make([]int, 32)
	description, err := json.Marshal(struct {
		Op   int `json:"op"`
		Data struct {
			Mode                string `json:"mode"`
			SecretKey           []int  `json:"secret_key"`
			DAVEProtocolVersion uint16 `json:"dave_protocol_version"`
		} `json:"d"`
	}{
		Op: 4,
		Data: struct {
			Mode                string `json:"mode"`
			SecretKey           []int  `json:"secret_key"`
			DAVEProtocolVersion uint16 `json:"dave_protocol_version"`
		}{Mode: voiceModeAES256GCMRTPSize, SecretKey: key, DAVEProtocolVersion: 1},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	voice.onEvent(description)
	voice.onDAVELocalSSRC(42)
	voice.onEvent([]byte(`{"op":11,"d":{"user_ids":["self","remote","remote"]}}`))
	voice.onEvent([]byte(`{"op":11,"d":{"user_ids":["remote"]}}`))
	voice.onEvent([]byte(`{"op":5,"d":{"user_id":"remote","ssrc":55,"speaking":1}}`))
	voice.onEvent([]byte(`{"op":21,"d":{"transition_id":0,"protocol_version":1}}`))
	voice.onEvent([]byte(`{"op":21,"d":{"transition_id":7,"protocol_version":0}}`))
	voice.onEvent([]byte(`{"op":22,"d":{"transition_id":7}}`))
	voice.onEvent([]byte(`{"op":24,"d":{"epoch":1,"protocol_version":1}}`))

	voice.handleDAVEBinaryFrame(daveBinaryFrame(10, 25, 1, 2))
	voice.handleDAVEBinaryFrame(daveBinaryFrame(11, 27, 0, 3, 4))
	voice.handleDAVEBinaryFrame(daveBinaryFrame(12, 29, 0, 0, 9))
	voice.handleDAVEBinaryFrame(daveBinaryFrame(13, 29, 0, 8, 5, 6))
	voice.onEvent([]byte(`{"op":22,"d":{"transition_id":8}}`))
	voice.handleDAVEBinaryFrame(daveBinaryFrame(14, 30, 0, 9, 7, 8))
	voice.onEvent([]byte(`{"op":13,"d":{"user_id":"remote"}}`))

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if !reflect.DeepEqual(fake.sessionDescriptions, []uint16{1}) {
		t.Fatalf("session descriptions = %v", fake.sessionDescriptions)
	}
	if fake.localSSRC != 42 {
		t.Fatalf("local SSRC = %d", fake.localSSRC)
	}
	if !reflect.DeepEqual(fake.addedUsers, []string{"remote"}) || !reflect.DeepEqual(fake.removedUsers, []string{"remote"}) {
		t.Fatalf("participant lifecycle = added %v removed %v", fake.addedUsers, fake.removedUsers)
	}
	if !reflect.DeepEqual(fake.preparedTransitions, []fakeDAVETransition{{id: 0, version: 1}, {id: 7, version: 0}}) {
		t.Fatalf("prepared transitions = %#v", fake.preparedTransitions)
	}
	if !reflect.DeepEqual(fake.executedTransitions, []uint16{7, 8}) {
		t.Fatalf("executed transitions = %v", fake.executedTransitions)
	}
	if !reflect.DeepEqual(fake.preparedEpochs, []fakeDAVEEpoch{{epoch: 1, version: 1}}) {
		t.Fatalf("prepared epochs = %#v", fake.preparedEpochs)
	}
	if !reflect.DeepEqual(fake.externalSenderPackages, [][]byte{{1, 2}}) || !reflect.DeepEqual(fake.proposals, [][]byte{{0, 3, 4}}) {
		t.Fatalf("MLS inputs = external %v proposals %v", fake.externalSenderPackages, fake.proposals)
	}
	if !reflect.DeepEqual(fake.commits, []fakeDAVEMessage{{transitionID: 0, payload: []byte{9}}, {transitionID: 8, payload: []byte{5, 6}}}) {
		t.Fatalf("commits = %#v", fake.commits)
	}
	if !reflect.DeepEqual(fake.welcomeMessages, []fakeDAVEMessage{{transitionID: 9, payload: []byte{7, 8}}}) {
		t.Fatalf("welcomes = %#v", fake.welcomeMessages)
	}

	voice.RLock()
	sequence := voice.sequence
	protocolVersion := voice.daveProtocolVersion
	_, mapped := voice.daveSSRCToUserID[55]
	voice.RUnlock()
	if sequence != 14 || protocolVersion != 1 || mapped {
		t.Fatalf("voice state = sequence %d version %d mapped %t", sequence, protocolVersion, mapped)
	}
}

func TestVoiceDAVEMalformedBinaryFrames(t *testing.T) {
	fake := &fakeVoiceDAVESession{maxVersion: 1, ready: true}
	voice := &VoiceConnection{LogLevel: -1}
	installFakeDAVE(voice, fake, 1)

	voice.handleDAVEBinaryFrame([]byte{0, 1})
	voice.handleDAVEBinaryFrame(daveBinaryFrame(1, 25))
	voice.handleDAVEBinaryFrame(daveBinaryFrame(2, 27))
	voice.handleDAVEBinaryFrame(daveBinaryFrame(2, 27, 2, 1))
	voice.handleDAVEBinaryFrame(daveBinaryFrame(3, 29, 0))
	voice.handleDAVEBinaryFrame(daveBinaryFrame(4, 30, 0, 1))
	voice.handleDAVEBinaryFrame(daveBinaryFrame(5, 99, 1))
	voice.onEvent([]byte(`{"op":21,"d":{"transition_id":1}}`))
	voice.onEvent([]byte(`{"op":22,"d":{}}`))
	voice.onEvent([]byte(`{"op":24,"d":{"epoch":1}}`))

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.externalSenderPackages) != 0 || len(fake.proposals) != 0 || len(fake.commits) != 0 || len(fake.welcomeMessages) != 0 || len(fake.preparedTransitions) != 0 || len(fake.executedTransitions) != 0 || len(fake.preparedEpochs) != 0 {
		t.Fatalf("malformed events reached backend: external=%d proposals=%d commits=%d welcomes=%d transitions=%d/%d epochs=%d",
			len(fake.externalSenderPackages), len(fake.proposals), len(fake.commits), len(fake.welcomeMessages), len(fake.preparedTransitions), len(fake.executedTransitions), len(fake.preparedEpochs))
	}
}

func TestVoiceDAVEFrameTransforms(t *testing.T) {
	fake := &fakeVoiceDAVESession{maxVersion: 1, ready: true}
	voice := &VoiceConnection{LogLevel: -1}
	installFakeDAVE(voice, fake, 1)
	voice.onDAVESpeakingUpdate(&VoiceSpeakingUpdate{UserID: "remote", SSRC: 55})
	if !voice.DAVEReady() {
		t.Fatal("DAVEReady() = false for a ready backend")
	}

	encrypted, err := voice.encryptDAVEFrame(42, []byte{1, 2})
	if err != nil || !reflect.DeepEqual(encrypted, []byte{0xd0, 1, 2}) {
		t.Fatalf("encryptDAVEFrame() = %v, %v", encrypted, err)
	}
	decrypted, err := voice.decryptDAVEFrame(55, []byte{0xd1, 3, 4})
	if err != nil || !reflect.DeepEqual(decrypted, []byte{3, 4}) {
		t.Fatalf("decryptDAVEFrame() = %v, %v", decrypted, err)
	}

	silence := []byte{0xf8, 0xff, 0xfe}
	if got, err := voice.encryptDAVEFrame(42, silence); err != nil || !reflect.DeepEqual(got, []byte{0xd0, 0xf8, 0xff, 0xfe}) {
		t.Fatalf("silence encrypt = %v, %v", got, err)
	}
	if got, err := voice.decryptDAVEFrame(999, silence); err != nil || !reflect.DeepEqual(got, silence) {
		t.Fatalf("silence decrypt = %v, %v", got, err)
	}

	fake.mu.Lock()
	fake.ready = false
	fake.mu.Unlock()
	if voice.DAVEReady() {
		t.Fatal("DAVEReady() = true during a pending transition")
	}
	if _, err := voice.encryptDAVEFrame(42, []byte{1}); !errors.Is(err, errDAVETransitionPending) {
		t.Fatalf("pending encrypt error = %v", err)
	}
	if _, err := voice.decryptDAVEFrame(999, []byte{0xd1, 1}); err == nil {
		t.Fatal("unknown SSRC decrypt error = nil")
	}
	fake.mu.Lock()
	fake.ready = true
	fake.encryptErr = errors.New("encrypt failed")
	fake.decryptErr = errors.New("decrypt failed")
	fake.mu.Unlock()
	if _, err := voice.encryptDAVEFrame(42, []byte{1}); err == nil || !strings.Contains(err.Error(), "encrypt failed") {
		t.Fatalf("backend encrypt error = %v", err)
	}
	if _, err := voice.decryptDAVEFrame(55, []byte{0xd1, 1}); err == nil || !strings.Contains(err.Error(), "decrypt failed") {
		t.Fatalf("backend decrypt error = %v", err)
	}

	voice.closeVoiceDAVE()
	voice.Lock()
	voice.daveProtocolVersion = 1
	voice.Unlock()
	if voice.DAVEReady() {
		t.Fatal("DAVEReady() = true without its selected backend")
	}
	if _, err := voice.encryptDAVEFrame(42, []byte{1}); !errors.Is(err, errDAVESessionMissing) {
		t.Fatalf("missing session encrypt error = %v", err)
	}
	plainVoice := &VoiceConnection{}
	if !plainVoice.DAVEReady() {
		t.Fatal("DAVEReady() = false when DAVE is disabled")
	}
	if got, err := plainVoice.encryptDAVEFrame(1, []byte{9}); err != nil || !reflect.DeepEqual(got, []byte{9}) {
		t.Fatalf("protocol 0 encrypt = %v, %v", got, err)
	}
	if got, err := plainVoice.decryptDAVEFrame(1, []byte{9}); err != nil || !reflect.DeepEqual(got, []byte{9}) {
		t.Fatalf("protocol 0 decrypt = %v, %v", got, err)
	}
}

func TestVoiceDAVECloseStopsTransportBeforeBackend(t *testing.T) {
	transportClose := make(chan struct{})
	transportStopped := false
	fake := &fakeVoiceDAVESession{
		maxVersion: 1,
		ready:      true,
		closeHook: func() {
			select {
			case <-transportClose:
				transportStopped = true
			default:
			}
		},
	}
	voice := &VoiceConnection{LogLevel: -1, close: transportClose}
	installFakeDAVE(voice, fake, 1)

	voice.Close()

	if !transportStopped {
		t.Fatal("DAVE backend closed before the media transport stopped")
	}
}

func TestVoiceDAVEBackendCloseDoesNotBlockFrameTransforms(t *testing.T) {
	closeStarted := make(chan struct{})
	releaseClose := make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(releaseClose) })
	}
	defer release()
	fake := &fakeVoiceDAVESession{
		maxVersion: 1,
		ready:      true,
		closeHook: func() {
			close(closeStarted)
			<-releaseClose
		},
	}
	voice := &VoiceConnection{LogLevel: -1}
	installFakeDAVE(voice, fake, 1)
	voice.onDAVESpeakingUpdate(&VoiceSpeakingUpdate{UserID: "remote", SSRC: 55})

	closeDone := make(chan struct{})
	go func() {
		voice.closeVoiceDAVE()
		close(closeDone)
	}()
	select {
	case <-closeStarted:
	case <-time.After(time.Second):
		t.Fatal("closeVoiceDAVE did not reach the backend")
	}

	type frameResult struct {
		frame []byte
		err   error
	}
	encryptDone := make(chan frameResult, 1)
	decryptDone := make(chan frameResult, 1)
	go func() {
		frame, err := voice.encryptDAVEFrame(42, []byte{1})
		encryptDone <- frameResult{frame: frame, err: err}
	}()
	go func() {
		frame, err := voice.decryptDAVEFrame(55, []byte{0xd1, 2})
		decryptDone <- frameResult{frame: frame, err: err}
	}()

	select {
	case result := <-encryptDone:
		if result.err != nil || !reflect.DeepEqual(result.frame, []byte{1}) {
			t.Fatalf("encrypt during backend close = %v, %v", result.frame, result.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("encrypt blocked behind the DAVE backend Close")
	}
	select {
	case result := <-decryptDone:
		if result.err != nil || !reflect.DeepEqual(result.frame, []byte{0xd1, 2}) {
			t.Fatalf("decrypt during backend close = %v, %v", result.frame, result.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("decrypt blocked behind the DAVE backend Close")
	}

	release()
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("closeVoiceDAVE did not finish")
	}
}

func TestVoiceDAVELifecycleSerializesOpenAndClose(t *testing.T) {
	closeStarted := make(chan struct{})
	releaseClose := make(chan struct{})
	fake := &fakeVoiceDAVESession{
		maxVersion: 1,
		ready:      true,
		closeHook: func() {
			close(closeStarted)
			<-releaseClose
		},
	}
	voice := &VoiceConnection{
		LogLevel:  -1,
		ChannelID: "1",
		sessionID: "session",
		close:     make(chan struct{}),
	}
	installFakeDAVE(voice, fake, 1)

	closeDone := make(chan struct{})
	go func() {
		voice.Close()
		close(closeDone)
	}()
	select {
	case <-closeStarted:
	case <-time.After(time.Second):
		t.Fatal("Close did not reach the DAVE backend")
	}

	openDone := make(chan error, 1)
	go func() {
		openDone <- voice.open()
	}()
	select {
	case err := <-openDone:
		t.Fatalf("open returned before Close completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseClose)
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("Close did not finish")
	}
	select {
	case err := <-openDone:
		if !errors.Is(err, ErrWSNotFound) {
			t.Fatalf("open error = %v, want %v", err, ErrWSNotFound)
		}
	case <-time.After(time.Second):
		t.Fatal("open did not resume after Close")
	}
}

func TestVoiceDAVEOutboundMediaIntegration(t *testing.T) {
	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("net.ListenUDP() error = %v", err)
	}
	defer server.Close()
	client, err := net.DialUDP("udp4", nil, server.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("net.DialUDP() error = %v", err)
	}
	defer client.Close()

	aead, err := newVoiceAEAD(voiceModeAES256GCMRTPSize, make([]int, 32))
	if err != nil {
		t.Fatalf("newVoiceAEAD() error = %v", err)
	}
	fake := &fakeVoiceDAVESession{maxVersion: 1}
	voice := &VoiceConnection{
		LogLevel: -1,
		speaking: true,
		aead:     aead,
		op2:      voiceOP2{SSRC: 42},
	}
	installFakeDAVE(voice, fake, 1)

	opus := make(chan []byte, 1)
	opus <- []byte{1, 2, 3}
	close(opus)
	done := make(chan struct{})
	go func() {
		voice.opusSender(client, make(chan struct{}), opus, 48000, 960)
		close(done)
	}()

	packet := make([]byte, 2048)
	if err := server.SetReadDeadline(time.Now().Add(60 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	if _, _, err := server.ReadFromUDP(packet); err == nil {
		t.Fatal("received media while the DAVE backend was not ready")
	} else if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
		t.Fatalf("ReadFromUDP() pending error = %v", err)
	}
	fake.mu.Lock()
	fake.ready = true
	fake.mu.Unlock()
	if err := server.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	n, _, err := server.ReadFromUDP(packet)
	if err != nil {
		t.Fatalf("ReadFromUDP() error = %v", err)
	}
	packet = packet[:n]
	nonce := make([]byte, aead.NonceSize())
	copy(nonce[:4], packet[n-4:])
	plain, err := aead.Open(nil, nonce, packet[12:n-4], packet[:12])
	if err != nil {
		t.Fatalf("transport decrypt error = %v", err)
	}
	if !reflect.DeepEqual(plain, []byte{0xd0, 1, 2, 3}) {
		t.Fatalf("transport plaintext = %v", plain)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("opusSender did not finish after sending the retained frame")
	}
}

func TestVoiceDAVEInboundMediaIntegration(t *testing.T) {
	receiver, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("net.ListenUDP() error = %v", err)
	}
	sender, err := net.DialUDP("udp4", nil, receiver.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("net.DialUDP() error = %v", err)
	}
	defer sender.Close()

	aead, err := newVoiceAEAD(voiceModeAES256GCMRTPSize, make([]int, 32))
	if err != nil {
		t.Fatalf("newVoiceAEAD() error = %v", err)
	}
	fake := &fakeVoiceDAVESession{maxVersion: 1, ready: true}
	voice := &VoiceConnection{LogLevel: -1, aead: aead}
	installFakeDAVE(voice, fake, 1)
	voice.onDAVESpeakingUpdate(&VoiceSpeakingUpdate{UserID: "remote", SSRC: 55})

	closeChan := make(chan struct{})
	packets := make(chan *Packet, 1)
	done := make(chan struct{})
	go func() {
		voice.opusReceiver(receiver, closeChan, packets)
		close(done)
	}()

	header := make([]byte, 12)
	header[0] = 0x80
	header[1] = 0x78
	binary.BigEndian.PutUint32(header[8:], 55)
	nonce := make([]byte, aead.NonceSize())
	setVoiceNonce(nonce, 7)
	ciphertext := aead.Seal(nil, nonce, []byte{0xd1, 5, 6}, header)
	wirePacket := append(append(header, ciphertext...), nonce[:4]...)
	if _, err := sender.Write(wirePacket); err != nil {
		t.Fatalf("sender.Write() error = %v", err)
	}

	select {
	case packet := <-packets:
		if packet.SSRC != 55 || !reflect.DeepEqual(packet.Opus, []byte{5, 6}) {
			t.Fatalf("received packet = %#v", packet)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for decrypted packet")
	}
	close(closeChan)
	_ = receiver.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("opusReceiver did not stop")
	}
}

func TestVoiceDAVEResumePreservesSessionAndReplaysBinary(t *testing.T) {
	resumePayload := make(chan voiceResumeOp, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var payload voiceResumeOp
		if err := conn.ReadJSON(&payload); err != nil {
			return
		}
		resumePayload <- payload
		if err := conn.WriteMessage(websocket.BinaryMessage, daveBinaryFrame(12, 25, 9, 8)); err != nil {
			return
		}
		if err := conn.WriteJSON(struct {
			Op       int         `json:"op"`
			Sequence int64       `json:"seq"`
			Data     interface{} `json:"d"`
		}{Op: 9, Sequence: 13, Data: nil}); err != nil {
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	fake := &fakeVoiceDAVESession{maxVersion: 1, ready: true}
	session := &Session{Dialer: &websocket.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	voice := &VoiceConnection{
		LogLevel:    -1,
		GuildID:     "100",
		UserID:      "200",
		ChannelID:   "300",
		sessionID:   "session",
		token:       "token",
		endpoint:    strings.TrimPrefix(server.URL, "https://"),
		session:     session,
		close:       make(chan struct{}),
		sequence:    10,
		sequenceSet: true,
		speaking:    true,
	}
	installFakeDAVE(voice, fake, 1)

	if err := voice.resumeVoice(); err != nil {
		t.Fatalf("resumeVoice() error = %v", err)
	}
	request := <-resumePayload
	if request.Op != 7 || request.Data.SequenceAck != 10 {
		t.Fatalf("resume payload = %#v", request)
	}

	voice.RLock()
	sequence := voice.sequence
	speaking := voice.speaking
	activeSession := voice.dave
	voice.RUnlock()
	if sequence != 13 || speaking || activeSession != fake {
		t.Fatalf("resumed voice state = sequence %d speaking %t session %T", sequence, speaking, activeSession)
	}
	fake.mu.Lock()
	external := append([][]byte(nil), fake.externalSenderPackages...)
	closed := fake.closed
	fake.mu.Unlock()
	if !reflect.DeepEqual(external, [][]byte{{9, 8}}) || closed != 0 {
		t.Fatalf("resumed DAVE state = external %v closed %d", external, closed)
	}

	voice.Close()
}

func TestVoiceDAVERequiredCloseDoesNotReconnect(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4017, "DAVE required"))
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	fake := &fakeVoiceDAVESession{maxVersion: 1, ready: true}
	session := &Session{VoiceConnections: make(map[string]*VoiceConnection)}
	voice := &VoiceConnection{
		GuildID:  "guild",
		LogLevel: -1,
		close:    make(chan struct{}),
		session:  session,
		wsConn:   conn,
	}
	installFakeDAVE(voice, fake, 1)
	session.VoiceConnections[voice.GuildID] = voice

	voice.wsListen(conn, voice.close)

	voice.RLock()
	reconnecting := voice.reconnecting
	activeDAVE := voice.dave
	voice.RUnlock()
	if reconnecting || activeDAVE != nil {
		t.Fatalf("4017 state = reconnecting %t DAVE %T", reconnecting, activeDAVE)
	}
	if _, ok := session.VoiceConnections[voice.GuildID]; ok {
		t.Fatal("4017 voice connection was not unregistered")
	}
	fake.mu.Lock()
	closed := fake.closed
	fake.mu.Unlock()
	if closed != 1 {
		t.Fatalf("DAVE Close calls = %d, want 1", closed)
	}
}

func TestVoiceDAVEConcurrentClose(t *testing.T) {
	fake := &fakeVoiceDAVESession{maxVersion: 1, ready: true}
	voice := &VoiceConnection{LogLevel: -1}
	installFakeDAVE(voice, fake, 1)
	voice.onDAVESpeakingUpdate(&VoiceSpeakingUpdate{UserID: "remote", SSRC: 55})

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, _ = voice.encryptDAVEFrame(42, []byte{1})
				_, _ = voice.decryptDAVEFrame(55, []byte{0xd1, 2})
				voice.handleDAVEBinaryFrame(daveBinaryFrame(uint16(j), 25, 1))
			}
		}()
	}
	voice.Close()
	wg.Wait()

	fake.mu.Lock()
	closed := fake.closed
	fake.mu.Unlock()
	if closed != 1 {
		t.Fatalf("DAVE Close calls = %d, want 1", closed)
	}
}
