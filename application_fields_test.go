package discordgo

import (
	"encoding/json"
	"testing"
)

func TestApplicationCurrentResponseFields(t *testing.T) {
	var application Application
	if err := json.Unmarshal([]byte(`{"type":4,"explicit_content_filter":1,"max_participants":100}`), &application); err != nil {
		t.Fatalf("Unmarshal values returned error: %v", err)
	}
	if application.Type == nil || *application.Type != ApplicationTypeGuildRoleSubscriptions {
		t.Fatalf("Type = %v", application.Type)
	}
	if application.ExplicitContentFilter != ApplicationExplicitContentFilterAlways {
		t.Fatalf("ExplicitContentFilter = %v", application.ExplicitContentFilter)
	}
	if application.MaxParticipants == nil || *application.MaxParticipants != 100 {
		t.Fatalf("MaxParticipants = %v", application.MaxParticipants)
	}

	application = Application{}
	if err := json.Unmarshal([]byte(`{"type":null,"explicit_content_filter":0,"max_participants":null}`), &application); err != nil {
		t.Fatalf("Unmarshal nulls returned error: %v", err)
	}
	if application.Type != nil || application.MaxParticipants != nil || application.ExplicitContentFilter != ApplicationExplicitContentFilterInherit {
		t.Fatalf("nullable fields = %#v", application)
	}

	application = Application{}
	if err := json.Unmarshal([]byte(`{}`), &application); err != nil {
		t.Fatalf("Unmarshal omitted fields returned error: %v", err)
	}
	if application.Type != nil || application.MaxParticipants != nil {
		t.Fatalf("omitted nullable fields = %#v", application)
	}
}
