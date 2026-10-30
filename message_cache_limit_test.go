package discordgo

import (
	"errors"
	"testing"
)

func TestMessageCacheRejectsNegativeLimit(t *testing.T) {
	for _, event := range []string{"create", "update", "direct"} {
		t.Run(event, func(t *testing.T) {
			session, err := New("")
			if err != nil {
				t.Fatal(err)
			}
			state := session.State
			if err := state.ChannelAdd(&Channel{ID: "channel", Type: ChannelTypeDM}); err != nil {
				t.Fatal(err)
			}
			state.MaxMessageCount = -1
			message := &Message{ID: "message", ChannelID: "channel"}
			switch event {
			case "create":
				err = state.OnInterface(session, &MessageCreate{Message: message})
			case "update":
				err = state.OnInterface(session, &MessageUpdate{Message: message})
			case "direct":
				err = state.MessageAdd(message)
			}
			if !errors.Is(err, ErrStateInvalidData) {
				t.Fatalf("error = %v, want ErrStateInvalidData", err)
			}
			channel, err := state.Channel("channel")
			if err != nil {
				t.Fatal(err)
			}
			if len(channel.Messages) != 0 {
				t.Fatal("invalid limit changed the message cache")
			}
		})
	}
}
