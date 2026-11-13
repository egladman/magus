package steps

import (
	"context"
	"maps"
	"slices"
	"sync"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/agent"
)

// Rule is a workspace's magus\guard.step function for one kind. facts is the
// calling session's marker gate, where once and count keep their state.
type Rule func(ctx context.Context, req agent.StepRequest, facts hint.Gate) (types.GuardVerdict, error)

// Group is the step rules one load of a root magusfile registered, by kind.
// It lives outside internal/workspace, which does not import types/agent.
// The zero value holds none. Safe for concurrent use.
type Group struct {
	mu    sync.Mutex
	rules map[string]Rule
}

// Set records rule for kind, replacing one an earlier evaluation of the
// same magusfile recorded. The binding refuses a kind registered twice in
// one load.
func (g *Group) Set(kind string, rule Rule) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.rules == nil {
		g.rules = map[string]Rule{}
	}
	g.rules[kind] = rule
}

// Rule returns the rule for kind, nil when none is registered or g is nil.
func (g *Group) Rule(kind string) Rule {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.rules[kind]
}

// Kinds returns every kind a rule is registered for, sorted; nil for none
// or a nil g.
func (g *Group) Kinds() []string {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.rules) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(g.rules))
}

type groupKey struct{}

// WithGroup returns ctx carrying g, the group a magusfile's magus\guard.step
// registers into.
func WithGroup(ctx context.Context, g *Group) context.Context {
	return context.WithValue(ctx, groupKey{}, g)
}

// GroupFromContext returns the group on ctx, or nil.
func GroupFromContext(ctx context.Context) *Group {
	g, _ := ctx.Value(groupKey{}).(*Group)
	return g
}
