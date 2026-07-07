package ward

import (
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// SpellShadows is the resolution-time sibling of Check: where Check guards a single
// op's argv against its kind, this guards the workspace's spell layout against a
// shadow footgun. Spell imports resolve root-wins (a spells/<name> higher in the
// tree is canonical), so a same-named spell in a nested project is never used. This
// ward blocks that dead definition (MGS1002) unless the author acknowledges it.
//
// acknowledged reports whether a shadow of the given import path is deliberate (the
// magus.yaml spells.allow_shadow list, keyed by import path with a required reason).
// An acknowledged shadow is trusted and produces no diagnostic. Results are one
// diagnostic per unacknowledged shadow, in the scan's stable order.
func SpellShadows(root string, acknowledged func(importPath string) bool) ([]*types.DiagnosticError, error) {
	conflicts, err := project.SpellShadows(root)
	if err != nil {
		return nil, err
	}
	var out []*types.DiagnosticError
	for _, c := range conflicts {
		if acknowledged != nil && acknowledged(c.Import) {
			continue
		}
		out = append(out, types.DiagnosticErrorf(types.SpellShadowed,
			"spell import %q is defined at %s but shadowed by %s: imports resolve "+
				"root-wins, so the deeper spell is dead. Move or rename it, or acknowledge "+
				"the shadow in magus.yaml (spells.allow_shadow) with a reason.",
			c.Import, c.Shadowed, c.Winner))
	}
	return out, nil
}
