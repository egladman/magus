package types

import (
	"context"
	"fmt"
	"slices"

	"github.com/egladman/magus/spells"
)

// CheckNeedsReady runs the readiness probe of each tool target's needs_ready
// lists, through the first bound spell that declares one. Load refused a tool
// no bound spell probes, so reaching that case here is a bug in the caller.
func (p *Project) CheckNeedsReady(ctx context.Context, target string) error {
	for _, tool := range p.TargetPolicies[target].NeedsReady {
		i := slices.IndexFunc(p.ResolvedSpells, func(s *spells.Spell) bool { return s.ProbesReady(tool) })
		if i < 0 {
			return fmt.Errorf("%s: needs_ready names %s, which no bound spell probes", target, tool)
		}
		if err := p.ResolvedSpells[i].CheckReady(ctx, tool, p.Dir); err != nil {
			return err
		}
	}
	return nil
}
