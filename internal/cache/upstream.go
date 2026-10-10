package cache

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/types"
)

func stepRef(s Step) types.TargetRef {
	return types.TargetRef{Project: s.ProjectPath, Target: s.Target}
}

func formatCycle(cycle []types.TargetRef) string {
	hops := make([]string, len(cycle))
	for i, ref := range cycle {
		hops[i] = DisplayRef(ref)
	}
	return strings.Join(hops, " -> ")
}

// upstreamRuns holds one TargetRun per step of a RunAll batch; a step waits on its
// upstreams' runs before it starts. Unlike TargetRuns nothing races to claim a run: every
// step is known up front, and checkAcyclic passes before any goroutine launches.
type upstreamRuns struct {
	done map[types.TargetRef]*TargetRun
}

// newUpstreamRuns builds one run per distinct step. The map is never written after, so
// reads take no lock.
func newUpstreamRuns(steps []Step) *upstreamRuns {
	done := make(map[types.TargetRef]*TargetRun, len(steps))
	for _, s := range steps {
		if ref := stepRef(s); done[ref] == nil {
			done[ref] = &TargetRun{done: make(chan struct{})}
		}
	}
	return &upstreamRuns{done: done}
}

// complete records the step's result and releases its dependents. Idempotent.
func (b *upstreamRuns) complete(ref types.TargetRef, err error) {
	if run, ok := b.done[ref]; ok {
		run.Complete(err)
	}
}

// waitForUpstreams blocks until s's in-scope upstreams complete or ctx ends, and fails if
// an upstream failed. It reads the upstream's error, not only ctx: complete runs before
// errgroup cancels ctx, so a dependent can see a failed upstream done while ctx is live.
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
		// Probe first: with a failed upstream and a cancelled ctx both ready, select
		// picks at random and would hide the "dependency failed" error half the time.
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

// waitForUpstream blocks on done, logging who waits on whom once the wait reaches
// waitFirstNotice and then at each doubling of the elapsed time.
//
// Unlike the keyed lock and the machine gate it does not beat the invocation heartbeat:
// the upstream is a step of this run and beats for itself, and beating here would keep
// the watchdog quiet through a deadlock.
func waitForUpstream(ctx context.Context, done <-chan struct{}, waiting, upstream types.TargetRef) error {
	beat := time.NewTicker(upstreamWaitHeartbeat)
	defer beat.Stop()
	started := time.Now()
	next := waitFirstNotice
	for {
		select {
		case <-done:
			return nil
		case <-beat.C:
			if elapsed := time.Since(started); elapsed >= next {
				next *= 2
				slog.With(attr.Component("magus")).InfoContext(ctx, fmt.Sprintf("%s is waiting for %s to finish (%s so far)",
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

// displayNodeLabel spells a step the way stepLabel does.
func displayNodeLabel(ref types.TargetRef) string {
	if ref.Target == "" {
		return displayProject(ref.Project)
	}
	return displayProject(ref.Project) + " " + ref.Target
}

// upstreamWaitHeartbeat matches the keyed lock's cadence, so waits that hold nothing log
// at one rhythm.
var upstreamWaitHeartbeat = lockWaitHeartbeat

// checkAcyclic reports a cycle among in-scope DependsOn or RunAfter edges. A batch that
// passes cannot deadlock on its upstream waits.
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
