package types

import (
	"context"
	"fmt"
	"slices"

	"github.com/egladman/magus/spells"
)

// ReadyProbe returns the spell that probes tool for p: a spell p binds first,
// else the first of registered that declares a probe. A probe needs no binding,
// and binding a spell only for its probe would add its source globs to every
// target's cache key.
func (p *Project) ReadyProbe(tool string, registered []*spells.Spell) (*spells.Spell, bool) {
	for _, set := range [][]*spells.Spell{p.ResolvedSpells, registered} {
		if i := slices.IndexFunc(set, func(s *spells.Spell) bool { return s.ProbesReady(tool) }); i >= 0 {
			return set[i], true
		}
	}
	return nil, false
}

// CheckNeedsReady runs the readiness probe of each tool target's needs_ready
// lists, through the spell [Project.ReadyProbe] picks from registered. Load
// refused a tool no spell probes, so reaching that case here is a bug in the
// caller.
func (p *Project) CheckNeedsReady(ctx context.Context, target string, registered []*spells.Spell) error {
	for _, tool := range p.TargetPolicies[target].NeedsReady {
		s, ok := p.ReadyProbe(tool, registered)
		if !ok {
			return fmt.Errorf("%s: needs_ready names %s, which no spell probes", target, tool)
		}
		if err := s.CheckReady(ctx, tool, p.Dir); err != nil {
			return err
		}
	}
	return nil
}
