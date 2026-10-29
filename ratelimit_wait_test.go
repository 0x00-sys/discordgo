package discordgo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

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
