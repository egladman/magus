package hostmem

import (
	"context"
	"fmt"
	"time"
)

const (
	// sampleEvery is how often the watchdog reads the host. Short on purpose: a
	// shard once died 30 seconds after a sample reading 13.8GB free, so a 30s
	// interval showed no pressure while saying nothing about the window that
	// killed it.
	//
	// The first sample lands one interval in, so a run shorter than this is never
	// sampled. A sub-2s target has not had time to grow into the machine, and
	// sampling at t=0 would report what the previous command left behind.
	sampleEvery = 2 * time.Second

	// warnBelowFraction is the share of total memory under which the watchdog
	// starts talking, as a denominator: available < total/8 (12.5%). Above it a
	// build is using the machine, which is what a build is for.
	warnBelowFraction = 8

	// reWarnDropBytes is how much further the floor must fall before saying so
	// again. Without it a tight run emits a line every 2 seconds and its reader
	// learns to scroll past.
	reWarnDropBytes = 250 << 20
)

// Sampler reads available memory. Injected so the watchdog's logic is testable
// without a machine under real memory pressure.
type Sampler func() int64

// Watch samples available memory until ctx is done, calling emit when headroom
// first falls below an eighth of total and on every further 250MB drop.
//
// Silent on a healthy run. A killed runner never lets magus reach its summary, so
// only what it already streamed survives; a watchdog that chattered would be
// scrolled past.
//
// A total or sample of 0 means UNKNOWN and Watch returns rather than guessing.
func Watch(ctx context.Context, total int64, sample Sampler, emit func(string)) {
	watch(ctx, total, sample, emit, sampleEvery)
}

// watch is Watch with the interval injected, so its tests drive the ticker
// instead of waiting on it.
func watch(ctx context.Context, total int64, sample Sampler, emit func(string), every time.Duration) {
	if total <= 0 || sample == nil || emit == nil {
		return
	}
	threshold := total / warnBelowFraction
	floor := int64(-1) // -1: nothing reported yet

	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		avail := sample()
		if avail <= 0 {
			continue
		}
		// Recovered: forget the old floor. Without this reset the gate below is
		// measured against a historical minimum forever, so a run that dips, comes
		// back, then collapses again reports only the first dip - and the second is
		// the one that kills the machine.
		if avail >= threshold {
			floor = -1
			continue
		}
		if floor >= 0 && avail > floor-reWarnDropBytes {
			continue
		}
		floor = avail
		emit(fmt.Sprintf(
			"memory headroom low: %dMB available of %dMB total; a target here is close to taking the machine down",
			avail>>20, total>>20))
	}
}
