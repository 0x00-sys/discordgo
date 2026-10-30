package discordgo

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

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
