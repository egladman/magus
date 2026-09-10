package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/std"
)

func TestRegistry_NonEmpty(t *testing.T) {
	require.NotEmpty(t, Registry, "Registry is empty; every magus MCP deployment needs at least one tool")
}

func TestRegistry_AllToolsHaveNames(t *testing.T) {
	seen := map[string]bool{}
	for i, d := range Registry {
		assert.NotEmptyf(t, d.Name, "Registry[%d].Name", i)
		assert.Falsef(t, seen[d.Name], "Registry: duplicate tool name %q at index %d", d.Name, i)
		seen[d.Name] = true
	}
}

func TestRegistry_AllParamsHaveNames(t *testing.T) {
	for _, d := range Registry {
		for j, p := range d.Params {
			assert.NotEmptyf(t, p.Name, "Registry[%q].Params[%d].Name", d.Name, j)
			assert.NotEmptyf(t, p.Type, "Registry[%q].Params[%d].Type", d.Name, j)
		}
	}
}

// TestRegistry_EveryToolHasADriver walks the REAL catalog against the REAL driver
// set, both directions. registerTools panics on either mismatch, but only when a
// server is actually built, which no unit test does and which a daemon does once at
// startup: the catalog is generated now, so a descriptor edit that adds a tool with
// no handler behind it should fail here rather than at somebody's first run.
func TestRegistry_EveryToolHasADriver(t *testing.T) {
	drivers := map[string]bool{}
	for _, d := range allMCPTools(Options{Magus: fixtureMagus(t)}) {
		drivers[d.Name()] = true
	}

	described := map[string]bool{}
	for _, d := range Registry {
		described[d.Name] = true
		assert.Truef(t, drivers[d.Name],
			"tool %q is in the catalog with no SpellDriver behind it; add the driver to allMCPTools", d.Name)
	}
	for name := range drivers {
		assert.Truef(t, described[name],
			"driver %q has no catalog entry and would mount nowhere; declare an MCPTool for it on std.Magus", name)
	}
}

// TestRegistry_MemberNamesADescriptorMember checks the link back from a tool to the
// descriptor member it wraps. std's own validation rejects a Member that names
// nothing, so this is the assertion that the link SURVIVES generation rather than
// being dropped on the way through.
func TestRegistry_MemberNamesADescriptorMember(t *testing.T) {
	t.Parallel()

	members := map[string]bool{}
	for _, m := range std.Magus.Methods {
		members[m.Name] = true
	}
	for _, ns := range std.Magus.Namespaces {
		members[ns.Name] = true
	}
	linked := 0
	for _, d := range Registry {
		if d.Member == "" {
			continue
		}
		linked++
		assert.Truef(t, members[d.Member],
			"tool %q names member %q, which std.Magus does not declare", d.Name, d.Member)
	}
	assert.NotZero(t, linked, "no tool links back to a descriptor member; the catalog stopped deriving")
}
