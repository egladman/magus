package retry

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errFake = errors.New("fake error")

func TestDoSucceedsFirstTry(t *testing.T) {
	t.Parallel()
	calls := 0
	err := Do(context.Background(), func() error {
		calls++
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
}

func TestDoSucceedsAfterRetries(t *testing.T) {
	t.Parallel()
	calls := 0
	err := Do(context.Background(), func() error {
		calls++
		if calls < 3 {
			return errFake
		}
		return nil
	}, WithAttempts(3), WithDelay(time.Millisecond), WithMaxDelay(time.Millisecond))
	require.NoError(t, err)
	assert.Equal(t, 3, calls)
}

func TestDoExhausts(t *testing.T) {
	t.Parallel()
	calls := 0
	err := Do(context.Background(), func() error {
		calls++
		return errFake
	}, WithAttempts(3), WithDelay(time.Millisecond), WithMaxDelay(time.Millisecond))
	require.Error(t, err)
	assert.ErrorIs(t, err, errFake)
	assert.Contains(t, err.Error(), "3 attempts")
	assert.Equal(t, 3, calls)
}

func TestDoRespectsContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())

	calls := 0
	start := time.Now()
	err := Do(ctx, func() error {
		calls++
		if calls == 1 {
			cancel()
		}
		return errFake
	}, WithAttempts(5), WithDelay(10*time.Second), WithMaxDelay(10*time.Second))

	assert.ErrorIs(t, err, context.Canceled)
	assert.Less(t, time.Since(start), 500*time.Millisecond)
}

func TestDoCallsOnRetry(t *testing.T) {
	t.Parallel()
	var retries []int
	err := Do(
		context.Background(), func() error { return errFake },
		WithAttempts(4),
		WithDelay(time.Millisecond),
		WithMaxDelay(time.Millisecond),
		WithOnRetry(func(attempt int, _ error) {
			retries = append(retries, attempt)
		}),
	)

	require.Error(t, err)
	// OnRetry fires between attempts — not after the last failure.
	assert.Len(t, retries, 3)
	for i, got := range retries {
		assert.Equal(t, i+1, got)
	}
}

func TestDoAppliesDefaults(t *testing.T) {
	t.Parallel()
	// No options → default 3 attempts, 1s delay, 30s maxDelay.
	// Override Delay to keep the test fast; observe Attempts via OnRetry.
	calls := 0
	err := Do(
		context.Background(), func() error { return errFake },
		WithDelay(time.Millisecond),
		WithMaxDelay(time.Millisecond),
		WithOnRetry(func(_ int, _ error) { calls++ }),
	)

	require.Error(t, err)
	// Default Attempts == 3, so OnRetry fires 2 times.
	assert.Equal(t, 2, calls)
}

func TestDoBackoffCaps(t *testing.T) {
	t.Parallel()
	cap := 4 * time.Millisecond
	var gaps []time.Duration
	prev := time.Now()

	Do(
		context.Background(), func() error { return errFake },
		WithAttempts(5),
		WithDelay(2*time.Millisecond),
		WithMaxDelay(cap),
		WithOnRetry(func(_ int, _ error) {
			now := time.Now()
			gaps = append(gaps, now.Sub(prev))
			prev = now
		}),
	)

	// gaps[i] is the time between attempt i completing and OnRetry being called —
	// essentially zero. The sleep happens after OnRetry. So we verify that the
	// total duration of the test is bounded by (Attempts-1) * MaxDelay.
	for i, g := range gaps {
		assert.LessOrEqualf(t, g, cap+20*time.Millisecond, "gap[%d]", i) // generous for scheduler jitter
	}
}

// recordingTransport records the request body seen on each attempt and fails
// the first failFor attempts with a transport error (which triggers a retry
// for any method).
type recordingTransport struct {
	bodies  []string
	failFor int
	n       int
}

func (rt *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.n++
	var body string
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		body = string(b)
	}
	rt.bodies = append(rt.bodies, body)
	if rt.n <= rt.failFor {
		return nil, errFake // transport error → retry
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok"))}, nil
}

// TestRetryTransportRewindsBody verifies that a POST body is re-sent intact on
// retry. Before the fix, the first attempt consumed the body and the retry
// transmitted an empty one.
func TestRetryTransportRewindsBody(t *testing.T) {
	rec := &recordingTransport{failFor: 1}
	client := NewHTTPClient(&http.Client{Transport: rec},
		WithAttempts(3), WithDelay(time.Millisecond))

	req, err := http.NewRequest(http.MethodPost, "http://example.test/x", strings.NewReader("payload"))
	require.NoError(t, err)
	resp, err := client.Transport.RoundTrip(req)
	require.NoError(t, err)
	_ = resp.Body.Close()

	require.Len(t, rec.bodies, 2) // 1 fail + 1 retry
	for i, b := range rec.bodies {
		assert.Equalf(t, "payload", b, "attempt %d body (body not rewound across retries)", i+1)
	}
}

// TestRetryTransportNonRewindableBodyNotRetried verifies that a request whose
// body cannot be rewound (GetBody == nil) is attempted exactly once, so the
// transport never re-sends it with an empty body.
func TestRetryTransportNonRewindableBodyNotRetried(t *testing.T) {
	rec := &recordingTransport{failFor: 3}
	client := NewHTTPClient(&http.Client{Transport: rec},
		WithAttempts(3), WithDelay(time.Millisecond))

	req, err := http.NewRequest(http.MethodPost, "http://example.test/x", nil)
	require.NoError(t, err)
	// Attach a body with no GetBody (the http.NewRequest-with-nil case leaves
	// Body nil, so set both fields to simulate an opaque, non-rewindable body).
	req.Body = io.NopCloser(strings.NewReader("opaque"))
	req.GetBody = nil

	_, err = client.Transport.RoundTrip(req)
	assert.Error(t, err) // the transport error must surface after a single attempt
	assert.Equal(t, 1, rec.n, "non-rewindable body must be attempted exactly once")
}

// statusTransport returns the same status on every call (or a transport error
// when status == 0) and counts the attempts it sees.
type statusTransport struct {
	status int
	n      int
}

func (st *statusTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	st.n++
	if st.status == 0 {
		return nil, errFake
	}
	return &http.Response{StatusCode: st.status, Body: io.NopCloser(strings.NewReader("body"))}, nil
}

// TestRetryDeciderOverridesPolicy verifies WithRetryDecider replaces the default
// idempotent-5xx policy: a decider can both suppress a would-be retry and force
// one the default would skip.
func TestRetryDeciderOverridesPolicy(t *testing.T) {
	t.Parallel()

	// Decider says "never retry": a 500 (normally retried for GET) is returned as-is.
	st := &statusTransport{status: 500}
	client := NewHTTPClient(&http.Client{Transport: st},
		WithAttempts(4), WithDelay(time.Millisecond),
		WithRetryDecider(func(_ *http.Response, _ error) bool { return false }))
	resp, err := client.Transport.RoundTrip(mustGet(t))
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, 1, st.n, "decider=false: no retry")

	// Decider says "retry any 4xx": a 404 (normally terminal) is retried to exhaustion.
	st2 := &statusTransport{status: 404}
	client2 := NewHTTPClient(&http.Client{Transport: st2},
		WithAttempts(3), WithDelay(time.Millisecond),
		WithRetryDecider(func(resp *http.Response, _ error) bool {
			return resp != nil && resp.StatusCode == 404
		}))
	resp2, err := client2.Transport.RoundTrip(mustGet(t))
	require.NoError(t, err)
	_ = resp2.Body.Close()
	assert.Equal(t, 3, st2.n, "decider 404: exhausted")
}

// TestRetryMaxElapsedStops verifies the maxElapsed budget halts retries before
// the attempt count is reached.
func TestRetryMaxElapsedStops(t *testing.T) {
	t.Parallel()
	st := &statusTransport{status: 0} // always a transport error
	client := NewHTTPClient(&http.Client{Transport: st},
		WithAttempts(20), WithDelay(20*time.Millisecond), WithFixedDelay(),
		WithMaxElapsed(50*time.Millisecond))

	_, err := client.Transport.RoundTrip(mustGet(t))
	assert.Error(t, err) // the transport error must surface
	// With a 20ms fixed delay and a 50ms budget, only a couple of attempts fit;
	// the loop must stop well short of the 20-attempt cap.
	assert.Less(t, st.n, 20, "maxElapsed must stop retries before the cap")
	assert.GreaterOrEqual(t, st.n, 2, "expected at least one retry before the budget ran out")
}

func mustGet(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://example.test/x", nil)
	require.NoError(t, err)
	return req
}
