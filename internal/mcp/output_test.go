//go:build mcp

package mcp

import (
	"context"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

// TestOutputToolRequiredParam covers the guard that returns before any store access.
func TestOutputToolRequiredParam(t *testing.T) {
	_, err := (&outputTool{}).Invoke(context.Background(), types.InvokeRequest{})
	assert.ErrorContains(t, err, "ref is required")
}

// TestOutputToolRejectsMalformedRef pins that magus_output validates the ref SHAPE
// before touching the store, so a non-ref argument fails loudly (and, unlike the old
// magus_query shape-routing, a graph search term can never land here by accident).
func TestOutputToolRejectsMalformedRef(t *testing.T) {
	_, err := (&outputTool{}).Invoke(context.Background(), types.InvokeRequest{Params: map[string]any{"ref": "refactor"}})
	assert.ErrorContains(t, err, "not a target-output reference")
}

// TestRegistryHasOutputDriver pins that magus_output is both described and wired:
// registerTools panics if a descriptor lacks a driver.
func TestRegistryHasOutputDriver(t *testing.T) {
	var described bool
	for _, d := range Registry {
		if d.Name == "magus_output" {
			described = true
		}
	}
	assert.True(t, described, "magus_output missing from Registry")
}
