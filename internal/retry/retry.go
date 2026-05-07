// Package retry provides exponential-backoff retry helpers: Do runs an
// arbitrary operation with capped, context-aware backoff, and NewHTTPClient
// wraps an http.Client so idempotent requests retry on transport errors and
// 5xx responses (honouring Retry-After). Non-idempotent requests retry only on
// transport errors, and request bodies are rewound between attempts.
package retry

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

const (
	defaultAttempts = 3
	defaultDelay    = time.Second
	defaultMaxDelay = 30 * time.Second
)

// options holds the accumulated configuration for a [Do] call.
type options struct {
	attempts int
	delay    time.Duration
	maxDelay time.Duration
	onRetry  func(attempt int, err error)
}

// Option configures a [Do] call.
type Option func(*options)

// WithAttempts sets the maximum number of attempts, including the first.
// Defaults to 3. A value of 1 disables retry. Values ≤ 0 are treated as 1.
func WithAttempts(n int) Option {
	if n <= 0 {
		n = 1
	}
	return func(o *options) { o.attempts = n }
}

// WithDelay sets the base backoff duration before the second attempt. Defaults to 1s.
func WithDelay(d time.Duration) Option { return func(o *options) { o.delay = d } }

// WithMaxDelay caps the exponential backoff. Defaults to 30s.
func WithMaxDelay(d time.Duration) Option { return func(o *options) { o.maxDelay = d } }

// WithOnRetry sets a callback invoked after each failed attempt before sleeping.
// attempt is 1-based. Not called after the final failure.
func WithOnRetry(fn func(attempt int, err error)) Option {
	return func(o *options) { o.onRetry = fn }
}

// Do runs fn with exponential backoff. It returns nil on the first success,
// or the final error wrapped with the attempt count. It returns ctx.Err()
// immediately if the context is cancelled mid-backoff.
func Do(ctx context.Context, fn func() error, opts ...Option) error {
	cfg := options{
		attempts: defaultAttempts,
		delay:    defaultDelay,
		maxDelay: defaultMaxDelay,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	var lastErr error
	for attempt := 1; attempt <= cfg.attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		if attempt == cfg.attempts {
			break
		}

		if cfg.onRetry != nil {
			cfg.onRetry(attempt, lastErr)
		}

		shift := attempt - 1
		if shift > 62 {
			shift = 62
		}
		delay := cfg.delay << shift
		if delay > cfg.maxDelay || delay < 0 {
			delay = cfg.maxDelay
		}

		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return fmt.Errorf("retry: %d attempts: %w", cfg.attempts, lastErr)
}

// retryTransport wraps an http.RoundTripper with the same exponential backoff
// logic as Do. Idempotent methods (GET, HEAD, OPTIONS, PUT, DELETE) retry on
// transport errors and 5xx responses. POST and PATCH retry only on transport
// errors. Retry-After headers are honoured when present.
type retryTransport struct {
	base http.RoundTripper
	opts options
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	isIdempotent := isIdempotentMethod(req.Method)
	cfg := t.opts

	// Bodies without GetBody cannot be rewound; collapse to a single attempt.
	if req.Body != nil && req.GetBody == nil {
		cfg.attempts = 1
	}

	var (
		resp    *http.Response
		lastErr error
	)
	for attempt := 1; attempt <= cfg.attempts; attempt++ {
		if err := req.Context().Err(); err != nil {
			return nil, err
		}

		cur := req
		if attempt > 1 && req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				if resp != nil {
					return resp, nil
				}
				return nil, err
			}
			// Clone per retry: RoundTrip must not mutate the caller's *http.Request.
			cur = req.Clone(req.Context())
			cur.Body = body
		}

		var err error
		resp, err = t.base.RoundTrip(cur)
		if err != nil { // transport-level error: always retry
			lastErr = err
			if attempt == cfg.attempts {
				break
			}
			if cfg.onRetry != nil {
				cfg.onRetry(attempt, err)
			}
			if sleep(req.Context(), backoff(cfg, attempt)) != nil {
				return nil, req.Context().Err()
			}
			continue
		}

		if resp.StatusCode >= 500 && isIdempotent { // 5xx: retry only for idempotent methods
			lastErr = &httpError{code: resp.StatusCode}
			if attempt == cfg.attempts {
				break
			}
			if cfg.onRetry != nil {
				cfg.onRetry(attempt, lastErr)
			}
			delay := retryAfterDelay(resp, backoff(cfg, attempt))
			_ = resp.Body.Close()
			resp = nil
			if sleep(req.Context(), delay) != nil {
				return nil, req.Context().Err()
			}
			continue
		}

		return resp, nil
	}

	if resp != nil {
		return resp, nil
	}
	return nil, lastErr
}

// NewHTTPClient returns a *http.Client whose transport retries failed requests
// using the same backoff options as Do. If base is nil, http.DefaultClient is
// used as the base.
func NewHTTPClient(base *http.Client, opts ...Option) *http.Client {
	cfg := options{
		attempts: defaultAttempts,
		delay:    defaultDelay,
		maxDelay: defaultMaxDelay,
	}
	for _, o := range opts {
		o(&cfg)
	}

	var baseTransport http.RoundTripper
	if base != nil {
		baseTransport = base.Transport
	}
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}

	clone := &http.Client{}
	if base != nil {
		*clone = *base
	}
	clone.Transport = &retryTransport{base: baseTransport, opts: cfg}
	return clone
}

func isIdempotentMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions,
		http.MethodPut, http.MethodDelete:
		return true
	}
	return false
}

func backoff(cfg options, attempt int) time.Duration {
	shift := attempt - 1
	if shift > 62 {
		shift = 62
	}
	d := cfg.delay << shift
	if d > cfg.maxDelay || d < 0 {
		d = cfg.maxDelay
	}
	return d
}

// retryAfterDelay returns the delay from a Retry-After header, falling back to
// the computed exponential delay when the header is absent or unparseable.
func retryAfterDelay(resp *http.Response, fallback time.Duration) time.Duration {
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return fallback
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type httpError struct{ code int }

func (e *httpError) Error() string {
	return "http: server returned " + strconv.Itoa(e.code)
}
