package main

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/bwmarrin/discordgo"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestConcurrentThreadMessages(t *testing.T) {
	s, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.State.GuildAdd(&discordgo.Guild{ID: "guild", Threads: []*discordgo.Channel{{ID: "thread", GuildID: "guild", Type: discordgo.ChannelTypeGuildPublicThread}}}); err != nil {
		t.Fatal(err)
	}
	var replies, edits atomic.Int32
	s.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			replies.Add(1)
		} else if r.Method == http.MethodPatch {
			edits.Add(1)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"thread","channel_id":"thread"}`)), Request: r}, nil
	})
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			messageCreate(s, &discordgo.MessageCreate{Message: &discordgo.Message{ID: "message", GuildID: "guild", ChannelID: "thread", Content: "ping", Author: &discordgo.User{ID: "user", Username: "user"}}})
		}()
	}
	close(start)
	wg.Wait()
	if replies.Load() != 8 || edits.Load() == 0 {
		t.Fatalf("replies = %d, archive edits = %d", replies.Load(), edits.Load())
	}
}
