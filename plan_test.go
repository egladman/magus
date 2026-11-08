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

func (s *PlanUnitsSuite) TestAnAnchorUnderMagusfilesPlansFromThatFile() {
	root := writeWorkspace(s.T(), map[string]string{
		"magusfiles/targets.buzz": `import "magus";
export fun build(ctx: magus\Context, args: [str]) > void {}
export fun security(ctx: magus\Context, args: [str]) > void {}
export fun ci(ctx: magus\Context, args: [str]) > void {
    ctx.needs(build);
    security(ctx, args: args);
}
`,
		"main.go": "package main\n",
	})
	m, err := Open(context.Background(), root, WithSpawnRule(TierCeiling))
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = m.Close() })
	calls, err := directCalls(m.Get("."), "ci")
	s.Require().NoError(err)
	s.Equal([]string{"security"}, calls)
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
	s.Require().NotEmpty(plan.Fallbacks)
	s.Equal(types.Fallback{
		What:   "At most 2 shards",
		Why:    "the run history holds no duration for any target",
		Effect: "Shards are packed by count, not time, so one can run far longer than the rest. A main run records durations for the next plan.",
	}, plan.Fallbacks[len(plan.Fallbacks)-1])
}

func (s *PlanUnitsSuite) TestNoMachineChecksNoBudgetAndSaysSo() {
	plan, err := s.m.Plan(context.Background(), "ci", PlanOptions{ChangedPaths: []string{"api/main.go"}, MaxShards: -1})
	s.Require().NoError(err)
	s.Nil(plan.Machine)
	s.Require().NotEmpty(plan.Fallbacks)
	s.Equal(types.Fallback{
		What: "No memory check",
		Why:  "magus.yaml sets no `ci.machine`",
		Effect: "No shard is checked against a runner's memory. A shard that runs out shows as cancelled, with no logs. " +
			"Set `ci.machine` to check.",
	}, plan.Fallbacks[0])
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

func (s *PlanUnitsSuite) TestEveryShardIsDigestedByItsUnitsDigest() {
	plan, err := s.m.Plan(context.Background(), "ci", PlanOptions{ChangedPaths: []string{"magusfile.buzz", "api/main.go"}, MaxShards: -1})
	s.Require().NoError(err)
	for _, sh := range plan.Shards {
		s.Equal(types.UnitsDigest(sh.Units), sh.Digest)
	}
}

func (s *PlanUnitsSuite) TestAnAnchorThatDoesMoreThanNeedsIsAnErrorNamingIt() {
	root := writeWorkspace(s.T(), map[string]string{
		"magusfile.buzz": `import "magus";
import "fs";
export fun build(ctx: magus\Context, args: [str]) > void {}
export fun ci(ctx: magus\Context, args: [str]) > void !> any {
    ctx.needs(build);
    fs\writeFile("out.txt", "x");
}
`,
	})
	m, err := Open(context.Background(), root, WithSpawnRule(TierCeiling))
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = m.Close() })
	_, err = m.planUnits(context.Background(), []types.Target{{Path: "."}}, "ci", nil, nil)
	s.ErrorContains(err, "ci in . does more than ctx.needs (fs.writeFile)")
}

func (s *PlanUnitsSuite) TestALeafAnchorWithEffectsIsItsOwnUnit() {
	root := writeWorkspace(s.T(), map[string]string{
		"magusfile.buzz": `import "magus";
import "fs";
export fun build(ctx: magus\Context, args: [str]) > void !> any {
    fs\writeFile("out.txt", "x");
}
`,
	})
	m, err := Open(context.Background(), root, WithSpawnRule(TierCeiling))
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = m.Close() })
	units, err := m.planUnits(context.Background(), []types.Target{{Path: "."}}, "build", nil, nil)
	s.Require().NoError(err)
	s.Equal([]types.Unit{{Project: ".", Target: "build"}}, units)
}

// PlanReachSuite plans a workspace whose targets declare their reads, so a
// change reaches some units and leaves the rest to the cache.
type PlanReachSuite struct {
	suite.Suite
	m *Magus
}

func TestPlanReachSuite(t *testing.T) { suite.Run(t, new(PlanReachSuite)) }

func (s *PlanReachSuite) SetupTest() {
	root := writeWorkspace(s.T(), map[string]string{
		"magusfile.buzz": `import "magus";
export fun build(ctx: magus\Context, args: [str]) > void !> any { ctx.readsFiles(["*.go"]); }
export fun docs(ctx: magus\Context, args: [str]) > void !> any { ctx.readsFiles(["docs/*"]); }
export fun test(ctx: magus\Context, args: [str]) > void !> any { ctx.needs(build); ctx.readsFiles(["*_test.go"]); }
export fun vet(ctx: magus\Context, args: [str]) > void !> any { ctx.needs(build); ctx.readsFiles(["*_test.go"]); }
export fun ci(ctx: magus\Context, args: [str]) > void !> any { ctx.needs(docs, test, vet); }
`,
		"main.go":   "package main\n",
		"docs/a.md": "a\n",
	})
	m, err := Open(context.Background(), root, WithSpawnRule(TierCeiling))
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = m.Close() })
	s.m = m
}

func (s *PlanReachSuite) plan(path string) types.ShardPlan {
	plan, err := s.m.Plan(context.Background(), "ci", PlanOptions{ChangedPaths: []string{path}, MaxShards: -1})
	s.Require().NoError(err)
	return plan
}

func (s *PlanReachSuite) TestAUnitWhoseClosureReadsNoChangedPathIsUnreached() {
	s.Equal([]types.Unit{{Project: ".", Target: "docs"}}, s.plan("main.go").Unreached)
}

func (s *PlanReachSuite) TestAPrerequisiteReadingTheChangeReachesItsDependents() {
	s.Equal([]types.Unit{{Project: ".", Target: "test"}, {Project: ".", Target: "vet"}}, s.plan("docs/a.md").Unreached)
}

func (s *PlanReachSuite) TestUnitsSharingAReachedPrerequisiteShareAShard() {
	plan := s.plan("main.go")
	s.Require().Len(plan.Shards, 1)
	s.ElementsMatch([]types.Unit{{Project: ".", Target: "test"}, {Project: ".", Target: "vet"}}, plan.Shards[0].Units)
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

// planBenchWorkspace is projects whose ci asks for lint and test, both
// needing build, with every project changed.
func planBenchWorkspace(b *testing.B, projects int) (*Magus, []string) {
	files := map[string]string{"magusfile.buzz": ""}
	var changed []string
	for p := range projects {
		dir := fmt.Sprintf("p%03d", p)
		files[dir+"/magusfile.buzz"] = `import "magus";
export fun build(ctx: magus\Context, args: [str]) > void !> any { ctx.readsFiles(["*.go"]); }
export fun lint(ctx: magus\Context, args: [str]) > void !> any { ctx.needs(build); }
export fun test(ctx: magus\Context, args: [str]) > void !> any { ctx.needs(build); }
export fun ci(ctx: magus\Context, args: [str]) > void !> any { ctx.needs(lint, test); }
`
		files[dir+"/main.go"] = "package main\n"
		changed = append(changed, dir+"/main.go")
	}
	m, err := Open(context.Background(), writeWorkspace(b, files), WithSpawnRule(TierCeiling))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = m.Close() })
	return m, changed
}

func BenchmarkPlan(b *testing.B) {
	for _, projects := range []int{10, 40} {
		m, changed := planBenchWorkspace(b, projects)
		b.Run(fmt.Sprintf("projects=%d", projects), func(b *testing.B) {
			for b.Loop() {
				if _, err := m.Plan(context.Background(), "ci", PlanOptions{ChangedPaths: changed, MaxShards: -1}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// PlanStageSuite plans a 200s test and a 200s vet whose prerequisites take
// 10s, from a measured history, so splitting them pays.
type PlanStageSuite struct {
	suite.Suite
	history string
}

func TestPlanStageSuite(t *testing.T) { suite.Run(t, new(PlanStageSuite)) }

func (s *PlanStageSuite) SetupTest() {
	s.history = filepath.Join(s.T().TempDir(), "history.json")
	h := forecast.History{
		Constants: forecast.Constants{SetupP50Ms: forecast.Millis(1_000)},
		Projects: map[string]map[string]forecast.Stats{".": {
			"build": {P75Ms: 10_000, Samples: 50},
			"gen":   {P75Ms: 10_000, Samples: 50},
			"test":  {P75Ms: 200_000, Samples: 50},
			"vet":   {P75Ms: 200_000, Samples: 50},
		}},
	}
	s.Require().NoError(forecast.UpdateHistory(context.Background(), s.history, func(dst *forecast.History) error {
		*dst = h
		return nil
	}))
}

// stagedMagusfile is a ci whose test needs build and whose vet needs VET_NEEDS.
const stagedMagusfile = `import "magus";
export fun build(ctx: magus\Context, args: [str]) > void !> any { ctx.readsFiles(["*.go"]); }
export fun gen(ctx: magus\Context, args: [str]) > void !> any { ctx.readsFiles(["*.go"]); }
export fun test(ctx: magus\Context, args: [str]) > void !> any { ctx.needs(build); ctx.readsFiles(["*.go"]); }
export fun vet(ctx: magus\Context, args: [str]) > void !> any { ctx.needs(VET_NEEDS); ctx.readsFiles(["*.go"]); }
export fun ci(ctx: magus\Context, args: [str]) > void !> any { ctx.needs(test, vet); }
`

// plan plans ci, with vet needing vetNeeds, after main.go changes.
func (s *PlanStageSuite) plan(vetNeeds string) types.ShardPlan {
	root := writeWorkspace(s.T(), map[string]string{
		"magusfile.buzz": strings.ReplaceAll(stagedMagusfile, "VET_NEEDS", vetNeeds),
		"main.go":        "package main\n",
	})
	m, err := Open(context.Background(), root, WithSpawnRule(TierCeiling))
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = m.Close() })
	plan, err := m.Plan(context.Background(), "ci", PlanOptions{
		ChangedPaths: []string{"main.go"}, MaxShards: -1, HistoryPath: s.history,
	})
	s.Require().NoError(err)
	return plan
}

func (s *PlanStageSuite) TestADependencySharedByTwoShardsIsPlannedOnceInStageZero() {
	plan := s.plan("build")
	build := []types.Unit{{Project: ".", Target: "build"}}
	s.Equal([]types.Shard{{
		ID: "0a", Digest: types.UnitsDigest(build), ProjectPaths: []string{"."}, Units: build,
	}}, plan.StageZero, "duties stay on the last stage")
	s.Require().Len(plan.Shards, 2)
	for _, sh := range plan.Shards {
		s.NotContains(sh.Units, build[0])
	}
}

func (s *PlanStageSuite) TestAPlanWithNoSharedWorkHasOneStage() {
	plan := s.plan("gen")
	s.Nil(plan.StageZero)
	test := []types.Unit{{Project: ".", Target: "test"}}
	vet := []types.Unit{{Project: ".", Target: "vet"}}
	s.Equal([]types.Shard{
		{ID: "1a", Digest: types.UnitsDigest(test), ProjectPaths: []string{"."}, Units: test, Duties: []types.Duty{types.DutyCacheExport, types.DutyNotesVerify}},
		{ID: "1b", Digest: types.UnitsDigest(vet), ProjectPaths: []string{"."}, Units: vet},
	}, plan.Shards)
}
