package mem

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

	// LowHeadroomDivisor sets the threshold: the watchdog talks below total/8. Above
	// it a build is using the machine, which is what a build is for.
	//
	// This is the OBSERVED half and it only ever warns. What magus queues or refuses on
	// is the DECLARED half, cache.MachineBudget, so the same command reaches the same
	// verdict whatever else the machine happens to be doing.
	LowHeadroomDivisor = 8

	// reWarnDropBytes is how much further the floor must fall before saying so
	// again. Without it a tight run emits a line every 2 seconds and its reader
	// learns to scroll past.
	reWarnDropBytes = 250 << 20

	// swapGrowthBytes is how much swap this run must have added before the watchdog
	// says so, and how much further before it says so again.
	//
	// GROWTH, not a level: a steady few gigabytes is ordinary on a machine up for
	// weeks and says nothing about the run.
	swapGrowthBytes = 512 << 20
)

// Reading is one sample of the host's memory, as the watchdog saw it.
//
// SwapUsedBytes is 0 both on a platform that cannot report it and on a machine with
// swap disabled; treat it as UNKNOWN, not as "nothing is swapped".
type Reading struct {
	AvailableBytes int64
	TotalBytes     int64
	SwapUsedBytes  int64
	// SwapGrowthBytes is the rise since the run's first sample, which is what
	// attributes swap to THIS run.
	SwapGrowthBytes int64
	// SwapTriggered reports that swap growth, not falling headroom, is why this was
	// reported. Word the warning from this rather than from SwapGrowthBytes being
	// non-zero: a headroom warning routinely carries a few megabytes of drift.
	SwapTriggered bool
}

// Watch reports memory trouble until ctx is done, calling report when headroom
// first falls below an eighth of total, on every further 250MB drop, and whenever
// this run has pushed another 512MB into swap. It reads the host itself, and
// returns at once when the host cannot be measured.
//
// Silent on a healthy run. A killed runner never lets magus reach its summary, so
// only what it already streamed survives; a watchdog that chattered would be
// scrolled past.
//
// report takes the whole reading rather than a sentence, so the caller owns the
// wording and whatever else the warning carries.
func Watch(ctx context.Context, report func(Reading)) {
	watch(ctx, UsableBytes(ctx), AvailableBytes, SwapUsedBytes, report, sampleEvery)
}

// watch is Watch with the host readings and the interval injected, so its tests
// drive both instead of waiting on a machine under real memory pressure.
func watch(ctx context.Context, total int64, sample, swap func(context.Context) int64, report func(Reading), every time.Duration) {
	if total <= 0 || sample == nil || report == nil {
		return
	}
	threshold := total / LowHeadroomDivisor
	floor := int64(-1)    // -1: nothing reported yet
	swapBase := int64(-1) // -1: no baseline sampled yet
	swapMark := int64(-1) // -1: no swap growth reported yet

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
		var swapUsed int64
		if swap != nil {
			swapUsed = swap(ctx)
		}
		// The first reading is the baseline, INCLUDING a zero one. Waiting for a
		// non-zero reading would make the first swap the run caused the baseline, so a
		// clean machine driven into the ground would measure as no growth at all.
		if swapBase < 0 {
			swapBase = swapUsed
		}
		growth := int64(0)
		if swapBase >= 0 && swapUsed > swapBase {
			growth = swapUsed - swapBase
		}
		swapTriggered := growth >= swapGrowthBytes && (swapMark < 0 || growth >= swapMark+swapGrowthBytes)
		if swapTriggered {
			swapMark = growth
		}

		// Maintained on EVERY tick, including one the swap trigger fired on, or the
		// next headroom warning is measured against a stale baseline. Recovery resets
		// it: without that a run that dips, recovers, then collapses reports only the
		// first dip, and the second is the one that kills the machine.
		headroomTriggered := false
		switch {
		case avail >= threshold:
			floor = -1
		case floor < 0 || avail <= floor-reWarnDropBytes:
			floor = avail
			headroomTriggered = true
		}

		if swapTriggered || headroomTriggered {
			report(Reading{
				AvailableBytes: avail, TotalBytes: total,
				SwapUsedBytes: swapUsed, SwapGrowthBytes: growth,
				SwapTriggered: swapTriggered,
			})
		}
	}
}
