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
