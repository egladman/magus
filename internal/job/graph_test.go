package job

import (
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/figure"
	"github.com/egladman/magus/types"
)

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
func drawnJobs(figs []*PlanFigure) map[string]int {
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

	figs := NewPlan([]types.Job{merge, child, lead}).Figures()

	s.Require().Len(figs, 1)
	s.Equal(&PlanFigure{
		ID:    "plan",
		Title: "Job plan: 3 jobs",
		Jobs:  []string{"lead", "child", "merge"},
		Nodes: []planNode{{Name: "lead", Job: "lead"}, {Name: "child", Job: "child"}, {Name: "merge", Job: "merge"}},
		Edges: []planEdge{
			{Src: "lead", Dst: "child", Kind: planForked},
			{Src: "merge", Dst: "child", Kind: planMerges},
			{Src: "lead", Dst: "merge", Kind: planAfter},
		},
	}, figs[0])
}

func (s *PlanFigureSuite) TestAnIntegratorGroupsTheJobsItMerges() {
	figs := NewPlan(planOfSixteen()).Figures()

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

	figs := NewPlan(rows).Figures()

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
	figs := NewPlan(planOfSixteen()).Figures()

	var wave *PlanFigure
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

	s.Equal(NewPlan(rows).Figures(), NewPlan(reversed).Figures())
}

func (s *PlanFigureSuite) TestAMergeCycleStillEndsInATree() {
	a := planRow("a", 1, types.StateRunning)
	a.Integrates = []string{"b"}
	b := planRow("b", 2, types.StateRunning)
	b.Integrates = []string{"a"}

	p := NewPlan([]types.Job{a, b})

	s.Equal([]string{"a", "b"}, p.subtree("a"))
	s.Equal([]planEdge{{Src: "a", Dst: "b", Kind: planMerges}, {Src: "b", Dst: "a", Kind: planMerges}}, p.edges)
}

func (s *PlanFigureSuite) TestTheRecordLinksAGroupAndDashesAnEdgeOfAfter() {
	figs := NewPlan(planOfSixteen()).Figures()
	holder := func(types.Job) string { return "me" }

	rec := NewPlan(planOfSixteen()).FigureRecord(figs[0], holder)

	s.Equal("plan", rec.ID)
	s.Require().Len(rec.Boxes, len(figs[0].Nodes))
	open := figure.Look("optional")
	s.Equal(&figure.Actor{Name: "lead", Sub: "2 jobs, 1 unfinished", Tag: "group", Link: "plan-lead.svg", Look: &open}, rec.Boxes[0].Actor)
	s.Len(rec.Legends, 1)
	s.Require().Len(rec.Flows, 1)
	s.Equal("after", rec.Flows[0].Label)
}
