package discordgo

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestChannelEditMarshalNullableFields(t *testing.T) {
	tests := []struct {
		name string
		edit ChannelEdit
		want string
	}{
		{
			name: "omits unset nullable fields",
			edit: ChannelEdit{Name: "ticket"},
			want: `{"name":"ticket"}`,
		},
		{
			name: "keeps non-empty parent id",
			edit: ChannelEdit{ParentID: "category"},
			want: `{"parent_id":"category"}`,
		},
		{
			name: "clears parent id",
			edit: ChannelEdit{ParentIDNull: true},
			want: `{"parent_id":null}`,
		},
		{
			name: "keeps empty permission overwrites",
			edit: ChannelEdit{PermissionOverwrites: []*PermissionOverwrite{}},
			want: `{"permission_overwrites":[]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.edit)
			if err != nil {
				t.Fatalf("Marshal returned error: %v", err)
			}
			assertJSONEqual(t, got, []byte(tt.want))
		})
	}
}

func assertJSONEqual(t *testing.T, got, want []byte) {
	t.Helper()

	var gotJSON interface{}
	if err := json.Unmarshal(got, &gotJSON); err != nil {
		t.Fatalf("got is invalid JSON: %v", err)
	}

	var wantJSON interface{}
	if err := json.Unmarshal(want, &wantJSON); err != nil {
		t.Fatalf("want is invalid JSON: %v", err)
	}

	if !reflect.DeepEqual(gotJSON, wantJSON) {
		t.Fatalf("JSON = %s, want %s", got, want)
	}
}
