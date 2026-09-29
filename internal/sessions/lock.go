package sessions

import "time"

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
