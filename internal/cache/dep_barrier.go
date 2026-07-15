package cache

import (
	"context"
	"fmt"
	"sync"
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

// depBarrier gates RunAll goroutines on inter-step completion. One entry per node
// key; dependents block in waitForDeps until markDone closes its channel.
// Out-of-scope edges (no entry) are skipped, never blocked on.
// Requires an acyclic graph: checkAcyclic must be called before launching goroutines.
type depBarrier struct {
	done map[string]*barrierEntry
}

type barrierEntry struct {
	ch   chan struct{}
	once sync.Once
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
	return &depBarrier{done: done}
}

// markDone signals completion for key, unblocking its dependents. Idempotent.
func (b *depBarrier) markDone(key string) {
	e, ok := b.done[key]
	if !ok {
		return
	}
	e.once.Do(func() { close(e.ch) })
}

// waitForDeps blocks until all in-scope DependsOn (same-target) upstreams have
// markDone'd, or ctx is cancelled.
func (b *depBarrier) waitForDeps(ctx context.Context, s Step) error {
	self := stepKey(s)
	wait := func(key string) error {
		if key == self {
			return nil
		}
		e, ok := b.done[key]
		if !ok {
			return nil
		}
		select {
		case <-e.ch:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for _, d := range s.DependsOn {
		if err := wait(DepKey(d, s.Target)); err != nil {
			return err
		}
	}
	return nil
}

// checkAcyclic reports an error if in-scope DependsOn edges form a cycle, using
// three-colour DFS. A batch that passes this check cannot deadlock the barrier.
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
			return fmt.Errorf("cache: RunAll: dependency cycle: %v", cyc)
		}
	}
	return nil
}
