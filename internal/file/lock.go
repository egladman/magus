package file

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

// LockWait is the bound a small-file read-modify-write waits for its lock: such a
// rewrite takes milliseconds, so a holder past this is stuck rather than busy.
const LockWait = 10 * time.Second

// lockRetryDelay is how often a blocked acquisition re-polls, matching the project
// locks' cadence in magus/lock.go.
const lockRetryDelay = 20 * time.Millisecond

// WithLock runs fn while holding an exclusive OS file lock on path, creating path's
// directory and the lock file as needed.
//
// An flock rather than a marker file: the kernel drops it when the holder exits, so a
// killed process never leaves the lock behind. Advisory: it serializes callers of
// WithLock on the same path and nothing else. Two acquisitions in one process exclude
// each other too, so a test with two stores stands in for two processes. It is not
// reentrant: fn must not take the same lock.
//
// The wait is bounded by wait and by ctx, whichever ends first; ctx shortens the wait
// and never lengthens it. The error says which ended it, because a caller cancelled by
// its own deadline and a caller blocked by a stuck holder have different problems.
// A zero wait tries once, for a caller that skips its work rather than repeat what the
// holder is doing. fn does not run when the lock is not acquired.
func WithLock(ctx context.Context, path string, wait time.Duration, fn func() error) (err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("file: lock %s: %w", path, err)
	}
	fl := flock.New(path)
	got, err := fl.TryLock()
	if err != nil {
		return fmt.Errorf("file: lock %s: %w", path, err)
	}
	if !got {
		wctx, cancel := context.WithTimeout(ctx, wait)
		defer cancel()
		if got, err = fl.TryLockContext(wctx, lockRetryDelay); err != nil || !got {
			switch {
			case ctx.Err() != nil:
				return fmt.Errorf("file: cancelled while waiting for the lock at %s: %w", path, ctx.Err())
			case err != nil && wctx.Err() == nil:
				return fmt.Errorf("file: lock %s: %w", path, err)
			}
			return fmt.Errorf("file: another process has held the lock at %s for more than %s."+
				" Look for a stuck magus process with `magus status`, then retry;"+
				" the lock is an OS file lock and is released the moment its holder exits", path, wait)
		}
	}
	defer func() {
		if uerr := fl.Unlock(); uerr != nil {
			err = errors.Join(err, fmt.Errorf("file: unlock %s: %w", path, uerr))
		}
	}()
	return fn()
}
