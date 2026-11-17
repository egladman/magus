package model

import (
	"context"
	"sync"

	"github.com/egladman/magus/libs/safetyscissors/tier"
)

// TierState is the model-tier declaration a workspace holds: the root
// magusfile's, none when it turned tiers off, or else the shipped default.
// The zero value holds no declaration and no default. Safe for concurrent use.
type TierState struct {
	mu  sync.Mutex
	set *tier.Set
	// declared tells a magusfile's nil (tiers off) apart from no
	// declaration, which leaves the tiers to build.
	declared bool
	build    func() (*tier.Set, error)
}

// TiersOffError is [TierState.Tiers]'s answer when the state holds no declaration.
// TurnedOff is true when the magusfile turned tiers off, false when nothing set a
// default.
type TiersOffError struct {
	TurnedOff bool
}

func (e *TiersOffError) Error() string {
	if e.TurnedOff {
		return "model tiers are off: magus\\modelTiers(null) turned them off"
	}
	return "model tiers are off: nothing declared them and no default is set"
}

// Declare records a magusfile's declaration, replacing one an earlier
// evaluation of the same magusfile recorded. nil records that it turned
// model tiers off, so the default does not apply.
func (s *TierState) Declare(set *tier.Set) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.set, s.declared = set, true
}

// SetDefault records how to build the declaration a workspace gets when its
// magusfile declares none. Nothing builds it until [TierState.Tiers].
func (s *TierState) SetDefault(build func() (*tier.Set, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.build = build
}

// Tiers returns the declaration, or a [*TiersOffError] when tiers are off or nothing
// set a default. A default that builds is kept. One that fails returns its error, and
// the next call builds again.
func (s *TierState) Tiers() (*tier.Set, error) {
	s.mu.Lock()
	if s.declared || s.build == nil {
		defer s.mu.Unlock()
		if s.set == nil {
			return nil, &TiersOffError{TurnedOff: s.declared}
		}
		return s.set, nil
	}
	build := s.build
	s.mu.Unlock()

	// Outside the lock: the build loads a Buzz module, which must not wait
	// on this state. Racing callers build the same set; the first kept wins.
	set, err := build()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.declared {
		s.set, s.declared = set, true
	}
	if s.set == nil {
		return nil, &TiersOffError{TurnedOff: true}
	}
	return s.set, nil
}

type tierStateKey struct{}

// WithTierState returns ctx carrying s, the state a magusfile's
// magus\modelTiers declares into.
func WithTierState(ctx context.Context, s *TierState) context.Context {
	return context.WithValue(ctx, tierStateKey{}, s)
}

// TierStateFromContext returns the state on ctx, or nil.
func TierStateFromContext(ctx context.Context) *TierState {
	s, _ := ctx.Value(tierStateKey{}).(*TierState)
	return s
}
