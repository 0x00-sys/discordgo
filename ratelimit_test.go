package discordgo

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This test takes ~2 seconds to run
func TestRatelimitReset(t *testing.T) {
	rl := NewRatelimiter()

	sendReq := func(endpoint string) {
		bucket := rl.LockBucket(endpoint)

		headers := http.Header(make(map[string][]string))

		headers.Set("X-RateLimit-Remaining", "0")
		// Reset for approx 2 seconds from now
		headers.Set("X-RateLimit-Reset", fmt.Sprint(float64(time.Now().Add(time.Second*2).UnixNano())/1e9))
		headers.Set("Date", time.Now().Format(time.RFC850))

		err := bucket.Release(headers)
		if err != nil {
			t.Errorf("Release returned error: %v", err)
		}
	}

	sent := time.Now()
	sendReq("/guilds/99/channels")
	sendReq("/guilds/55/channels")
	sendReq("/guilds/66/channels")

	sendReq("/guilds/99/channels")
	sendReq("/guilds/55/channels")
	sendReq("/guilds/66/channels")

	// We hit the same endpoint 2 times, so we should only be ratelimited 2 second
	// And always less than 4 seconds (unless you're on a stoneage computer or using swap or something...)
	if time.Since(sent) >= time.Second && time.Since(sent) < time.Second*4 {
		t.Log("OK", time.Since(sent))
	} else {
		t.Error("Did not ratelimit correctly, got:", time.Since(sent))
	}
}

// This test takes ~1 seconds to run
func TestRatelimitGlobal(t *testing.T) {
	rl := NewRatelimiter()

	sendReq := func(endpoint string) {
		bucket := rl.LockBucket(endpoint)

		headers := http.Header(make(map[string][]string))

		headers.Set("X-RateLimit-Global", "1")
		// Reset for approx 1 seconds from now
		headers.Set("X-RateLimit-Reset-After", "1")

		err := bucket.Release(headers)
		if err != nil {
			t.Errorf("Release returned error: %v", err)
		}
	}

	sent := time.Now()

	// This should trigger a global ratelimit
	sendReq("/guilds/99/channels")
	time.Sleep(time.Millisecond * 100)

	// This shouldn't go through in less than 1 second
	sendReq("/guilds/55/channels")

	if time.Since(sent) >= time.Second && time.Since(sent) < time.Second*2 {
		t.Log("OK", time.Since(sent))
	} else {
		t.Error("Did not ratelimit correctly, got:", time.Since(sent))
	}
}

func TestRatelimitCleansStaleBuckets(t *testing.T) {
	oldTTL := rateLimitBucketTTL
	oldInterval := rateLimitBucketCleanupInterval
	rateLimitBucketTTL = time.Minute
	rateLimitBucketCleanupInterval = 0
	defer func() {
		rateLimitBucketTTL = oldTTL
		rateLimitBucketCleanupInterval = oldInterval
	}()

	rl := NewRatelimiter()
	stale := rl.GetBucket("/channels/stale/messages")
	stale.touch(time.Now().Add(-2 * time.Minute))

	rl.GetBucket("/channels/fresh/messages")

	if _, ok := rl.buckets["/channels/stale/messages"]; ok {
		t.Fatal("stale bucket was not cleaned up")
	}
	if _, ok := rl.buckets["/channels/fresh/messages"]; !ok {
		t.Fatal("fresh bucket was unexpectedly removed")
	}
}

func TestRatelimitDoesNotCleanActiveBuckets(t *testing.T) {
	oldTTL := rateLimitBucketTTL
	oldInterval := rateLimitBucketCleanupInterval
	rateLimitBucketTTL = time.Minute
	rateLimitBucketCleanupInterval = 0
	defer func() {
		rateLimitBucketTTL = oldTTL
		rateLimitBucketCleanupInterval = oldInterval
	}()

	rl := NewRatelimiter()
	active := rl.LockBucket("/channels/active/messages")
	active.touch(time.Now().Add(-2 * time.Minute))

	rl.GetBucket("/channels/fresh/messages")

	if _, ok := rl.buckets["/channels/active/messages"]; !ok {
		t.Fatal("active bucket was unexpectedly removed")
	}

	if err := active.Release(nil); err != nil {
		t.Fatalf("Release returned error: %v", err)
	}
}

func TestRatelimitDoesNotCleanBucketsBeforeReset(t *testing.T) {
	oldTTL := rateLimitBucketTTL
	oldInterval := rateLimitBucketCleanupInterval
	rateLimitBucketTTL = time.Minute
	rateLimitBucketCleanupInterval = 0
	defer func() {
		rateLimitBucketTTL = oldTTL
		rateLimitBucketCleanupInterval = oldInterval
	}()

	rl := NewRatelimiter()
	limited := rl.GetBucket("/channels/limited/messages")
	limited.touch(time.Now().Add(-2 * time.Minute))
	limited.setReset(time.Now().Add(time.Minute))

	rl.GetBucket("/channels/fresh/messages")

	if _, ok := rl.buckets["/channels/limited/messages"]; !ok {
		t.Fatal("limited bucket was unexpectedly removed before reset")
	}
}

func TestWebhookExecuteUsesStableTokenBucket(t *testing.T) {
	session := newTestWebhookSession(t)

	_, err := session.WebhookExecute("webhook", "token", true, &WebhookParams{
		Content: "hello",
	})
	if err != nil {
		t.Fatalf("WebhookExecute returned error: %v", err)
	}

	want := webhookTokenBucketID("webhook")
	if _, ok := session.Ratelimiter.buckets[want]; !ok {
		t.Fatalf("bucket %q was not created", want)
	}
	for key := range session.Ratelimiter.buckets {
		if strings.Contains(key, "?") {
			t.Fatalf("bucket %q includes query parameters", key)
		}
		if strings.Contains(key, "token") {
			t.Fatalf("bucket %q includes webhook token", key)
		}
	}
}

func TestWebhookMessageEditUsesMessageRouteBucket(t *testing.T) {
	session := newTestWebhookSession(t)

	content := "edited"
	_, err := session.WebhookMessageEdit("webhook", "token", "message", &WebhookEdit{
		Content: &content,
	})
	if err != nil {
		t.Fatalf("WebhookMessageEdit returned error: %v", err)
	}

	want := webhookMessageBucketID("webhook")
	if _, ok := session.Ratelimiter.buckets[want]; !ok {
		t.Fatalf("bucket %q was not created", want)
	}
	if _, ok := session.Ratelimiter.buckets[EndpointWebhookToken("", "")]; ok {
		t.Fatalf("shared placeholder bucket %q was created", EndpointWebhookToken("", ""))
	}
}

func newTestWebhookSession(t *testing.T) *Session {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-RateLimit-Remaining", "1")
		w.Header().Set("X-RateLimit-Reset-After", "0.1")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)

	oldEndpointWebhooks := EndpointWebhooks
	EndpointWebhooks = server.URL + "/webhooks/"
	t.Cleanup(func() {
		EndpointWebhooks = oldEndpointWebhooks
	})

	return &Session{
		Client:                 server.Client(),
		MaxRestRetries:         0,
		Ratelimiter:            NewRatelimiter(),
		UserAgent:              "DiscordGo test",
		ShouldRetryOnRateLimit: true,
	}
}

func BenchmarkRatelimitSingleEndpoint(b *testing.B) {
	rl := NewRatelimiter()
	for i := 0; i < b.N; i++ {
		sendBenchReq("/guilds/99/channels", rl)
	}
}

func BenchmarkRatelimitParallelMultiEndpoints(b *testing.B) {
	rl := NewRatelimiter()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			sendBenchReq("/guilds/"+strconv.Itoa(i)+"/channels", rl)
			i++
		}
	})
}

// Does not actually send requests, but locks the bucket and releases it with made-up headers
func sendBenchReq(endpoint string, rl *RateLimiter) {
	bucket := rl.LockBucket(endpoint)

	headers := http.Header(make(map[string][]string))

	headers.Set("X-RateLimit-Remaining", "10")
	headers.Set("X-RateLimit-Reset", fmt.Sprint(float64(time.Now().UnixNano())/1e9))
	headers.Set("Date", time.Now().Format(time.RFC850))

	bucket.Release(headers)
}
