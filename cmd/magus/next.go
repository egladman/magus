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

// nextServer is what one result needs to serve its breadcrumbs: the marker store
// that holds each Why to a single firing, the cache dir the served-next journal
// lands in, and the role the entries are filtered for.
//
// A CLI run carries no host session id (only a hook envelope reports one), so both
// the markers and the journal land in the anonymous bucket, which expires on
// advisoryAnonWindow: one firing per checkout for a working session's length. A
// workspace magus cannot locate suppresses nothing and records nothing, which is
// the right direction to fail.
type nextServer struct {
	gate advisoryGate
	base string
	role hint.Role
}

// nextFor resolves the server for a command running against root.
func nextFor(root string) nextServer {
	dir, err := magus.ResolveCacheDir(resolveRootOrEmpty(root), magus.WithLoadedConfig(globalCfg))
	if err != nil {
		return nextServer{role: hint.RoleUnbound}
	}
	return nextServer{gate: newAdvisoryGate(dir, ""), base: dir, role: actingRole(dir, resolveRootOrEmpty(root))}
}

// actingRole reads the role off the acting lease's row: no lease is unbound, a
// read-only row or one owning no path is a reviewer, anything else is a worker.
//
// Derived rather than stored. The row already says what a lease may write, and a
// second field saying the same thing is a field that can disagree with it. A bound
// lease whose row is gone still grades as a worker: something claimed a lane, and
// serving the full unbound set on the strength of a missing row is the wrong way to
// be wrong.
func actingRole(cacheDir, root string) hint.Role {
	id := ledger.ActingLease(cacheDir)
	if id == "" {
		return hint.RoleUnbound
	}
	rows, err := ledger.NewStore(ledger.Location{CacheDir: cacheDir, Root: root}).List()
	if err != nil {
		return hint.RoleWorker
	}
	for _, row := range rows {
		if row.ID != id {
			continue
		}
		if row.ReadOnly || len(row.OwnedPaths) == 0 {
			return hint.RoleReviewer
		}
		return hint.RoleWorker
	}
	return hint.RoleWorker
}

// serve filters next for the acting role and records what survived to the journal.
//
// Every surface that carries breadcrumbs calls it, structured output included: the
// journal's readers ask what a reader was HANDED, and an entry that reached a
// harness as a field was handed over exactly as one printed on a terminal was.
func (s nextServer) serve(next []hint.Next) []hint.Next {
	served := hint.ForRole(s.role, next)
	hint.AppendServedNext(s.base, "", served)
	return served
}

// printNext writes a result's breadcrumbs, the command on its own line and the
// reason indented under it.
//
// Two lines rather than one: a reader copies the command line, and a parenthetical
// riding it comes along. The Run line prints every time, since it is navigation and
// a reader who has seen it before still needs the ids filled in. The Why is advice,
// and advice says nothing the second time, so it goes through the gate. -s drops it
// outright without spending the firing, so the next full run still explains itself.
func printNext(w io.Writer, s nextServer, next []hint.Next) {
	if len(next) == 0 {
		return
	}
	fmt.Fprintf(w, "\nnext:\n")
	for _, n := range next {
		fmt.Fprintf(w, "  %s\n", n.Run)
		if global.silent {
			continue
		}
		if why := s.gate.once(advisoryKind("next-"+n.ID), n.Why); why != "" {
			fmt.Fprintf(w, "      %s\n", why)
		}
	}
}
