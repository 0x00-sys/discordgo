package discordgo

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

var voiceResumeTimeout = 10 * time.Second

type voiceResumeData struct {
	ServerID    string `json:"server_id"`
	SessionID   string `json:"session_id"`
	Token       string `json:"token"`
	SequenceAck int64  `json:"seq_ack"`
}

type voiceResumeOp struct {
	Op   int             `json:"op"`
	Data voiceResumeData `json:"d"`
}

func (v *VoiceConnection) canResumeVoice() bool {
	v.RLock()
	defer v.RUnlock()
	return v.close != nil && v.udpConn != nil && v.aead != nil
}

// resumeVoice replaces only the severed voice websocket. UDP transport and
// DAVE MLS state remain alive so v8 can replay every missed JSON and binary
// message after the last acknowledged sequence.
func (v *VoiceConnection) resumeVoice() (err error) {
	v.RLock()
	session := v.session
	endpoint := v.endpoint
	guildID := v.GuildID
	sessionID := v.sessionID
	token := v.token
	closeChan := v.close
	v.RUnlock()
	if session == nil || endpoint == "" || guildID == "" || sessionID == "" || token == "" || closeChan == nil {
		return errors.New("voice resume data is incomplete")
	}

	session.RLock()
	dialer := session.Dialer
	session.RUnlock()
	if dialer == nil {
		return ErrWSNotFound
	}

	gateway := "wss://" + strings.TrimSuffix(endpoint, ":80") + "?v=" + strconv.Itoa(voiceGatewayVersion)
	conn, _, err := dialer.Dial(gateway, nil)
	if err != nil {
		return fmt.Errorf("dialing voice resume websocket: %w", err)
	}
	resumed := false
	defer func() {
		if resumed {
			return
		}
		_ = conn.Close()
		v.Lock()
		if v.wsConn == conn {
			v.wsConn = nil
		}
		v.Unlock()
	}()

	writeDone := make(chan struct{})
	writeWatcherDone := make(chan struct{})
	go func() {
		defer close(writeWatcherDone)
		select {
		case <-closeChan:
			_ = conn.Close()
		case <-writeDone:
		}
	}()

	sequenceAck := v.voiceSequenceAck()
	v.wsMutex.Lock()
	err = conn.WriteJSON(voiceResumeOp{Op: 7, Data: voiceResumeData{
		ServerID:    guildID,
		SessionID:   sessionID,
		Token:       token,
		SequenceAck: sequenceAck,
	}})
	v.wsMutex.Unlock()
	close(writeDone)
	<-writeWatcherDone
	if err != nil {
		return fmt.Errorf("sending voice resume: %w", err)
	}

	v.Lock()
	if v.disconnecting || v.close != closeChan || v.wsConn != nil {
		v.Unlock()
		return errors.New("voice connection changed during resume")
	}
	v.wsConn = conn
	v.Unlock()

	if err = conn.SetReadDeadline(time.Now().Add(voiceResumeTimeout)); err != nil {
		return fmt.Errorf("setting voice resume deadline: %w", err)
	}
	for {
		messageType, message, readErr := conn.ReadMessage()
		if readErr != nil {
			return fmt.Errorf("waiting for voice resume: %w", readErr)
		}

		if messageType != websocket.BinaryMessage {
			operation, operationErr := voiceMessageOperation(message)
			if operationErr == nil && operation == 9 {
				v.updateSequence(message)
				if err = conn.SetReadDeadline(time.Time{}); err != nil {
					return fmt.Errorf("clearing voice resume deadline: %w", err)
				}
				v.Lock()
				v.speaking = false
				v.Unlock()
				resumed = true
				go v.wsListen(conn, closeChan)
				return nil
			}
		}

		v.dispatchVoiceMessage(messageType, message)
	}
}
