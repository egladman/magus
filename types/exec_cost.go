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
	// series holds each exec's samples, offset from the step's start.
	series [][]MemorySample
	cpuMs  int64
}

// WithExecCost returns a context that collects the memory curve, the peak resident
// memory and the CPU time of every process executed under it, timed from now.
// Install it once per unit of work you want figures for; nested installs are
// independent, so an inner one does not feed its outer.
func WithExecCost(ctx context.Context) context.Context {
	return context.WithValue(ctx, execCostKey{}, &execCost{start: time.Now(), now: time.Now})
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

// RecordCurve adds one exec's sampled series. start is when the exec began and
// each sample's At is its offset from that. A no-op without a collector or
// without samples.
func RecordCurve(ctx context.Context, start time.Time, samples []MemorySample) {
	if len(samples) == 0 {
		return
	}
	c, ok := ctx.Value(execCostKey{}).(*execCost)
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	shift := start.Sub(c.start)
	series := make([]MemorySample, len(samples))
	for i, s := range samples {
		series[i] = MemorySample{At: shift + s.At, Bytes: s.Bytes}
	}
	c.series = append(c.series, series)
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
		for _, s := range series {
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
