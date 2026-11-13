package steprule

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

// Set is the step rules one load of a root magusfile registered, by kind.
// It lives outside internal/workspace, which does not import types/agent.
// The zero value holds none. Safe for concurrent use.
type Set struct {
	mu    sync.Mutex
	rules map[string]Rule
}

// Set records rule for kind, replacing one an earlier evaluation of the
// same magusfile recorded. The binding refuses a kind registered twice in
// one load.
func (s *Set) Set(kind string, rule Rule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rules == nil {
		s.rules = map[string]Rule{}
	}
	s.rules[kind] = rule
}

// Rule returns the rule for kind, nil when none is registered or s is nil.
func (s *Set) Rule(kind string) Rule {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rules[kind]
}

// Kinds returns every kind a rule is registered for, sorted; nil for none
// or a nil s.
func (s *Set) Kinds() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.rules) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(s.rules))
}

type setKey struct{}

// WithSet returns ctx carrying s, the set a magusfile's magus\guard.step
// registers into.
func WithSet(ctx context.Context, s *Set) context.Context {
	return context.WithValue(ctx, setKey{}, s)
}

// FromContext returns the set on ctx, or nil.
func FromContext(ctx context.Context) *Set {
	s, _ := ctx.Value(setKey{}).(*Set)
	return s
}
