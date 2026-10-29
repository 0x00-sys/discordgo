package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func TestHandleVoiceCancellationLeavesReceiveChannelOpen(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	packets := make(chan *discordgo.Packet)
	done := make(chan struct{})
	go func() {
		handleVoice(ctx, packets)
		close(done)
	}()
	select {
	case packets <- &discordgo.Packet{SSRC: 42, Opus: []byte{0xf8, 0xff, 0xfe}}:
	case <-time.After(time.Second):
		t.Fatal("recorder did not accept packet")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("recorder did not stop on cancellation")
	}
	// A final transport send must remain legal after the consumer stops.
	sent := make(chan struct{})
	go func() { packets <- &discordgo.Packet{}; close(sent) }()
	select {
	case _, ok := <-packets:
		if !ok {
			t.Fatal("recorder closed the transport's channel")
		}
	case <-time.After(time.Second):
		t.Fatal("receive channel was not usable")
	}
	<-sent
	// Ogg Close marks the final page as EOS. Cancellation must finalize it.
	data, err := os.ReadFile("42.ogg")
	if err != nil {
		t.Fatal(err)
	}
	foundEOS := false
	for i := 0; i+6 <= len(data); i++ {
		if string(data[i:i+4]) == "OggS" && data[i+5]&4 != 0 {
			foundEOS = true
		}
	}
	if !foundEOS {
		t.Fatal("recording was not finalized on cancellation")
	}
}

func TestHandleVoiceCanceledWithoutPackets(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { handleVoice(ctx, make(chan *discordgo.Packet)); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("silent recording did not stop on cancellation")
	}
}
