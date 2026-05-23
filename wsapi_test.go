package discordgo

import (
	"testing"

	"github.com/gorilla/websocket"
)

func TestShouldReconnectOnGatewayClose(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "authentication failed",
			err:  &websocket.CloseError{Code: 4004},
			want: false,
		},
		{
			name: "invalid shard",
			err:  &websocket.CloseError{Code: 4010},
			want: false,
		},
		{
			name: "sharding required",
			err:  &websocket.CloseError{Code: 4011},
			want: false,
		},
		{
			name: "invalid api version",
			err:  &websocket.CloseError{Code: 4012},
			want: false,
		},
		{
			name: "invalid intents",
			err:  &websocket.CloseError{Code: 4013},
			want: false,
		},
		{
			name: "disallowed intents",
			err:  &websocket.CloseError{Code: 4014},
			want: false,
		},
		{
			name: "session timed out",
			err:  &websocket.CloseError{Code: 4009},
			want: true,
		},
		{
			name: "network error",
			err:  websocket.ErrCloseSent,
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldReconnectOnGatewayClose(tt.err); got != tt.want {
				t.Fatalf("shouldReconnectOnGatewayClose() = %v, want %v", got, tt.want)
			}
		})
	}
}
