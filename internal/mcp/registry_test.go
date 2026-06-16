package mcp

import (
	"testing"
)

func TestRegistry_NonEmpty(t *testing.T) {
	if len(Registry) == 0 {
		t.Fatal("Registry is empty; every magus MCP deployment needs at least one tool")
	}
}

func TestRegistry_AllToolsHaveNames(t *testing.T) {
	seen := map[string]bool{}
	for i, d := range Registry {
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
	for _, d := range Registry {
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
