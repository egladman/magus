package mcp_test

import (
	"testing"

	"github.com/egladman/magus/internal/mcp"
)

func TestDescribeTools_CountMatchesRegistry(t *testing.T) {
	out := mcp.DescribeTools()
	if out.Count != len(mcp.Registry) {
		t.Errorf("DescribeTools().Count = %d, want %d (len(Registry))", out.Count, len(mcp.Registry))
	}
	if len(out.MCPTools) != out.Count {
		t.Errorf("len(MCPTools) = %d, want %d", len(out.MCPTools), out.Count)
	}
	if out.Definition == "" {
		t.Error("DescribeTools().Definition is empty")
	}
}

func TestDescribeTools_AllEntriesHaveNames(t *testing.T) {
	out := mcp.DescribeTools()
	for i, tool := range out.MCPTools {
		if tool.Name == "" {
			t.Errorf("MCPTools[%d].Name is empty", i)
		}
	}
}
