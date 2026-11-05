package magus

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
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

	m, err := Open(context.Background(), root, WithSpawnRule(TierCeiling))
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
	assert.Contains(t, plan.Affected, "api")
	assert.NotContains(t, plan.Affected, "web")
	assert.Empty(t, plan.UnboundedBy, "a Go edit cannot move the declarations")

	for _, p := range []string{"api/magusfile.buzz", ".magus.yaml", "api/go.mod"} {
		edit, err := m.Plan(context.Background(), "ci", PlanOptions{ChangedPaths: []string{"api/main.go", p}, MaxShards: -1})
		require.NoError(t, err)
		assert.Equal(t, p+" can change the dependency graph or every project's build, which the affected set cannot see", edit.UnboundedBy)
	}
}

func TestUnboundedByNamesWhatTheClosureCannotVouchFor(t *testing.T) {
	edgeInput := func(p string) bool {
		return p == "magus.lock" || p == "app/go.mod" || strings.HasSuffix(p, ".buzz")
	}
	claimed := map[string]bool{"api/main.go": true, "app/go.mod": true, "spells/go/spell.buzz": true, "magus.lock": true}
	for _, tc := range []struct {
		changed []string
		want    string
	}{
		{[]string{"api/main.go"}, ""},
		{[]string{"api/main.go", "magus.lock"}, "magus.lock can change the dependency graph or every project's build, which the affected set cannot see"},
		{[]string{"spells/go/spell.buzz"}, "spells/go/spell.buzz can change the dependency graph or every project's build, which the affected set cannot see"},
		{[]string{"app/go.mod"}, "app/go.mod can change the dependency graph or every project's build, which the affected set cannot see"},
		{[]string{"stray.txt"}, "no project claims stray.txt"},
		{[]string{"api/main.go", "stray.txt"}, "no project claims stray.txt"},
		{nil, ""},
	} {
		assert.Equal(t, tc.want, unboundedBy(tc.changed, claimed, edgeInput), "%v", tc.changed)
	}
}

// TestPlanCapsCrossShardConcurrency: the runner-pool budget only ever narrows the
// plan, so a budget larger than the shard count leaves MaxParallel at the shards.
func TestPlanCapsCrossShardConcurrency(t *testing.T) {
	root := writeWorkspace(t, map[string]string{
		"magusfile.buzz":     "",
		"api/magusfile.buzz": "",
		"api/main.go":        "package main\n",
	})

	m, err := Open(context.Background(), root, WithSpawnRule(TierCeiling))
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
	m, err := Open(context.Background(), root, WithSpawnRule(TierCeiling))
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
		"history.json":   "{ this is not json\n",
	})
	m, err := Open(context.Background(), root, WithSpawnRule(TierCeiling))
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	_, err = m.Plan(context.Background(), "ci", PlanOptions{
		ChangedPaths: []string{},
		HistoryPath:  filepath.Join(root, "history.json"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "load history")
}

type PlanUnitsSuite struct {
	suite.Suite
	m *Magus
}

func TestPlanUnitsSuite(t *testing.T) { suite.Run(t, new(PlanUnitsSuite)) }

func (s *PlanUnitsSuite) SetupTest() {
	root := writeWorkspace(s.T(), map[string]string{
		"magusfile.buzz": `import "magus";
export fun build(ctx: magus\Context, args: [str]) > void {}
export fun lint(ctx: magus\Context, args: [str]) > void {}
export fun test(ctx: magus\Context, args: [str]) > void { ctx.needs(build); }
export fun figures(ctx: magus\Context, args: [str]) > void {}
export fun security(ctx: magus\Context, args: [str]) > void { ctx.needs(build); }
export fun ci(ctx: magus\Context, args: [str]) > void {
    ctx.needs(lint, test);
    if (!ctx.hasCharm("gha")) { ctx.needs(figures); }
    if (args.len() > 0) {
        security(ctx, args: args);
        return;
    }
    ctx.needs(security);
}
`,
		"api/magusfile.buzz": "",
		"api/main.go":        "package main\n",
	})
	m, err := Open(context.Background(), root, WithSpawnRule(TierCeiling))
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = m.Close() })
	s.m = m
}

func (s *PlanUnitsSuite) plan(charms, args []string) ([]types.Unit, error) {
	return s.m.planUnits(context.Background(), []types.Target{{Path: "."}, {Path: "api"}}, "ci", charms, args)
}

func (s *PlanUnitsSuite) TestEachStepCIAsksForIsAUnit() {
	got, err := s.plan(nil, nil)
	s.Require().NoError(err)
	s.Equal([]types.Unit{
		{Project: ".", Target: "lint"}, {Project: ".", Target: "test"}, {Project: ".", Target: "figures"},
		{Project: ".", Target: "security"}, {Project: "api", Target: "ci"},
	}, got)
}

func (s *PlanUnitsSuite) TestAStepTheCharmSkipsIsNoUnit() {
	got, err := s.plan([]string{"gha"}, nil)
	s.Require().NoError(err)
	s.NotContains(got, types.Unit{Project: ".", Target: "figures"})
	s.Contains(got, types.Unit{Project: ".", Target: "lint"})
}

func (s *PlanUnitsSuite) TestADirectCallUnderArgsIsAnErrorNamingIt() {
	_, err := s.plan([]string{"gha"}, []string{"--strict"})
	s.ErrorContains(err, ".:ci calls security")
}

func (s *PlanUnitsSuite) TestAnAffectedProjectOutsideTheWorkspaceIsAnError() {
	_, err := s.m.planUnits(context.Background(), []types.Target{{Path: "ghost"}}, "ci", nil, nil)
	s.ErrorContains(err, `project "ghost" is affected but not in the workspace`)
}

func (s *PlanUnitsSuite) TestAnUnreadableMagusfileIsAnError() {
	p := s.m.Get(".")
	s.Require().NoError(os.Remove(filepath.Join(p.Dir, types.MagusfileName)))
	_, err := directCalls(p, "ci")
	s.ErrorContains(err, "magusfile for direct calls")
}

func (s *PlanUnitsSuite) TestAUnitNeedsItsTransitiveSteps() {
	got, err := s.m.unitNeeds(types.Unit{Project: ".", Target: "test"})
	s.Require().NoError(err)
	s.Equal([]types.Unit{{Project: ".", Target: "test"}, {Project: ".", Target: "build"}}, got)
}

func (s *PlanUnitsSuite) TestThePlanCarriesUnitsAndNamesTheUnmeasured() {
	plan, err := s.m.Plan(context.Background(), "ci", PlanOptions{ChangedPaths: []string{"magusfile.buzz", "api/main.go"}, MaxShards: -1})
	s.Require().NoError(err)
	var placed []types.Unit
	for _, sh := range plan.Shards {
		placed = append(placed, sh.Units...)
	}
	s.ElementsMatch([]types.Unit{
		{Project: ".", Target: "lint"}, {Project: ".", Target: "test"}, {Project: ".", Target: "figures"},
		{Project: ".", Target: "security"}, {Project: "api", Target: "ci"},
	}, placed)
	s.Contains(plan.Unmeasured, types.Unit{Project: ".", Target: "build"}, "no history: every target fell back")
}

func (s *PlanUnitsSuite) TestColdHistoryOpensFewShardsAndSaysSo() {
	plan, err := s.m.Plan(context.Background(), "ci", PlanOptions{ChangedPaths: []string{"magusfile.buzz", "api/main.go"}, MaxShards: -1})
	s.Require().NoError(err)
	s.LessOrEqual(len(plan.Shards), forecast.ColdShards)
	s.Contains(plan.Fallbacks, "the history holds no duration for any unit, so the plan opens at most 2 shards")
}

func (s *PlanUnitsSuite) TestNoMachineChecksNoBudgetAndSaysSo() {
	plan, err := s.m.Plan(context.Background(), "ci", PlanOptions{ChangedPaths: []string{"api/main.go"}, MaxShards: -1})
	s.Require().NoError(err)
	s.Nil(plan.Machine)
	s.Contains(plan.Fallbacks, "no ci.machine is configured, so the plan checks no shard against a memory budget")
}

func (s *PlanUnitsSuite) TestTheConfiguredMachineIsRecorded() {
	s.m.cfg.CI.Machine = config.CIMachine{Name: "ubuntu-latest", MemoryMB: 16384, Cores: 4}
	plan, err := s.m.Plan(context.Background(), "ci", PlanOptions{ChangedPaths: []string{"api/main.go"}, MaxShards: -1})
	s.Require().NoError(err)
	s.Equal(&types.Machine{Name: "ubuntu-latest", MemoryMB: 16384, Cores: 4}, plan.Machine)
}

func (s *PlanUnitsSuite) TestAHalfConfiguredMachineIsAnError() {
	s.m.cfg.CI.Machine = config.CIMachine{Name: "ubuntu-latest", MemoryMB: 16384}
	_, err := s.m.Plan(context.Background(), "ci", PlanOptions{ChangedPaths: []string{"api/main.go"}, MaxShards: -1})
	s.ErrorContains(err, `ci.machine "ubuntu-latest" needs both memory_mb and cores above zero`)
}

func (s *PlanUnitsSuite) TestEveryShardIsLabeledByItsUnitsDigest() {
	plan, err := s.m.Plan(context.Background(), "ci", PlanOptions{ChangedPaths: []string{"magusfile.buzz", "api/main.go"}, MaxShards: -1})
	s.Require().NoError(err)
	for _, sh := range plan.Shards {
		s.Equal(types.UnitsDigest(sh.Units), sh.Label)
	}
}

type PlaceDutiesSuite struct{ suite.Suite }

func TestPlaceDutiesSuite(t *testing.T) { suite.Run(t, new(PlaceDutiesSuite)) }

func (s *PlaceDutiesSuite) TestTheCacheExportGoesWhereTheRootsUnitsAre() {
	shards := []types.Shard{
		{Units: []types.Unit{{Project: "docs", Target: "lint"}}},
		{Units: []types.Unit{{Project: ".", Target: "lint"}, {Project: ".", Target: "test"}}},
	}
	placeDuties(shards)
	s.Equal([]types.Duty{types.DutyCacheExport}, shards[1].Duties)
}

func (s *PlaceDutiesSuite) TestNotesVerifyGoesToTheLightestShard() {
	shards := []types.Shard{
		{Units: []types.Unit{{Project: ".", Target: "lint"}, {Project: ".", Target: "test"}}},
		{Units: []types.Unit{{Project: "docs", Target: "lint"}}},
	}
	placeDuties(shards)
	s.Equal([]types.Duty{types.DutyNotesVerify}, shards[1].Duties)
}

func (s *PlaceDutiesSuite) TestEachDutyLandsOnExactlyOneShard() {
	shards := []types.Shard{
		{Units: []types.Unit{{Project: "a", Target: "lint"}}},
		{Units: []types.Unit{{Project: "b", Target: "lint"}}},
		{Units: []types.Unit{{Project: "c", Target: "lint"}}},
	}
	placeDuties(shards)
	count := map[types.Duty]int{}
	for _, sh := range shards {
		for _, d := range sh.Duties {
			count[d]++
		}
	}
	s.Equal(map[types.Duty]int{types.DutyCacheExport: 1, types.DutyNotesVerify: 1}, count)
}

func (s *PlaceDutiesSuite) TestNoShardsTakeNoDuties() {
	s.NotPanics(func() { placeDuties(nil) })
}

// Example_runATarget runs a target, reads its result from the run's records, and
// reads the stored output its ref names.
func Example_runATarget() {
	root, cleanup := newGuideWorkspace(map[string]string{
		"magusfile.buzz":     "",
		"api/magusfile.buzz": "",
		"api/greeting.txt":   "hello\n",
	})
	defer cleanup()
	var runs int
	defer registerCopySpell("guide-run", &runs)()

	reg := NewWorkspaceRegistry()
	reg.RegisterProject("api", WithSpell("guide-run"))

	ctx := context.Background()
	m, err := Open(ctx, root, WithWorkspaceRegistry(reg), WithSpawnRule(TierCeiling))
	if err != nil {
		fmt.Println("open:", err)
		return
	}
	defer m.Close()

	results, err := runRecorded(ctx, m, []types.Target{{Path: "api", Name: "build"}})
	if err != nil {
		fmt.Println("run:", err)
		return
	}
	for _, r := range results {
		fmt.Printf("%s:%s %s, cache hit %v\n", r.Project, r.Target, r.Status, r.CacheHit)

		_, d, err := m.OutputByRef(ctx, r.Ref)
		if err != nil {
			fmt.Println("output:", err)
			return
		}
		fmt.Printf("ref names %s:%s, failed %v\n", d.Project, d.Target, d.Failed)
	}
	built, err := os.ReadFile(filepath.Join(root, "api", "out", "greeting.txt"))
	if err != nil {
		fmt.Println("read output:", err)
		return
	}
	fmt.Printf("api/out/greeting.txt: %s", built)
	// Output:
	// api:build ok, cache hit false
	// ref names api:build, failed false
	// api/out/greeting.txt: hello
}
