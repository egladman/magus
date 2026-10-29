package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

// describe reads the graph under a stale index whose changed files all miss the row's write
// paths, and keeps today's refusal when one falls inside them or the index names none.
func TestJobGraphReadsUnderAStaleIndexOnlyWhenItsChangesMissTheWritePaths(t *testing.T) {
	t.Parallel()

	built := knowledge.NewGraph()
	listed := func(files ...string) types.KnowledgeStaleIndex {
		return types.KnowledgeStaleIndex{Project: ".", Language: "go", Op: "scip", Changed: files, ChangedCount: len(files)}
	}
	for name, tc := range map[string]struct {
		paths  []string
		stale  types.KnowledgeStaleIndex
		reads  bool
		domain bool
	}{
		"changes elsewhere":            {paths: []string{"internal/job/**"}, stale: listed("run.go", "symbol.go"), reads: true},
		"a change inside a glob":       {paths: []string{"internal/job/**"}, stale: listed("internal/job/plan.go")},
		"a change to a claimed file":   {paths: []string{"run.go#Run"}, stale: listed("run.go")},
		"no files named":               {paths: []string{"internal/job/**"}, stale: types.KnowledgeStaleIndex{Project: "."}},
		"a list short of its count":    {paths: []string{"internal/job/**"}, stale: types.KnowledgeStaleIndex{Project: ".", Changed: []string{"run.go"}, ChangedCount: 2}},
		"no write paths to clear":      {stale: listed("run.go")},
		"the domain graph, elsewhere":  {paths: []string{"docs/**"}, stale: listed("run.go"), reads: true, domain: true},
		"the domain graph, inside one": {paths: []string{"docs/**"}, stale: listed("docs/x.md"), domain: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			loads := 0
			load := func(context.Context) (*knowledge.Graph, error) { loads++; return built, nil }
			graph := &jobGraph{
				stale:  func(context.Context) []types.KnowledgeStaleIndex { return []types.KnowledgeStaleIndex{tc.stale} },
				build:  load,
				domain: load,
			}
			var got *knowledge.Graph
			if tc.domain {
				got = graph.loadDomain(t.Context(), tc.paths)
			} else {
				got = graph.loadScoped(tc.paths)(t.Context())
			}
			assert.Equal(t, tc.reads, got != nil)
			assert.Equal(t, tc.reads, loads == 1)
			if tc.reads {
				assert.Empty(t, graph.staleIndexes(t.Context()), "an index that kept nothing from the read names no ungraded goal")
			} else {
				assert.Equal(t, []types.KnowledgeStaleIndex{tc.stale}, graph.staleIndexes(t.Context()))
			}
		})
	}
}

// The sibling tests a fork adds to a row are recorded as write paths and as soft paths, so
// they overlap no other row until their holder writes one.
func TestExtendedFootprintRecordsSiblingTestsAsSoftPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, rel := range []string{"cli/registry.go", "cli/registry_test.go"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, "cli"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, rel), []byte(rel), 0o644))
	}
	g := knowledge.NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "file:cli/registry.go", Kind: types.KindFile, Label: "cli/registry.go"})
	g.AddNode(types.KnowledgeNode{ID: "symbol:cli.Registry", Kind: types.KindSymbol, Label: "Registry", Source: "cli/registry.go:3"})
	g.AddEdge(types.KnowledgeEdge{Source: "file:cli/registry.go", Target: "symbol:cli.Registry", Relation: types.RelationDefines})
	g.AddEdge(types.KnowledgeEdge{Source: "file:cli/registry_test.go", Target: "symbol:cli.Registry", Relation: types.RelationReferences, Provenance: "scip count=2"})
	graph := &jobGraph{
		rebuild: true,
		build:   func(context.Context) (*knowledge.Graph, error) { return g, nil },
	}
	store := job.NewStore(job.Location{Root: root, StateBase: t.TempDir(), CacheDir: t.TempDir(), Actor: &job.Actor{}})
	ws := job.Workspace{Projects: []*types.Project{{Path: ".", Sources: types.MustParseGlobs("**/*.go")}}}

	rec, notes := extendedFootprint(t.Context(), store, graph, root,
		types.Declaration{ID: "w", WritePaths: []string{"cli/registry.go"}}, ws)

	assert.Equal(t, types.Declaration{
		ID:         "w",
		WritePaths: []string{"cli/registry.go", "cli/registry_test.go"},
		SoftPaths:  []string{"cli/registry_test.go"},
	}, rec)
	assert.Len(t, notes, 1)
}

// A lazy graph builds at most once per command: a load, a failed one and a skipped one
// each answer every later get alike without asking again.
func TestLazyGraphLoadsOnce(t *testing.T) {
	t.Parallel()

	built := knowledge.NewGraph()
	for name, tc := range map[string]struct {
		skip  bool
		err   error
		want  *knowledge.Graph
		loads int
	}{
		"a load":         {want: built, loads: 1},
		"a failed load":  {err: errors.New("no index"), loads: 1},
		"a skipped load": {skip: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var lazy lazyGraph
			var skips, loads int
			skip := func(context.Context) bool { skips++; return tc.skip }
			build := func(context.Context) (*knowledge.Graph, error) {
				loads++
				if tc.err != nil {
					return nil, tc.err
				}
				return built, nil
			}
			for range 3 {
				assert.Same(t, tc.want, lazy.get(t.Context(), skip, build))
			}
			assert.Equal(t, []int{1, tc.loads}, []int{skips, loads})
		})
	}
}

func TestJobGraphOpensAnotherCheckoutInItsOwnScope(t *testing.T) {
	process, other := t.TempDir(), t.TempDir()
	for _, dir := range []string{process, other} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "magusfile.buzz"), nil, 0o644))
	}
	opened := magusOpened
	t.Cleanup(func() { magusOpened = opened })
	magusOpened = openedRoot{override: process, root: process}
	magusOnce.Do(func() {})

	graphIn := graphsByTree(process, newJobGraph(process, true), true)
	graph := graphIn(other)

	require.NotPanics(t, func() { graph.staleIndexes(t.Context()) })
	require.NotPanics(t, func() { graph.loadScoped(nil)(t.Context()) })
	require.NotPanics(t, func() { graph.loadDomain(t.Context(), nil) })
	assert.Equal(t, openedRoot{override: process, root: process}, magusOpened)
}

type PlanFigureSuite struct{ suite.Suite }

func TestPlanFigureSuite(t *testing.T) { suite.Run(t, new(PlanFigureSuite)) }

func planRow(id string, created int64, state types.JobState) types.Job {
	return types.Job{ID: id, Created: created, State: state}
}

// planOfSixteen is shaped like a real wave: an integrator merging ten
// jobs, a forked pair, a dependency and three loners.
func planOfSixteen() []types.Job {
	var rows []types.Job
	merge := planRow("integrate-wave", 100, types.StateRunning)
	for i := range 10 {
		id := fmt.Sprintf("wave-%02d", i)
		merge.Integrates = append(merge.Integrates, id)
		rows = append(rows, planRow(id, int64(i+1), types.StateExited))
	}
	rows = append(rows, merge)
	lead := planRow("lead", 20, types.StateRunning)
	scout := planRow("lead/scout", 21, types.StateExited)
	scout.Parent = "lead"
	after := planRow("after-lead", 22, types.StateDeclared)
	after.DependsOn = []string{"lead"}
	rows = append(rows, lead, scout, after)
	for i := range 2 {
		rows = append(rows, planRow(fmt.Sprintf("loner-%d", i), int64(30+i), types.StateExited))
	}
	return rows
}

// drawnJobs is every job the figures draw as a job box, with how often.
func drawnJobs(figs []*planFigure) map[string]int {
	seen := map[string]int{}
	for _, f := range figs {
		for _, n := range f.Nodes {
			if n.Group == nil {
				seen[n.Job]++
			}
		}
	}
	return seen
}

func (s *PlanFigureSuite) TestASmallPlanIsOneFigureWithEveryEdgeKind() {
	lead := planRow("lead", 1, types.StateRunning)
	child := planRow("child", 2, types.StateDeclared)
	child.Parent = "lead"
	merge := planRow("merge", 3, types.StateDeclared)
	merge.Integrates = []string{"child"}
	merge.DependsOn = []string{"lead"}

	figs := newJobPlan([]types.Job{merge, child, lead}).figures()

	s.Require().Len(figs, 1)
	s.Equal("plan", figs[0].ID)
	s.Equal("Job plan: 3 jobs", figs[0].Title)
	s.Equal([]string{"lead", "child", "merge"}, figs[0].Jobs)
	s.Equal([]planEdge{
		{Src: "lead", Dst: "child", Kind: planForked},
		{Src: "merge", Dst: "child", Kind: planMerges},
		{Src: "lead", Dst: "merge", Kind: planAfter},
	}, figs[0].Edges)
}

func (s *PlanFigureSuite) TestAnIntegratorGroupsTheJobsItMerges() {
	figs := newJobPlan(planOfSixteen()).figures()

	s.Equal("plan", figs[0].ID)
	var names []string
	for _, n := range figs[0].Nodes {
		names = append(names, n.Name)
	}
	s.Equal([]string{"lead", "after-lead", "loner-0", "loner-1", "integrate-wave"}, names)
	s.Equal("plan-lead", figs[0].Nodes[0].Draws)
	s.Equal("plan-integrate-wave", figs[0].Nodes[4].Draws)
	s.Equal([]planEdge{{Src: "lead", Dst: "after-lead", Kind: planAfter}}, figs[0].Edges)
}

func (s *PlanFigureSuite) TestEveryFigureFitsTheBudgetsAndDrawsEachJobOnce() {
	rows := planOfSixteen()
	for i := range 70 {
		rows = append(rows, planRow(fmt.Sprintf("solo-%02d", i), int64(200+i), types.StatePass))
	}

	figs := newJobPlan(rows).figures()

	for _, f := range figs {
		s.LessOrEqual(len(f.Nodes), planMaxNodes, f.ID)
		s.LessOrEqual(len(f.Edges), planMaxEdges, f.ID)
	}
	seen := drawnJobs(figs)
	s.Len(seen, len(rows))
	for id, n := range seen {
		s.Equal(1, n, id)
	}
}

func (s *PlanFigureSuite) TestAGroupPastTheBudgetDrawsItsTopAndBundles() {
	figs := newJobPlan(planOfSixteen()).figures()

	var wave *planFigure
	for _, f := range figs {
		if f.ID == "plan-integrate-wave" {
			wave = f
		}
	}
	s.Require().NotNil(wave)
	s.Equal("integrate-wave", wave.Nodes[0].Job)
	s.Len(wave.Nodes, planMaxNodes)
	s.Len(wave.Jobs, 11)
	for _, e := range wave.Edges {
		s.Equal(planEdge{Src: "integrate-wave", Dst: e.Dst, Kind: planMerges}, e)
	}
}

func (s *PlanFigureSuite) TestTheSplitIgnoresRowOrder() {
	rows := planOfSixteen()
	reversed := slices.Clone(rows)
	slices.Reverse(reversed)

	s.Equal(newJobPlan(rows).figures(), newJobPlan(reversed).figures())
}

func (s *PlanFigureSuite) TestAMergeCycleStillEndsInATree() {
	a := planRow("a", 1, types.StateRunning)
	a.Integrates = []string{"b"}
	b := planRow("b", 2, types.StateRunning)
	b.Integrates = []string{"a"}

	p := newJobPlan([]types.Job{a, b})

	s.Equal([]string{"a"}, p.roots)
	s.Equal([]planEdge{{Src: "a", Dst: "b", Kind: planMerges}, {Src: "b", Dst: "a", Kind: planMerges}}, p.edges)
}

func (s *PlanFigureSuite) TestTheSVGsDrawWithoutARefusalAndRepeatByteForByte() {
	p := newJobPlan(planOfSixteen())
	first, second := s.T().TempDir(), s.T().TempDir()

	paths, err := writePlanSVGs(s.T().Context(), p, p.figures(), first)
	s.Require().NoError(err)
	again, err := writePlanSVGs(s.T().Context(), p, p.figures(), second)
	s.Require().NoError(err)

	s.Require().Len(again, len(paths))
	s.Equal(filepath.Join(first, "plan.svg"), paths[0])
	for i := range paths {
		a, err := os.ReadFile(paths[i])
		s.Require().NoError(err)
		b, err := os.ReadFile(again[i])
		s.Require().NoError(err)
		s.Contains(string(a), "<svg")
		s.Equal(string(a), string(b), paths[i])
	}
}
