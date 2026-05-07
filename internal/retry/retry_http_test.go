package retry_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/retry"
)

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
