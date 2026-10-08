package httpx

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/types"
)

// A caller spends its burst, waits out the refill, and never borrows another caller's.
func TestFailureLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewFailureLimiter(20, 40)
	l.now = func() time.Time { return now }

	for range 40 {
		_, exhausted := l.Exhausted("a")
		require.False(t, exhausted)
		l.Fail("a")
	}
	wait, exhausted := l.Exhausted("a")
	assert.True(t, exhausted, "the burst is spent")
	assert.Equal(t, 50*time.Millisecond, wait, "one failure refills every 1/20 s")
	_, exhausted = l.Exhausted("b")
	assert.False(t, exhausted, "callers do not share a bucket")

	now = now.Add(50 * time.Millisecond)
	_, exhausted = l.Exhausted("a")
	assert.False(t, exhausted, "refilled")
}

// Past maxBuckets, refilled buckets are forgotten first, and with none to forget every new
// caller shares one overflow bucket, so varying the key buys no fresh allowance.
func TestFailureLimiterBoundsItsCallers(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewFailureLimiter(20, 1)
	l.now = func() time.Time { return now }
	for i := range maxBuckets {
		l.Fail(fmt.Sprint(i))
	}
	l.Fail("new")
	_, exhausted := l.Exhausted("another")
	assert.True(t, exhausted, "a new caller past the bound shares the spent overflow bucket")
	assert.LessOrEqual(t, len(l.buckets), maxBuckets+1)

	now = now.Add(time.Second)
	_, exhausted = l.Exhausted("later")
	assert.False(t, exhausted, "refilled buckets are swept to make room")
	assert.Less(t, len(l.buckets), maxBuckets)
}

// The guard answers an exhausted caller 429 with Retry-After, keeps admitting its valid
// token, and charges a failure to the Origin, else the peer.
func TestGuardLimitsOnlyFailures(t *testing.T) {
	l := NewFailureLimiter(20, 2)
	verify := func(p string) (types.Credential, bool) {
		return types.Credential{Grant: types.GrantConnector}, p == "good"
	}
	h, err := ProcedureGuard(rpcerr.FormatJSON, verify, map[string]types.Need{"/mcp": {Scope: types.ScopeMCP, Level: types.LevelWrite}},
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }), WithFailureLimit(l))
	require.NoError(t, err)
	send := func(origin, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	assert.Equal(t, http.StatusUnauthorized, send("https://a.example", "bad").Code)
	assert.Equal(t, http.StatusUnauthorized, send("https://a.example", "").Code, "a missing token is a failure too")
	w := send("https://a.example", "bad")
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, "1", w.Header().Get("Retry-After"))
	assert.Contains(t, w.Body.String(), string(types.AuthFailuresThrottled))
	assert.Contains(t, w.Body.String(), "more than 20 times a second (bursts of 2)")
	assert.Contains(t, w.Body.String(), "only failed attempts count")
	assert.Equal(t, http.StatusOK, send("https://a.example", "good").Code, "a valid token is never limited")
	assert.Equal(t, http.StatusUnauthorized, send("https://b.example", "bad").Code, "another Origin has its own bucket")
	assert.Equal(t, http.StatusUnauthorized, send("", "bad").Code, "and so does the peer")
	assert.Equal(t, "peer 192.0.2.1", FailureKey(httptest.NewRequest(http.MethodPost, "/mcp", nil)))
}
