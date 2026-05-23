// Discordgo - Discord bindings for Go
// Available at https://github.com/bwmarrin/discordgo

// Copyright 2015-2016 Bruce Marriner <bruce@sqls.net>.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package discordgo

import (
	"encoding/json"
	"testing"
)

func TestMember_DisplayName(t *testing.T) {
	user := &User{
		GlobalName: "Global",
	}
	t.Run("no server nickname set", func(t *testing.T) {
		m := &Member{
			Nick: "",
			User: user,
		}
		want := user.DisplayName()
		if dn := m.DisplayName(); dn != want {
			t.Errorf("Member.DisplayName() = %v, want %v", dn, want)
		}
	})
	t.Run("server nickname set", func(t *testing.T) {
		m := &Member{
			Nick: "Server",
			User: user,
		}
		if dn := m.DisplayName(); dn != m.Nick {
			t.Errorf("Member.DisplayName() = %v, want %v", dn, m.Nick)
		}
	})
}

func TestActivityUnmarshalApplicationID(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{
			name: "string",
			data: `{"name":"Rocket League","type":0,"created_at":1511200066000,"application_id":"379286085710381999"}`,
			want: "379286085710381999",
		},
		{
			name: "number",
			data: `{"name":"Rocket League","type":0,"created_at":1511200066000,"application_id":379286085710381999}`,
			want: "379286085710381999",
		},
		{
			name: "null",
			data: `{"name":"Rocket League","type":0,"created_at":1511200066000,"application_id":null}`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var activity Activity
			if err := json.Unmarshal([]byte(tt.data), &activity); err != nil {
				t.Fatalf("json.Unmarshal() returned error: %v", err)
			}
			if activity.ApplicationID != tt.want {
				t.Fatalf("ApplicationID = %q, want %q", activity.ApplicationID, tt.want)
			}
		})
	}
}

func TestForumTagUnmarshalID(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{
			name: "string",
			data: `{"id":"123456789012345678","name":"open","moderated":true,"emoji_id":"987654321098765432"}`,
			want: "123456789012345678",
		},
		{
			name: "number",
			data: `{"id":1000,"name":"mod queue","moderated":false,"emoji_name":"tag"}`,
			want: "1000",
		},
		{
			name: "null",
			data: `{"id":null,"name":"missing","moderated":false}`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var tag ForumTag
			if err := json.Unmarshal([]byte(tt.data), &tag); err != nil {
				t.Fatalf("json.Unmarshal() returned error: %v", err)
			}
			if tag.ID != tt.want {
				t.Fatalf("ID = %q, want %q", tag.ID, tt.want)
			}
		})
	}
}
