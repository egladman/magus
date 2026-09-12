package job

import (
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// Terms is what one job grants its holder: the declared job, and the workspace facts
// magus resolved against it.
//
// CONTEXT, NEVER A STATUS, which is the shape `magus diff --prompt` already has. magus
// assembles what it holds and a person or an orchestrator hands it on; nothing here calls
// a model and nothing here decides what the holder should do.
//
// IT CARRIES NO PROCEDURE. Taking a job is `magus job exec`'s work to DO, and the fifty
// lines of "run this, then this" that used to render here were a procedure a reader could
// skip, mistype, or half-follow. A command magus can run itself is not documentation.
//
// DETERMINISM IS THE REQUIREMENT, not a nicety. Terms an orchestrating model paraphrases
// diverge between two holders reading one contract, which is what seven hand-typed briefs
// did on 2026-09-09: the gate leaked into every one of them and two units disagreed about
// a dedup key. Two renders of one job are byte-identical or the store has stopped being
// the single statement of the plan.
type Terms struct {
	Lease types.Job `json:"job" yaml:"job"`
	// Evidence is one line per write path the knowledge graph resolved, in declaration
	// order. A path the graph does not know is ABSENT here rather than reported as zero:
	// an unknown blast radius and a blast radius of zero are different facts, and the
	// second one would read as permission to edit freely.
	Evidence []TermsEvidence `json:"evidence,omitempty" yaml:"evidence,omitempty"`
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
	DerivedDenyPaths []TermsBoundary `json:"derived_deny_paths,omitempty" yaml:"derived_deny_paths,omitempty"`
	// WorkspaceCold marks a brief rendered without a loadable workspace, so an empty
	// DerivedDenyPaths reads as "not asked". The row alone carries the goal, the boundary
	// and the check, and a worker in a tree whose magusfile is mid-edit still needs them.
	WorkspaceCold bool `json:"workspace_cold,omitempty" yaml:"workspace_cold,omitempty"`
	// Affinity is the co-change evidence for the lease's projects against projects it
	// does NOT own. A WARNING and never a boundary: the skill reads strong hidden
	// affinity as a reason to reduce parallelism, which is the orchestrator's call to
	// make and not a path this worker is refused.
	Affinity []TermsAffinity `json:"affinity,omitempty" yaml:"affinity,omitempty"`
}

// TermsEvidence is what the knowledge graph knows about one write path: the node it
// resolved to and how many nodes can transitively reach it, the same count
// `magus explain` prints.
type TermsEvidence struct {
	Path        string `json:"path"         yaml:"path"`
	Node        string `json:"node"         yaml:"node"`
	BlastRadius int    `json:"blast_radius" yaml:"blast_radius"`
}

// TermsBoundary is one path the workspace keeps out of a lease's reach, with the
// declaration that keeps it there.
//
// The reason is the load-bearing half. A bare path reads as an arbitrary fence, and a
// worker cannot tell a generated file it should REGENERATE from a file another live
// lease is holding, which are opposite instructions.
type TermsBoundary struct {
	Path   string `json:"path"   yaml:"path"`
	Reason string `json:"reason" yaml:"reason"`
}

// TermsAffinity is one project pair that changes together while the lease owns only one
// side of it, and neither project declares a dependency on the other.
//
// HIDDEN coupling only. A pair that changes together and says so in its declarations is
// the workspace working as designed, and the root project of a monorepo co-changes with
// everything; reporting those turns a warning list into a census. What is worth a
// worker's attention is coupling no declaration would have told it about, which is the
// pair a partition is most likely to have split wrongly.
type TermsAffinity struct {
	Project string `json:"project" yaml:"project"`
	With    string `json:"with"    yaml:"with"`
	Commits int    `json:"commits" yaml:"commits"`
}

// TermsFacts is what the WORKSPACE contributes to a brief: everything [NewTerms] cannot
// derive from the row alone, because it needs a knowledge graph and a loadable workspace.
// The zero value is a brief rendered from the row and nothing else.
type TermsFacts struct {
	Evidence         []TermsEvidence
	GraphCold        bool
	WorkspaceCold    bool
	Projects         []string
	DerivedDenyPaths []TermsBoundary
	Affinity         []TermsAffinity
}

// NewTerms renders one job's terms: the job itself, plus what the workspace contributed.
func NewTerms(row types.Job, facts TermsFacts) Terms {
	return Terms{
		Lease:            row,
		Evidence:         facts.Evidence,
		GraphCold:        facts.GraphCold,
		WorkspaceCold:    facts.WorkspaceCold,
		Projects:         facts.Projects,
		DerivedDenyPaths: facts.DerivedDenyPaths,
		Affinity:         facts.Affinity,
	}
}

// String renders the terms. The order is fixed and nothing outside it is printed: a
// section with nothing in it is dropped, so a holder never reads a heading that grants it
// room the job did not.
//
// The narrowest section is the check, and it is the whole point of rendering at all.
// Naming one check and no others is what stops `ci` leaking into every job's terms.
func (b Terms) String() string {
	var s strings.Builder
	fmt.Fprintf(&s, "job: %s\n", b.Lease.ID)

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

	writeBlock(&s, "the only check you run", b.Lease.Validation)
	writeList(&s, "depends on", b.Lease.DependsOn)
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
