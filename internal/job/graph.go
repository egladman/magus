package job

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/figure"
	"github.com/egladman/magus/libs/text/plural"
	"github.com/egladman/magus/types"
)

// The budgets magus/figure holds one figure to.
const (
	planMaxNodes = 9
	planMaxEdges = 12
)

// planEdgeKind is what joins two jobs. Where several kinds join
// two boxes, the first in planEdgeKinds is drawn.
type planEdgeKind string

const (
	planForked planEdgeKind = "forked"
	planMerges planEdgeKind = "merges"
	planAfter  planEdgeKind = "after"
)

var planEdgeKinds = []planEdgeKind{planForked, planMerges, planAfter}

// planEdge runs from the forking, merging or depended-on job.
type planEdge struct {
	Src  string       `json:"src"`
	Dst  string       `json:"dst"`
	Kind planEdgeKind `json:"kind"`
}

// planNode is one box: a job, or a group a figure of its own draws.
type planNode struct {
	Name  string      `json:"name"`
	Job   string      `json:"job,omitempty"`
	Group *PlanFigure `json:"-"`
	Draws string      `json:"figure,omitempty"`
}

// PlanFigure is one figure of the plan. Jobs counts its groups' jobs.
type PlanFigure struct {
	ID    string     `json:"id"`
	Title string     `json:"title"`
	Jobs  []string   `json:"jobs"`
	Nodes []planNode `json:"nodes"`
	Edges []planEdge `json:"edges"`
}

// Plan is the job rows as a forest. A job hangs from its fork
// parent, else from the first integrator that merges it.
type Plan struct {
	byID     map[string]types.Job
	order    []string
	children map[string][]string
	roots    []string
	edges    []planEdge
}

// NewPlan lays rows out as a forest, whatever order they come in.
func NewPlan(rows []types.Job) *Plan {
	rows = slices.Clone(rows)
	slices.SortStableFunc(rows, func(a, b types.Job) int {
		return cmp.Or(cmp.Compare(a.Created, b.Created), cmp.Compare(a.ID, b.ID))
	})
	p := &Plan{byID: map[string]types.Job{}, children: map[string][]string{}}
	for _, r := range rows {
		if _, dup := p.byID[r.ID]; !dup {
			p.byID[r.ID] = r
			p.order = append(p.order, r.ID)
		}
	}
	p.edges = p.collectEdges()
	p.hang(p.parents())
	return p
}

// collectEdges is each fork, merge and dependency between known jobs.
func (p *Plan) collectEdges() []planEdge {
	var edges []planEdge
	seen := map[planEdge]bool{}
	add := func(e planEdge) {
		_, src := p.byID[e.Src]
		_, dst := p.byID[e.Dst]
		if src && dst && e.Src != e.Dst && !seen[e] {
			seen[e] = true
			edges = append(edges, e)
		}
	}
	for _, id := range p.order {
		r := p.byID[id]
		add(planEdge{Src: r.Parent, Dst: id, Kind: planForked})
		for _, merged := range r.Integrates {
			add(planEdge{Src: id, Dst: merged, Kind: planMerges})
		}
		for _, dep := range r.DependsOn {
			add(planEdge{Src: dep, Dst: id, Kind: planAfter})
		}
	}
	return edges
}

// parents takes forks before merges and skips an edge that closes a cycle.
func (p *Plan) parents() map[string]string {
	parent := map[string]string{}
	reaches := func(from, to string) bool {
		for at := from; at != ""; at = parent[at] {
			if at == to {
				return true
			}
		}
		return false
	}
	for _, kind := range []planEdgeKind{planForked, planMerges} {
		for _, e := range p.edges {
			if e.Kind == kind && parent[e.Dst] == "" && !reaches(e.Src, e.Dst) {
				parent[e.Dst] = e.Src
			}
		}
	}
	return parent
}

func (p *Plan) hang(parent map[string]string) {
	for _, id := range p.order {
		if up := parent[id]; up != "" {
			p.children[up] = append(p.children[up], id)
		} else {
			p.roots = append(p.roots, id)
		}
	}
}

func (p *Plan) subtree(id string) []string {
	out := append(make([]string, 0, len(p.children[id])+1), id)
	for _, c := range p.children[id] {
		out = append(out, p.subtree(c)...)
	}
	return out
}

// Figures is the plan split to fit the budgets, the overview first.
// A group's figure follows the figure that draws it as a box.
func (p *Plan) Figures() []*PlanFigure {
	title := fmt.Sprintf("Job plan: %d %s", len(p.order), plural.Word(len(p.order), "job", "jobs"))
	out := numberFigures(p.group(title, p.roots), "plan", map[string]int{})
	for _, f := range out {
		for i, n := range f.Nodes {
			if n.Group != nil {
				f.Nodes[i].Draws = n.Group.ID
			}
		}
	}
	return out
}

// numberFigures gives f and the group figures under it their ids, depth
// first. An id taken before gets -2, -3. taken counts them across the walk.
func numberFigures(f *PlanFigure, id string, taken map[string]int) []*PlanFigure {
	taken[id]++
	if n := taken[id]; n > 1 {
		id = fmt.Sprintf("%s-%d", id, n)
	}
	f.ID = id
	out := []*PlanFigure{f}
	for _, n := range f.Nodes {
		if n.Group != nil {
			out = append(out, numberFigures(n.Group, "plan-"+planSlug(n.Name), taken)...)
		}
	}
	return out
}

// group draws the subtrees under tops: every job when they fit, else
// the top job and a box per child subtree, bundled by fit.
func (p *Plan) group(title string, tops []string) *PlanFigure {
	subtrees := make([][]string, len(tops))
	for i, t := range tops {
		subtrees[i] = p.subtree(t)
	}
	jobs := slices.Concat(subtrees...)
	f := &PlanFigure{Title: title, Jobs: jobs}
	flat := make([]planNode, len(jobs))
	for i, id := range jobs {
		flat[i] = planNode{Name: id, Job: id}
	}
	if edges := p.between(flat); len(flat) <= planMaxNodes && len(edges) <= planMaxEdges {
		f.Nodes, f.Edges = flat, edges
		return f
	}
	head, rest := p.headAndRest(tops)
	parts := make([]planNode, len(rest))
	for i, t := range rest {
		parts[i] = p.box(t)
	}
	f.Nodes = p.fit(head, parts)
	f.Edges = p.between(f.Nodes)
	return f
}

// headAndRest keeps a lone top as its own box and boxes its children.
func (p *Plan) headAndRest(tops []string) (head []planNode, rest []string) {
	if len(tops) != 1 {
		return nil, tops
	}
	return []planNode{{Name: tops[0], Job: tops[0]}}, p.children[tops[0]]
}

// box is the subtree under id as one box.
func (p *Plan) box(id string) planNode {
	if len(p.children[id]) == 0 {
		return planNode{Name: id, Job: id}
	}
	n := len(p.subtree(id))
	return planNode{Name: id, Group: p.group(fmt.Sprintf("%s: %d jobs", id, n), []string{id})}
}

// fit is head then parts, folding the tail of parts into bundles when
// they overflow. It takes the fewest bundles that each fit whole, and
// more while the edges overflow. A bundle's figure fits it the same way.
func (p *Plan) fit(head, parts []planNode) []planNode {
	nodes := append(slices.Clone(head), parts...)
	if len(nodes) <= planMaxNodes && len(p.between(nodes)) <= planMaxEdges {
		return nodes
	}
	room := planMaxNodes - len(head)
	least := max(1, min(room, ceilDiv(len(parts)-room, planMaxNodes-1)))
	if len(head) == 0 {
		least = max(least, 2)
	}
	for bundles := least; bundles <= room; bundles++ {
		loose := room - bundles
		if loose >= len(parts) {
			continue
		}
		nodes = p.bundle(head, parts, loose, bundles)
		if len(p.between(nodes)) <= planMaxEdges {
			break
		}
	}
	return nodes
}

// bundle keeps the first loose parts as boxes and chunks the rest.
func (p *Plan) bundle(head, parts []planNode, loose, bundles int) []planNode {
	nodes := append(slices.Clone(head), parts[:loose]...)
	for chunk := range slices.Chunk(parts[loose:], ceilDiv(len(parts)-loose, bundles)) {
		nodes = append(nodes, p.bundled(chunk))
	}
	return nodes
}

func ceilDiv(a, b int) int {
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

func (p *Plan) bundled(parts []planNode) planNode {
	if len(parts) == 1 {
		return parts[0]
	}
	name := fmt.Sprintf("%s +%d", parts[0].Name, len(parts)-1)
	f := &PlanFigure{Title: name, Nodes: p.fit(nil, parts)}
	f.Edges = p.between(f.Nodes)
	for _, n := range parts {
		f.Jobs = append(f.Jobs, n.jobs()...)
	}
	return planNode{Name: name, Group: f}
}

func (n planNode) jobs() []string {
	if n.Group != nil {
		return n.Group.Jobs
	}
	return []string{n.Job}
}

// between is the plan's edges among nodes, one per ordered pair. An
// edge with an end in no box belongs to a figure further up.
func (p *Plan) between(nodes []planNode) []planEdge {
	at := map[string]string{}
	for _, n := range nodes {
		for _, id := range n.jobs() {
			at[id] = n.Name
		}
	}
	type pair struct{ src, dst string }
	kinds := map[pair]planEdgeKind{}
	var order []pair
	for _, e := range p.edges {
		k := pair{at[e.Src], at[e.Dst]}
		if k.src == "" || k.dst == "" || k.src == k.dst {
			continue
		}
		have, ok := kinds[k]
		if !ok {
			order = append(order, k)
		}
		if !ok || slices.Index(planEdgeKinds, e.Kind) < slices.Index(planEdgeKinds, have) {
			kinds[k] = e.Kind
		}
	}
	out := make([]planEdge, len(order))
	for i, k := range order {
		out[i] = planEdge{Src: k.src, Dst: k.dst, Kind: kinds[k]}
	}
	return out
}

func planSlug(name string) string {
	slug := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		}
		return '-'
	}, name)
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	return strings.Trim(slug, "-")
}

func planUnfinished(state types.JobState) bool {
	return state == types.StateDeclared || state == types.StateRunning
}

func planDash(s string) string {
	if strings.TrimSpace(s) == "" || s == "0" {
		return "-"
	}
	return s
}

// FigureRecord is f as a magus/figure record. A group's box links to
// the file its figure is written to.
func (p *Plan) FigureRecord(f *PlanFigure, holder func(types.Job) string) figure.Figure {
	plain, optional := figure.Look("plain"), figure.Look("optional")
	rec := figure.Figure{
		ID:          f.ID,
		Title:       f.Title,
		Eyebrow:     "job plan",
		Desc:        "A box is a job with its state and holder, or a group another figure draws. A solid edge is a fork or a merge, a dashed one a dependency.",
		Direction:   figure.Direction("down"),
		UnscopedWhy: "a plan's jobs are units of work, not directories",
	}
	actors := map[string]figure.Actor{}
	anyOpen := false
	for _, n := range f.Nodes {
		a, open := p.actor(n, holder, plain, optional)
		anyOpen = anyOpen || open
		actors[n.Name] = a
		rec.Boxes = append(rec.Boxes, figure.Box{Actor: &a})
	}
	for _, e := range f.Edges {
		rec.Flows = append(rec.Flows, planFlow(e, actors))
	}
	if anyOpen {
		rec.Legends = append(rec.Legends, figure.Legend{Look: optional, Label: "not finished"})
	}
	return rec
}

// actor is n as a box, and whether a job it draws is unfinished.
func (p *Plan) actor(n planNode, holder func(types.Job) string, plain, optional figure.Look) (figure.Actor, bool) {
	a := figure.Actor{Name: n.Name, Look: &plain}
	open := 0
	for _, id := range n.jobs() {
		if planUnfinished(p.byID[id].State) {
			open++
		}
	}
	if n.Group != nil {
		a.Sub = fmt.Sprintf("%d jobs, %d unfinished", len(n.Group.Jobs), open)
		a.Tag = "group"
		a.Link = n.Draws + ".svg"
	} else {
		a.Sub = holder(p.byID[n.Job])
		a.Tag = planDash(string(p.byID[n.Job].State))
	}
	if open > 0 {
		a.Look = &optional
	}
	return a, open > 0
}

// planFlow is solid for a fork, labeled for a merge, dashed for a dependency.
func planFlow(e planEdge, actors map[string]figure.Actor) figure.Flow {
	solid, dashed := figure.Stroke("plain"), figure.Stroke("optional")
	src, dst := actors[e.Src], actors[e.Dst]
	fl := figure.Flow{Src: figure.End{Actor: &src}, Dst: figure.End{Actor: &dst}, Stroke: &solid}
	switch e.Kind {
	case planMerges:
		fl.Label = "merges"
	case planAfter:
		fl.Label, fl.Stroke = "after", &dashed
	}
	return fl
}

// Print writes each figure's boxes and edges as text.
func (p *Plan) Print(out io.Writer, figs []*PlanFigure, holder func(types.Job) string) {
	for i, f := range figs {
		if i > 0 {
			fmt.Fprintln(out)
		}
		fmt.Fprintf(out, "%s: %s\n", f.ID, f.Title)
		for _, n := range f.Nodes {
			if n.Group != nil {
				fmt.Fprintf(out, "  %s: %d jobs in %s\n", n.Name, len(n.Group.Jobs), n.Draws)
				continue
			}
			row := p.byID[n.Job]
			fmt.Fprintf(out, "  %s: %s, %s\n", n.Name, planDash(string(row.State)), holder(row))
		}
		for _, e := range f.Edges {
			fmt.Fprintf(out, "  %s -> %s (%s)\n", e.Src, e.Dst, e.Kind)
		}
	}
}
