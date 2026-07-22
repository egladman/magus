package magus

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMergeTargetNodes proves the static and discovered node sets unify into one node
// per target. A ctx-form target appears in both, complementary: the static read has its
// spell ops and doc but no ctx.needs/inputs; discovery has its deps/inputs/outputs/charms
// but no spell ops. The merge must field-union them, not let one shadow the other. An
// old-form target (static only) and a pure-ctx target (discovered only) pass through.
func TestMergeTargetNodes(t *testing.T) {
	static := []types.TargetGraphNode{
		{ // ctx-form target: static read caught only the spell op and the doc
			Name:   "build",
			Doc:    "Build the binary.",
			Spells: []types.TargetSpellUse{{Spell: "go", Ops: []string{"go-build"}}},
		},
		{ // old-form target: static only
			Name:         "generate",
			Dependencies: []string{"proto-generate"},
		},
	}
	discovered := []types.TargetGraphNode{
		{ // same ctx-form build: discovery caught deps/inputs/outputs/charms/policy
			Name:         "build",
			Doc:          "Build the binary.",
			Dependencies: []string{"format"},
			Inputs:       []types.InputRef{{Glob: "cmd/**/*.go"}},
			Outputs:      []string{"bin/app"},
			Charms:       []string{"cd"},
		},
		{ // pure-ctx target: discovered only
			Name:         "lint",
			Dependencies: []string{"format"},
		},
	}

	merged := mergeTargetNodes(static, discovered)
	by := map[string]types.TargetGraphNode{}
	for _, n := range merged {
		by[n.Name] = n
	}

	require.Len(t, merged, 3, "build unifies into one node; generate and lint pass through")

	// The build node carries BOTH the static spell op and the discovered deps/inputs/outputs/charms.
	assert.Equal(t, types.TargetGraphNode{
		Name:         "build",
		Doc:          "Build the binary.",
		Dependencies: []string{"format"},
		Charms:       []string{"cd"},
		Spells:       []types.TargetSpellUse{{Spell: "go", Ops: []string{"go-build"}}},
		Inputs:       []types.InputRef{{Glob: "cmd/**/*.go"}},
		Outputs:      []string{"bin/app"},
	}, by["build"])

	assert.Equal(t, []string{"proto-generate"}, by["generate"].Dependencies, "old-form node passes through unchanged")
	assert.Equal(t, []string{"format"}, by["lint"].Dependencies, "pure-ctx node is appended")

	// Static (source) order is preserved for the nodes present statically.
	assert.Equal(t, "build", merged[0].Name)
	assert.Equal(t, "generate", merged[1].Name)
}
