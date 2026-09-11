package ledger

import (
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// Brief is the worker brief for one lease: the declared row, the workspace facts magus
// resolved against it, and the commands the worker starts with.
//
// CONTEXT, NEVER A VERDICT, which is the shape `magus diff --prompt` already has. magus
// assembles what it holds and a person or an orchestrator hands it on; nothing here calls
// a model and nothing here decides what the worker should do.
//
// DETERMINISM IS THE REQUIREMENT, not a nicety. A brief an orchestrating model paraphrases
// diverges between two workers reading one contract, which is what seven hand-typed briefs
// did on 2026-09-09: the gate leaked into every one of them and two units disagreed about
// a dedup key. Two renders of one row are byte-identical or the ledger has stopped being
// the single statement of the plan.
type Brief struct {
	Lease types.Lease `json:"lease" yaml:"lease"`
	// Bind is the single command that binds this lease to the worker's checkout. It is
	// the only instruction the brief gives that is not already a field of the row: every
	// rule the worker owes rides on the lease, and the guard reads them from there.
	Bind string `json:"bind" yaml:"bind"`
	// Evidence is one line per write path the knowledge graph resolved, in declaration
	// order. A path the graph does not know is ABSENT here rather than reported as zero:
	// an unknown blast radius and a blast radius of zero are different facts, and the
	// second one would read as permission to edit freely.
	Evidence []BriefEvidence `json:"evidence,omitempty" yaml:"evidence,omitempty"`
	// GraphCold marks a brief rendered against no graph at all, so empty Evidence reads
	// as "not asked" rather than "asked, and nothing depends on any of this".
	GraphCold bool `json:"graph_cold,omitempty" yaml:"graph_cold,omitempty"`
	// Projects are the workspace projects the write paths reach, the same set
	// `magus affected` computes from those paths. It is what makes the two derived
	// lists below readable: a boundary without the projects it came from is a fence
	// with no map.
	Projects []string `json:"projects,omitempty" yaml:"projects,omitempty"`
	// DerivedDenyPaths is the boundary the WORKSPACE puts on this lease, kept apart from
	// the row's own DenyPaths. Those are what an orchestrator remembered to write
	// down; these hold whether anybody wrote them down or not, which is why a brief
	// that carried only the declared list handed workers a boundary its author's
	// memory had bounded.
	DerivedDenyPaths []BriefBoundary `json:"derived_forbidden,omitempty" yaml:"derived_forbidden,omitempty"`
	// WorkspaceCold marks a brief rendered without a loadable workspace, so an empty
	// DerivedDenyPaths reads as "not asked". The row alone carries the goal, the boundary
	// and the check, and a worker in a tree whose magusfile is mid-edit still needs them.
	WorkspaceCold bool `json:"workspace_cold,omitempty" yaml:"workspace_cold,omitempty"`
	// Affinity is the co-change evidence for the lease's projects against projects it
	// does NOT own. A WARNING and never a boundary: the skill reads strong hidden
	// affinity as a reason to reduce parallelism, which is the orchestrator's call to
	// make and not a path this worker is refused.
	Affinity []BriefAffinity `json:"affinity,omitempty" yaml:"affinity,omitempty"`
	// Bootstrap is what the worker runs before it edits anything, one command with the
	// reason it exists. Commands, not rules: the guard states every rule at the moment a
	// command meets it, and a rules block in a brief is a paragraph a worker reads once
	// and a denial is a sentence it reads when it matters.
	Bootstrap []BriefStep `json:"bootstrap,omitempty" yaml:"bootstrap,omitempty"`
}

// BriefStep is one bootstrap command and why it is there.
//
// The pair rather than a rendered line, because the two halves have different readers: a
// harness composes a prompt from Run, and a person reads Why to see what the step is for.
// A line carrying both is one a skimmer copies with the parenthetical still in it.
type BriefStep struct {
	Run string `json:"run" yaml:"run"`
	Why string `json:"why" yaml:"why"`
}

// BriefEvidence is what the knowledge graph knows about one write path: the node it
// resolved to and how many nodes can transitively reach it, the same count
// `magus explain` prints.
type BriefEvidence struct {
	Path        string `json:"path"         yaml:"path"`
	Node        string `json:"node"         yaml:"node"`
	BlastRadius int    `json:"blast_radius" yaml:"blast_radius"`
}

// BriefBoundary is one path the workspace keeps out of a lease's reach, with the
// declaration that keeps it there.
//
// The reason is the load-bearing half. A bare path reads as an arbitrary fence, and a
// worker cannot tell a generated file it should REGENERATE from a file another live
// lease is holding, which are opposite instructions.
type BriefBoundary struct {
	Path   string `json:"path"   yaml:"path"`
	Reason string `json:"reason" yaml:"reason"`
}

// BriefAffinity is one project pair that changes together while the lease owns only one
// side of it, and neither project declares a dependency on the other.
//
// HIDDEN coupling only. A pair that changes together and says so in its declarations is
// the workspace working as designed, and the root project of a monorepo co-changes with
// everything; reporting those turns a warning list into a census. What is worth a
// worker's attention is coupling no declaration would have told it about, which is the
// pair a partition is most likely to have split wrongly.
type BriefAffinity struct {
	Project string `json:"project" yaml:"project"`
	With    string `json:"with"    yaml:"with"`
	Commits int    `json:"commits" yaml:"commits"`
}

// BriefFacts is what the WORKSPACE contributes to a brief: everything [NewBrief] cannot
// derive from the row alone, because it needs a knowledge graph and a loadable workspace.
// The zero value is a brief rendered from the row and nothing else.
type BriefFacts struct {
	Evidence         []BriefEvidence
	GraphCold        bool
	WorkspaceCold    bool
	Projects         []string
	DerivedDenyPaths []BriefBoundary
	Affinity         []BriefAffinity
}

// NewBrief renders the brief for one row: the bind line and the bootstrap steps the id
// determines, plus what the workspace contributed. Both lines are rendered here and
// nowhere else, so the brief, its golden test, and any caller quoting a line cannot drift.
//
// The steps are magus's OWN commands, which is why they are computed rather than read
// from a workspace template: every one of them is a magus verb this binary defines, and a
// per-workspace copy of them is a copy to keep true.
func NewBrief(row types.Lease, facts BriefFacts) Brief {
	return Brief{
		Lease:            row,
		Bind:             hint.SessionLease.With(row.ID),
		Evidence:         facts.Evidence,
		GraphCold:        facts.GraphCold,
		WorkspaceCold:    facts.WorkspaceCold,
		Projects:         facts.Projects,
		DerivedDenyPaths: facts.DerivedDenyPaths,
		Affinity:         facts.Affinity,
		Bootstrap: []BriefStep{
			{Run: "git status --short", Why: "work in your own worktree and confirm it is clean before you edit"},
			{Run: hint.SessionLease.With(row.ID), Why: "bind the lease so every lease-scoped rule grades your writes here"},
			{
				Run: hint.VCSCheckpoint.With("-o name"),
				Why: "record the base you landed on, and register what it prints on lease " + row.ID +
					" with the " + hint.ToolLedger.String() + " tool (op register); writes are denied until the lease has one",
			},
		},
	}
}

// String renders the brief. The order is fixed and nothing outside it is printed: a
// section with nothing in it is dropped, so a worker never reads a heading that grants it
// room the row did not.
//
// The narrowest section is the validation one, and it is the whole point of rendering at
// all. Naming one check and no others is what stops `ci` leaking into a worker's brief.
func (b Brief) String() string {
	var s strings.Builder
	fmt.Fprintf(&s, "lease: %s\n", b.Lease.ID)
	fmt.Fprintf(&s, "bind: %s\n", b.Bind)

	writeBlock(&s, "goal and acceptance criteria", b.Lease.Goal)
	writeList(&s, "write paths", b.Lease.WritePaths)
	writeList(&s, "deny paths", b.Lease.DenyPaths)
	writeList(&s, "projects", b.Projects)

	switch {
	case b.WorkspaceCold:
		writeList(&s, "deny paths the workspace declares",
			[]string{"none: this workspace would not load, so nothing was derived from it"})
	case len(b.DerivedDenyPaths) > 0:
		lines := make([]string, len(b.DerivedDenyPaths))
		for i, d := range b.DerivedDenyPaths {
			lines[i] = fmt.Sprintf("%s: %s", d.Path, d.Reason)
		}
		writeList(&s, "deny paths the workspace declares", lines)
	}

	if len(b.Affinity) > 0 {
		lines := make([]string, len(b.Affinity))
		for i, a := range b.Affinity {
			lines[i] = fmt.Sprintf("%s changes with %s in %d commits, and neither declares a dependency on the other", a.Project, a.With, a.Commits)
		}
		writeList(&s, "affinity warnings", lines)
	}

	switch {
	case b.GraphCold:
		writeList(&s, "graph evidence", []string{"none: the knowledge graph is cold (" + hint.GraphBuild.String() + ")"})
	case len(b.Evidence) > 0:
		lines := make([]string, len(b.Evidence))
		for i, e := range b.Evidence {
			lines[i] = fmt.Sprintf("%s -> %s, blast radius %d", e.Path, e.Node, e.BlastRadius)
		}
		writeList(&s, "graph evidence", lines)
	}

	writeBlock(&s, "validation, the only check you run", b.Lease.Validation)
	writeList(&s, "depends on", b.Lease.DependsOn)

	if len(b.Bootstrap) > 0 {
		fmt.Fprint(&s, "\nbootstrap\n")
		for _, step := range b.Bootstrap {
			fmt.Fprintf(&s, "  %s\n    %s\n", step.Why, step.Run)
		}
	}
	return s.String()
}

// writeBlock emits a heading and a verbatim body. Verbatim is deliberate for the goal:
// acceptance criteria the orchestrator wrote are the contract, and reflowing them is the
// paraphrase this whole type exists to remove.
func writeBlock(s *strings.Builder, heading, body string) {
	if strings.TrimSpace(body) == "" {
		return
	}
	fmt.Fprintf(s, "\n%s\n%s\n", heading, strings.TrimRight(body, "\n"))
}

func writeList(s *strings.Builder, heading string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(s, "\n%s\n", heading)
	for _, it := range items {
		fmt.Fprintf(s, "  %s\n", it)
	}
}
