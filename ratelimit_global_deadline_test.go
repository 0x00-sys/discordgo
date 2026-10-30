package discordgo

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"testing/synctest"
	"time"
)

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
