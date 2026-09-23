package magus

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPlanFromChangedPaths builds a shard plan without a VCS diff, which is the
// `magus watch | magus affected --stdin` path. With no runtime history every
// project costs the same, so what this pins is the plan's shape and its source
// attribution rather than the packing.
func TestPlanFromChangedPaths(t *testing.T) {
	root := writeWorkspace(t, map[string]string{
		"magusfile.buzz":     "",
		"api/magusfile.buzz": "",
		"web/magusfile.buzz": "",
		"api/main.go":        "package main\n",
	})

	m, err := Open(context.Background(), root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	plan, err := m.Plan(context.Background(), "ci", PlanOptions{ChangedPaths: []string{"api/main.go"}, MaxShards: -1})
	require.NoError(t, err)
	assert.Equal(t, "stdin paths", plan.Source)
	require.NotEmpty(t, plan.Shards)

	var planned []string
	for _, s := range plan.Shards {
		assert.NotEmpty(t, s.ID, "every shard is addressable")
		planned = append(planned, s.ProjectPaths...)
	}
	assert.Contains(t, planned, "api")
	assert.NotContains(t, planned, "web", "an unaffected project must not be sharded")
	assert.Positive(t, plan.MaxParallel)
}

// TestPlanCapsCrossShardConcurrency: the runner-pool budget only ever narrows the
// plan, so a budget larger than the shard count leaves MaxParallel at the shards.
func TestPlanCapsCrossShardConcurrency(t *testing.T) {
	root := writeWorkspace(t, map[string]string{
		"magusfile.buzz":     "",
		"api/magusfile.buzz": "",
		"api/main.go":        "package main\n",
	})

	m, err := Open(context.Background(), root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	opts := PlanOptions{ChangedPaths: []string{"api/main.go"}, RunnerPoolBudget: 1, MaxShards: -1}
	plan, err := m.Plan(context.Background(), "ci", opts)
	require.NoError(t, err)
	assert.Equal(t, 1, plan.MaxParallel)

	opts.RunnerPoolBudget = 1000
	plan, err = m.Plan(context.Background(), "ci", opts)
	require.NoError(t, err)
	assert.Equal(t, len(plan.Shards), plan.MaxParallel, "a budget above the shard count narrows nothing")
}

// TestPlanRejectsBothSources: a base ref and an explicit path list are two
// different answers to "what changed", and silently preferring one would make the
// plan describe a diff the caller did not ask for.
func TestPlanRejectsBothSources(t *testing.T) {
	root := writeWorkspace(t, map[string]string{"magusfile.buzz": ""})
	m, err := Open(context.Background(), root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	_, err = m.Plan(context.Background(), "ci", PlanOptions{
		ChangedPaths: []string{"a.go"},
		BaseRef:      "main",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

// TestPlanReportsAnUnreadableHistory: adaptive sharding is the point of the
// history, so a path that cannot be loaded fails the plan rather than silently
// producing the uniform-cost one.
func TestPlanReportsAnUnreadableHistory(t *testing.T) {
	root := writeWorkspace(t, map[string]string{
		"magusfile.buzz": "",
		"history.json":   "{ this is not json",
	})
	m, err := Open(context.Background(), root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	_, err = m.Plan(context.Background(), "ci", PlanOptions{
		ChangedPaths: []string{},
		HistoryPath:  filepath.Join(root, "history.json"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "load history")
}
