package ledger

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// Brief is the worker brief for one lease: the declared row, the workspace facts magus
// resolved against it, and the footer the workspace's own template carries.
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
	// Evidence is one line per owned path the knowledge graph resolved, in declaration
	// order. A path the graph does not know is ABSENT here rather than reported as zero:
	// an unknown blast radius and a blast radius of zero are different facts, and the
	// second one would read as permission to edit freely.
	Evidence []BriefEvidence `json:"evidence,omitempty" yaml:"evidence,omitempty"`
	// GraphCold marks a brief rendered against no graph at all, so empty Evidence reads
	// as "not asked" rather than "asked, and nothing depends on any of this".
	GraphCold bool `json:"graph_cold,omitempty" yaml:"graph_cold,omitempty"`
	// Footer is the workspace template already rendered. Empty when the workspace ships
	// none, which is a brief with no bootstrap, rules or skills blocks rather than an
	// error: the footer is the workspace's to own, including owning nothing.
	Footer string `json:"footer,omitempty" yaml:"footer,omitempty"`
}

// BriefEvidence is what the knowledge graph knows about one owned path: the node it
// resolved to and how many nodes can transitively reach it, the same count
// `magus explain` prints.
type BriefEvidence struct {
	Path        string `json:"path"         yaml:"path"`
	Node        string `json:"node"         yaml:"node"`
	BlastRadius int    `json:"blast_radius" yaml:"blast_radius"`
}

// NewBrief starts the brief for one row with the fields the row alone determines. The
// bind line is rendered here and nowhere else, so the brief, its golden test, and any
// caller quoting the line cannot drift; evidence and the footer are the caller's to
// add, since they need a graph and a workspace.
func NewBrief(row types.Lease) Brief {
	return Brief{Lease: row, Bind: hint.SessionLease.With(row.ID)}
}

// BriefTemplatePath is where a workspace keeps the brief footer, relative to its root.
// It sits with the other agent-integration templates so the guide that documents it and
// the file itself are one directory.
const BriefTemplatePath = "docs/guides/integrations/agents/brief.md.tmpl"

// RenderBriefFooter renders a workspace's footer template against the lease row. The
// template is Go text/template with the row as its data, so a workspace can name the
// lease it is briefing without magus deciding what the blocks say.
//
// Errors carry the template path, because the reader who has to fix one is editing that
// file and not this code.
func RenderBriefFooter(tmpl string, row types.Lease) (string, error) {
	t, err := template.New("brief").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("ledger: parse %s: %w", BriefTemplatePath, err)
	}
	var out strings.Builder
	if err := t.Execute(&out, row); err != nil {
		return "", fmt.Errorf("ledger: render %s: %w", BriefTemplatePath, err)
	}
	return out.String(), nil
}

// Text renders the brief. The order is fixed and nothing outside it is printed: a section
// with nothing in it is dropped, so a worker never reads a heading that grants it room the
// row did not.
//
// The narrowest section is the validation one, and it is the whole point of rendering at
// all. Naming one check and no others is what stops `ci` leaking into a worker's brief,
// which is the failure this replaced.
func (b Brief) Text() string {
	var s strings.Builder
	fmt.Fprintf(&s, "lease: %s\n", b.Lease.ID)
	fmt.Fprintf(&s, "bind: %s\n", b.Bind)

	writeBlock(&s, "goal and acceptance criteria", b.Lease.Goal)
	writeList(&s, "owned paths", b.Lease.OwnedPaths)
	writeList(&s, "forbidden paths", b.Lease.ForbiddenPaths)

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

	if strings.TrimSpace(b.Footer) != "" {
		fmt.Fprintf(&s, "\n%s", strings.TrimRight(b.Footer, "\n")+"\n")
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
