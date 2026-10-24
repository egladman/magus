package settle

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
	"github.com/stretchr/testify/assert"
)

// TestGeneratorProjectsCountsMovedOutputs: a project whose output the operation moved
// regenerates too, since that output was made against a base the operation replaced.
func TestGeneratorProjectsCountsMovedOutputs(t *testing.T) {
	got := generatorProjects([]types.FileEntry{
		{Path: "docs/a.md", Role: "source", SourceOf: []string{".", "docs"}},
		{Path: "api/gen/x.go", Role: "output", OutputOf: []string{"api"}},
		{Path: "README", Role: "unclaimed"},
	})
	assert.Equal(t, []string{".", "api", "docs"}, got)
}

// TestSettleInvocationsOneRunPerTarget: every project of a target shares one
// `<target>:rw` run, whatever its depth, so the scheduler orders and parallelizes them;
// the owed records' own targets sit beside the generate convention, and a project with
// no such target is left out rather than failing the run.
func TestSettleInvocationsOneRunPerTarget(t *testing.T) {
	defines := func(path, target string) bool {
		return target != "generate" || (path != "api" && path != "gone")
	}
	got := Invocations([]string{".", "api", "docs", "docs/site", "gone", "libs/a"}, []vcs.OwedRegeneration{
		{Project: ".", Target: "index-generate"},
		{Project: "docs", Target: "generate"},
		{Project: "api", Target: "proto"},
	}, defines)
	assert.Equal(t, [][]string{
		{"generate:rw", ".", "docs", "docs/site", "libs/a"},
		{"index-generate:rw", "."},
		{"proto:rw", "api"},
	}, got)
}
