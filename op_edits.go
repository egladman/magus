package magus

import (
	"fmt"
	"slices"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/types"
)

// directOps returns the ops a run of target on p dispatches to with no magusfile body
// between, chosen as runTarget and makeSpellFilteredHandler choose them: under a
// spell::op filter the named spell's op, otherwise every spell's op of that name, unless
// a magusfile export shadows it (magusfileOverride). It returns nil when a body runs.
func directOps(p *types.Project, target, spell string) []spellOp {
	if spell == "" && magusfileOverride(p, p.ResolvedSpells, target) >= 0 {
		return nil
	}
	var out []spellOp
	for _, s := range p.ResolvedSpells {
		if s.Name() == types.MagusfileSpellName || (spell != "" && s.Name() != spell) {
			continue
		}
		if op, ok := s.Op(target); ok {
			out = append(out, spellOp{s, target, op})
		}
	}
	return out
}

// foldOpEdits adds the Edits of the ops step runs directly (directOps) to its Sources
// and Updates, rooted at p, and names those ops for MGS4007 (ADR 0053). Like
// ctx.writesFiles(..., editsExistingSource: true) the files key the step and are never
// snapshotted, replayed or cleaned. A target that composes the op inherits nothing: the
// op cannot know which of its edits the target's call leaves in force.
//
// It only adds, so filterStep folds a spell::op filter's dispatch over buildStep's
// unfiltered one. What a filter drops is an op of the same name in another spell, and
// keeping that op's edits over-invalidates, nothing worse.
func foldOpEdits(step *cache.Step, p *types.Project, target, spell string) {
	var edits []types.Glob
	for _, so := range directOps(p, target, spell) {
		step.Ops = appendMissing(step.Ops, []string{fmt.Sprintf("%s::%s", so.spell.Name(), so.name)})
		// Command.Validate rejected a malformed list at decode.
		globs, _ := types.ParseGlobs(so.op.Edits)
		for _, g := range globs {
			edits = append(edits, g.Root(p.Path))
		}
	}
	if len(edits) == 0 {
		return
	}
	step.Sources = types.CompactGlobs(slices.Concat(step.Sources, edits))
	step.Updates = types.CompactGlobs(slices.Concat(step.Updates, edits))
}

// filterStep applies the invocation's spell::op filter, empty on a plain target run.
// The filter selects which definition runs (an explicit op bypasses a shadowing
// magusfile export), so it keys the cache; without it a compile-only go::go-build
// recorded a pass the real go-build target then replayed around a stale binary. The op
// it selects is the step, so its edits fold in.
func filterStep(step *cache.Step, p *types.Project, target, spell string) {
	step.Spell = spell
	if spell != "" {
		foldOpEdits(step, p, target, spell)
	}
}
