package magus

import (
	"testing"
	"time"

	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

func TestLimiterFacade(t *testing.T) {
	limiter := NewLimiter(3)
	var load workspace.Load
	WithLimiter(limiter)(&load)

	assert.Equal(t, struct {
		Capacity      int
		Default       int
		InjectedLimit any
	}{
		Capacity:      3,
		Default:       DefaultConcurrency(),
		InjectedLimit: limiter.lim,
	}, struct {
		Capacity      int
		Default       int
		InjectedLimit any
	}{
		Capacity:      limiter.Capacity(),
		Default:       DefaultConcurrency(),
		InjectedLimit: load.Limiter,
	})
}

// TestSlotPressureNudge pins every reason the nudge stays silent, beside the one case
// where it speaks.
func TestSlotPressureNudge(t *testing.T) {
	pressured := slotPressure{
		waited: 41*time.Second + 300*time.Millisecond, width: 8, aggressiveWidth: 16,
		profile: types.ProfileBalanced,
	}
	with := func(edit func(*slotPressure)) slotPressure {
		p := pressured
		edit(&p)
		return p
	}
	got := map[string]string{
		"pressured":        pressured.nudge(),
		"explicit":         with(func(p *slotPressure) { p.explicitConcurrency = true }).nudge(),
		"aggressive":       with(func(p *slotPressure) { p.profile = types.ProfileAggressive }).nudge(),
		"machine queued":   with(func(p *slotPressure) { p.machineQueued = true }).nudge(),
		"no idle cores":    with(func(p *slotPressure) { p.aggressiveWidth = 8 }).nudge(),
		"under the floor":  with(func(p *slotPressure) { p.waited = slotWaitFloor - time.Millisecond }).nudge(),
		"at the floor":     with(func(p *slotPressure) { p.waited = slotWaitFloor }).nudge(),
		"conservative too": with(func(p *slotPressure) { p.profile = types.ProfileConservative; p.width = 4 }).nudge(),
	}
	assert.Equal(t, map[string]string{
		"pressured":        "this run waited 41s for slots; concurrency_profile: aggressive would have given it 16",
		"explicit":         "",
		"aggressive":       "",
		"machine queued":   "",
		"no idle cores":    "",
		"under the floor":  "",
		"at the floor":     "this run waited 15s for slots; concurrency_profile: aggressive would have given it 16",
		"conservative too": "this run waited 41s for slots; concurrency_profile: aggressive would have given it 16",
	}, got)
}
