package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDescribeTools_CountMatchesRegistry(t *testing.T) {
	out := DescribeTools()
	assert.Equal(t, len(Registry), out.Count)
	assert.Len(t, out.MCPTools, out.Count)
	assert.NotEmpty(t, out.Definition)
}

func TestDescribeTools_AllEntriesHaveNames(t *testing.T) {
	out := DescribeTools()
	for i, tool := range out.MCPTools {
		assert.NotEmptyf(t, tool.Name, "MCPTools[%d].Name", i)
	}
}
