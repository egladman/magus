package cache

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/egladman/magus/types"
)

// stepRef is the scheduling identity of s itself.
func stepRef(s Step) types.TargetRef {
	return types.TargetRef{Project: s.ProjectPath, Target: s.Target}
}

// formatCycle renders a cycle of steps for a human, arrowing the hops in the order they
// close.
func formatCycle(cycle []types.TargetRef) string {
	hops := make([]string, len(cycle))
	for i, ref := range cycle {
		hops[i] = DisplayRef(ref)
	}
	return strings.Join(hops, " -> ")
}

// upstreamRuns holds one TargetRun per step of a RunAll batch, which is what a step waits
// on before it starts: the runs of its upstreams (DependsOn and RunAfter). Out-of-scope
// upstreams (no run) are skipped, never waited on. It is not a TargetRuns: every step here
// is known before any starts and has one owner, so nothing races to claim a run, and the
// order comes from checkAcyclic, which must pass before any goroutine launches.
type upstreamRuns struct {
	done map[types.TargetRef]*TargetRun
}

// newUpstreamRuns builds one run per distinct step. The map is immutable after
// construction.
func newUpstreamRuns(steps []Step) *upstreamRuns {
	done := make(map[types.TargetRef]*TargetRun, len(steps))
	for _, s := range steps {
		if ref := stepRef(s); done[ref] == nil {
			done[ref] = &TargetRun{done: make(chan struct{})}
		}
	}
	return &upstreamRuns{done: done}
}

// complete finishes ref's run with the step's own result, releasing its dependents and
// letting them tell success from failure. Idempotent.
func (b *upstreamRuns) complete(ref types.TargetRef, err error) {
	if run, ok := b.done[ref]; ok {
		run.Complete(err)
	}
}

// waitForUpstreams blocks until all in-scope DependsOn (same-target) and RunAfter upstreams
// have completed, or ctx is cancelled, and fails a dependent whose upstream itself failed,
// even if ctx has not observed that cancellation yet.
//
// Checking the upstream's error rather than only ctx.Done() closes a real race: complete
// runs as a defer inside the SAME goroutine errgroup wraps, so it fires before that
// goroutine returns to errgroup's own wrapper, which is what cancels the shared ctx. A
// dependent's select can see the upstream done while ctx is still live, and used to read
// that as "upstream done, proceed", running its own fn after a dependency it depends on
// had already failed.
func (b *upstreamRuns) waitForUpstreams(ctx context.Context, s Step) error {
	self := stepRef(s)
	wait := func(ref types.TargetRef) error {
		if ref == self {
			return nil
		}
		run, ok := b.done[ref]
		if !ok {
			return nil
		}
		// Probe the upstream before blocking, so a settled one always wins over a
		// cancelled ctx. Both can be ready at once (the upstream failed AND a sibling
		// already cancelled the group), and select picks uniformly at random among
		// ready cases, which would surface the specific "dependency X failed" error
		// this function exists to produce only about half the time.
		select {
		case <-run.done:
		default:
			if err := waitForUpstream(ctx, run.done, self, ref); err != nil {
				return err
			}
		}
		if run.err != nil {
			return fmt.Errorf("cache: RunAll: dependency %s failed: %w", DisplayRef(ref), run.err)
		}
		return nil
	}
	for _, d := range s.DependsOn {
		if err := wait(types.TargetRef{Project: d, Target: s.Target}); err != nil {
			return err
		}
	}
	for _, ref := range s.RunAfter {
		if err := wait(ref); err != nil {
			return err
		}
	}
	return nil
}

// waitForUpstream blocks on done, naming both parties on the way.
//
// This is where a reader waits for the writer the derived order put ahead of it, and that
// wait can legitimately be as long as the writer's whole run. Unattributable, an aborted
// run read as silence; named, it reads as one target waiting on another. The log hears
// the first beat, then one per doubling of the elapsed time, because several readers
// waiting out one long writer otherwise print the same line every beat each.
//
// It deliberately does NOT beat the invocation heartbeat, unlike the keyed lock and the
// machine gate. What those wait for is outside this invocation, so nothing else would
// report liveness; what this waits for is a step of this same run, which beats for itself
// while it works. Beating here told the watchdog the run was fine because something was
// waiting, which is how a gate wedged for 19 minutes on 2026-09-11 with every project
// lock held and nothing running: the upstream wait's heartbeats outlived the deadlock.
func waitForUpstream(ctx context.Context, done <-chan struct{}, waiting, upstream types.TargetRef) error {
	beat := time.NewTicker(upstreamWaitHeartbeat)
	defer beat.Stop()
	started := time.Now()
	next := upstreamWaitHeartbeat
	for {
		select {
		case <-done:
			return nil
		case <-beat.C:
			if elapsed := time.Since(started); elapsed >= next {
				next *= 2
				slog.InfoContext(ctx, fmt.Sprintf("magus: %s is waiting for %s to finish (%s so far)",
					displayNodeLabel(waiting), displayNodeLabel(upstream), elapsed.Round(time.Second)))
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// DisplayRef spells ref for a message naming it inline: "project target", or the bare
// project for a project-level node.
func DisplayRef(ref types.TargetRef) string {
	if ref.Target == "" {
		return ref.Project
	}
	return ref.Project + " " + ref.Target
}

// displayNodeLabel spells a step the way stepLabel does, so a wait names its parties the
// way the lines around it name them.
func displayNodeLabel(ref types.TargetRef) string {
	if ref.Target == "" {
		return displayProject(ref.Project)
	}
	return displayProject(ref.Project) + " " + ref.Target
}

// upstreamWaitHeartbeat is how often a step waiting on its upstream says so. It matches
// the keyed lock's cadence: both are waits a step spends holding nothing, and a reader
// meeting one in a log should not have to learn two rhythms.
var upstreamWaitHeartbeat = lockWaitHeartbeat

// checkAcyclic reports an error if in-scope DependsOn or RunAfter edges form a cycle, using
// three-color DFS. A batch that passes this check cannot deadlock on its upstream waits.
func checkAcyclic(steps []Step) error {
	inScope := make(map[types.TargetRef]bool, len(steps))
	for _, s := range steps {
		inScope[stepRef(s)] = true
	}
	adj := make(map[types.TargetRef][]types.TargetRef, len(steps))
	for _, s := range steps {
		self := stepRef(s)
		add := func(dep types.TargetRef) {
			if dep == self || !inScope[dep] {
				return
			}
			adj[self] = append(adj[self], dep)
		}
		for _, d := range s.DependsOn {
			add(types.TargetRef{Project: d, Target: s.Target})
		}
		for _, ref := range s.RunAfter {
			add(ref)
		}
	}

	const (
		white = iota
		grey
		black
	)
	color := make(map[types.TargetRef]int, len(steps))
	var stack []types.TargetRef

	var visit func(n types.TargetRef) []types.TargetRef
	visit = func(n types.TargetRef) []types.TargetRef {
		color[n] = grey
		stack = append(stack, n)
		for _, m := range adj[n] {
			switch color[m] {
			case grey:
				for i, p := range stack {
					if p == m {
						return append(append([]types.TargetRef(nil), stack[i:]...), m)
					}
				}
				return append(append([]types.TargetRef(nil), stack...), m)
			case white:
				if cyc := visit(m); cyc != nil {
					return cyc
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
		return nil
	}

	for _, s := range steps {
		ref := stepRef(s)
		if color[ref] != white {
			continue
		}
		if cyc := visit(ref); cyc != nil {
			return fmt.Errorf("cache: RunAll: dependency cycle: %s", formatCycle(cyc))
		}
	}
	return nil
}
