package job

import (
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/diagram"
	"github.com/egladman/magus/libs/safetyscissors/job"
)

type PlanDiagramSuite struct{ suite.Suite }

func TestPlanDiagramSuite(t *testing.T) { suite.Run(t, new(PlanDiagramSuite)) }

func planRow(id string, created int64, state job.State) job.Job {
	return job.Job{ID: id, Created: created, State: state}
}

// planOfSixteen is shaped like a real wave: an integrator merging ten
// jobs, a forked pair, a dependency and three loners.
func planOfSixteen() []job.Job {
	var rows []job.Job
	merge := planRow("integrate-wave", 100, job.StateRunning)
	for i := range 10 {
		id := fmt.Sprintf("wave-%02d", i)
		merge.Integrates = append(merge.Integrates, id)
		rows = append(rows, planRow(id, int64(i+1), job.StateExited))
	}
	rows = append(rows, merge)
	lead := planRow("lead", 20, job.StateRunning)
	scout := planRow("lead/scout", 21, job.StateExited)
	scout.Parent = "lead"
	after := planRow("after-lead", 22, job.StateDeclared)
	after.DependsOn = []string{"lead"}
	rows = append(rows, lead, scout, after)
	for i := range 2 {
		rows = append(rows, planRow(fmt.Sprintf("loner-%d", i), int64(30+i), job.StateExited))
	}
	return rows
}

// drawnJobs is every job the diagrams draw as a job box, with how often.
func drawnJobs(figs []*PlanDiagram) map[string]int {
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

func (s *PlanDiagramSuite) TestASmallPlanIsOneDiagramWithEveryEdgeKind() {
	lead := planRow("lead", 1, job.StateRunning)
	child := planRow("child", 2, job.StateDeclared)
	child.Parent = "lead"
	merge := planRow("merge", 3, job.StateDeclared)
	merge.Integrates = []string{"child"}
	merge.DependsOn = []string{"lead"}

	figs := NewPlan([]job.Job{merge, child, lead}).Diagrams()

	s.Require().Len(figs, 1)
	s.Equal(&PlanDiagram{
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

func (s *PlanDiagramSuite) TestAnIntegratorGroupsTheJobsItMerges() {
	figs := NewPlan(planOfSixteen()).Diagrams()

	var names []string
	for _, n := range figs[0].Nodes {
		names = append(names, n.Name)
	}
	s.Equal([]string{"lead", "after-lead", "loner-0", "loner-1", "integrate-wave"}, names)
	s.Equal("plan-lead", figs[0].Nodes[0].Draws)
	s.Equal("plan-integrate-wave", figs[0].Nodes[4].Draws)
	s.Equal([]planEdge{{Src: "lead", Dst: "after-lead", Kind: planAfter}}, figs[0].Edges)
}

func (s *PlanDiagramSuite) TestEveryDiagramFitsTheBudgetsAndDrawsEachJobOnce() {
	rows := planOfSixteen()
	for i := range 70 {
		rows = append(rows, planRow(fmt.Sprintf("solo-%02d", i), int64(200+i), job.StatePass))
	}

	figs := NewPlan(rows).Diagrams()

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

func (s *PlanDiagramSuite) TestAGroupPastTheBudgetDrawsItsTopAndBundles() {
	figs := NewPlan(planOfSixteen()).Diagrams()

	var wave *PlanDiagram
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

func (s *PlanDiagramSuite) TestTheSplitIgnoresRowOrder() {
	rows := planOfSixteen()
	reversed := slices.Clone(rows)
	slices.Reverse(reversed)

	s.Equal(NewPlan(rows).Diagrams(), NewPlan(reversed).Diagrams())
}

func (s *PlanDiagramSuite) TestAMergeCycleStillEndsInATree() {
	a := planRow("a", 1, job.StateRunning)
	a.Integrates = []string{"b"}
	b := planRow("b", 2, job.StateRunning)
	b.Integrates = []string{"a"}

	p := NewPlan([]job.Job{a, b})

	s.Equal([]string{"a", "b"}, p.subtree("a"))
	s.Equal([]planEdge{{Src: "a", Dst: "b", Kind: planMerges}, {Src: "b", Dst: "a", Kind: planMerges}}, p.edges)
}

func (s *PlanDiagramSuite) TestTheRecordLinksAGroupAndDashesAnEdgeOfAfter() {
	figs := NewPlan(planOfSixteen()).Diagrams()
	holder := func(job.Job) string { return "me" }

	rec := NewPlan(planOfSixteen()).DiagramRecord(figs[0], holder)

	s.Equal("plan", rec.ID)
	s.Require().Len(rec.Boxes, len(figs[0].Nodes))
	open := diagram.Look("optional")
	s.Equal(&diagram.Actor{Name: "lead", Sub: "2 jobs, 1 unfinished", Tag: "group", Link: "plan-lead.svg", Look: &open}, rec.Boxes[0].Actor)
	s.Len(rec.Legends, 1)
	s.Require().Len(rec.Flows, 1)
	s.Equal("after", rec.Flows[0].Label)
}
