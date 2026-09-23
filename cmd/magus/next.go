package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/sessions"
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
	// AgentSessions is what the loaded transcripts recorded about this node's file, the
	// same fact the text view prints. Absent when nothing touched it, or when the node is
	// not about a file.
	//
	// Here as well as in the text renderer because the machine-readable surface is the one
	// an agent reads, and the argument for printing contact at all is that omitting it
	// makes a reader conclude a file is quiet. That argument does not weaken when the
	// reader is a program.
	AgentSessions *sessions.PathContact `json:"agent_sessions,omitempty" yaml:"agent_sessions,omitempty"`
}

type filesWithNext struct {
	types.FileReport `yaml:",inline"`
	Next             []hint.Next `json:"next,omitempty" yaml:"next,omitempty"`
}

// nextGate is what one result needs to serve its breadcrumbs: the marker store that
// holds each Why to a single firing, and the role and write paths the entries are
// filtered for.
//
// A CLI run carries no host session id (only a hook envelope reports one), so the
// markers land in the anonymous bucket, which expires on hint's anonWindow: one
// firing per checkout for a working session's length. A workspace magus cannot locate
// suppresses nothing and records nothing, which is the right direction to fail.
type nextGate struct {
	gate       hint.Gate
	role       hint.Role
	writePaths []string
}

// newNextGate resolves the gate for a command running against root.
func newNextGate(root string) nextGate {
	dir, err := magus.ResolveCacheDir(resolveRootOrEmpty(root), magus.WithLoadedConfig(globalCfg))
	if err != nil {
		return nextGate{role: hint.RoleUnbound}
	}
	role, writePaths := actingRole(dir, resolveRootOrEmpty(root))
	return nextGate{gate: hint.NewGate(dir, ""), role: role, writePaths: writePaths}
}

// actingRole grades the acting lease against this checkout's job store.
func actingRole(cacheDir, root string) (hint.Role, []string) {
	id, _ := job.ActingLease(cacheDir)
	if id == "" {
		return hint.RoleUnbound, nil
	}
	rows, err := job.NewStore(job.Location{CacheDir: cacheDir, Root: root}).List()
	if err != nil {
		return hint.RoleWorker, nil
	}
	return hint.LeaseRole(rows, id)
}

// served filters next for the acting role and records what survived to the journal.
//
// Every surface that carries breadcrumbs calls it, structured output included: the
// journal's readers ask what a reader was given, and an entry that reached a harness
// as a field was given over exactly as one printed on a terminal was.
func (n nextGate) served(next []hint.Next) []hint.Next {
	served := hint.ServableTo(n.role, n.writePaths, next)
	hint.AppendServedNext(n.gate.CacheDir(), served)
	return served
}

// emitConcurrencyNudge prints a finished run's concurrency_profile nudge, at most once
// per session, with the re-run as a breadcrumb journaled under its id so `magus session
// hints` can count uptake. -s and disabled hints skip it without spending the firing.
func emitConcurrencyNudge(ctx context.Context, sink *magus.Sink, m *magus.Magus, args []string) {
	if global.silent || !interactive.HintsEnabled() {
		return
	}
	line := m.ConcurrencyNudge()
	if line == "" {
		return
	}
	next := []hint.Next{hint.NextForSlotWait(string(types.ProfileAggressive), args)}
	gate := hint.NewGate(m.CacheDir(), terminalWindow())
	if gate.MarkFired(hint.MarkerKind(next[0].ID)) {
		return
	}
	hint.AppendServedNext(gate.CacheDir(), next)
	rerun := strings.TrimSuffix(hint.Render(next, func(hint.Next) string { return "" }), "\n")
	sink.EmitNotice(ctx, slog.LevelInfo, "", line+"\n"+rerun)
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
