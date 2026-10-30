package discordgo

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

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
