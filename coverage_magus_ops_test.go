package magus

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// TestTargetLabel renders the scope header a run prints. The empty case is the one
// worth pinning: a scope that selected nothing says so, rather than rendering as
// "0 projects" among the plural forms.
func TestTargetLabel(t *testing.T) {
	one := []types.Target{{Path: "api", Name: "build"}}
	several := []types.Target{{Path: "api"}, {Path: "web"}, {Path: "."}}

	assert.Equal(t, "no projects", TargetLabel(nil, ""))
	assert.Equal(t, "no projects (affected)", TargetLabel(nil, "affected"))
	assert.Equal(t, "api", TargetLabel(one, ""))
	assert.Equal(t, "api (affected)", TargetLabel(one, "affected"))
	assert.Equal(t, "3 projects", TargetLabel(several, ""))
	assert.Equal(t, "3 projects (stdin paths)", TargetLabel(several, "stdin paths"))
}

// TestCharmsForCI: both write-granting charms come off a ci run. rw so a
// check-only target stays check-only, and relock so ci verifies the committed
// dependency state rather than re-resolving it against today's registry.
func TestCharmsForCI(t *testing.T) {
	assert.Equal(t, []string{"race", "coverage"},
		CharmsForCI([]string{"race", types.CharmReadWrite, "coverage", types.CharmRelock}))
	assert.Empty(t, CharmsForCI([]string{types.CharmReadWrite}))
	assert.Nil(t, CharmsForCI(nil))

	// The input is not mutated: the caller's RunOptions keep the charms it set.
	given := []string{types.CharmReadWrite, "race"}
	CharmsForCI(given)
	assert.Equal(t, []string{types.CharmReadWrite, "race"}, given)
}

// TestResolveTargetOutputs answers "where did the artifact land" from the same
// fold the cache keys and snapshots, so what it reports and what the cache replays
// cannot disagree. Directories are excluded: a consumer cannot open one.
func TestResolveTargetOutputs(t *testing.T) {
	m, root := openTempWorkspace(t, "api", []string{"dist/**"})

	dist := filepath.Join(root, "api", "dist")
	require.NoError(t, os.MkdirAll(filepath.Join(dist, "assets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dist, "app.js"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dist, "assets", "logo.svg"), []byte("y"), 0o644))

	got, err := m.ResolveTargetOutputs(context.Background(), m.All(), "build")
	require.NoError(t, err)

	paths := make([]string, 0, len(got))
	for _, a := range got {
		paths = append(paths, a.Path)
		assert.Equal(t, "api", a.ProjectPath, "the DECLARING project is recorded, not the tree the file sits in")
		assert.NotEmpty(t, a.Glob, "a reader chasing an unexpected artifact needs the declaration that claimed it")
	}
	assert.Equal(t, []string{"api/dist/app.js", "api/dist/assets/logo.svg"}, paths,
		"sorted, deduplicated, and directories excluded")
}

func TestResolveTargetOutputsHonorsCancellation(t *testing.T) {
	m, _ := openTempWorkspace(t, "api", []string{"dist/**"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := m.ResolveTargetOutputs(ctx, m.All(), "build")
	assert.ErrorIs(t, err, context.Canceled)
}

func TestResolveTargetOutputsWithNothingDeclared(t *testing.T) {
	m, _ := openTempWorkspace(t, "api", nil)

	got, err := m.ResolveTargetOutputs(context.Background(), m.All(), "build")
	require.NoError(t, err)
	assert.Empty(t, got)
}

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

// TestApplyUnionSandboxIsInertWithoutAnOptIn: the daemon applies a kernel policy
// only when some workspace asked for one. No roots, or roots that never enable
// sandboxing, must leave the process unconfined - applying a policy nobody
// requested would break every other workspace the daemon serves.
func TestApplyUnionSandboxIsInertWithoutAnOptIn(t *testing.T) {
	ctx := context.Background()

	assert.NoError(t, ApplyUnionSandbox(ctx, nil))
	assert.NoError(t, ApplyUnionSandbox(ctx, []string{}))

	root := writeWorkspace(t, map[string]string{"magusfile.buzz": ""})
	assert.NoError(t, ApplyUnionSandbox(ctx, []string{root}),
		"a workspace with no sandbox block requests nothing")
}
