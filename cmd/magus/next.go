package main

import (
	"fmt"
	"io"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/ledger"
	"github.com/egladman/magus/types"
)

// The rendering half of hint.Next: the structured results carry the field, and text
// mode prints the same commands under a `next:` label so a person sees the step an
// agent's harness would.

// queryWithNext, explainWithNext and filesWithNext add `next` to a structured result
// without changing the record around it.
//
// Wrappers rather than fields on the types, because types/ deliberately imports
// almost nothing and hint is where the command paths live. json and yaml flatten the
// embedded result, so the wire shape is the result plus one key.
type queryWithNext struct {
	types.KnowledgeQueryOutput `yaml:",inline"`
	Next                       []hint.Next `json:"next,omitempty" yaml:"next,omitempty"`
}

type explainWithNext struct {
	types.KnowledgeExplainOutput `yaml:",inline"`
	Next                         []hint.Next `json:"next,omitempty" yaml:"next,omitempty"`
}

type filesWithNext struct {
	types.FileReport `yaml:",inline"`
	Next             []hint.Next `json:"next,omitempty" yaml:"next,omitempty"`
}

// nextGate is what one result needs to serve its breadcrumbs: the marker store that
// holds each Why to a single firing, and the role and lane the entries are filtered
// for.
//
// A CLI run carries no host session id (only a hook envelope reports one), so the
// markers land in the anonymous bucket, which expires on hint's anonWindow: one
// firing per checkout for a working session's length. A workspace magus cannot locate
// suppresses nothing and records nothing, which is the right direction to fail.
type nextGate struct {
	gate hint.Gate
	role hint.Role
	lane []string
}

// newNextGate resolves the gate for a command running against root.
func newNextGate(root string) nextGate {
	dir, err := magus.ResolveCacheDir(resolveRootOrEmpty(root), magus.WithLoadedConfig(globalCfg))
	if err != nil {
		return nextGate{role: hint.RoleUnbound}
	}
	role, lane := actingRole(dir, resolveRootOrEmpty(root))
	return nextGate{gate: hint.NewGate(dir, ""), role: role, lane: lane}
}

// actingRole grades the acting lease against this checkout's ledger.
func actingRole(cacheDir, root string) (hint.Role, []string) {
	id := ledger.ActingLease(cacheDir)
	if id == "" {
		return hint.RoleUnbound, nil
	}
	rows, err := ledger.NewStore(ledger.Location{CacheDir: cacheDir, Root: root}).List()
	if err != nil {
		return hint.RoleWorker, nil
	}
	return hint.RoleFor(rows, id)
}

// served filters next for the acting role and records what survived to the journal.
//
// Every surface that carries breadcrumbs calls it, structured output included: the
// journal's readers ask what a reader was given, and an entry that reached a harness
// as a field was given over exactly as one printed on a terminal was.
func (n nextGate) served(next []hint.Next) []hint.Next {
	served := hint.ServableTo(n.role, n.lane, next)
	hint.AppendServedNext(n.gate.CacheDir(), served)
	return served
}

// printNext writes a result's breadcrumbs, the command on its own line and the reason
// indented under it.
//
// The Run line prints every time, since it is navigation and a reader who has seen it
// before still needs the ids filled in. The Why is advice, so it goes through the
// gate; -s drops it without spending the firing, and the next full run still explains
// itself.
func printNext(w io.Writer, n nextGate, next []hint.Next) {
	fmt.Fprint(w, hint.Render(next, func(entry hint.Next) string {
		if global.silent {
			return ""
		}
		return n.gate.Once(hint.MarkerKind("next-"+entry.ID), entry.Why)
	}))
}
