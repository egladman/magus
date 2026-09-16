package sessions

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

// Serializing the store's one read-modify-write. Appending a record needs no lock, which
// is the property the package is built on; building a dedup set from the whole store and
// then appending against it does, and LoadEvents is the only caller that does it.

// loadLockName is the lock file, beside the session files it guards. A dotfile so a
// reader walking the store for session records does not have to know about it.
const loadLockName = ".load.lock"

// loadLockTimeout bounds the wait for another loader.
//
// Generous, because the holder is doing real work: a first ingest reads a machine's whole
// transcript history, measured at 11s here and unbounded in principle. Short enough that
// a stale holder cannot wedge a graph build for the rest of the day, and giving up is
// safe: this load stores nothing, and the next one picks up the same transcripts from the
// same byte offsets.
const loadLockTimeout = 2 * time.Minute

// loadLockPoll is how often the wait retries. Coarse: contention here is between
// background jobs on a six-hour timer, so nothing is waiting on a fast answer.
const loadLockPoll = 250 * time.Millisecond

// lockStore takes the store's load lock and returns the release.
//
// An OS file lock (the same flock the project locker uses) rather than a marker file,
// because the contending processes are unrelated magus invocations on one machine and a
// lock the kernel drops on exit is the only kind a killed loader cannot leak.
func lockStore(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("sessions: prepare store %s: %w", dir, err)
	}
	lock := flock.New(filepath.Join(dir, loadLockName))
	deadline := time.Now().Add(loadLockTimeout)
	for {
		ok, err := lock.TryLock()
		if err != nil {
			return nil, fmt.Errorf("sessions: lock store %s: %w", dir, err)
		}
		if ok {
			return func() { _ = lock.Unlock() }, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("sessions: another load has held the store lock in %s for over %s", dir, loadLockTimeout)
		}
		time.Sleep(loadLockPoll)
	}
}
