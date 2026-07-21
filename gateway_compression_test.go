package discordgo

import (
	"bytes"
	"compress/zlib"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

func compressedGatewayPayload(t testing.TB, payload []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}

func TestGatewayCompressedDispatchReuseAfterError(t *testing.T) {
	message := compressedGatewayPayload(t, []byte(`{"op":11,"s":42,"d":null}`))
	session, err := New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	session.StateEnabled = false

	if _, err = session.onEvent(websocket.BinaryMessage, message); err != nil {
		t.Fatalf("initial valid payload failed: %v", err)
	}
	if _, err = session.onEvent(websocket.BinaryMessage, []byte("invalid")); err == nil {
		t.Fatal("invalid compressed payload returned nil error")
	}
	if _, err = session.onEvent(websocket.BinaryMessage, message[:len(message)/2]); err == nil {
		t.Fatal("truncated compressed payload returned nil error")
	}
	for i := 0; i < 100; i++ {
		if _, err = session.onEvent(websocket.BinaryMessage, message); err != nil {
			t.Fatalf("valid payload %d failed after invalid payloads: %v", i, err)
		}
	}
}

func TestPeekGatewayCompressedEvent(t *testing.T) {
	message := compressedGatewayPayload(t, []byte(`{"op":10,"s":42,"t":"READY","d":{}}`))
	event, err := peekGatewayEvent(websocket.BinaryMessage, message)
	if err != nil {
		t.Fatalf("peekGatewayEvent() error = %v", err)
	}
	if event == nil || event.Operation != 10 || event.Type != "READY" {
		t.Fatalf("peekGatewayEvent() = %#v", event)
	}
	if _, err = peekGatewayEvent(websocket.BinaryMessage, message[:len(message)/2]); err == nil {
		t.Fatal("peekGatewayEvent() accepted a truncated payload")
	}
}

func TestGatewayCompressedDispatchConcurrentReuse(t *testing.T) {
	message := compressedGatewayPayload(t, []byte(`{"op":11,"s":42,"d":null}`))
	const goroutines = 32
	const iterations = 100
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			session, err := New("Bot token")
			if err != nil {
				errs <- err
				return
			}
			session.StateEnabled = false
			for j := 0; j < iterations; j++ {
				if _, err = session.onEvent(websocket.BinaryMessage, message); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func BenchmarkGatewayCompressedDispatch(b *testing.B) {
	payload := []byte(`{"op":0,"s":42,"t":"MESSAGE_CREATE","d":{"id":"100","channel_id":"200","guild_id":"300","author":{"id":"400","username":"bot"},"content":"hello","timestamp":"2026-07-21T00:00:00Z"}}`)
	session, err := New("Bot token")
	if err != nil {
		b.Fatal(err)
	}
	session.StateEnabled = false
	session.SyncEvents = true
	message := compressedGatewayPayload(b, payload)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err = session.onEvent(websocket.BinaryMessage, message); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGatewayTextDispatch(b *testing.B) {
	message := []byte(`{"op":0,"s":42,"t":"MESSAGE_CREATE","d":{"id":"100","channel_id":"200","guild_id":"300","author":{"id":"400","username":"bot"},"content":"hello","timestamp":"2026-07-21T00:00:00Z"}}`)
	session, err := New("Bot token")
	if err != nil {
		b.Fatal(err)
	}
	session.StateEnabled = false
	session.SyncEvents = true

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err = session.onEvent(websocket.TextMessage, message); err != nil {
			b.Fatal(err)
		}
	}
}
