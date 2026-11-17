package job

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// The rendering half of types.NextStep: the structured results carry the field, and text
// mode prints the same commands under a `next:` label so a person sees the step an
// agent's harness would.

// QueryWithNext, ExplainWithNext and FilesWithNext add `next` to a structured result
// without changing the record around it.
//
// Wrappers rather than fields on the types, because types/ deliberately imports
// almost nothing and hint is where the command paths live. json and yaml flatten the
// embedded result, so the wire shape is the result plus one key.
type QueryWithNext struct {
	types.KnowledgeQueryOutput `yaml:",inline"`
	Next                       []types.NextStep `json:"next,omitempty" yaml:"next,omitempty"`
}

type ExplainWithNext struct {
	types.KnowledgeExplainOutput `yaml:",inline"`
	Next                         []types.NextStep `json:"next,omitempty" yaml:"next,omitempty"`
	// AgentSessions is what the loaded transcripts recorded about this node's file, the
	// same fact the text view prints. Absent when nothing touched it, or when the node is
	// not about a file.
	//
	// Here as well as in the text renderer because the machine-readable output is the one
	// an agent reads, and the argument for printing contact at all is that omitting it
	// makes a reader conclude a file is quiet. That argument does not weaken when the
	// reader is a program.
	AgentSessions *sessions.PathContact `json:"agent_sessions,omitempty" yaml:"agent_sessions,omitempty"`
	// Source is a doc section's lines, set only under --source.
	Source *types.KnowledgeDefinitionSite `json:"section_source,omitempty" yaml:"section_source,omitempty"`
}

type FilesWithNext struct {
	types.FileReport `yaml:",inline"`
	Next             []types.NextStep `json:"next,omitempty" yaml:"next,omitempty"`
}

// NextGate is what one result needs to serve its breadcrumbs: the marker store that
// holds each Why to a single firing, and the role and write paths the entries are
// filtered for.
//
// A CLI run carries no host session id (only a hook envelope reports one), so the
// markers land in the anonymous bucket, which expires on hint's anonWindow: one
// firing per checkout for a working session's length. A workspace magus cannot locate
// suppresses nothing and records nothing, which is the right direction to fail.
type NextGate struct {
	gate       hint.Gate
	role       hint.Role
	writePaths []string
}

// NewNextGate is the gate over the cache dir for a holder of role and writePaths.
func NewNextGate(cacheDir string, role hint.Role, writePaths []string) NextGate {
	return NextGate{gate: hint.NewGate(cacheDir, ""), role: role, writePaths: writePaths}
}

// UnboundNextGate is the gate of a workspace whose cache dir cannot be placed: it serves
// the unbound set and records nothing.
func UnboundNextGate() NextGate {
	return NextGate{role: hint.RoleUnbound}
}

// ActingRole grades the acting lease against the job store at loc, which is placed
// by placeErr. A store that cannot be read serves the worker's narrower set.
func ActingRole(ctx context.Context, loc Location, placeErr error) (hint.Role, []string) {
	if placeErr != nil {
		return hint.RoleWorker, nil
	}
	id, _ := ActingLease(loc, trail.LeaseFromEnv())
	if id == "" {
		return hint.RoleUnbound, nil
	}
	rows, err := NewStore(loc).List(ctx)
	if err != nil {
		return hint.RoleWorker, nil
	}
	return hint.LeaseRole(rows, id)
}

// Served filters next for the acting role and records what survived to the journal.
//
// Every output that carries breadcrumbs calls it, structured output included: the
// journal's readers ask what a reader was given, and an entry that reached a harness
// as a field was given over exactly as one printed on a terminal was.
func (n NextGate) Served(next []types.NextStep) []types.NextStep {
	served := hint.FilterServable(n.role, n.writePaths, next)
	hint.AppendServedNext(n.gate.CacheDir(), served)
	return served
}

// ConcurrencyNudge is a finished run's concurrency_profile nudge, at most once per
// session: line, then the re-run as a breadcrumb journaled under its id so `magus session
// hints` can count uptake. It is false when the hint already fired, which a skipped call
// never spends.
func ConcurrencyNudge(line, cacheDir, window string, args []string) (string, bool) {
	next := []types.NextStep{hint.NextForSlotWait(string(types.ProfileAggressive), args)}
	gate := hint.NewGate(cacheDir, window)
	if gate.MarkFired(hint.MarkerKind(next[0].ID)) {
		return "", false
	}
	hint.AppendServedNext(gate.CacheDir(), next)
	rerun := strings.TrimSuffix(hint.Render(next, func(types.NextStep) string { return "" }), "\n")
	return fmt.Sprintf("%s\n%s", line, rerun), true
}

// PrintNext writes a result's breadcrumbs, the command on its own line and the reason
// indented under it.
//
// The Run line prints every time, since it is navigation and a reader who has seen it
// before still needs the ids filled in. The Why is advice, so it goes through the
// gate; silent drops it without spending the firing, and the next full run still explains
// itself.
func PrintNext(w io.Writer, n NextGate, next []types.NextStep, silent bool) {
	fmt.Fprint(w, hint.Render(next, func(entry types.NextStep) string {
		if silent {
			return ""
		}
		return n.gate.Once(hint.MarkerKind("next-"+entry.ID), entry.Why)
	}))
}
