package hostmem

import (
	"context"
	"fmt"
	"time"
)

const (
	// sampleEvery is how often the watchdog reads the host. It is deliberately
	// short: this replaced a CI-side shell loop that sampled every 30s, and that
	// interval could not answer the question it existed for - a runner died 30
	// seconds after a sample reporting 13.8GB free, so the trace showed no
	// pressure while saying nothing about the window that killed it. A test
	// binary can allocate through a machine's memory in far less than that.
	//
	// Note the first sample lands one interval IN, not at zero, so a run shorter
	// than this is never sampled at all. That is deliberate: a sub-2s target has
	// not had time to grow into the machine, and sampling at t=0 would report the
	// memory the PREVIOUS command left behind as though this run caused it.
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
// It is SILENT on a healthy run, which is the point: this exists to leave a trace
// in a log that outlives the process. When a CI runner dies, magus never reaches
// the end of the invocation where it would print a summary, so anything buffered
// for later is lost - only what was already streamed survives. A watchdog that
// chattered would be scrolled past; one that says nothing until it matters is
// worth reading.
//
// A total or sample of 0 means UNKNOWN and Watch returns immediately rather than
// guessing: a platform magus cannot measure gets no warnings, not invented ones.
func Watch(ctx context.Context, total int64, sample Sampler, emit func(string)) {
	watch(ctx, total, sample, emit, sampleEvery)
}

// watch is Watch with the interval injected, so its tests drive the ticker
// instead of waiting on it - at the real 2s cadence the table below costs 34
// seconds of wall clock to assert pure arithmetic.
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
		if avail <= 0 || avail >= threshold {
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
