package mcp

import (
	"testing"
)

func TestDescribeTools_CountMatchesRegistry(t *testing.T) {
	out := DescribeTools()
	if out.Count != len(Registry) {
		t.Errorf("DescribeTools().Count = %d, want %d (len(Registry))", out.Count, len(Registry))
	}
	if len(out.MCPTools) != out.Count {
		t.Errorf("len(MCPTools) = %d, want %d", len(out.MCPTools), out.Count)
	}
	if out.Definition == "" {
		t.Error("DescribeTools().Definition is empty")
	}
}

func TestDescribeTools_AllEntriesHaveNames(t *testing.T) {
	out := DescribeTools()
	for i, tool := range out.MCPTools {
		if tool.Name == "" {
			t.Errorf("MCPTools[%d].Name is empty", i)
		}
	}
}
