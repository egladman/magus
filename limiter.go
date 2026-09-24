package magus

import (
	"fmt"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
)

// Limiter is a weighted semaphore that caps concurrent spell executions.
// Obtain one with [NewLimiter] and share it across daemon workspaces via [WithLimiter].
type Limiter struct{ lim *cache.Limiter }

// NewLimiter creates a Limiter with capacity n. n ≤ 0 defaults to
// [DefaultConcurrency].
func NewLimiter(n int) *Limiter {
	if n <= 0 {
		n = cache.DefaultConcurrency()
	}
	return &Limiter{lim: cache.NewLimiter(n)}
}

// DefaultConcurrency returns the balanced profile's width: the concurrency cap used
// when no explicit cap is set, resolved by precedence: the MAGUS_CONCURRENCY env var
// if set to a positive int, then min(NumCPU, 8).
func DefaultConcurrency() int { return cache.DefaultConcurrency() }

// Capacity returns the configured concurrency cap.
func (l *Limiter) Capacity() int { return l.lim.Capacity() }

// WithLimiter injects a pre-built Limiter (e.g. shared across daemon workspaces).
// When omitted, Open constructs a private limiter from magus.yaml/Concurrency.
func WithLimiter(l *Limiter) Option {
	return func(o *workspace.Load) { o.Limiter = l.lim }
}

// slotWaitFloor is the least slot wait worth a nudge. It is the cadence at which magus
// already treats a wait as worth telling the reader about (the machine budget and the
// project lock both heartbeat at 15s); below it the win from a wider pool is inside the
// variance of an ordinary run.
const slotWaitFloor = 15 * time.Second

// slotPressure is what a run knows, once it ends, about whether its width held it back.
type slotPressure struct {
	waited              time.Duration
	width               int
	aggressiveWidth     int
	explicitConcurrency bool
	profile             types.ConcurrencyProfile
}

// nudge returns the one line suggesting the aggressive profile, or "" when the run
// has no case for it.
func (p slotPressure) nudge() string {
	switch {
	case p.explicitConcurrency, p.profile == types.ProfileAggressive:
		// The user chose a width; second-guessing that choice is noise.
		return ""
	case p.aggressiveWidth <= p.width, p.waited < slotWaitFloor:
		return ""
	}
	return fmt.Sprintf("this run waited %s for slots; concurrency_profile: aggressive would have given it %d",
		p.waited.Round(time.Second), p.aggressiveWidth)
}

// ConcurrencyNudge returns a one-line suggestion to raise concurrency_profile when this
// Magus's targets queued for slots while cores the aggressive profile would use sat
// idle, and "" otherwise. The caller decides whether and how often to print it.
//
// Silent when concurrency was set explicitly and when the profile is already
// aggressive. A caller whose limiter spans other invocations' runs (the server's) does
// not ask: a wait there is not this invocation's profile.
func (m *Magus) ConcurrencyNudge() string {
	if m.cache == nil {
		return ""
	}
	aggressiveWidth, _ := cache.ClampConcurrency(cache.ProfileConcurrency(types.ProfileAggressive))
	return slotPressure{
		waited:              m.slotWaits.Waited(),
		width:               m.limiter().Capacity(),
		aggressiveWidth:     aggressiveWidth,
		explicitConcurrency: m.cfg.Concurrency > 0,
		profile:             m.cfg.ConcurrencyProfile,
	}.nudge()
}
