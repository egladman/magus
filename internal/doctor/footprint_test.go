package doctor

import (
	"testing"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSourceIsAlsoOutputFindsOnlyTheOverlap(t *testing.T) {
	r := &runner{}

	t.Run("a target reading its own output fails and names the path", func(t *testing.T) {
		got := r.checkSourceIsAlsoOutput([]*types.Project{{
			Path:          ".",
			TargetInputs:  map[string][]types.InputRef{"badge": {{Glob: "src/*.go"}, {Glob: "assets/badge.svg"}}},
			TargetOutputs: map[string][]types.OutputRef{"badge": {{Glob: "assets/badge.svg"}}},
		}})
		assert.Equal(t, types.CheckFail, got.Status)
		require.Len(t, got.Details, 1, "only the overlapping path is a finding, not every declared input")
		assert.Contains(t, got.Details[0], "assets/badge.svg")
		assert.NotContains(t, got.Details[0], "src/*.go")
	})

	t.Run("reading one target's output from a DIFFERENT target is fine", func(t *testing.T) {
		got := r.checkSourceIsAlsoOutput([]*types.Project{{
			Path:          ".",
			TargetInputs:  map[string][]types.InputRef{"lint": {{Glob: "assets/badge.svg"}}},
			TargetOutputs: map[string][]types.OutputRef{"badge": {{Glob: "assets/badge.svg"}}},
		}})
		assert.Equal(t, types.CheckOK, got.Status,
			"the ordinary producer/consumer pair, which ctx.needs sequences and the cache keys correctly")
	})

	t.Run("the same glob owned by different projects does not collide", func(t *testing.T) {
		got := r.checkSourceIsAlsoOutput([]*types.Project{{
			Path:          ".",
			TargetInputs:  map[string][]types.InputRef{"badge": {{Project: "docs", Glob: "assets/badge.svg"}}},
			TargetOutputs: map[string][]types.OutputRef{"badge": {{Project: ".", Glob: "assets/badge.svg"}}},
		}})
		assert.Equal(t, types.CheckOK, got.Status)
	})

	t.Run("a repeated declaration is reported once", func(t *testing.T) {
		got := r.checkSourceIsAlsoOutput([]*types.Project{{
			Path:          ".",
			TargetInputs:  map[string][]types.InputRef{"badge": {{Glob: "assets/badge.svg"}, {Glob: "assets/badge.svg"}}},
			TargetOutputs: map[string][]types.OutputRef{"badge": {{Glob: "assets/badge.svg"}}},
		}})
		assert.Len(t, got.Details, 1)
	})
}

// TestFootprintDropsOpGlobsFiresOnlyOnTotalOmission pins the line the check is worth
// having for. The real case it was written from is the one in the first subtest: three
// libraries narrowed `format` to the markdown dprint reads and kept calling go-fmt, so the
// Go tree left the key and a pure-Go edit replayed the formatter while the gate stayed
// green. Narrowing to one Go path is the opposite case and must stay silent, or the check
// reports every deliberate footprint in the workspace.
func TestFootprintDropsOpGlobsFiresOnlyOnTotalOmission(t *testing.T) {
	r := &runner{}
	goSpell := spells.NewSpell("go", spells.WithSources("**/*.go", "**/*.txtar", "go.mod", "go.sum"))

	project := func(globs []string, updates []string) *types.Project {
		p := &types.Project{
			Path:           ".",
			TargetInputs:   map[string][]types.InputRef{"format": {}},
			TargetSpellOps: map[string][]types.TargetSpellUse{"format": {{Spell: "go", Ops: []string{"go-fmt"}}}},
			ResolvedSpells: []*spells.Spell{goSpell},
		}
		for _, g := range globs {
			p.TargetInputs["format"] = append(p.TargetInputs["format"], types.InputRef{Glob: g})
		}
		for _, g := range updates {
			p.TargetUpdates = map[string][]types.UpdateRef{"format": append(p.TargetUpdates["format"], types.UpdateRef{Glob: g})}
		}
		return p
	}

	t.Run("a footprint naming no file the spell reads fails and names the op", func(t *testing.T) {
		got := r.checkFootprintDropsOpGlobs([]*types.Project{project([]string{"**/*.md", "dprint.json"}, nil)})
		assert.Equal(t, types.CheckFail, got.Status)
		require.Len(t, got.Details, 1)
		assert.Contains(t, got.Details[0], "go[go-fmt]")
	})

	t.Run("naming one Go path is a narrowing, not an omission", func(t *testing.T) {
		got := r.checkFootprintDropsOpGlobs([]*types.Project{project([]string{"internal/compress/*.go"}, nil)})
		assert.Equal(t, types.CheckOK, got.Status,
			"a target that narrows to the package it compiles has said what it reads")
	})

	t.Run("ctx.modifiesExistingFiles counts as naming them", func(t *testing.T) {
		got := r.checkFootprintDropsOpGlobs([]*types.Project{project([]string{"dprint.json"}, []string{"**/*.go", "go.mod"})})
		assert.Equal(t, types.CheckOK, got.Status,
			"gofmt amends rather than produces, so modifiesExistingFiles is the correct declaration and it folds into Sources")
	})

	t.Run("a literal path answers for the wildcard that would match it", func(t *testing.T) {
		got := r.checkFootprintDropsOpGlobs([]*types.Project{project([]string{"tapes/demo.txtar"}, nil)})
		assert.Equal(t, types.CheckOK, got.Status)
	})

	t.Run("a target that declared no footprint of its own is not asked", func(t *testing.T) {
		p := project(nil, nil)
		p.TargetInputs = nil
		got := r.checkFootprintDropsOpGlobs([]*types.Project{p})
		assert.Equal(t, types.CheckOK, got.Status,
			"without ctx.readsFiles the project baseline still keys the target, so there is nothing to drop")
	})

	t.Run("a footprint taking a whole tree is not an omission", func(t *testing.T) {
		got := r.checkFootprintDropsOpGlobs([]*types.Project{project([]string{"**/*"}, nil)})
		assert.Equal(t, types.CheckOK, got.Status,
			"it carries no extension to compare and drops nothing; reporting it would be a false positive")
	})

	t.Run("globs the spell declares for THIS target survive the reset", func(t *testing.T) {
		perTarget := spells.NewSpell("go",
			spells.WithSources("**/*.go"),
			spells.WithTargetSources(map[string][]string{"format": {"**/*.go"}}))
		p := project([]string{"**/*.md"}, nil)
		p.ResolvedSpells = []*spells.Spell{perTarget}
		got := r.checkFootprintDropsOpGlobs([]*types.Project{p})
		assert.Equal(t, types.CheckOK, got.Status,
			"buildStep folds TargetSources back in after the reset, so such a spell loses nothing")
	})
}
