package hostmem

import (
	"context"
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

	// warnBelowDivisor sets the threshold: the watchdog talks below total/8. Above
	// it a build is using the machine, which is what a build is for.
	warnBelowDivisor = 8

	// reWarnDropBytes is how much further the floor must fall before saying so
	// again. Without it a tight run emits a line every 2 seconds and its reader
	// learns to scroll past.
	reWarnDropBytes = 250 << 20
)

// Watch reports low memory until ctx is done, calling report when headroom first
// falls below an eighth of total and on every further 250MB drop. It reads the
// host itself, and returns at once when the host cannot be measured.
//
// Silent on a healthy run. A killed runner never lets magus reach its summary, so
// only what it already streamed survives; a watchdog that chattered would be
// scrolled past.
//
// report takes the two readings rather than a sentence, so the caller owns the
// wording and whatever else the warning carries.
func Watch(ctx context.Context, report func(available, total int64)) {
	watch(ctx, TotalBytes(ctx), AvailableBytes, report, sampleEvery)
}

// watch is Watch with the host readings and the interval injected, so its tests
// drive both instead of waiting on a machine under real memory pressure.
func watch(ctx context.Context, total int64, sample func(context.Context) int64, report func(available, total int64), every time.Duration) {
	if total <= 0 || sample == nil || report == nil {
		return
	}
	threshold := total / warnBelowDivisor
	floor := int64(-1) // -1: nothing reported yet

	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		avail := sample(ctx)
		if avail <= 0 {
			continue
		}
		// Recovered: forget the old floor. Without this reset the gate below is
		// measured against a historical minimum forever, so a run that dips, comes
		// back, then collapses again reports only the first dip, and the second is
		// the one that kills the machine.
		if avail >= threshold {
			floor = -1
			continue
		}
		if floor >= 0 && avail > floor-reWarnDropBytes {
			continue
		}
		floor = avail
		report(avail, total)
	}
}
