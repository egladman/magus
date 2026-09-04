package types

import "context"

type captureLogKey struct{}

// WithCaptureLog returns a context naming the file a target's output is being teed
// to. The cache layer opens that file and knows its path; the code that has to NAME
// it in an error - a target cancelled at its declared ceiling, say - is several
// layers below and has no route to the store.
//
// Deliberately a path and not a store handle: the value is for a human to open, and
// a plain string cannot grow into a second way to read the cache.
func WithCaptureLog(ctx context.Context, path string) context.Context {
	if path == "" {
		return ctx
	}
	return context.WithValue(ctx, captureLogKey{}, path)
}

// CaptureLog returns the log path installed for the running target, or "" when the
// caller is not running under one (a bare `magus buzz` script, a unit test).
func CaptureLog(ctx context.Context) string {
	s, _ := ctx.Value(captureLogKey{}).(string)
	return s
}
