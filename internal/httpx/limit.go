package httpx

import (
	"math"
	"net"
	"net/http"
	"sync"
	"time"
)

// FailureLimiter is a token bucket per caller over FAILED authentications: each refusal
// spends one token, and the bucket refills at a steady rate up to its burst. It never sees a
// request that verified, so a valid token is never limited. Safe for concurrent use.
type FailureLimiter struct {
	perSecond float64
	burst     float64
	now       func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	at     time.Time
}

// maxBuckets bounds the callers tracked at once. Past it, buckets already refilled are
// dropped, and if none are, every new caller shares one overflow bucket, so a client that
// varies its Origin buys no memory and no fresh allowance.
const maxBuckets = 4096

// NewFailureLimiter returns a limiter allowing perSecond failures a second per caller, with
// bursts of up to burst.
func NewFailureLimiter(perSecond, burst int) *FailureLimiter {
	return &FailureLimiter{
		perSecond: float64(perSecond),
		burst:     float64(burst),
		now:       time.Now,
		buckets:   map[string]*bucket{},
	}
}

// Limits returns the failures allowed a second and the burst, as NewFailureLimiter took them.
func (l *FailureLimiter) Limits() (perSecond, burst int) {
	return int(l.perSecond), int(l.burst)
}

// FailureKey names the caller a request's failures are charged to: its Origin when it sends
// one, which a browser sets and a page cannot change, else the TCP peer's host.
func FailureKey(r *http.Request) string {
	if o := r.Header.Get("Origin"); o != "" {
		return "origin " + o
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return "peer " + host
}

// Exhausted reports whether key has no failures left, and if so how long until it has one.
func (l *FailureLimiter) Exhausted(key string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.refill(key)
	if b.tokens >= 1 {
		return 0, false
	}
	return time.Duration((1 - b.tokens) / l.perSecond * float64(time.Second)), true
}

// Fail spends one failure from key's bucket.
func (l *FailureLimiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.refill(key)
	b.tokens = math.Max(b.tokens-1, 0)
}

// refill returns key's bucket topped up for the time since it was last touched. The caller
// holds l.mu.
func (l *FailureLimiter) refill(key string) *bucket {
	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= maxBuckets {
			l.sweep(now)
		}
		if len(l.buckets) >= maxBuckets {
			key = ""
			if b, ok = l.buckets[key]; ok {
				return l.topUp(b, now)
			}
		}
		b = &bucket{tokens: l.burst, at: now}
		l.buckets[key] = b
		return b
	}
	return l.topUp(b, now)
}

func (l *FailureLimiter) topUp(b *bucket, now time.Time) *bucket {
	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.at).Seconds()*l.perSecond)
	b.at = now
	return b
}

// sweep drops every bucket that would be full by now: forgetting it changes nothing.
func (l *FailureLimiter) sweep(now time.Time) {
	for k, b := range l.buckets {
		if b.tokens+now.Sub(b.at).Seconds()*l.perSecond >= l.burst {
			delete(l.buckets, k)
		}
	}
}
