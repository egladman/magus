package cache

import (
	"sync"
	"time"
)

// SlotWaitMeter totals the wall time during which at least one caller was queued on a
// [Limiter], fed the deltas its onWait hook reports. The zero value is ready and it is
// safe for concurrent use.
//
// Wall time rather than a sum of waits: ten targets queued together for five seconds
// cost the run five seconds, not fifty, and the sum would overstate what a wider pool
// could win back. Queue depth rather than per-grant intervals because hooks run outside
// the limiter's lock, so two grants from one release reach the meter in either order.
type SlotWaitMeter struct {
	mu     sync.Mutex
	depth  int
	since  time.Time
	waited time.Duration
}

// Add applies one onWait delta: +n as a caller starts waiting for n slots, -n once its
// acquire returns.
func (m *SlotWaitMeter) Add(delta int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addLocked(delta, time.Now())
}

func (m *SlotWaitMeter) addLocked(delta int, at time.Time) {
	was := m.depth
	m.depth += delta
	switch {
	case was == 0 && m.depth > 0:
		m.since = at
	case was > 0 && m.depth == 0:
		m.waited += at.Sub(m.since)
	}
}

// Waited returns the wall time observed so far with a caller queued, including a wait
// still open.
func (m *SlotWaitMeter) Waited() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.depth > 0 {
		return m.waited + time.Since(m.since)
	}
	return m.waited
}
