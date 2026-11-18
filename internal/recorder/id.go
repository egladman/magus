package recorder

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"sync/atomic"
	"time"
)

// permitKey carries the step's permit count on a context.
type permitKey struct{}

// WithPermits gives a step a place to record what it permitted.
func WithPermits(ctx context.Context) context.Context {
	return context.WithValue(ctx, permitKey{}, new(atomic.Int64))
}

// RecordPermits adds n permitted findings to the running step, and reports
// false when ctx carries no step to record them on.
func RecordPermits(ctx context.Context, n int) bool {
	count, ok := ctx.Value(permitKey{}).(*atomic.Int64)
	if ok {
		count.Add(int64(n))
	}
	return ok
}

// Permits is how many findings the running step has permitted.
func Permits(ctx context.Context) int {
	count, ok := ctx.Value(permitKey{}).(*atomic.Int64)
	if !ok {
		return 0
	}
	return int(count.Load())
}

// invSeq makes minted invocation ids unique within a process without a uuid dep.
var invSeq atomic.Uint64

// NewInvocationID mints an id unique within this process: "inv", a
// millisecond stamp and a counter. It satisfies journal.ValidInvocationID.
func NewInvocationID() string {
	return fmt.Sprintf("inv%s%s", strconv.FormatInt(time.Now().UnixMilli(), 36), strconv.FormatUint(invSeq.Add(1), 36))
}

// invPattern is the shape NewInvocationID mints. A pasted id routes to the
// journal; the graph grammar would find nothing and read as a missing run.
var invPattern = regexp.MustCompile("^inv[0-9a-z]+$")

// LooksLikeInvocationID reports whether s has the shape NewInvocationID mints.
// It is a recognizer for routing a pasted word, never the guard on a path.
func LooksLikeInvocationID(s string) bool { return invPattern.MatchString(s) }
