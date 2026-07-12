package discordgo

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/gorilla/websocket"
)

// VoiceDAVESessionFactory creates a DAVE session for one voice connection.
// The factory is called before voice Identify so its supported protocol
// version can be advertised to Discord.
type VoiceDAVESessionFactory func(userID string, channelID uint64, callbacks VoiceDAVECallbacks) (VoiceDAVESession, error)

// VoiceDAVECallbacks sends messages produced by a DAVE backend to the voice
// gateway. Implementations may call these methods from any goroutine.
type VoiceDAVECallbacks interface {
	SendMLSKeyPackage(keyPackage []byte) error
	SendMLSCommitWelcome(commitWelcome []byte) error
	SendReadyForTransition(transitionID uint16) error
	SendInvalidCommitWelcome(transitionID uint16) error
}

// VoiceDAVESession supplies the MLS and frame cryptography required by the
// DAVE protocol. DiscordGo owns gateway framing and participant/SSRC routing;
// the backend owns MLS state, transition key ratchets, and media transforms.
//
// A maintained libdave binding can be adapted to this interface without
// making CGO or a native library a dependency of DiscordGo itself. Event
// methods own their protocol-mandated responses through VoiceDAVECallbacks,
// including transition readiness and invalid commit/welcome recovery. Close
// must be safe to call more than once.
type VoiceDAVESession interface {
	MaxSupportedProtocolVersion() int
	Ready() bool
	SetLocalSSRC(ssrc uint32)
	EncryptFrame(ssrc uint32, frame []byte) ([]byte, error)
	DecryptFrame(userID string, frame []byte) ([]byte, error)

	AddUser(userID string)
	RemoveUser(userID string)

	OnSessionDescription(protocolVersion uint16)
	OnDAVEPrepareTransition(transitionID, protocolVersion uint16)
	OnDAVEExecuteTransition(transitionID uint16)
	OnDAVEPrepareEpoch(epoch uint64, protocolVersion uint16)
	OnDAVEMLSExternalSenderPackage(externalSender []byte)
	OnDAVEMLSProposals(proposals []byte)
	OnDAVEMLSAnnounceCommitTransition(transitionID uint16, commit []byte)
	OnDAVEMLSWelcome(transitionID uint16, welcome []byte)

	Close() error
}

const (
	voiceOpDAVEPrepareTransition        = 21
	voiceOpDAVEExecuteTransition        = 22
	voiceOpDAVEReadyForTransition       = 23
	voiceOpDAVEPrepareEpoch             = 24
	voiceOpDAVEMLSExternalSenderPackage = 25
	voiceOpDAVEMLSKeyPackage            = 26
	voiceOpDAVEMLSProposals             = 27
	voiceOpDAVEMLSCommitWelcome         = 28
	voiceOpDAVEMLSAnnounceCommit        = 29
	voiceOpDAVEMLSWelcome               = 30
	voiceOpDAVEMLSInvalidCommitWelcome  = 31
)

var (
	errDAVEFrameShort        = errors.New("discordgo: DAVE binary frame is shorter than its header")
	errDAVESessionMissing    = errors.New("discordgo: DAVE session is unavailable")
	errDAVETransitionPending = errors.New("discordgo: DAVE transition is not ready")
)

type voiceDAVECallbacks struct {
	mu         sync.Mutex
	voice      *VoiceConnection
	generation uint64
}

func (c *voiceDAVECallbacks) bind(generation uint64) {
	c.mu.Lock()
	c.generation = generation
	c.mu.Unlock()
}

func (c *voiceDAVECallbacks) connection() (*websocket.Conn, error) {
	c.mu.Lock()
	generation := c.generation
	c.mu.Unlock()
	if generation == 0 || c.voice == nil {
		return nil, ErrWSNotFound
	}

	c.voice.RLock()
	defer c.voice.RUnlock()
	if c.voice.daveGeneration != generation || c.voice.wsConn == nil {
		return nil, ErrWSNotFound
	}
	return c.voice.wsConn, nil
}

func (c *voiceDAVECallbacks) writeJSON(data interface{}) error {
	conn, err := c.connection()
	if err != nil {
		return err
	}
	c.voice.wsMutex.Lock()
	defer c.voice.wsMutex.Unlock()
	return conn.WriteJSON(data)
}

func (c *voiceDAVECallbacks) writeBinary(opcode byte, payload []byte) error {
	if len(payload) == 0 {
		return errors.New("discordgo: empty DAVE binary payload")
	}

	message := make([]byte, len(payload)+1)
	message[0] = opcode
	copy(message[1:], payload)

	conn, err := c.connection()
	if err != nil {
		return err
	}
	c.voice.wsMutex.Lock()
	defer c.voice.wsMutex.Unlock()
	return conn.WriteMessage(websocket.BinaryMessage, message)
}

func (c *voiceDAVECallbacks) SendMLSKeyPackage(keyPackage []byte) error {
	return c.writeBinary(voiceOpDAVEMLSKeyPackage, keyPackage)
}

func (c *voiceDAVECallbacks) SendMLSCommitWelcome(commitWelcome []byte) error {
	return c.writeBinary(voiceOpDAVEMLSCommitWelcome, commitWelcome)
}

func (c *voiceDAVECallbacks) SendReadyForTransition(transitionID uint16) error {
	return c.writeJSON(struct {
		Op   int `json:"op"`
		Data struct {
			TransitionID uint16 `json:"transition_id"`
		} `json:"d"`
	}{
		Op: voiceOpDAVEReadyForTransition,
		Data: struct {
			TransitionID uint16 `json:"transition_id"`
		}{TransitionID: transitionID},
	})
}

func (c *voiceDAVECallbacks) SendInvalidCommitWelcome(transitionID uint16) error {
	return c.writeJSON(struct {
		Op   int `json:"op"`
		Data struct {
			TransitionID uint16 `json:"transition_id"`
		} `json:"d"`
	}{
		Op: voiceOpDAVEMLSInvalidCommitWelcome,
		Data: struct {
			TransitionID uint16 `json:"transition_id"`
		}{TransitionID: transitionID},
	})
}

func (v *VoiceConnection) newVoiceDAVESession(factory VoiceDAVESessionFactory, userID, channelID string) (VoiceDAVESession, *voiceDAVECallbacks, int, error) {
	if factory == nil {
		return nil, nil, 0, nil
	}

	channel, err := strconv.ParseUint(channelID, 10, 64)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("parsing DAVE channel id: %w", err)
	}

	callbacks := &voiceDAVECallbacks{voice: v}
	session, err := factory(userID, channel, callbacks)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("creating DAVE session: %w", err)
	}
	if session == nil {
		return nil, nil, 0, errors.New("creating DAVE session: factory returned nil")
	}

	maxVersion := session.MaxSupportedProtocolVersion()
	if maxVersion < 0 || maxVersion > 0xffff {
		_ = session.Close()
		return nil, nil, 0, fmt.Errorf("invalid DAVE protocol version %d", maxVersion)
	}

	return session, callbacks, maxVersion, nil
}

// installVoiceDAVESessionLocked installs a session before its callbacks can
// write. The caller must hold v.Lock.
func (v *VoiceConnection) installVoiceDAVESessionLocked(session VoiceDAVESession, callbacks *voiceDAVECallbacks, maxVersion int) {
	v.daveGeneration++
	v.dave = session
	v.daveMaxProtocolVersion = maxVersion
	v.daveProtocolVersion = 0
	v.davePreparedProtocolVersion = 0
	v.davePendingTransitions = make(map[uint16]uint16)
	v.daveUsers = make(map[string]struct{})
	v.daveSSRCToUserID = make(map[uint32]string)
	if callbacks != nil {
		callbacks.bind(v.daveGeneration)
	}
}

func (v *VoiceConnection) closeVoiceDAVE() {
	v.daveMu.Lock()
	defer v.daveMu.Unlock()

	v.Lock()
	session := v.dave
	v.dave = nil
	v.daveGeneration++
	v.daveMaxProtocolVersion = 0
	v.daveProtocolVersion = 0
	v.davePreparedProtocolVersion = 0
	v.davePendingTransitions = nil
	v.daveUsers = nil
	v.daveSSRCToUserID = nil
	v.Unlock()

	if session != nil {
		if err := session.Close(); err != nil {
			v.log(LogError, "DAVE close error, %s", err)
		}
	}
}

func (v *VoiceConnection) onDAVESessionDescription(protocolVersion uint16) error {
	v.daveMu.Lock()
	defer v.daveMu.Unlock()

	v.RLock()
	session := v.dave
	maxVersion := v.daveMaxProtocolVersion
	v.RUnlock()

	if session == nil {
		if protocolVersion != 0 {
			return fmt.Errorf("DAVE protocol version %d selected without a backend", protocolVersion)
		}
		return nil
	}
	if int(protocolVersion) > maxVersion {
		return fmt.Errorf("DAVE protocol version %d exceeds advertised maximum %d", protocolVersion, maxVersion)
	}

	v.Lock()
	v.daveProtocolVersion = protocolVersion
	v.davePreparedProtocolVersion = protocolVersion
	v.davePendingTransitions = make(map[uint16]uint16)
	v.Unlock()
	session.OnSessionDescription(protocolVersion)
	return nil
}

// DAVEReady reports whether this voice connection can currently transform
// media for its selected DAVE protocol. It returns true when DAVE is disabled.
func (v *VoiceConnection) DAVEReady() bool {
	v.daveMu.Lock()
	defer v.daveMu.Unlock()

	v.RLock()
	session := v.dave
	protocolVersion := v.daveProtocolVersion
	v.RUnlock()
	if protocolVersion == 0 {
		return true
	}
	return session != nil && session.Ready()
}

func (v *VoiceConnection) onDAVELocalSSRC(ssrc uint32) {
	v.daveMu.Lock()
	defer v.daveMu.Unlock()
	v.RLock()
	session := v.dave
	v.RUnlock()
	if session != nil {
		session.SetLocalSSRC(ssrc)
	}
}

func (v *VoiceConnection) onDAVESpeakingUpdate(update *VoiceSpeakingUpdate) {
	if update == nil || update.UserID == "" || update.SSRC <= 0 {
		return
	}
	v.Lock()
	if v.daveSSRCToUserID != nil {
		v.daveSSRCToUserID[uint32(update.SSRC)] = update.UserID
	}
	v.Unlock()
}

func (v *VoiceConnection) onDAVEClientsConnect(data json.RawMessage) {
	var payload struct {
		UserIDs []string `json:"user_ids"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		v.log(LogError, "DAVE clients connect unmarshal error, %s", err)
		return
	}

	v.daveMu.Lock()
	defer v.daveMu.Unlock()
	v.Lock()
	session := v.dave
	selfID := v.UserID
	if session == nil {
		v.Unlock()
		return
	}
	users := make([]string, 0, len(payload.UserIDs))
	if v.daveUsers == nil {
		v.daveUsers = make(map[string]struct{})
	}
	for _, userID := range payload.UserIDs {
		if userID == "" || userID == selfID {
			continue
		}
		if _, ok := v.daveUsers[userID]; ok {
			continue
		}
		v.daveUsers[userID] = struct{}{}
		users = append(users, userID)
	}
	v.Unlock()

	for _, userID := range users {
		session.AddUser(userID)
	}
}

func (v *VoiceConnection) onDAVEClientDisconnect(data json.RawMessage) {
	var payload struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		v.log(LogError, "DAVE client disconnect unmarshal error, %s", err)
		return
	}
	if payload.UserID == "" {
		return
	}

	v.daveMu.Lock()
	defer v.daveMu.Unlock()
	v.Lock()
	session := v.dave
	_, connected := v.daveUsers[payload.UserID]
	delete(v.daveUsers, payload.UserID)
	for ssrc, userID := range v.daveSSRCToUserID {
		if userID == payload.UserID {
			delete(v.daveSSRCToUserID, ssrc)
		}
	}
	v.Unlock()
	if session != nil && connected {
		session.RemoveUser(payload.UserID)
	}
}

func (v *VoiceConnection) onDAVEPrepareTransition(data json.RawMessage) {
	var payload struct {
		TransitionID    *uint16 `json:"transition_id"`
		ProtocolVersion *uint16 `json:"protocol_version"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		v.log(LogError, "DAVE prepare transition unmarshal error, %s", err)
		return
	}
	if payload.TransitionID == nil || payload.ProtocolVersion == nil {
		v.log(LogError, "DAVE prepare transition is missing required fields")
		return
	}

	v.daveMu.Lock()
	defer v.daveMu.Unlock()
	v.RLock()
	session := v.dave
	v.RUnlock()
	if session == nil {
		return
	}
	session.OnDAVEPrepareTransition(*payload.TransitionID, *payload.ProtocolVersion)

	v.Lock()
	if *payload.TransitionID == 0 {
		v.daveProtocolVersion = *payload.ProtocolVersion
		v.davePreparedProtocolVersion = *payload.ProtocolVersion
	} else {
		if v.davePendingTransitions == nil {
			v.davePendingTransitions = make(map[uint16]uint16)
		}
		v.davePendingTransitions[*payload.TransitionID] = *payload.ProtocolVersion
	}
	v.Unlock()
}

func (v *VoiceConnection) onDAVEExecuteTransition(data json.RawMessage) {
	var payload struct {
		TransitionID *uint16 `json:"transition_id"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		v.log(LogError, "DAVE execute transition unmarshal error, %s", err)
		return
	}
	if payload.TransitionID == nil {
		v.log(LogError, "DAVE execute transition is missing its transition id")
		return
	}

	v.daveMu.Lock()
	defer v.daveMu.Unlock()
	v.RLock()
	session := v.dave
	v.RUnlock()
	if session == nil {
		return
	}
	session.OnDAVEExecuteTransition(*payload.TransitionID)

	v.Lock()
	if protocolVersion, ok := v.davePendingTransitions[*payload.TransitionID]; ok {
		v.daveProtocolVersion = protocolVersion
		delete(v.davePendingTransitions, *payload.TransitionID)
	}
	v.Unlock()
}

func (v *VoiceConnection) onDAVEPrepareEpoch(data json.RawMessage) {
	var payload struct {
		Epoch           *uint64 `json:"epoch"`
		ProtocolVersion *uint16 `json:"protocol_version"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		v.log(LogError, "DAVE prepare epoch unmarshal error, %s", err)
		return
	}
	if payload.Epoch == nil || payload.ProtocolVersion == nil {
		v.log(LogError, "DAVE prepare epoch is missing required fields")
		return
	}

	v.daveMu.Lock()
	defer v.daveMu.Unlock()
	v.Lock()
	session := v.dave
	if session != nil {
		v.davePreparedProtocolVersion = *payload.ProtocolVersion
	}
	v.Unlock()
	if session != nil {
		session.OnDAVEPrepareEpoch(*payload.Epoch, *payload.ProtocolVersion)
	}
}

func parseDAVEBinaryFrame(frame []byte) (sequence uint16, opcode byte, payload []byte, err error) {
	if len(frame) < 3 {
		return 0, 0, nil, errDAVEFrameShort
	}
	return binary.BigEndian.Uint16(frame[:2]), frame[2], frame[3:], nil
}

func (v *VoiceConnection) handleDAVEBinaryFrame(frame []byte) {
	sequence, opcode, payload, err := parseDAVEBinaryFrame(frame)
	if err != nil {
		v.log(LogError, "DAVE binary frame error, %s", err)
		return
	}
	v.updateVoiceSequence(int64(sequence))

	v.daveMu.Lock()
	defer v.daveMu.Unlock()
	v.RLock()
	session := v.dave
	v.RUnlock()
	if session == nil {
		v.log(LogWarning, "DAVE binary opcode %d received without a backend", opcode)
		return
	}

	switch opcode {
	case voiceOpDAVEMLSExternalSenderPackage:
		if len(payload) == 0 {
			v.log(LogError, "DAVE external sender payload is empty")
			return
		}
		session.OnDAVEMLSExternalSenderPackage(payload)
	case voiceOpDAVEMLSProposals:
		if len(payload) == 0 {
			v.log(LogError, "DAVE proposals payload is empty")
			return
		}
		if payload[0] > 1 {
			v.log(LogError, "DAVE proposals operation %d is invalid", payload[0])
			return
		}
		session.OnDAVEMLSProposals(payload)
	case voiceOpDAVEMLSAnnounceCommit, voiceOpDAVEMLSWelcome:
		if len(payload) < 2 {
			v.log(LogError, "DAVE opcode %d payload is shorter than its transition id", opcode)
			return
		}
		transitionID := binary.BigEndian.Uint16(payload[:2])
		message := payload[2:]
		if len(message) == 0 {
			v.log(LogError, "DAVE opcode %d has an empty MLS message", opcode)
			return
		}
		if opcode == voiceOpDAVEMLSAnnounceCommit {
			session.OnDAVEMLSAnnounceCommitTransition(transitionID, message)
		} else {
			session.OnDAVEMLSWelcome(transitionID, message)
		}

		v.Lock()
		protocolVersion := v.davePreparedProtocolVersion
		if protocolVersion == 0 {
			protocolVersion = v.daveProtocolVersion
		}
		if transitionID == 0 {
			v.daveProtocolVersion = protocolVersion
		} else {
			v.davePendingTransitions[transitionID] = protocolVersion
		}
		v.Unlock()
	default:
		v.log(LogWarning, "unknown DAVE binary opcode %d", opcode)
	}
}

func isOpusSilenceFrame(frame []byte) bool {
	return len(frame) == 3 && frame[0] == 0xf8 && frame[1] == 0xff && frame[2] == 0xfe
}

func (v *VoiceConnection) encryptDAVEFrame(ssrc uint32, frame []byte) ([]byte, error) {
	v.daveMu.Lock()
	defer v.daveMu.Unlock()
	v.RLock()
	session := v.dave
	protocolVersion := v.daveProtocolVersion
	v.RUnlock()
	if protocolVersion == 0 {
		return frame, nil
	}
	if session == nil {
		return nil, errDAVESessionMissing
	}
	if !session.Ready() {
		return nil, errDAVETransitionPending
	}

	encrypted, err := session.EncryptFrame(ssrc, frame)
	if err != nil {
		return nil, fmt.Errorf("encrypting DAVE frame: %w", err)
	}
	if encrypted == nil && len(frame) != 0 {
		return nil, errors.New("encrypting DAVE frame: backend returned nil")
	}
	return encrypted, nil
}

func (v *VoiceConnection) decryptDAVEFrame(ssrc uint32, frame []byte) ([]byte, error) {
	if isOpusSilenceFrame(frame) {
		return frame, nil
	}

	v.daveMu.Lock()
	defer v.daveMu.Unlock()
	v.RLock()
	session := v.dave
	protocolVersion := v.daveProtocolVersion
	userID := v.daveSSRCToUserID[ssrc]
	v.RUnlock()
	if session == nil {
		if protocolVersion == 0 {
			return frame, nil
		}
		return nil, errDAVESessionMissing
	}
	if userID == "" {
		return nil, fmt.Errorf("decrypting DAVE frame: unknown ssrc %d", ssrc)
	}

	decrypted, err := session.DecryptFrame(userID, frame)
	if err != nil {
		return nil, fmt.Errorf("decrypting DAVE frame: %w", err)
	}
	if decrypted == nil && len(frame) != 0 {
		return nil, errors.New("decrypting DAVE frame: backend returned nil")
	}
	return decrypted, nil
}

func voiceMessageOperation(message []byte) (int, error) {
	var payload struct {
		Operation int `json:"op"`
	}
	if err := json.Unmarshal(message, &payload); err != nil {
		return 0, err
	}
	return payload.Operation, nil
}

func (v *VoiceConnection) updateDAVESpeakingMessage(message []byte) {
	var event Event
	if err := json.Unmarshal(message, &event); err != nil {
		return
	}
	var update VoiceSpeakingUpdate
	if err := json.Unmarshal(event.RawData, &update); err != nil {
		return
	}
	v.onDAVESpeakingUpdate(&update)
}

func (v *VoiceConnection) dispatchVoiceMessage(messageType int, message []byte) {
	if messageType == websocket.BinaryMessage {
		v.handleDAVEBinaryFrame(message)
		return
	}

	v.updateSequence(message)
	operation, err := voiceMessageOperation(message)
	if err != nil {
		go v.onEvent(message)
		return
	}
	if operation == 5 {
		v.updateDAVESpeakingMessage(message)
	}

	switch operation {
	case 4, 11, 13,
		voiceOpDAVEPrepareTransition,
		voiceOpDAVEExecuteTransition,
		voiceOpDAVEPrepareEpoch:
		v.onEvent(message)
	default:
		go v.onEvent(message)
	}
}
