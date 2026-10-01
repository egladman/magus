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

func TestDescribeTools_CarriesEveryParam(t *testing.T) {
	out := DescribeTools()
	for i, d := range Registry {
		got := out.MCPTools[i].Params
		assert.Lenf(t, got, len(d.Params), "%s params", d.Name)
		for j, p := range d.Params {
			assert.Equal(t, p.Name, got[j].Name)
			assert.Equal(t, p.Type, got[j].Type)
			assert.Equal(t, p.Required, got[j].Required)
			assert.Equal(t, p.Description, got[j].Description)
		}
	}
}

func TestDescribeTools_AllEntriesHaveNames(t *testing.T) {
	out := DescribeTools()
	for i, tool := range out.MCPTools {
		assert.NotEmptyf(t, tool.Name, "MCPTools[%d].Name", i)
	}
}
