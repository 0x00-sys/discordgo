package discordgo

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestStatusHelpersSendIdleStatus(t *testing.T) {
	helpers := []struct {
		name string
		call func(*Session, int) error
		kind ActivityType
		url  string
	}{
		{"game", func(s *Session, idle int) error { return s.UpdateGameStatus(idle, "activity") }, ActivityTypeGame, ""},
		{"watch", func(s *Session, idle int) error { return s.UpdateWatchStatus(idle, "activity") }, ActivityTypeWatching, ""},
		{"stream", func(s *Session, idle int) error {
			return s.UpdateStreamingStatus(idle, "activity", "https://www.twitch.tv/example")
		}, ActivityTypeStreaming, "https://www.twitch.tv/example"},
	}
	for _, helper := range helpers {
		for _, test := range []struct {
			idle   int
			status string
		}{
			{91879201, "idle"},
			{0, "online"},
			{-1, "online"},
		} {
			t.Run(fmt.Sprintf("%s/idle=%d", helper.name, test.idle), func(t *testing.T) {
				writes := captureGatewayWrites(t, 1, func(s *Session) error {
					return helper.call(s, test.idle)
				})
				if writes[0].Op != 3 {
					t.Fatalf("opcode = %d, want 3", writes[0].Op)
				}
				var data struct {
					Status     string `json:"status"`
					IdleSince  *int   `json:"since"`
					Activities []*struct {
						Name string       `json:"name"`
						Type ActivityType `json:"type"`
						URL  string       `json:"url"`
					} `json:"activities"`
				}
				if err := json.Unmarshal(writes[0].Data, &data); err != nil {
					t.Fatal(err)
				}
				if data.Status != test.status {
					t.Errorf("status = %q, want %q", data.Status, test.status)
				}
				if test.idle > 0 {
					if data.IdleSince == nil || *data.IdleSince != test.idle {
						t.Errorf("idle timestamp was not preserved: %s", writes[0].Data)
					}
				} else if data.IdleSince != nil {
					t.Errorf("non-idle status has timestamp %d", *data.IdleSince)
				}
				if len(data.Activities) != 1 || data.Activities[0] == nil {
					t.Fatalf("activities = %#v, want one activity", data.Activities)
				}
				activity := data.Activities[0]
				if activity.Name != "activity" || activity.Type != helper.kind || activity.URL != helper.url {
					t.Errorf("activity changed: %+v", activity)
				}
			})
		}
	}
}
