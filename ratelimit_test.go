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
	"time"
)

func resetSharedUnauthenticatedGlobal(t *testing.T) {
	t.Helper()
	atomic.StoreInt64(sharedUnauthenticatedGlobal, 0)
	t.Cleanup(func() {
		atomic.StoreInt64(sharedUnauthenticatedGlobal, 0)
	})
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
	if _, ok := session.Ratelimiter.buckets[want]; !ok {
		t.Fatalf("bucket %q was not created", want)
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
		if _, ok := session.Ratelimiter.buckets[want]; !ok {
			t.Fatalf("bucket %q was not created", want)
		}
	}
	assertRateLimitBucketsDoNotContain(t, session, "secret-one")
	assertRateLimitBucketsDoNotContain(t, session, "secret-two")
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
		EndpointChannelPublicArchivedThreads("channel"),
		EndpointChannelPrivateArchivedThreads("channel"),
		EndpointChannelJoinedPrivateArchivedThreads("channel"),
	} {
		if _, ok := session.Ratelimiter.buckets[want]; !ok {
			t.Fatalf("bucket %q was not created", want)
		}
	}

	for _, value := range []string{"message-one", "message-two", "member-one", "member-two", "member-three", "after-user", "?"} {
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

func TestRatelimitResetUsesEpoch(t *testing.T) {
	rl := NewRatelimiter()
	bucket := rl.LockBucket("/channels/99/messages")

	resetAt := time.Now().Add(2 * time.Second).Round(time.Millisecond)
	headers := http.Header{}
	headers.Set("X-RateLimit-Remaining", "0")
	headers.Set("X-RateLimit-Reset", fmt.Sprintf("%.3f", float64(resetAt.UnixNano())/float64(time.Second)))
	headers.Set("Date", time.Now().Add(-time.Hour).Format(time.RFC1123))

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
