package mcp_test

import (
	"testing"

	"github.com/egladman/magus/internal/mcp"
)

func TestRegistry_NonEmpty(t *testing.T) {
	if len(mcp.Registry) == 0 {
		t.Fatal("Registry is empty; every magus MCP deployment needs at least one tool")
	}
}

func TestRegistry_AllToolsHaveNames(t *testing.T) {
	seen := map[string]bool{}
	for i, d := range mcp.Registry {
		if d.Name == "" {
			t.Errorf("Registry[%d].Name is empty", i)
		}
		if seen[d.Name] {
			t.Errorf("Registry: duplicate tool name %q at index %d", d.Name, i)
		}
		seen[d.Name] = true
	}
}

func TestRegistry_AllParamsHaveNames(t *testing.T) {
	for _, d := range mcp.Registry {
		for j, p := range d.Params {
			if p.Name == "" {
				t.Errorf("Registry[%q].Params[%d].Name is empty", d.Name, j)
			}
			if p.Type == "" {
				t.Errorf("Registry[%q].Params[%d].Type is empty", d.Name, j)
			}
		}
	}
}
