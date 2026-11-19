package codec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// LockWait is the bound a small-file read-modify-write waits for its lock: such a
// rewrite takes milliseconds, so a holder past this is stuck rather than busy.
const LockWait = 10 * time.Second

// lockRetryDelay is how often a blocked acquisition re-polls.
const lockRetryDelay = 20 * time.Millisecond

// errLockHeld is what tryLock answers when another holder has the lock.
var errLockHeld = errors.New("lock held")

// LockHeldError is [WithLock] giving up because another holder kept the lock at Path
// for the whole Wait. A zero Wait means the one try found it held.
type LockHeldError struct {
	Path string
	Wait time.Duration
}

func (e *LockHeldError) Error() string {
	return fmt.Sprintf("codec: another process has held the lock at %s for more than %s,"+
		" the lock is an OS file lock and is released the moment its holder exits", e.Path, e.Wait)
}

// WithLock runs fn while holding an exclusive OS file lock on path, creating path's
// directory and the lock file as needed.
//
// An OS lock rather than a marker file: the kernel drops it when the holder exits, so a
// killed process never leaves the lock behind. Advisory: it serializes callers of
// WithLock on the same path and nothing else. Two acquisitions in one process exclude
// each other too, so a test with two stores stands in for two processes. It is not
// reentrant: fn must not take the same lock.
//
// The lock is flock(2) on unix and a one-byte LockFileEx at offset zero on Windows, the
// locks github.com/gofrs/flock takes, so a holder through either excludes the other.
//
// The wait is bounded by wait and by ctx, whichever ends first; ctx shortens the wait
// and never lengthens it. The error says which ended it, because a caller cancelled by
// its own deadline and a caller blocked by a stuck holder have different problems: a
// wait that ran out is a *[LockHeldError].
// A zero wait tries once, for a caller that skips its work rather than repeat what the
// holder is doing. fn does not run when the lock is not acquired.
func WithLock(ctx context.Context, path string, wait time.Duration, fn func() error) (err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("lock %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("lock %s: %w", path, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("codec: close %s: %w", path, cerr))
		}
	}()
	if err := acquire(ctx, f, path, wait); err != nil {
		return err
	}
	defer func() {
		if uerr := unlock(f); uerr != nil {
			err = errors.Join(err, fmt.Errorf("unlock %s: %w", path, uerr))
		}
	}()
	return fn()
}

// acquire takes the lock on f, polling until wait or ctx ends.
func acquire(ctx context.Context, f *os.File, path string, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	ticker := time.NewTicker(lockRetryDelay)
	defer ticker.Stop()
	for {
		err := tryLock(f)
		if err == nil {
			return nil
		}
		if !errors.Is(err, errLockHeld) {
			return fmt.Errorf("lock %s: %w", path, err)
		}
		if !time.Now().Before(deadline) {
			return &LockHeldError{Path: path, Wait: wait}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("codec: cancelled while waiting for the lock at %s: %w", path, ctx.Err())
		case <-ticker.C:
		}
	}
}
