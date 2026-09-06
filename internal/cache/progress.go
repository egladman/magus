package cache

import (
	"sync"
	"sync/atomic"
	"time"
)

// Progress is one invocation's liveness heartbeat: when something last moved, and what
// it was. Every accounting edge beats it - a step taking or handing back its seat
// ([Cache.admit]) and every line of subprocess output - so "is anything happening?" is
// one comparison rather than a poll across the limiter, the inflight set and the
// journal.
//
// It answers a different question from a deadline on a target. A deadline bounds work
// that runs LONG; this records whether ANY work is happening, which is what an
// invocation stops doing when it wedges with its project locks still held.
//
// A nil *Progress is usable and inert, so a caller with no heartbeat installed (a bare
// [Cache.Run] in a test) needs no guard of its own.
type Progress struct {
	at   atomic.Int64 // unix nanos of the last beat; beaten per output line, so not under mu
	mu   sync.Mutex
	last Mark
}

// Mark names one transition Progress saw. Log is the step's captured log path, empty
// until [Cache.captureRun] has opened one.
type Mark struct {
	At      time.Time
	Project string
	Target  string
	What    string // "running", "executing", "finished"
	Log     string
}

// NewProgress returns a heartbeat beating as of now.
func NewProgress() *Progress {
	p := &Progress{}
	p.at.Store(time.Now().UnixNano())
	return p
}

// Beat records that something moved without naming it, in one atomic store. The named
// transition on record is deliberately left alone: under a stall nothing beats at all,
// so the last transition IS the last thing that ran.
func (p *Progress) Beat() {
	if p == nil {
		return
	}
	p.at.Store(time.Now().UnixNano())
}

// Record notes a named transition and beats.
func (p *Progress) Record(m Mark) {
	if p == nil {
		return
	}
	m.At = time.Now()
	p.mu.Lock()
	p.last = m
	p.mu.Unlock()
	p.at.Store(m.At.UnixNano())
}

// Last returns the last recorded transition, zero if none.
func (p *Progress) Last() Mark {
	if p == nil {
		return Mark{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.last
}

// Idle reports how long it has been since anything beat. A nil heartbeat is never
// idle, so a watchdog reading one cannot fire on work it was never wired to see.
func (p *Progress) Idle() time.Duration {
	if p == nil {
		return 0
	}
	return time.Since(time.Unix(0, p.at.Load()))
}
