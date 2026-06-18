package cache

import (
	"context"
	"sync"
)

// keyedLock is a per-key mutex with context-cancellable acquisition.
// Each key maps to a refcounted 1-buffered channel semaphore; entries are freed
// when refcount drops to zero.
type keyedLock struct {
	mu      sync.Mutex
	entries map[string]*keyedLockEntry
}

type keyedLockEntry struct {
	sem     chan struct{} // capacity 1: present = held, empty = free
	waiters int
}

func newKeyedLock() *keyedLock { return &keyedLock{} }

// acquire takes the lock for key. Returns an unlock func (call exactly once on nil error)
// or ctx.Err() if cancelled while waiting.
func (k *keyedLock) acquire(ctx context.Context, key string) (func(), error) {
	k.mu.Lock()
	if k.entries == nil {
		k.entries = make(map[string]*keyedLockEntry)
	}
	e, ok := k.entries[key]
	if !ok {
		e = &keyedLockEntry{sem: make(chan struct{}, 1)}
		k.entries[key] = e
	}
	e.waiters++
	k.mu.Unlock()

	select {
	case e.sem <- struct{}{}:
	case <-ctx.Done():
		k.mu.Lock()
		e.waiters--
		if e.waiters == 0 {
			delete(k.entries, key)
		}
		k.mu.Unlock()
		return nil, ctx.Err()
	}

	// The unlock closure operates on the captured e, not a fresh map lookup. That
	// is what makes key resurrection safe: if this holder is the last waiter, the
	// entry is deleted, and a new acquirer may insert a different entry under the
	// same key — but we still release this e.sem, never the resurrected one.
	return func() {
		<-e.sem
		k.mu.Lock()
		e.waiters--
		if e.waiters == 0 {
			delete(k.entries, key)
		}
		k.mu.Unlock()
	}, nil
}

// hashLocks serialises cache.Run calls per (project, hash) within this process.
// Cross-process races produce duplicate work but never corrupt the cache (blobs are
// content-addressed and written atomically).
var hashLocks = newKeyedLock()
