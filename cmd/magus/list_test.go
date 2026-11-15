package main

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

// TestTargetSummaryPrefersTheAuthorsWords pins the fallback order: a doc comment beats a
// derived description, and a target that composes only siblings still says something.
func TestTargetSummaryPrefersTheAuthorsWords(t *testing.T) {
	assert.Equal(t, "build the site", targetSummary(types.TargetGraphNode{
		Doc:    "build the site",
		Spells: []types.TargetSpellUse{{Spell: "go", Ops: []string{"go-build"}}},
	}))

	assert.Equal(t, "[go: go-build, go-vet; md: lint]", targetSummary(types.TargetGraphNode{
		Spells: []types.TargetSpellUse{
			{Spell: "go", Ops: []string{"go-build", "go-vet"}},
			{Spell: "md", Ops: []string{"lint"}},
		},
	}))

	assert.Equal(t, "[needs: build, test]", targetSummary(types.TargetGraphNode{
		Dependencies: []string{"build", "test"},
	}))

	assert.Equal(t, "", targetSummary(types.TargetGraphNode{Name: "bare"}))
}
