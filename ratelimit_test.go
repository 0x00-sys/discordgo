package discordgo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
	"unsafe"
)

func resetSharedUnauthenticatedGlobal(t *testing.T) {
	t.Helper()
	atomic.StoreInt64(sharedUnauthenticatedGlobal, 0)
	t.Cleanup(func() {
		atomic.StoreInt64(sharedUnauthenticatedGlobal, 0)
	})
}

func TestRatelimitBucketAtomicAlignment(t *testing.T) {
	var bucket Bucket
	for name, offset := range map[string]uintptr{
		"resetAt":  unsafe.Offsetof(bucket.resetAt),
		"lastUsed": unsafe.Offsetof(bucket.lastUsed),
	} {
		if offset%8 != 0 {
			t.Errorf("Bucket.%s offset = %d, want 64-bit alignment", name, offset)
		}
	}
}

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

func TestRatelimitWaitUsesLongerGlobalReset(t *testing.T) {
	rl := NewRatelimiter()
	bucket := rl.GetBucket("/channels/99/messages")
	bucket.Remaining = 0
	bucket.reset = time.Now().Add(10 * time.Millisecond)
	bucket.setReset(bucket.reset)

	atomic.StoreInt64(rl.global, time.Now().Add(time.Minute).UnixNano())

	wait := rl.GetWaitTime(bucket, 1)
	if wait < 30*time.Second {
		t.Fatalf("GetWaitTime returned %v, want it to honor longer global reset", wait)
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

func TestRatelimitCleansBucketWithCustomTTL(t *testing.T) {
	oldTTL := rateLimitBucketTTL
	oldInterval := rateLimitBucketCleanupInterval
	rateLimitBucketTTL = time.Hour
	rateLimitBucketCleanupInterval = 0
	defer func() {
		rateLimitBucketTTL = oldTTL
		rateLimitBucketCleanupInterval = oldInterval
	}()

	rl := NewRatelimiter()
	shortLived := rl.getBucketWithTTL("/webhooks/application/token", rl.unauthenticatedGlobal, time.Minute)
	defaultLived := rl.GetBucket("/channels/default/messages")
	shortLived.touch(time.Now().Add(-2 * time.Minute))
	defaultLived.touch(time.Now().Add(-2 * time.Minute))

	rl.GetBucket("/channels/fresh/messages")

	if _, ok := rl.buckets["/webhooks/application/token"]; ok {
		t.Fatal("custom TTL bucket was not cleaned up")
	}
	if _, ok := rl.buckets["/channels/default/messages"]; !ok {
		t.Fatal("default TTL bucket was unexpectedly removed")
	}
}

func TestLockBucketObjectContextRespectsContextWhileWaitingForLock(t *testing.T) {
	rl := NewRatelimiter()
	bucket := rl.LockBucket("/channels/99/messages")

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := rl.LockBucketObjectContext(ctx, bucket)
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("LockBucketObjectContext error = %v, want context deadline exceeded", err)
		}
	case <-time.After(time.Second):
		if err := bucket.Release(nil); err != nil {
			t.Fatalf("Release returned error: %v", err)
		}
		t.Fatal("LockBucketObjectContext did not return after context deadline")
	}

	if active := atomic.LoadInt32(&bucket.activeRequests); active != 1 {
		t.Fatalf("activeRequests = %d, want 1", active)
	}

	if err := bucket.Release(nil); err != nil {
		t.Fatalf("Release returned error: %v", err)
	}
	if active := atomic.LoadInt32(&bucket.activeRequests); active != 0 {
		t.Fatalf("activeRequests after release = %d, want 0", active)
	}
}

func TestLockBucketObjectContextDoesNotLeakWaitersOnCancel(t *testing.T) {
	rl := NewRatelimiter()
	bucket := rl.LockBucket("/channels/99/messages")
	defer func() {
		if err := bucket.Release(nil); err != nil {
			t.Fatalf("Release returned error: %v", err)
		}
	}()

	before := runtime.NumGoroutine()
	for i := 0; i < 25; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := rl.LockBucketObjectContext(ctx, bucket)
			done <- err
		}()

		time.Sleep(time.Millisecond)
		cancel()

		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("LockBucketObjectContext error = %v, want context canceled", err)
			}
		case <-time.After(time.Second):
			t.Fatal("LockBucketObjectContext did not return after context cancellation")
		}
	}

	time.Sleep(50 * time.Millisecond)
	runtime.GC()
	after := runtime.NumGoroutine()
	if after > before+5 {
		t.Fatalf("goroutines after canceled waits = %d, before = %d", after, before)
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

func TestWebhookTokenEndpointsBypassBotGlobalRateLimit(t *testing.T) {
	tests := []struct {
		name string
		call func(*Session, RequestOption) error
	}{
		{
			name: "get webhook with token",
			call: func(s *Session, opt RequestOption) error {
				_, err := s.WebhookWithToken("webhook", "token", opt)
				return err
			},
		},
		{
			name: "edit webhook with token",
			call: func(s *Session, opt RequestOption) error {
				_, err := s.WebhookEditWithToken("webhook", "token", "name", "", opt)
				return err
			},
		},
		{
			name: "delete webhook with token",
			call: func(s *Session, opt RequestOption) error {
				_, err := s.WebhookDeleteWithToken("webhook", "token", opt)
				return err
			},
		},
		{
			name: "execute webhook",
			call: func(s *Session, opt RequestOption) error {
				_, err := s.WebhookExecute("webhook", "token", false, &WebhookParams{
					Content: "hello",
				}, opt)
				return err
			},
		},
		{
			name: "execute webhook in thread",
			call: func(s *Session, opt RequestOption) error {
				_, err := s.WebhookThreadExecute("webhook", "token", false, "thread", &WebhookParams{
					Content: "hello",
				}, opt)
				return err
			},
		},
		{
			name: "get webhook message",
			call: func(s *Session, opt RequestOption) error {
				_, err := s.WebhookMessage("webhook", "token", "message", opt)
				return err
			},
		},
		{
			name: "edit webhook message",
			call: func(s *Session, opt RequestOption) error {
				content := "edited"
				_, err := s.WebhookMessageEdit("webhook", "token", "message", &WebhookEdit{
					Content: &content,
				}, opt)
				return err
			},
		},
		{
			name: "delete webhook message",
			call: func(s *Session, opt RequestOption) error {
				return s.WebhookMessageDelete("webhook", "token", "message", opt)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := newTestWebhookSession(t)
			session.Token = "Bot secret"
			atomic.StoreInt64(session.Ratelimiter.global, time.Now().Add(time.Hour).UnixNano())

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			if err := tt.call(session, WithContext(ctx)); err != nil {
				t.Fatalf("%s returned error: %v", tt.name, err)
			}
		})
	}
}

func TestWebhookTokenGlobalRateLimitDoesNotSetBotGlobal(t *testing.T) {
	resetSharedUnauthenticatedGlobal(t)

	session, err := New("Bot secret")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Status:     "429 Too Many Requests",
			Header: http.Header{
				"Content-Type":            []string{"application/json"},
				"Retry-After":             []string{"60"},
				"X-RateLimit-Global":      []string{"true"},
				"X-RateLimit-Reset-After": []string{"60"},
			},
			Body:    io.NopCloser(strings.NewReader(`{"message":"rate limited","retry_after":60,"global":true}`)),
			Request: r,
		}, nil
	})

	_, err = session.WebhookExecute("webhook", "token", false, &WebhookParams{
		Content: "hello",
	}, WithRetryOnRatelimit(false))
	if err == nil {
		t.Fatal("WebhookExecute returned nil error, want rate limit error")
	}

	var rateLimitErr *RateLimitError
	if !errors.As(err, &rateLimitErr) {
		t.Fatalf("WebhookExecute returned error %T, want *RateLimitError", err)
	}

	if global := atomic.LoadInt64(session.Ratelimiter.global); global != 0 {
		t.Fatalf("bot global reset = %d, want 0", global)
	}
}

func TestWebhookTokenGlobalRateLimitBlocksOtherUnauthenticatedBuckets(t *testing.T) {
	resetSharedUnauthenticatedGlobal(t)

	session, err := New("Bot secret")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization header = %q, want empty", got)
		}

		if atomic.AddInt32(&requests, 1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "60")
			w.Header().Set("X-RateLimit-Global", "true")
			w.Header().Set("X-RateLimit-Scope", "global")
			w.Header().Set("X-RateLimit-Reset-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message":"rate limited","retry_after":60,"global":true}`))
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	oldEndpointWebhooks := EndpointWebhooks
	oldEndpointAPI := EndpointAPI
	EndpointWebhooks = server.URL + "/webhooks/"
	EndpointAPI = server.URL + "/"
	defer func() {
		EndpointWebhooks = oldEndpointWebhooks
		EndpointAPI = oldEndpointAPI
	}()

	_, err = session.WebhookExecute("webhook-one", "token-one", false, &WebhookParams{
		Content: "hello",
	}, WithRetryOnRatelimit(false))
	if err == nil {
		t.Fatal("WebhookExecute returned nil error, want rate limit error")
	}

	if global := atomic.LoadInt64(session.Ratelimiter.global); global != 0 {
		t.Fatalf("bot global reset = %d, want 0", global)
	}
	if global := atomic.LoadInt64(session.Ratelimiter.unauthenticatedGlobal); global == 0 {
		t.Fatal("unauthenticated global reset was not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	_, err = session.WebhookExecute("webhook-two", "token-two", false, &WebhookParams{
		Content: "hello",
	}, WithContext(ctx))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WebhookExecute returned %v, want context deadline exceeded", err)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("HTTP request count = %d, want second request blocked before transport", got)
	}

	ctx, cancel = context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	err = session.InteractionRespond(&Interaction{
		ID:    "interaction",
		Token: "interaction-token",
	}, &InteractionResponse{
		Type: InteractionResponsePong,
	}, WithContext(ctx))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("InteractionRespond returned %v, want context deadline exceeded", err)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("HTTP request count = %d, want interaction blocked before transport", got)
	}
}

func TestWebhookTokenGlobalRateLimitBlocksOtherSessions(t *testing.T) {
	resetSharedUnauthenticatedGlobal(t)

	sessionOne, err := New("Bot one")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	sessionTwo, err := New("Bot two")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization header = %q, want empty", got)
		}

		if atomic.AddInt32(&requests, 1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "60")
			w.Header().Set("X-RateLimit-Global", "true")
			w.Header().Set("X-RateLimit-Scope", "global")
			w.Header().Set("X-RateLimit-Reset-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message":"rate limited","retry_after":60,"global":true}`))
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	oldEndpointWebhooks := EndpointWebhooks
	EndpointWebhooks = server.URL + "/webhooks/"
	defer func() {
		EndpointWebhooks = oldEndpointWebhooks
	}()

	_, err = sessionOne.WebhookExecute("webhook-one", "token-one", false, &WebhookParams{
		Content: "hello",
	}, WithRetryOnRatelimit(false))
	if err == nil {
		t.Fatal("WebhookExecute returned nil error, want rate limit error")
	}
	if global := atomic.LoadInt64(sessionOne.Ratelimiter.global); global != 0 {
		t.Fatalf("session one bot global reset = %d, want 0", global)
	}
	if global := atomic.LoadInt64(sessionTwo.Ratelimiter.global); global != 0 {
		t.Fatalf("session two bot global reset = %d, want 0", global)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	_, err = sessionTwo.WebhookExecute("webhook-two", "token-two", false, &WebhookParams{
		Content: "hello",
	}, WithContext(ctx))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WebhookExecute returned %v, want context deadline exceeded", err)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("HTTP request count = %d, want second request blocked before transport", got)
	}
}

func TestInteractionRespondUsesEphemeralBucket(t *testing.T) {
	session := newTestInteractionSession(t)
	atomic.StoreInt64(session.Ratelimiter.global, time.Now().Add(time.Hour).UnixNano())

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := session.InteractionRespond(&Interaction{
		ID:    "interaction",
		Token: "secret-token",
	}, &InteractionResponse{
		Type: InteractionResponsePong,
	}, WithContext(ctx))
	if err != nil {
		t.Fatalf("InteractionRespond returned error: %v", err)
	}

	if len(session.Ratelimiter.buckets) != 0 {
		t.Fatalf("bucket count = %d, want 0", len(session.Ratelimiter.buckets))
	}
	assertRateLimitBucketsDoNotContain(t, session, "secret-token")
}

func TestInteractionResponseDeleteUsesMessageRouteBucket(t *testing.T) {
	session := newTestInteractionSession(t)
	atomic.StoreInt64(session.Ratelimiter.global, time.Now().Add(time.Hour).UnixNano())

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := session.InteractionResponseDelete(&Interaction{
		AppID: "application",
		Token: "secret-token",
	}, WithContext(ctx))
	if err != nil {
		t.Fatalf("InteractionResponseDelete returned error: %v", err)
	}

	want := interactionWebhookMessageBucketID("application", "secret-token")
	bucket, ok := session.Ratelimiter.buckets[want]
	if !ok {
		t.Fatalf("bucket %q was not created", want)
	}
	if bucket.ttl != interactionWebhookBucketTTL {
		t.Fatalf("bucket ttl = %v, want %v", bucket.ttl, interactionWebhookBucketTTL)
	}
	assertRateLimitBucketsDoNotContain(t, session, "secret-token")
}

func TestInteractionFollowupCreateUsesTokenScopedNoGlobalBucket(t *testing.T) {
	session := newTestInteractionSession(t)
	atomic.StoreInt64(session.Ratelimiter.global, time.Now().Add(time.Hour).UnixNano())

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := session.FollowupMessageCreate(&Interaction{
		AppID: "application",
		Token: "secret-one",
	}, false, &WebhookParams{
		Content: "hello",
	}, WithContext(ctx))
	if err != nil {
		t.Fatalf("FollowupMessageCreate returned error: %v", err)
	}

	_, err = session.FollowupMessageCreate(&Interaction{
		AppID: "application",
		Token: "secret-two",
	}, false, &WebhookParams{
		Content: "hello",
	}, WithContext(ctx))
	if err != nil {
		t.Fatalf("FollowupMessageCreate returned error: %v", err)
	}

	wantOne := interactionWebhookTokenBucketID("application", "secret-one")
	wantTwo := interactionWebhookTokenBucketID("application", "secret-two")
	if wantOne == wantTwo {
		t.Fatal("interaction tokens mapped to the same bucket")
	}
	for _, want := range []string{wantOne, wantTwo} {
		bucket, ok := session.Ratelimiter.buckets[want]
		if !ok {
			t.Fatalf("bucket %q was not created", want)
		}
		if bucket.ttl != interactionWebhookBucketTTL {
			t.Fatalf("bucket %q ttl = %v, want %v", want, bucket.ttl, interactionWebhookBucketTTL)
		}
	}
	assertRateLimitBucketsDoNotContain(t, session, "secret-one")
	assertRateLimitBucketsDoNotContain(t, session, "secret-two")
}

func TestWebhookExecuteUsesDefaultBucketTTL(t *testing.T) {
	session := newTestWebhookSession(t)

	_, err := session.WebhookExecute("webhook", "secret-token", false, &WebhookParams{
		Content: "hello",
	})
	if err != nil {
		t.Fatalf("WebhookExecute returned error: %v", err)
	}

	want := webhookTokenBucketID("webhook")
	bucket, ok := session.Ratelimiter.buckets[want]
	if !ok {
		t.Fatalf("bucket %q was not created", want)
	}
	if bucket.ttl != 0 {
		t.Fatalf("bucket ttl = %v, want default ttl", bucket.ttl)
	}
	assertRateLimitBucketsDoNotContain(t, session, "secret-token")
}

func TestThreadRoutesUseStableBuckets(t *testing.T) {
	session := newTestThreadSession(t)
	before := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)

	if _, err := session.MessageThreadStartComplex("channel", "message-one", &ThreadStart{Name: "thread"}); err != nil {
		t.Fatalf("MessageThreadStartComplex returned error: %v", err)
	}
	if _, err := session.MessageThreadStartComplex("channel", "message-two", &ThreadStart{Name: "thread"}); err != nil {
		t.Fatalf("MessageThreadStartComplex returned error: %v", err)
	}
	if err := session.ThreadJoin("thread"); err != nil {
		t.Fatalf("ThreadJoin returned error: %v", err)
	}
	if err := session.ThreadLeave("thread"); err != nil {
		t.Fatalf("ThreadLeave returned error: %v", err)
	}
	if err := session.ThreadMemberAdd("thread", "member-one"); err != nil {
		t.Fatalf("ThreadMemberAdd returned error: %v", err)
	}
	if err := session.ThreadMemberRemove("thread", "member-two"); err != nil {
		t.Fatalf("ThreadMemberRemove returned error: %v", err)
	}
	if _, err := session.ThreadMember("thread", "member-three", true); err != nil {
		t.Fatalf("ThreadMember returned error: %v", err)
	}
	if _, err := session.ThreadMembers("thread", 50, true, "after-user"); err != nil {
		t.Fatalf("ThreadMembers returned error: %v", err)
	}
	if _, err := session.ThreadsSearch("channel", &ThreadSearchOptions{Name: "needle", Tags: []string{"tag"}}); err != nil {
		t.Fatalf("ThreadsSearch returned error: %v", err)
	}
	if _, err := session.ThreadsArchived("channel", &before, 50); err != nil {
		t.Fatalf("ThreadsArchived returned error: %v", err)
	}
	if _, err := session.ThreadsPrivateArchived("channel", &before, 50); err != nil {
		t.Fatalf("ThreadsPrivateArchived returned error: %v", err)
	}
	if _, err := session.ThreadsPrivateJoinedArchived("channel", &before, 50); err != nil {
		t.Fatalf("ThreadsPrivateJoinedArchived returned error: %v", err)
	}

	for _, want := range []string{
		EndpointChannelMessageThread("channel", ""),
		EndpointThreadMember("thread", ""),
		EndpointThreadMembers("thread"),
		EndpointChannelThreadsSearch("channel"),
		EndpointChannelPublicArchivedThreads("channel"),
		EndpointChannelPrivateArchivedThreads("channel"),
		EndpointChannelJoinedPrivateArchivedThreads("channel"),
	} {
		if _, ok := session.Ratelimiter.buckets[want]; !ok {
			t.Fatalf("bucket %q was not created", want)
		}
	}

	for _, value := range []string{"message-one", "message-two", "member-one", "member-two", "member-three", "after-user", "needle", "tag", "?"} {
		assertRateLimitBucketsDoNotContain(t, session, value)
	}
}

func TestUserApplicationEntitlementsUsesStableBucket(t *testing.T) {
	session, err := New("Bearer test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`[]`)),
			Request:    r,
		}, nil
	})

	trueValue := true
	falseValue := false
	for _, options := range []*UserApplicationEntitlementFilterOptions{
		{SKUIDs: []string{"sku-one"}, ExcludeConsumed: &trueValue},
		{SKUIDs: []string{"sku-two"}, ExcludeConsumed: &falseValue},
	} {
		if _, err := session.UserApplicationEntitlements("application", options); err != nil {
			t.Fatalf("UserApplicationEntitlements returned error: %v", err)
		}
	}

	want := EndpointUserApplicationEntitlements("application")
	if _, ok := session.Ratelimiter.buckets[want]; !ok {
		t.Fatalf("bucket %q was not created", want)
	}
	for _, value := range []string{"sku-one", "sku-two", "exclude_consumed", "?"} {
		assertRateLimitBucketsDoNotContain(t, session, value)
	}
}

func assertRateLimitBucketsDoNotContain(t *testing.T, session *Session, value string) {
	t.Helper()

	for key := range session.Ratelimiter.buckets {
		if strings.Contains(key, value) {
			t.Fatalf("bucket %q includes %q", key, value)
		}
	}
}

func newTestThreadSession(t *testing.T) *Session {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-RateLimit-Remaining", "1")
		w.Header().Set("X-RateLimit-Reset-After", "0.1")

		if r.Method == http.MethodPut || r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		switch {
		case strings.Contains(r.URL.Path, "/thread-members/"):
			_, _ = w.Write([]byte(`{}`))
		case strings.Contains(r.URL.Path, "/thread-members"):
			_, _ = w.Write([]byte(`[]`))
		case strings.Contains(r.URL.Path, "/threads/archived/"):
			_, _ = w.Write([]byte(`{"threads":[],"members":[],"has_more":false}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(server.Close)

	oldEndpointChannels := EndpointChannels
	EndpointChannels = server.URL + "/channels/"
	t.Cleanup(func() {
		EndpointChannels = oldEndpointChannels
	})

	return &Session{
		Client:                 server.Client(),
		MaxRestRetries:         0,
		Ratelimiter:            NewRatelimiter(),
		UserAgent:              "DiscordGo test",
		ShouldRetryOnRateLimit: true,
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

func newTestInteractionSession(t *testing.T) *Session {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	oldEndpointAPI := EndpointAPI
	oldEndpointWebhooks := EndpointWebhooks
	EndpointAPI = server.URL + "/"
	EndpointWebhooks = server.URL + "/webhooks/"
	t.Cleanup(func() {
		EndpointAPI = oldEndpointAPI
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

func TestRatelimitResetUsesServerDateForSkewedClocks(t *testing.T) {
	rl := NewRatelimiter()
	bucket := rl.LockBucket("/channels/99/messages")

	// Server clock runs 30s ahead of ours; Date and Reset are both in
	// server time, with the reset 2s after server-now. The local wait
	// must be ~2s, not the 32s the raw epoch implies.
	serverNow := time.Now().Add(30 * time.Second)
	resetAt := serverNow.Add(2 * time.Second)
	headers := http.Header{}
	headers.Set("X-RateLimit-Remaining", "0")
	headers.Set("X-RateLimit-Reset", fmt.Sprintf("%.3f", float64(resetAt.UnixNano())/float64(time.Second)))
	headers.Set("Date", serverNow.UTC().Format(http.TimeFormat))

	err := bucket.Release(headers)
	if err != nil {
		t.Fatalf("Release returned error: %v", err)
	}

	// The Date header only has second resolution and the computation
	// pads by 250ms, so accept ~2s to ~3.5s.
	wait := time.Until(bucket.reset)
	if wait < 1500*time.Millisecond || wait > 3500*time.Millisecond {
		t.Fatalf("wait = %v, want roughly 2s despite the 30s clock skew", wait)
	}
}

func TestRatelimitResetIgnoresMalformedDateHeader(t *testing.T) {
	rl := NewRatelimiter()
	bucket := rl.LockBucket("/channels/99/messages")

	resetAt := time.Now().Add(2 * time.Second).Round(time.Millisecond)
	headers := http.Header{}
	headers.Set("X-RateLimit-Remaining", "0")
	headers.Set("X-RateLimit-Reset", fmt.Sprintf("%.3f", float64(resetAt.UnixNano())/float64(time.Second)))
	headers.Set("Date", "not a date")

	err := bucket.Release(headers)
	if err != nil {
		t.Fatalf("Release returned error: %v", err)
	}

	if bucket.reset.Before(resetAt.Add(-time.Millisecond)) || bucket.reset.After(resetAt.Add(time.Millisecond)) {
		t.Fatalf("reset = %v, want %v", bucket.reset, resetAt)
	}
}

func TestRatelimitResetDoesNotRequireDateHeader(t *testing.T) {
	rl := NewRatelimiter()
	bucket := rl.LockBucket("/channels/99/messages")

	resetAt := time.Now().Add(2 * time.Second).Round(time.Millisecond)
	headers := http.Header{}
	headers.Set("X-RateLimit-Remaining", "0")
	headers.Set("X-RateLimit-Reset", fmt.Sprintf("%.3f", float64(resetAt.UnixNano())/float64(time.Second)))

	err := bucket.Release(headers)
	if err != nil {
		t.Fatalf("Release returned error: %v", err)
	}

	if bucket.reset.Before(resetAt.Add(-time.Millisecond)) || bucket.reset.After(resetAt.Add(time.Millisecond)) {
		t.Fatalf("reset = %v, want %v", bucket.reset, resetAt)
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

func TestRateLimitFractionalSecondsPreserveDeadline(t *testing.T) {
	for _, source := range []string{"route-header", "resource-body", "global-body"} {
		for _, delay := range []struct {
			seconds string
			want    time.Duration
		}{{"2.3", 2300 * time.Millisecond}, {"64.57", 64570 * time.Millisecond}} {
			t.Run(fmt.Sprintf("%s/%s", source, delay.seconds), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					session, err := New("Bot test")
					if err != nil {
						t.Fatal(err)
					}
					attempts := 0
					session.Client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
						attempts++
						status, body := http.StatusOK, `{}`
						headers := make(http.Header)
						if attempts == 1 {
							if source == "route-header" {
								headers.Set("X-RateLimit-Remaining", "0")
								headers.Set("X-RateLimit-Reset-After", delay.seconds)
							} else {
								status = http.StatusTooManyRequests
								body = fmt.Sprintf(`{"message":"limited","retry_after":%s,"global":%t}`, delay.seconds, source == "global-body")
							}
						}
						return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
					})
					request := func(bucket string) error {
						_, err := session.RequestWithBucketID(http.MethodGet, "https://discord.com/"+bucket, nil, bucket, WithRetryOnRatelimit(false))
						return err
					}
					started := time.Now()
					err = request("limited")
					if source == "route-header" {
						if err != nil {
							t.Fatal(err)
						}
					} else {
						var limited *RateLimitError
						if !errors.As(err, &limited) {
							t.Fatalf("first error = %v, want RateLimitError", err)
						}
						if limited.RetryAfter != delay.want {
							t.Errorf("reported retry delay = %v, want %v", limited.RetryAfter, delay.want)
						}
					}
					nextBucket := "limited"
					if source == "global-body" {
						nextBucket = "other"
					}
					if err := request(nextBucket); err != nil {
						t.Fatal(err)
					}
					if elapsed := time.Since(started); elapsed != delay.want {
						t.Errorf("second request sent after %v, want %v", elapsed, delay.want)
					}
					if attempts != 2 {
						t.Fatalf("requests sent = %d, want 2", attempts)
					}
				})
			})
		}
	}
}

func TestGlobalRateLimitPreservesLongestDeadline(t *testing.T) {
	for _, authenticated := range []bool{true, false} {
		for _, longerFirst := range []bool{true, false} {
			t.Run(fmt.Sprintf("authenticated=%t/longerFirst=%t", authenticated, longerFirst), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					limiter := NewRatelimiter()
					limiter.unauthenticatedGlobal = new(int64)
					// Both requests are already in flight before either limit is known.
					first, err := limiter.lockBucketContext(context.Background(), "first", authenticated)
					if err != nil {
						t.Fatal(err)
					}
					second, err := limiter.lockBucketContext(context.Background(), "second", authenticated)
					if err != nil {
						t.Fatal(err)
					}
					resets := []string{"1", "0.1"}
					if !longerFirst {
						resets[0], resets[1] = resets[1], resets[0]
					}
					for i, bucket := range []*Bucket{first, second} {
						if err := bucket.Release(http.Header{
							"X-Ratelimit-Global":      {"true"},
							"X-Ratelimit-Reset-After": {resets[i]},
						}); err != nil {
							t.Fatal(err)
						}
					}
					started := time.Now()
					next, err := limiter.lockBucketContext(context.Background(), "next", authenticated)
					if err != nil {
						t.Fatal(err)
					}
					if err := next.Release(nil); err != nil {
						t.Fatal(err)
					}
					if elapsed := time.Since(started); elapsed != time.Second {
						t.Fatalf("next request waited %v, want longest global delay of 1s", elapsed)
					}
				})
			})
		}
	}
}

func TestRateLimitHeadersApplyBeforeBodyRead(t *testing.T) {
	for _, scope := range []string{"resource", "global-flag", "global-scope"} {
		for _, bodyFailed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/bodyFailed=%t", scope, bodyFailed), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					session, err := New("Bot test")
					if err != nil {
						t.Fatal(err)
					}
					headers := http.Header{"Retry-After": {"1"}}
					switch scope {
					case "resource":
						headers.Set("X-RateLimit-Scope", "shared")
						headers.Set("X-RateLimit-Remaining", "9")
						headers.Set("X-RateLimit-Reset-After", "0.1")
					case "global-flag":
						headers.Set("X-RateLimit-Global", "true")
					case "global-scope":
						headers.Set("X-RateLimit-Scope", "global")
					}
					reader, writer := io.Pipe()
					var attempts atomic.Int32
					session.Client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
						if attempts.Add(1) == 1 {
							return &http.Response{StatusCode: http.StatusTooManyRequests, Header: headers, Body: reader, Request: req}, nil
						}
						return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
					})
					request := func(bucket string) error {
						_, err := session.RequestWithBucketID(http.MethodGet, "https://discord.com/"+bucket, nil, bucket, WithRetryOnRatelimit(false))
						return err
					}
					first := make(chan error, 1)
					go func() { first <- request("limited") }()
					pending := true
					defer func() {
						_ = writer.CloseWithError(io.ErrUnexpectedEOF)
						if pending {
							if err := <-first; !errors.Is(err, io.ErrUnexpectedEOF) {
								t.Errorf("first request error = %v, want unexpected EOF", err)
							}
						}
					}()
					// The first request has received its headers and is waiting for
					// the body. A known cooldown must already protect later calls.
					synctest.Wait()
					if attempts.Load() != 1 {
						t.Fatal("first request did not reach its response body")
					}
					if bodyFailed {
						_ = writer.CloseWithError(io.ErrUnexpectedEOF)
						err := <-first
						pending = false
						if !errors.Is(err, io.ErrUnexpectedEOF) {
							t.Fatalf("first request error = %v, want unexpected EOF", err)
						}
					}
					started := time.Now()
					nextBucket := "other"
					if scope == "resource" {
						if err := request("other"); err != nil {
							t.Fatal(err)
						}
						if elapsed := time.Since(started); elapsed != 0 {
							t.Errorf("unrelated bucket delayed by resource limit: %v", elapsed)
						}
						nextBucket = "limited"
					}
					if err := request(nextBucket); err != nil {
						t.Fatal(err)
					}
					if elapsed := time.Since(started); elapsed != time.Second {
						t.Errorf("next request waited %v, want Retry-After delay of 1s", elapsed)
					}
				})
			})
		}
	}
}

func TestResourceRateLimitDelaysOtherRequests(t *testing.T) {
	for _, tc := range []struct {
		retry bool
		idle  time.Duration
	}{{}, {retry: true}, {idle: rateLimitBucketTTL + time.Second}} {
		t.Run(fmt.Sprintf("retry=%t/idle=%v", tc.retry, tc.idle), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				delay := time.Second + tc.idle
				seconds := fmt.Sprintf("%.0f", delay.Seconds())
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				session.SyncEvents = true
				limited := make(chan struct{})
				session.AddHandler(func(_ *Session, _ *RateLimit) { close(limited) })
				var attempts atomic.Int32
				session.Client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
					if req.URL.Path == "/limited" && attempts.Add(1) == 1 {
						// Resource limits can leave normal route quota available.
						return &http.Response{
							StatusCode: http.StatusTooManyRequests,
							Header: http.Header{
								"Retry-After":             {seconds},
								"X-Ratelimit-Remaining":   {"9"},
								"X-Ratelimit-Reset-After": {"0.1"},
								"X-Ratelimit-Scope":       {"shared"},
							},
							Body:    io.NopCloser(strings.NewReader(`{"message":"The resource is being rate limited.","retry_after":` + seconds + `,"global":false}`)),
							Request: req,
						}, nil
					}
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
				})
				request := func(bucket string) error {
					_, err := session.RequestWithBucketID(http.MethodGet, "https://discord.com/"+bucket, nil, bucket, WithRetryOnRatelimit(tc.retry))
					return err
				}
				started := time.Now()
				first := make(chan error, 1)
				go func() { first <- request("limited") }()
				if tc.retry {
					<-limited
					// The original request is now waiting to retry.
					synctest.Wait()
				} else {
					var rateLimitErr *RateLimitError
					if err := <-first; !errors.As(err, &rateLimitErr) {
						t.Fatalf("first request error = %v, want RateLimitError", err)
					}
				}
				// Long resource limits must also survive idle bucket cleanup.
				time.Sleep(tc.idle)
				if err := request("other"); err != nil {
					t.Fatal(err)
				}
				if elapsed := time.Since(started) - tc.idle; elapsed != 0 {
					t.Errorf("unrelated bucket waited %v", elapsed)
				}
				if err := request("limited"); err != nil {
					t.Fatal(err)
				}
				if elapsed := time.Since(started); elapsed != delay {
					t.Errorf("next request to limited resource sent after %v, want %v", elapsed, delay)
				}
				if tc.retry {
					if err := <-first; err != nil {
						t.Fatal(err)
					}
				}
				want := int32(2)
				if tc.retry {
					want++
				}
				if got := attempts.Load(); got != want {
					t.Errorf("limited attempts = %d, want %d", got, want)
				}
			})
		})
	}
}

func TestResourceRateLimitPreservesLongestBodyDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		session, err := New("Bot test")
		if err != nil {
			t.Fatal(err)
		}
		var attempts atomic.Int32
		bodies := make(chan *io.PipeWriter, 2)
		session.Client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if attempts.Add(1) <= 2 {
				reader, writer := io.Pipe()
				bodies <- writer
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Header:     http.Header{"X-Ratelimit-Remaining": {"9"}},
					Body:       reader,
					Request:    req,
				}, nil
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
		})
		request := func() error {
			_, err := session.RequestWithBucketID(http.MethodGet, "https://discord.com/limited", nil, "limited", WithRetryOnRatelimit(false))
			return err
		}
		done := make(chan error, 2)
		writers := make([]*io.PipeWriter, 0, 2)
		pending := 0
		defer func() {
			for _, writer := range writers {
				_ = writer.Close()
			}
			for range pending {
				<-done
			}
		}()
		for range 2 {
			pending++
			go func() { done <- request() }()
			writers = append(writers, <-bodies)
			// Each request releases the shared bucket before its body arrives.
			synctest.Wait()
		}
		for i, seconds := range []string{"1", "0.1"} {
			if _, err := io.WriteString(writers[i], `{"message":"limited","retry_after":`+seconds+`,"global":false}`); err != nil {
				t.Fatal(err)
			}
			if err := writers[i].Close(); err != nil {
				t.Fatal(err)
			}
			var rateLimitErr *RateLimitError
			err := <-done
			pending--
			if !errors.As(err, &rateLimitErr) {
				t.Fatalf("request error = %v, want RateLimitError", err)
			}
		}
		started := time.Now()
		if err := request(); err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(started); elapsed != time.Second {
			t.Fatalf("next request waited %v, want longest body deadline of 1s", elapsed)
		}
	})
}

func TestBucketWaitRechecksGlobalLimit(t *testing.T) {
	for _, initialGlobal := range []bool{false, true} {
		for _, unauthenticated := range []bool{false, true} {
			for _, cancelWait := range []bool{false, true} {
				t.Run(fmt.Sprintf("initialGlobal=%v/unauthenticated=%v/cancel=%v", initialGlobal, unauthenticated, cancelWait), func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						limiter := NewRatelimiter()
						// Keep the unauthenticated deadline local to this test.
						limiter.unauthenticatedGlobal = new(int64)
						bucket, err := limiter.lockBucketContext(context.Background(), "waiting", !unauthenticated)
						if err != nil {
							t.Fatal(err)
						}
						// This request is already in flight when the first limit arrives.
						other, err := limiter.lockBucketContext(context.Background(), "other", !unauthenticated)
						if err != nil {
							t.Fatal(err)
						}
						headers := http.Header{
							"X-Ratelimit-Remaining":   {"0"},
							"X-Ratelimit-Reset-After": {"0.1"},
						}
						if initialGlobal {
							headers.Set("X-RateLimit-Global", "true")
						}
						if err := bucket.Release(headers); err != nil {
							t.Fatal(err)
						}
						ctx := context.Background()
						if cancelWait {
							var cancel context.CancelFunc
							ctx, cancel = context.WithTimeout(ctx, 500*time.Millisecond)
							defer cancel()
						}
						done := make(chan error, 1)
						started := time.Now()
						go func() {
							locked, err := limiter.LockBucketObjectContext(ctx, bucket)
							if err == nil {
								err = locked.Release(nil)
							}
							done <- err
						}()
						// The waiter has computed its first delay and is asleep.
						synctest.Wait()
						headers.Set("X-RateLimit-Global", "true")
						headers.Set("X-RateLimit-Reset-After", "1")
						if err := other.Release(headers); err != nil {
							t.Fatal(err)
						}
						err = <-done
						if cancelWait {
							if !errors.Is(err, context.DeadlineExceeded) {
								t.Errorf("wait error = %v, want context deadline exceeded", err)
							}
						} else if err != nil || time.Since(started) < time.Second {
							t.Errorf("wait = %v, error = %v; want at least one second and no error", time.Since(started), err)
						}
						if active := atomic.LoadInt32(&bucket.activeRequests); active != 0 {
							t.Errorf("activeRequests = %d, want zero", active)
						}
						// Cancellation must also release the bucket's acquisition queue.
						locked, err := limiter.LockBucketObjectContext(context.Background(), bucket)
						if err != nil {
							t.Fatal(err)
						}
						if err := locked.Release(nil); err != nil {
							t.Fatal(err)
						}
					})
				})
			}
		}
	}
}
