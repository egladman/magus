package cache

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// nodeKeySep is a control byte that cannot appear in a filesystem path, making DepKey unambiguous.
const nodeKeySep = "\x01"

// DepKey returns the scheduling identity of a (project, target) node.
// Empty target → bare project path (backward-compatible with DependsOn-as-path callers).
func DepKey(project, target string) string {
	if target == "" {
		return project
	}
	return project + nodeKeySep + target
}

// stepKey is the node key for s itself.
func stepKey(s Step) string { return DepKey(s.ProjectPath, s.Target) }

// formatCycle renders a node-key cycle for a human. The keys carry nodeKeySep, a raw
// control byte, so printing them with %v puts an unprintable character in a user-facing
// error; this spells the separator out and arrows the hops in the order they close.
func formatCycle(cycle []string) string {
	hops := make([]string, len(cycle))
	for i, k := range cycle {
		hops[i] = DisplayNodeKey(k)
	}
	return strings.Join(hops, " -> ")
}

// DisplayNodeKey renders one node key for a human, spelling out the control byte DepKey
// joins on. Every user-facing message naming a node goes through here; exported because
// DepKey is, so a caller holding a key can print it.
func DisplayNodeKey(key string) string { return strings.Replace(key, nodeKeySep, " ", 1) }

// depBarrier gates RunAll goroutines on inter-step completion. One entry per node
// key; dependents block in waitForDeps until markDone closes its channel.
// Out-of-scope edges (no entry) are skipped, never blocked on.
// Requires an acyclic graph: checkAcyclic must be called before launching goroutines.
type depBarrier struct {
	done map[string]*barrierEntry
	// members holds one entry per (member, step running it) pair a step Releases, so a
	// reader waits out each copy of a member separately. A member two steps run is two
	// entries: waiting on one says nothing about the other, and a reader that runs the
	// member itself waits on the other steps' copies without waiting on its own.
	members map[MemberWait]*barrierEntry
}

// MemberWait is one step's run of a chain member: what a reader waits on when the
// writer it overlaps is a member of another step's chain rather than that step's own
// target. See Step.RunAfterMembers.
type MemberWait struct {
	// Member is the member's node key (DepKey); StepKey is the key of the step running
	// it, spelled out because a bare Step beside cache.Step reads as that type.
	Member, StepKey string
}

type barrierEntry struct {
	ch   chan struct{}
	once sync.Once
	// err is the upstream's own result, set before ch closes. Safe to read once <-ch
	// unblocks: close happens-before any receive that observes it (Go memory model),
	// and err is written strictly before the close in the same once.Do.
	err error
}

// newDepBarrier builds a barrier with one entry per distinct node key.
// The map is immutable after construction; per-entry close is serialized by sync.Once.
func newDepBarrier(steps []Step) *depBarrier {
	done := make(map[string]*barrierEntry, len(steps))
	for _, s := range steps {
		k := stepKey(s)
		if _, ok := done[k]; !ok {
			done[k] = &barrierEntry{ch: make(chan struct{})}
		}
	}
	members := map[MemberWait]*barrierEntry{}
	for _, s := range steps {
		for _, k := range s.Releases {
			members[MemberWait{Member: k, StepKey: stepKey(s)}] = &barrierEntry{ch: make(chan struct{})}
		}
	}
	return &depBarrier{done: done, members: members}
}

// release opens one step's run of a member, recording the verdict its readers inherit:
// the member's own error, nil once the caller has decided the failure does not speak for
// the bytes (an advisory member fails without failing its step), or the step's verdict
// when the step itself is what ended, which carries "your writer never got there".
//
// A reader released with a non-nil err fails instead of running, which is the same
// contract a step-level upstream has. Releasing a failed member as a success would let
// that reader run against half-written bytes and CACHE the result, and nothing would
// cancel it: a batch tolerates failures by default (WithMaxFailures).
func (b *depBarrier) release(w MemberWait, err error) {
	e, ok := b.members[w]
	if !ok {
		return
	}
	e.once.Do(func() {
		e.err = err
		close(e.ch)
	})
}

type barrierCtxKey struct{}

// releaser is the barrier a step reports its finished members to, and which step is
// reporting. Both halves come from RunAll; a nested run overrides the pair together.
type releaser struct {
	barrier *depBarrier
	step    string
}

// withReleaser carries the barrier to the targets step runs, for ReleaseTarget.
func withReleaser(ctx context.Context, b *depBarrier, step string) context.Context {
	return context.WithValue(ctx, barrierCtxKey{}, releaser{barrier: b, step: step})
}

// ReleaseMember reports that the chain member named by key (a DepKey) finished inside
// the running batch step, so a step whose RunAfterMembers names it may start before the
// step running it ends. err is what its readers inherit: the member's own error, or nil
// when the caller has decided that failure does not speak for the bytes (see release).
//
// Safe to call for any member, from any goroutine, any number of times: a member no step
// Releases is ignored, as is a ctx outside RunAll, and the first call wins.
func ReleaseMember(ctx context.Context, key string, err error) {
	if r, ok := ctx.Value(barrierCtxKey{}).(releaser); ok {
		r.barrier.release(MemberWait{Member: key, StepKey: r.step}, err)
	}
}

// markDone signals completion for key, unblocking its dependents, and records the
// step's own result so a waiting dependent can tell success from failure. Idempotent.
func (b *depBarrier) markDone(key string, err error) {
	e, ok := b.done[key]
	if !ok {
		return
	}
	e.once.Do(func() {
		e.err = err
		close(e.ch)
	})
}

// waitForDeps blocks until all in-scope DependsOn (same-target), RunAfter (exact-key)
// and RunAfterMembers (one step's run of a chain member) upstreams have reported, or ctx
// is cancelled, and fails a dependent whose upstream itself
// failed, even if ctx has not observed that cancellation yet.
//
// Checking e.err rather than only ctx.Done() closes a real race: markDone runs as a
// defer inside the SAME goroutine errgroup wraps, so it fires before that goroutine
// returns to errgroup's own wrapper, which is what cancels the shared ctx. A
// dependent's select can see e.ch already closed while ctx is still live, and used to
// read that as "upstream done, proceed", running its own fn after a dependency it
// depends on had already failed.
func (b *depBarrier) waitForDeps(ctx context.Context, s Step) error {
	self := stepKey(s)
	wait := func(key string, e *barrierEntry, ok bool) error {
		if !ok {
			return nil
		}
		// Probe the upstream before blocking, so a settled one always wins over a
		// cancelled ctx. Both can be ready at once (the upstream failed AND a sibling
		// already cancelled the group), and select picks uniformly at random among
		// ready cases, which would surface the specific "dependency X failed" error
		// this function exists to produce only about half the time.
		select {
		case <-e.ch:
		default:
			if err := waitForUpstream(ctx, e.ch, stepKey(s), key); err != nil {
				return err
			}
		}
		if e.err != nil {
			return fmt.Errorf("cache: RunAll: dependency %s failed: %w", DisplayNodeKey(key), e.err)
		}
		return nil
	}
	step := func(key string) error {
		if key == self {
			return nil
		}
		e, ok := b.done[key]
		return wait(key, e, ok)
	}
	for _, d := range s.DependsOn {
		if err := step(DepKey(d, s.Target)); err != nil {
			return err
		}
	}
	for _, k := range s.RunAfter {
		if err := step(k); err != nil {
			return err
		}
	}
	for _, w := range s.RunAfterMembers {
		// This step's own run of the member is its own body's business: waiting on it
		// would be waiting on itself, and that overlap is the same-step question.
		if w.StepKey == self {
			continue
		}
		e, ok := b.members[w]
		if !ok {
			// The step running it never declared the release, so nothing will open this
			// key. Waiting out that whole step is what the edge meant before it was
			// narrowed, which makes a derivation that emits half the pair slower rather
			// than unordered; skipping instead would drop the edge silently.
			if err := step(w.StepKey); err != nil {
				return err
			}
			continue
		}
		if err := wait(w.Member, e, ok); err != nil {
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
// lock held and nothing running: the barrier's heartbeats outlived the deadlock.
func waitForUpstream(ctx context.Context, done <-chan struct{}, waiting, upstream string) error {
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

// displayNodeLabel spells a node key the way stepLabel spells a step, so a wait names its
// parties the way the lines around it name them.
func displayNodeLabel(key string) string {
	project, target, ok := strings.Cut(key, nodeKeySep)
	if !ok {
		return displayProject(key)
	}
	return displayProject(project) + " " + target
}

// upstreamWaitHeartbeat is how often a step waiting on its upstream says so. It matches
// the keyed lock's cadence: both are waits a step spends holding nothing, and a reader
// meeting one in a log should not have to learn two rhythms.
var upstreamWaitHeartbeat = lockWaitHeartbeat

// checkAcyclic reports an error if in-scope DependsOn or RunAfter edges form a cycle, using
// three-color DFS. A batch that passes this check cannot deadlock the barrier.
func checkAcyclic(steps []Step) error {
	inScope := make(map[string]bool, len(steps))
	for _, s := range steps {
		inScope[stepKey(s)] = true
	}
	adj := make(map[string][]string, len(steps))
	for _, s := range steps {
		self := stepKey(s)
		add := func(dep string) {
			if dep == self || !inScope[dep] {
				return
			}
			adj[self] = append(adj[self], dep)
		}
		for _, d := range s.DependsOn {
			add(DepKey(d, s.Target))
		}
		for _, k := range s.RunAfter {
			add(k)
		}
		// A member opens no earlier than the step running it starts, so waiting on one
		// is a wait on that step for deadlock purposes. A step waiting on its own run of
		// a member does not wait at all (see waitForDeps).
		for _, w := range s.RunAfterMembers {
			add(w.StepKey)
		}
	}

	const (
		white = iota
		grey
		black
	)
	color := make(map[string]int, len(steps))
	var stack []string

	var visit func(n string) []string
	visit = func(n string) []string {
		color[n] = grey
		stack = append(stack, n)
		for _, m := range adj[n] {
			switch color[m] {
			case grey:
				for i, p := range stack {
					if p == m {
						return append(append([]string(nil), stack[i:]...), m)
					}
				}
				return append(append([]string(nil), stack...), m)
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
		k := stepKey(s)
		if color[k] != white {
			continue
		}
		if cyc := visit(k); cyc != nil {
			return fmt.Errorf("cache: RunAll: dependency cycle: %s", formatCycle(cyc))
		}
	}
	return nil
}
