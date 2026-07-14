package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// TestApplyTargetFilter locks the run fault-tolerance matrix: tolerant across the
// workspace / multiple projects, strict for a single named project and for a name no
// project serves.
func TestApplyTargetFilter(t *testing.T) {
	// "go-vet" is defined only in the go projects.
	defines := func(path, target string) bool {
		return target == "go-vet" && (path == "." || path == "gopherbuzz")
	}
	label := func(path string) string { return path }
	tgt := func(paths ...string) []types.Target {
		out := make([]types.Target, len(paths))
		for i, p := range paths {
			out[i] = types.Target{Path: p, Name: "go-vet"}
		}
		return out
	}

	t.Run("multi partial: skip the undefined, keep the defined", func(t *testing.T) {
		got, err := applyTargetFilter(tgt(".", "gopherbuzz", "website", "proto"), "go-vet", defines, label)
		require.NoError(t, err)
		assert.Equal(t, tgt(".", "gopherbuzz"), got, "undefined projects are dropped, not errored")
	})

	t.Run("single project undefined: error", func(t *testing.T) {
		_, err := applyTargetFilter(tgt("website"), "go-vet", defines, label)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "website", "names the single project")
	})

	t.Run("single project defined: runs", func(t *testing.T) {
		got, err := applyTargetFilter(tgt("gopherbuzz"), "go-vet", defines, label)
		require.NoError(t, err)
		assert.Equal(t, tgt("gopherbuzz"), got)
	})

	t.Run("none serve across many: unknown-target error", func(t *testing.T) {
		_, err := applyTargetFilter(tgt("website", "proto"), "bogus", func(string, string) bool { return false }, label)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "any of the 2 selected projects")
	})

	t.Run("all defined: pass through", func(t *testing.T) {
		got, err := applyTargetFilter(tgt(".", "gopherbuzz"), "go-vet", defines, label)
		require.NoError(t, err)
		assert.Len(t, got, 2)
	})
}
