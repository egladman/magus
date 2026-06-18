package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
