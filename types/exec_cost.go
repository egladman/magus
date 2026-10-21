package types

import (
	"context"
	"sync"
	"time"
)

// Peak resident memory and CPU time, collected per target execution.
//
// The exec layer already knows this number (the kernel counted it, and
// spawn.ExecResult.MaxRSSBytes reads it off the same ProcessState the exit code
// comes from), but the code that DECIDES what may run alongside what is the
// shard planner, several layers up, and nothing carried the number between
// them. This is that carrier, and it is deliberately the same shape as the
// return capture above it in this package: a context-scoped collector written
// by code far below the orchestrator and read once by the orchestrator.
//
// The aggregate ACROSS EXECS follows time. Each exec hands over its sampled series,
// and the step's curve is, per bin, the sum of each exec's largest sample in that
// bin: a compiler then a linker never add, because their samples land in different
// bins, while two commands the step runs at once do, because they held memory
// together. The step's peak is that curve's largest bin, floored by the largest
// kernel figure any one exec reported.
//
// What each exec contributes is a different question, and one this package used to
// get wrong. It is not one process: `go test` runs package binaries concurrently
// (-p defaults to GOMAXPROCS), each under -race carrying the race detector's
// shadow memory, and the kernel will not total them. wait4's ru_maxrss propagates
// up a reaped subtree as a MAXIMUM, so a driver that forked four concurrent 800MB
// children reports 801MB, measured on darwin. One instant of a real
// `go test -race ./internal/...` held 3.11GiB across 17 processes while the
// largest single one was 1.28GiB.
//
// spawn.ExecResult.MaxRSSBytes therefore folds the kernel's figure together with a
// sampled total of the live process tree, and reports the larger. A figure here is
// still a FLOOR, because sampling only sees the instants it looks at, but it no
// longer misses concurrency by construction.
//
// CPU time rides the same collector; see spawn.ExecResult.CPUMs for what each exec
// counts.

type execCostKey struct{}

type execCost struct {
	mu    sync.Mutex
	start time.Time
	now   func() time.Time
	// kernelPeak is the largest ru_maxrss any one exec reported: exact for that
	// process, so the step's peak never reads below it.
	kernelPeak int64
	series     []*execSeries
	cpuMs      int64
	// parent is the collector this one was installed under. Its listeners hear this
	// one's samples too, because a step's spell installs its own collector inside the
	// step's.
	parent    *execCost
	listeners []func(at time.Duration, bytes int64)
}

// execSeries is one exec's samples, offset from the step's start. live is false once
// the exec ended, so its last sample stops counting toward the live total.
type execSeries struct {
	samples []MemorySample
	live    bool
}

// WithExecCost returns a context that collects the memory curve, the peak resident
// memory and the CPU time of every process executed under it, timed from now.
// Install it once per unit of work you want figures for; nested installs are
// independent, so an inner one does not feed its outer's figures. An outer's
// OnRSSSample listeners do hear an inner one's samples.
func WithExecCost(ctx context.Context) context.Context {
	parent, _ := ctx.Value(execCostKey{}).(*execCost)
	return context.WithValue(ctx, execCostKey{}, &execCost{start: time.Now(), now: time.Now, parent: parent})
}

// OnRSSSample registers fn on the collector under ctx. fn is called as each sample
// arrives with its offset from the collector's start and the bytes the collector's
// live execs hold together, each counted at its latest sample. It runs on the
// sampler's goroutine, which every exec in the process shares, so fn must not block.
// A no-op without a collector.
func OnRSSSample(ctx context.Context, fn func(at time.Duration, bytes int64)) {
	c, ok := ctx.Value(execCostKey{}).(*execCost)
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.listeners = append(c.listeners, fn)
}

// RecordPeakRSS reports one process's kernel peak resident memory in bytes, the
// floor under PeakRSS. Calls with a non-positive value are ignored: the platforms
// that cannot report this (windows, wasm) and a process that never started both
// yield zero, and zero means UNKNOWN rather than "used nothing": a planner that
// averaged it in would read an unmeasurable target as a free one.
func RecordPeakRSS(ctx context.Context, bytes int64) {
	if bytes <= 0 {
		return
	}
	c, ok := ctx.Value(execCostKey{}).(*execCost)
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.kernelPeak = max(c.kernelPeak, bytes)
}

// RecordCurve opens one exec's series. start is when the exec began. record adds a
// sample as it is taken, its At the offset from start, and calls every OnRSSSample
// listener; end closes the series. Call record from one goroutine at a time and never
// after end. Both are no-ops without a collector.
func RecordCurve(ctx context.Context, start time.Time) (record func(MemorySample), end func()) {
	c, ok := ctx.Value(execCostKey{}).(*execCost)
	if !ok {
		return func(MemorySample) {}, func() {}
	}
	series := &execSeries{live: true}
	c.mu.Lock()
	c.series = append(c.series, series)
	c.mu.Unlock()
	shift := start.Sub(c.start)
	record = func(s MemorySample) {
		at := shift + s.At
		c.mu.Lock()
		series.samples = append(series.samples, MemorySample{At: at, Bytes: s.Bytes})
		var held int64
		for _, other := range c.series {
			if other.live && len(other.samples) > 0 {
				held += other.samples[len(other.samples)-1].Bytes
			}
		}
		c.mu.Unlock()
		c.notify(at, held)
	}
	end = func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		series.live = false
	}
	return record, end
}

// notify calls the listeners of c and of every collector above it, each with at
// shifted to that collector's start. Called without c.mu held, so a listener may
// read the collector.
func (c *execCost) notify(at time.Duration, held int64) {
	for p := c; p != nil; p = p.parent {
		p.mu.Lock()
		listeners := p.listeners
		p.mu.Unlock()
		for _, fn := range listeners {
			fn(at+c.start.Sub(p.start), held)
		}
	}
}

// Curve folds every series recorded under ctx into one curve over the step's
// elapsed time at the call: each bucket sums, across execs, each exec's largest
// sample in that bucket. A sample past the elapsed time lands in the last bucket.
// The zero curve means nothing was sampled.
func Curve(ctx context.Context) MemoryCurve {
	var out MemoryCurve
	c, ok := ctx.Value(execCostKey{}).(*execCost)
	if !ok {
		return out
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, bytes := range c.binSums() {
		out[i] = uint32((bytes + 1<<20 - 1) >> 20)
	}
	return out
}

// binSums returns the curve in bytes. Callers hold c.mu.
func (c *execCost) binSums() [MemoryCurveBuckets]int64 {
	var sums [MemoryCurveBuckets]int64
	width := max(c.now().Sub(c.start)/MemoryCurveBuckets, 1)
	for _, series := range c.series {
		var peaks [MemoryCurveBuckets]int64
		for _, s := range series.samples {
			bin := min(max(int(s.At/width), 0), MemoryCurveBuckets-1)
			peaks[bin] = max(peaks[bin], s.Bytes)
		}
		for i, p := range peaks {
			sums[i] += p
		}
	}
	return sums
}

// PeakRSS returns the step's peak resident memory in bytes: the largest bucket of
// its curve, floored by the largest RecordPeakRSS figure. 0 means no process
// reported one, so a second return value would distinguish nothing.
func PeakRSS(ctx context.Context) int64 {
	c, ok := ctx.Value(execCostKey{}).(*execCost)
	if !ok {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	peak := c.kernelPeak
	for _, bytes := range c.binSums() {
		peak = max(peak, bytes)
	}
	return peak
}

// RecordCPUMs adds one process's CPU time, user plus system, in milliseconds.
// Unlike the peak it sums: a compile then a link spent both, and so did two
// processes running at once. A non-positive value is ignored, like a peak.
func RecordCPUMs(ctx context.Context, ms int64) {
	if ms <= 0 {
		return
	}
	c, ok := ctx.Value(execCostKey{}).(*execCost)
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cpuMs += ms
}

// CPUMs returns the CPU milliseconds reported under ctx, summed, or 0 when no
// process reported any.
func CPUMs(ctx context.Context) int64 {
	c, ok := ctx.Value(execCostKey{}).(*execCost)
	if !ok {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cpuMs
}
