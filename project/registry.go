package project

import (
	"fmt"
	"slices"
	"sync"

	"github.com/egladman/magus/types"
)

// SpellRegistry is the metadata repository for registered spells.
type SpellRegistry struct {
	mu     sync.RWMutex
	items  []*types.Spell
	ensure func() // lazy-init hook; must be idempotent (typically sync.OnceFunc-wrapped)
}

// NewSpellRegistry returns an empty SpellRegistry.
func NewSpellRegistry() *SpellRegistry { return &SpellRegistry{} }

var defaultRegistry = NewSpellRegistry()

// DefaultSpellRegistry returns the process-level spell registry singleton.
func DefaultSpellRegistry() *SpellRegistry { return defaultRegistry }

// SetEnsureHook installs an idempotent hook called before each registry read; nil clears it.
func (r *SpellRegistry) SetEnsureHook(fn func()) {
	r.mu.Lock()
	r.ensure = fn
	r.mu.Unlock()
}

func (r *SpellRegistry) runEnsure() {
	r.mu.RLock()
	fn := r.ensure
	r.mu.RUnlock()
	if fn != nil {
		fn()
	}
}

// RegisterSpell adds s to the registry; panics on nil or duplicate name.
func (r *SpellRegistry) RegisterSpell(s *types.Spell) {
	if s == nil {
		panic("magus/project: SpellRegistry.RegisterSpell called with nil spell")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.items {
		if existing.Name() == s.Name() {
			panic(fmt.Sprintf("magus/project: duplicate spell registration %q", s.Name()))
		}
	}
	r.items = append(r.items, s)
}

// RegisterIfAbsent registers s and returns it, or — if a spell of the same name
// is already registered — returns the existing one without adding a duplicate.
// The ensure-read, name check, and insert happen as one critical section, so
// concurrent callers that load the same spell (parallel magusfile evaluation,
// remote-cache backend resolution and an `import` racing for one spell) settle on
// a single registration instead of racing into RegisterSpell's duplicate panic.
func (r *SpellRegistry) RegisterIfAbsent(s *types.Spell) *types.Spell {
	if s == nil {
		panic("magus/project: SpellRegistry.RegisterIfAbsent called with nil spell")
	}
	r.runEnsure()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.items {
		if existing.Name() == s.Name() {
			return existing
		}
	}
	r.items = append(r.items, s)
	return s
}

// UnregisterSpell removes the named spell; no-ops if not found.
func (r *SpellRegistry) UnregisterSpell(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = slices.DeleteFunc(r.items, func(s *types.Spell) bool {
		return s.Name() == name
	})
}

// All returns a snapshot of every registered spell.
func (r *SpellRegistry) All() []*types.Spell {
	r.runEnsure()
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*types.Spell, len(r.items))
	copy(out, r.items)
	return out
}

// Lookup returns the named spell, or (nil, false) if not found.
func (r *SpellRegistry) Lookup(name string) (*types.Spell, bool) {
	r.runEnsure()
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.items {
		if s.Name() == name {
			return s, true
		}
	}
	return nil, false
}
