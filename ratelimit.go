package discordgo

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// customRateLimit holds information for defining a custom rate limit
type customRateLimit struct {
	suffix   string
	requests int
	reset    time.Duration
}

// RateLimiter holds all ratelimit buckets
type RateLimiter struct {
	sync.Mutex
	global                *int64
	unauthenticatedGlobal *int64
	buckets               map[string]*Bucket
	lastCleanup           time.Time
	globalRateLimit       time.Duration
	customRateLimits      []*customRateLimit
}

var (
	rateLimitBucketTTL             = time.Hour
	rateLimitBucketCleanupInterval = time.Minute
	sharedUnauthenticatedGlobal    = new(int64)
)

// NewRatelimiter returns a new RateLimiter
func NewRatelimiter() *RateLimiter {

	return &RateLimiter{
		buckets:               make(map[string]*Bucket),
		global:                new(int64),
		unauthenticatedGlobal: sharedUnauthenticatedGlobal,
		customRateLimits: []*customRateLimit{
			{
				suffix:   "//reactions//",
				requests: 1,
				reset:    200 * time.Millisecond,
			},
		},
	}
}

// GetBucket retrieves or creates a bucket
func (r *RateLimiter) GetBucket(key string) *Bucket {
	return r.getBucket(key, r.global)
}

func (r *RateLimiter) getBucket(key string, global *int64) *Bucket {
	return r.getBucketWithTTL(key, global, 0)
}

func (r *RateLimiter) getBucketWithTTL(key string, global *int64, ttl time.Duration) *Bucket {
	r.Lock()
	defer r.Unlock()

	now := time.Now()
	r.cleanupStaleBuckets(now)

	if bucket, ok := r.buckets[key]; ok {
		if ttl > 0 {
			bucket.ttl = ttl
		}
		bucket.touch(now)
		return bucket
	}

	b := &Bucket{
		Remaining: 1,
		Key:       key,
		global:    global,
		ttl:       ttl,
	}
	b.touch(now)

	// Check if there is a custom ratelimit set for this bucket ID.
	for _, rl := range r.customRateLimits {
		if strings.HasSuffix(b.Key, rl.suffix) {
			b.customRateLimit = rl
			break
		}
	}

	r.buckets[key] = b
	return b
}

func (r *RateLimiter) cleanupStaleBuckets(now time.Time) {
	if rateLimitBucketTTL <= 0 {
		return
	}
	if rateLimitBucketCleanupInterval > 0 && !r.lastCleanup.IsZero() && now.Sub(r.lastCleanup) < rateLimitBucketCleanupInterval {
		return
	}

	r.lastCleanup = now
	nowUnix := now.UnixNano()
	for key, bucket := range r.buckets {
		if atomic.LoadInt32(&bucket.activeRequests) != 0 {
			continue
		}
		if reset := atomic.LoadInt64(&bucket.resetAt); reset > nowUnix {
			continue
		}
		ttl := rateLimitBucketTTL
		if bucket.ttl > 0 {
			ttl = bucket.ttl
		}
		expiresBefore := now.Add(-ttl).UnixNano()
		if atomic.LoadInt64(&bucket.lastUsed) < expiresBefore {
			delete(r.buckets, key)
		}
	}
}

// GetWaitTime returns the duration you should wait for a Bucket
func (r *RateLimiter) GetWaitTime(b *Bucket, minRemaining int) time.Duration {
	now := time.Now()
	wait := time.Duration(0)

	// If we ran out of calls and the reset time is still ahead of us
	// then we need to take it easy and relax a little
	if b.Remaining < minRemaining && b.reset.After(now) {
		wait = b.reset.Sub(now)
	}

	// Check for global ratelimits
	global := r.global
	if b.global != nil {
		global = b.global
	}
	sleepTo := time.Unix(0, atomic.LoadInt64(global))
	if now.Before(sleepTo) {
		globalWait := sleepTo.Sub(now)
		if globalWait > wait {
			wait = globalWait
		}
	}

	return wait
}

// LockBucket Locks until a request can be made
func (r *RateLimiter) LockBucket(bucketID string) *Bucket {
	b, _ := r.LockBucketContext(context.Background(), bucketID)
	return b
}

// LockBucketContext locks until a request can be made or ctx is canceled.
func (r *RateLimiter) LockBucketContext(ctx context.Context, bucketID string) (*Bucket, error) {
	return r.LockBucketObjectContext(ctx, r.GetBucket(bucketID))
}

func (r *RateLimiter) lockBucketContext(ctx context.Context, bucketID string, useGlobalRateLimit bool) (*Bucket, error) {
	return r.lockBucketContextWithTTL(ctx, bucketID, useGlobalRateLimit, 0)
}

func (r *RateLimiter) lockBucketContextWithTTL(ctx context.Context, bucketID string, useGlobalRateLimit bool, ttl time.Duration) (*Bucket, error) {
	globalReset := r.global
	if !useGlobalRateLimit {
		globalReset = r.unauthenticatedGlobal
	}

	return r.LockBucketObjectContext(ctx, r.getBucketWithTTL(bucketID, globalReset, ttl))
}

func (r *RateLimiter) lockEphemeralBucketContext(ctx context.Context, bucketID string, useGlobalRateLimit bool) (*Bucket, error) {
	globalReset := r.global
	if !useGlobalRateLimit {
		globalReset = r.unauthenticatedGlobal
	}

	return r.LockBucketObjectContext(ctx, &Bucket{
		Remaining: 1,
		Key:       bucketID,
		global:    globalReset,
	})
}

// LockBucketObject Locks an already resolved bucket until a request can be made
func (r *RateLimiter) LockBucketObject(b *Bucket) *Bucket {
	b, _ = r.LockBucketObjectContext(context.Background(), b)
	return b
}

// LockBucketObjectContext locks an already resolved bucket until a request can be made or ctx is canceled.
func (r *RateLimiter) LockBucketObjectContext(ctx context.Context, b *Bucket) (*Bucket, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	atomic.AddInt32(&b.activeRequests, 1)
	b.touch(time.Now())
	if err := b.lockContext(ctx); err != nil {
		atomic.AddInt32(&b.activeRequests, -1)
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		atomic.AddInt32(&b.activeRequests, -1)
		b.Unlock()
		b.unlockContext()
		return nil, err
	}

	if wait := r.GetWaitTime(b, 1); wait > 0 {
		if err := sleepWithContext(ctx, wait); err != nil {
			atomic.AddInt32(&b.activeRequests, -1)
			b.Unlock()
			b.unlockContext()
			return nil, err
		}
	}

	b.Remaining--
	return b, nil
}

// Bucket represents a ratelimit bucket, each bucket gets ratelimited individually (-global ratelimits)
type Bucket struct {
	sync.Mutex
	Key       string
	Remaining int
	limit     int
	reset     time.Time
	global    *int64

	lockOnce        sync.Once
	lockQueue       chan struct{}
	lastReset       time.Time
	resetAt         int64
	lastUsed        int64
	activeRequests  int32
	ttl             time.Duration
	customRateLimit *customRateLimit
	Userdata        interface{}
}

func (b *Bucket) contextLock() chan struct{} {
	b.lockOnce.Do(func() {
		b.lockQueue = make(chan struct{}, 1)
	})
	return b.lockQueue
}

func (b *Bucket) lockContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	lockQueue := b.contextLock()
	select {
	case lockQueue <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}

	if err := ctx.Err(); err != nil {
		b.unlockContext()
		return err
	}

	b.Lock()
	return nil
}

func (b *Bucket) unlockContext() {
	select {
	case <-b.contextLock():
	default:
	}
}

func (b *Bucket) setReset(reset time.Time) {
	atomic.StoreInt64(&b.resetAt, reset.UnixNano())
}

func (b *Bucket) setGlobalReset(reset time.Time) {
	atomic.StoreInt64(b.global, reset.UnixNano())
}

func (b *Bucket) touch(now time.Time) {
	atomic.StoreInt64(&b.lastUsed, now.UnixNano())
}

// Release unlocks the bucket and reads the headers to update the buckets ratelimit info
// and locks up the whole thing in case if there's a global ratelimit.
func (b *Bucket) Release(headers http.Header) error {
	defer func() {
		atomic.AddInt32(&b.activeRequests, -1)
		b.Unlock()
		b.unlockContext()
	}()

	// Check if the bucket uses a custom ratelimiter
	if rl := b.customRateLimit; rl != nil {
		if time.Now().Sub(b.lastReset) >= rl.reset {
			b.Remaining = rl.requests - 1
			b.lastReset = time.Now()
		}
		if b.Remaining < 1 {
			b.reset = time.Now().Add(rl.reset)
			b.setReset(b.reset)
		}
		return nil
	}

	if headers == nil {
		return nil
	}

	remaining := headers.Get("X-RateLimit-Remaining")
	reset := headers.Get("X-RateLimit-Reset")
	global := headers.Get("X-RateLimit-Global")
	resetAfter := headers.Get("X-RateLimit-Reset-After")

	// Update global and per bucket reset time if the proper headers are available
	// If global is set, then it will block all buckets until after Retry-After
	// If Retry-After without global is provided it will use that for the new reset
	// time since it's more accurate than X-RateLimit-Reset.
	// If Retry-After after is not proided, it will update the reset time from X-RateLimit-Reset
	if resetAfter != "" {
		parsedAfter, err := strconv.ParseFloat(resetAfter, 64)
		if err != nil {
			return err
		}

		whole, frac := math.Modf(parsedAfter)
		resetAt := time.Now().Add(time.Duration(whole) * time.Second).Add(time.Duration(frac*1000) * time.Millisecond)

		// Lock either this single bucket or all buckets
		if global != "" {
			b.setGlobalReset(resetAt)
		} else {
			b.reset = resetAt
			b.setReset(resetAt)
		}
	} else if reset != "" {
		unix, err := strconv.ParseFloat(reset, 64)
		if err != nil {
			return err
		}

		whole, frac := math.Modf(unix)
		resetAt := time.Unix(int64(whole), 0).Add(time.Duration(frac * float64(time.Second)))

		// The reset epoch is in the server's clock domain. When the
		// Date header is available, apply the server-relative delta to
		// the local clock so client clock skew cannot shorten or
		// stretch the wait; the 250ms pad absorbs sub-second
		// disagreement between the two headers. Without a parseable
		// Date header, fall back to the raw epoch.
		if discordTime, dateErr := http.ParseTime(headers.Get("Date")); dateErr == nil {
			resetAt = time.Now().Add(resetAt.Sub(discordTime) + 250*time.Millisecond)
		}

		b.reset = resetAt
		b.setReset(resetAt)
	}

	// Udpate remaining if header is present
	if remaining != "" {
		parsedRemaining, err := strconv.ParseInt(remaining, 10, 32)
		if err != nil {
			return err
		}
		b.Remaining = int(parsedRemaining)
	}

	return nil
}
