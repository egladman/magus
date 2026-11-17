package main

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/libs/testkit/register"
	"github.com/egladman/magus/spells"
)

// TestDescribeTargets_SpellsSorted pins that the spells a target lists come out in name
// order however the registry was filled: they used to follow the built-ins' map order, so
// `describe targets -o json` flipped between runs.
func TestDescribeTargets_SpellsSorted(t *testing.T) {
	// No two built-in spells share a target, so the fixture registers two that do, the
	// later name first.
	later := register.Spell(t, "zz-shared-target", spells.WithTargets("shared"))
	earlier := register.Spell(t, "aa-shared-target", spells.WithTargets("shared"))
	w := testkit.NewWorkspace(t)
	w.Magusfile("")
	reg := magus.NewWorkspaceRegistry()
	reg.RegisterProject(".", magus.WithSpell(later), magus.WithSpell(earlier))
	m, err := magus.Open(t.Context(), w.Root(), magus.WithWorkspaceRegistry(reg), magus.WithSpawnRule(tier.Ceiling))
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	targets, err := m.ListTargets(t.Context())
	require.NoError(t, err)
	shared := 0
	for _, e := range targets {
		assert.Truef(t, slices.IsSorted(e.Spells), "target %q lists its spells out of order: %v", e.Name, e.Spells)
		if len(e.Spells) > 1 {
			shared++
		}
	}
	assert.NotZero(t, shared, "the fixture must bind spells that share a target, or the check proves nothing")
}

// TestDescribeWorkspacesOutput_MultiDeclared verifies that `describe workspaces`
// enumerates every declared server workspace, not just the active one.
func TestDescribeWorkspacesOutput_MultiDeclared(t *testing.T) {
	mkWorkspace := func() string {
		w := testkit.NewWorkspace(t)
		// An empty magusfile.buzz marks the directory as a workspace root.
		w.Magusfile("")
		return w.Root()
	}
	wsA, wsB := mkWorkspace(), mkWorkspace()

	saved := globalCfg
	t.Cleanup(func() { globalCfg = saved })
	globalCfg = config.Config{}
	globalCfg.Server.Workspaces = []string{wsA, wsB}

	out, err := describeWorkspacesOutput(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.NotEqual(t, out[1].Root, out[0].Root, "expected two distinct workspace roots")
}

// TestDescribeAlias pins that singular and plural resolve to the same canonical
// noun (kubectl-style interchangeability).
func TestDescribeAlias(t *testing.T) {
	pairs := [][2]string{
		{"module", "modules"},
		{"project", "projects"},
		{"spell", "spells"},
		{"target", "targets"},
		{"workspace", "workspaces"},
		{"mcp-tool", "mcp-tools"},
	}
	for _, p := range pairs {
		assert.Equal(t, describeAlias[p[0]], describeAlias[p[1]], "%q and %q resolve differently", p[0], p[1])
		assert.Equal(t, p[0], describeAlias[p[0]], "canonical of %q should be itself", p[0])
	}
}
