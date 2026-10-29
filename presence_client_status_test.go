package discordgo

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestPresenceUpdateClearsInactiveClients(t *testing.T) {
	for _, test := range []struct {
		name    string
		status  Status
		clients string
		want    ClientStatus
	}{
		{"desktop only", StatusIdle, `{"desktop":"idle"}`, ClientStatus{Desktop: StatusIdle}},
		{"mobile only", StatusOnline, `{"mobile":"online"}`, ClientStatus{Mobile: StatusOnline}},
		{"web only", StatusDoNotDisturb, `{"web":"dnd"}`, ClientStatus{Web: StatusDoNotDisturb}},
		{"offline", StatusOffline, `{}`, ClientStatus{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := NewState()
			initial := &Presence{
				User:         &User{ID: "user", Username: "cached-name"},
				Status:       StatusOnline,
				ClientStatus: ClientStatus{Desktop: StatusOnline, Mobile: StatusOnline, Web: StatusOnline},
			}
			if err := state.GuildAdd(&Guild{ID: "guild", Presences: []*Presence{initial}}); err != nil {
				t.Fatal(err)
			}
			old, err := state.Presence("guild", "user")
			if err != nil {
				t.Fatal(err)
			}
			var update PresenceUpdate
			payload := fmt.Sprintf(`{"guild_id":"guild","user":{"id":"user"},"status":%q,"activities":[],"client_status":%s}`, test.status, test.clients)
			if err := json.Unmarshal([]byte(payload), &update); err != nil {
				t.Fatal(err)
			}
			if err := state.OnInterface(&Session{StateEnabled: true}, &update); err != nil {
				t.Fatal(err)
			}
			current, err := state.Presence("guild", "user")
			if err != nil {
				t.Fatal(err)
			}
			if current.ClientStatus != test.want {
				t.Errorf("client status = %+v, want %+v", current.ClientStatus, test.want)
			}
			if current.Status != test.status || current.User.Username != "cached-name" {
				t.Errorf("presence status/user = %s/%s", current.Status, current.User.Username)
			}
			if old.ClientStatus != (ClientStatus{Desktop: StatusOnline, Mobile: StatusOnline, Web: StatusOnline}) {
				t.Errorf("previous snapshot changed: %+v", old.ClientStatus)
			}
		})
	}
}
