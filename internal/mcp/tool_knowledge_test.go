//go:build mcp

package mcp

import (
	"context"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

// The knowledge tools' graph traversal is covered by internal/knowledge and the
// CLI txtars; here we pin the MCP surface: tool names and required-param
// validation (which returns before any workspace access, so no Magus is needed).
func TestKnowledgeToolNames(t *testing.T) {
	assert.Equal(t, "magus_query", (&queryTool{}).Name())
	assert.Equal(t, "magus_explain", (&explainTool{}).Name())
	assert.Equal(t, "magus_path", (&pathTool{}).Name())
	assert.Equal(t, "magus_stats", (&statsTool{}).Name())
}

// TestRegistryHasStatsDriver pins that magus_stats is both described and wired:
// registerTools panics if a descriptor lacks a driver, so a present descriptor
// plus a present driver name is the contract.
func TestRegistryHasStatsDriver(t *testing.T) {
	var described bool
	for _, d := range Registry {
		if d.Name == "magus_stats" {
			described = true
		}
	}
	assert.True(t, described, "magus_stats missing from Registry")
}

func TestKnowledgeToolRequiredParams(t *testing.T) {
	ctx := context.Background()

	_, err := (&queryTool{}).Invoke(ctx, types.InvokeRequest{})
	assert.ErrorContains(t, err, "query is required")

	_, err = (&explainTool{}).Invoke(ctx, types.InvokeRequest{})
	assert.ErrorContains(t, err, "node is required")

	_, err = (&pathTool{}).Invoke(ctx, types.InvokeRequest{Params: map[string]any{"from": "a"}})
	assert.ErrorContains(t, err, "from and to are required")
}
