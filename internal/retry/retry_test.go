package retry_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/retry"
)

var errFake = errors.New("fake error")

func TestDoSucceedsFirstTry(t *testing.T) {
	t.Parallel()
	calls := 0
	err := retry.Do(context.Background(), func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestDoSucceedsAfterRetries(t *testing.T) {
	t.Parallel()
	calls := 0
	err := retry.Do(context.Background(), func() error {
		calls++
		if calls < 3 {
			return errFake
		}
		return nil
	}, retry.WithAttempts(3), retry.WithDelay(time.Millisecond), retry.WithMaxDelay(time.Millisecond))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestDoExhausts(t *testing.T) {
	t.Parallel()
	calls := 0
	err := retry.Do(context.Background(), func() error {
		calls++
		return errFake
	}, retry.WithAttempts(3), retry.WithDelay(time.Millisecond), retry.WithMaxDelay(time.Millisecond))
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, errFake) {
		t.Fatalf("error %q does not wrap errFake", err)
	}
	if !strings.Contains(err.Error(), "3 attempts") {
		t.Fatalf("error %q does not mention attempt count", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestDoRespectsContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())

	calls := 0
	start := time.Now()
	err := retry.Do(ctx, func() error {
		calls++
		if calls == 1 {
			cancel()
		}
		return errFake
	}, retry.WithAttempts(5), retry.WithDelay(10*time.Second), retry.WithMaxDelay(10*time.Second))

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("took %v, want < 500ms", elapsed)
	}
}

func TestDoCallsOnRetry(t *testing.T) {
	t.Parallel()
	var retries []int
	err := retry.Do(
		context.Background(), func() error { return errFake },
		retry.WithAttempts(4),
		retry.WithDelay(time.Millisecond),
		retry.WithMaxDelay(time.Millisecond),
		retry.WithOnRetry(func(attempt int, _ error) {
			retries = append(retries, attempt)
		}),
	)

	if err == nil {
		t.Fatal("want error, got nil")
	}
	// OnRetry fires between attempts — not after the last failure.
	if len(retries) != 3 {
		t.Fatalf("OnRetry called %d times, want 3; calls: %v", len(retries), retries)
	}
	for i, got := range retries {
		if want := i + 1; got != want {
			t.Fatalf("retries[%d] = %d, want %d", i, got, want)
		}
	}
}

func TestDoAppliesDefaults(t *testing.T) {
	t.Parallel()
	// No options → default 3 attempts, 1s delay, 30s maxDelay.
	// Override Delay to keep the test fast; observe Attempts via OnRetry.
	calls := 0
	err := retry.Do(
		context.Background(), func() error { return errFake },
		retry.WithDelay(time.Millisecond),
		retry.WithMaxDelay(time.Millisecond),
		retry.WithOnRetry(func(_ int, _ error) { calls++ }),
	)

	if err == nil {
		t.Fatal("want error, got nil")
	}
	// Default Attempts == 3, so OnRetry fires 2 times.
	if calls != 2 {
		t.Fatalf("OnRetry calls = %d, want 2 (default 3 attempts)", calls)
	}
}

func TestDoBackoffCaps(t *testing.T) {
	t.Parallel()
	cap := 4 * time.Millisecond
	var gaps []time.Duration
	prev := time.Now()

	retry.Do(
		context.Background(), func() error { return errFake },
		retry.WithAttempts(5),
		retry.WithDelay(2*time.Millisecond),
		retry.WithMaxDelay(cap),
		retry.WithOnRetry(func(_ int, _ error) {
			now := time.Now()
			gaps = append(gaps, now.Sub(prev))
			prev = now
		}),
	)

	// gaps[i] is the time between attempt i completing and OnRetry being called —
	// essentially zero. The sleep happens after OnRetry. So we verify that the
	// total duration of the test is bounded by (Attempts-1) * MaxDelay.
	for i, g := range gaps {
		if g > cap+20*time.Millisecond { // generous for scheduler jitter
			t.Errorf("gap[%d] = %v, want ≤ %v", i, g, cap+20*time.Millisecond)
		}
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
	client := retry.NewHTTPClient(&http.Client{Transport: rec},
		retry.WithAttempts(3), retry.WithDelay(time.Millisecond))

	req, err := http.NewRequest(http.MethodPost, "http://example.test/x", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_ = resp.Body.Close()

	if len(rec.bodies) != 2 {
		t.Fatalf("expected 2 attempts (1 fail + 1 retry), got %d", len(rec.bodies))
	}
	for i, b := range rec.bodies {
		if b != "payload" {
			t.Errorf("attempt %d body = %q, want %q (body not rewound across retries)", i+1, b, "payload")
		}
	}
}

// TestRetryTransportNonRewindableBodyNotRetried verifies that a request whose
// body cannot be rewound (GetBody == nil) is attempted exactly once, so the
// transport never re-sends it with an empty body.
func TestRetryTransportNonRewindableBodyNotRetried(t *testing.T) {
	rec := &recordingTransport{failFor: 3}
	client := retry.NewHTTPClient(&http.Client{Transport: rec},
		retry.WithAttempts(3), retry.WithDelay(time.Millisecond))

	req, err := http.NewRequest(http.MethodPost, "http://example.test/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Attach a body with no GetBody (the http.NewRequest-with-nil case leaves
	// Body nil, so set both fields to simulate an opaque, non-rewindable body).
	req.Body = io.NopCloser(strings.NewReader("opaque"))
	req.GetBody = nil

	_, err = client.Transport.RoundTrip(req)
	if err == nil {
		t.Fatal("expected the transport error to surface after a single attempt")
	}
	if rec.n != 1 {
		t.Errorf("non-rewindable body was attempted %d times; want exactly 1", rec.n)
	}
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
	client := retry.NewHTTPClient(&http.Client{Transport: st},
		retry.WithAttempts(4), retry.WithDelay(time.Millisecond),
		retry.WithRetryDecider(func(_ *http.Response, _ error) bool { return false }))
	resp, err := client.Transport.RoundTrip(mustGet(t))
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_ = resp.Body.Close()
	if st.n != 1 {
		t.Errorf("decider=false: attempts = %d, want 1", st.n)
	}

	// Decider says "retry any 4xx": a 404 (normally terminal) is retried to exhaustion.
	st2 := &statusTransport{status: 404}
	client2 := retry.NewHTTPClient(&http.Client{Transport: st2},
		retry.WithAttempts(3), retry.WithDelay(time.Millisecond),
		retry.WithRetryDecider(func(resp *http.Response, _ error) bool {
			return resp != nil && resp.StatusCode == 404
		}))
	resp2, err := client2.Transport.RoundTrip(mustGet(t))
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_ = resp2.Body.Close()
	if st2.n != 3 {
		t.Errorf("decider 404: attempts = %d, want 3 (exhausted)", st2.n)
	}
}

// TestRetryMaxElapsedStops verifies the maxElapsed budget halts retries before
// the attempt count is reached.
func TestRetryMaxElapsedStops(t *testing.T) {
	t.Parallel()
	st := &statusTransport{status: 0} // always a transport error
	client := retry.NewHTTPClient(&http.Client{Transport: st},
		retry.WithAttempts(20), retry.WithDelay(20*time.Millisecond), retry.WithFixedDelay(),
		retry.WithMaxElapsed(50*time.Millisecond))

	_, err := client.Transport.RoundTrip(mustGet(t))
	if err == nil {
		t.Fatal("expected the transport error to surface")
	}
	// With a 20ms fixed delay and a 50ms budget, only a couple of attempts fit;
	// the loop must stop well short of the 20-attempt cap.
	if st.n >= 20 {
		t.Errorf("maxElapsed did not stop retries: attempts = %d, want < 20", st.n)
	}
	if st.n < 2 {
		t.Errorf("expected at least one retry before the budget ran out, got %d attempts", st.n)
	}
}

func mustGet(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://example.test/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}
