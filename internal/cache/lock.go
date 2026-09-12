package cache

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
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
	// holder names whoever took the lock, so a waiter can say what it is waiting for
	// rather than only that it is waiting. Guarded by keyedLock.mu.
	holder string
}

func newKeyedLock() *keyedLock { return &keyedLock{} }

// lockWaitHeartbeat is how often a queued acquirer says it is still queued. It matches
// the machine gate's cadence for the same reason: a wait with nothing on screen is
// indistinguishable from a hang, to a reader and to the stall watchdog alike.
//
// A var, not a const, for the reason stated above about lock.go's wait timings.
var lockWaitHeartbeat = 15 * time.Second

// acquireNamed takes the lock for key on waiter's behalf, recording waiter as the holder
// so the next caller can name what it is waiting for. Returns an unlock func (call
// exactly once on nil error) or ctx.Err() if cancelled while waiting.
//
// onBlock, when set, is called once with the current holder the moment this caller has to
// wait, and whatever it returns runs when the wait ends. It is the seam the slot watch
// hangs its "this seat cannot proceed" mark on, and the reason the mark can name the
// holder rather than only the key.
//
// A wait here beats the invocation heartbeat and says who it is waiting for. Waiting is
// legitimate (two steps keyed the same target and one of them is running it), so it must
// not read as a stall; and a run the watchdog does abort has to read as one thing waiting
// on another rather than as silence.
func (k *keyedLock) acquireNamed(ctx context.Context, key, waiter string, onBlock func(holder string) func()) (func(), error) {
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

	// The unlock closure operates on the captured e, not a fresh map lookup. That
	// is what makes key resurrection safe: if this holder is the last waiter, the
	// entry is deleted, and a new acquirer may insert a different entry under the
	// same key, and this closure still releases this e.sem, never the resurrected one.
	unlock := func() {
		<-e.sem
		k.mu.Lock()
		e.waiters--
		e.holder = ""
		if e.waiters == 0 {
			delete(k.entries, key)
		}
		k.mu.Unlock()
	}
	abandon := func() {
		k.mu.Lock()
		e.waiters--
		if e.waiters == 0 {
			delete(k.entries, key)
		}
		k.mu.Unlock()
	}
	took := func() func() {
		k.mu.Lock()
		e.holder = waiter
		k.mu.Unlock()
		return unlock
	}

	select {
	case e.sem <- struct{}{}:
		return took(), nil
	default:
	}

	// Past here this caller is queued behind somebody. Report it once, by name.
	holder := k.holderOf(key)
	if onBlock != nil {
		done := onBlock(holder)
		defer done()
	}
	slog.InfoContext(ctx, fmt.Sprintf("magus: %s is waiting for a cache lock held by %s",
		displayLockParty(waiter), displayLockParty(holder)))
	beat := time.NewTicker(lockWaitHeartbeat)
	defer beat.Stop()
	started := time.Now()
	next := lockWaitHeartbeat
	for {
		select {
		case e.sem <- struct{}{}:
			return took(), nil
		case <-beat.C:
			// The beat says this run is not hung, to the watchdog that would otherwise
			// abort a legitimate wait exactly as if it had wedged. The log backs off as
			// the wait doubles; the watchdog does not.
			ProgressFromContext(ctx).Beat()
			if elapsed := time.Since(started); elapsed >= next {
				next *= 2
				slog.InfoContext(ctx, fmt.Sprintf("magus: %s is still waiting for a cache lock held by %s (%s so far)",
					displayLockParty(waiter), displayLockParty(k.holderOf(key)), elapsed.Round(time.Second)))
			}
		case <-ctx.Done():
			abandon()
			return nil, ctx.Err()
		}
	}
}

// holderOf names whoever holds key right now, empty when nobody does or the holder went
// unnamed. Racy by nature, and only ever used in a message.
func (k *keyedLock) holderOf(key string) string {
	k.mu.Lock()
	defer k.mu.Unlock()
	if e, ok := k.entries[key]; ok {
		return e.holder
	}
	return ""
}

// displayLockParty renders a lock party for a message, standing in for the callers that
// name none (a bare keyedLock in a test).
func displayLockParty(name string) string {
	if name == "" {
		return "another step"
	}
	return name
}

// hashLocks serializes cache.Run calls per (project, hash) within this process.
// Cross-process races produce duplicate work but never corrupt the cache (blobs are
// content-addressed and written atomically).
var hashLocks = newKeyedLock()
