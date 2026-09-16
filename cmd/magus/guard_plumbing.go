package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// The workspace facts and the output plumbing `magus shell` needs, kept out of shell.go
// so that file reads as the command and this one as what it stands on.

// guardDependencies hands the guard the five workspace facts its rules cannot resolve for
// themselves: the memoized inspect, this process's loaded config, the index-staleness
// advice, and the spell catalog, all of which live in the CLI rather than in the rules.
func guardDependencies() guard.Dependencies {
	shellRules, shellDialect := loadWorkspaceShellRules(context.Background())
	return guard.Dependencies{
		Inspect: func(ctx context.Context, root string) (types.WorkspaceRepository, error) {
			if root == "" {
				return inspectWorkspace(ctx, "")
			}
			return magus.Inspect(ctx, root,
				magus.WithLoadedConfig(globalCfg), magus.WithVersion(version))
		},
		CacheDir: func(root string) (string, error) {
			if root != "" {
				return magus.ResolveCacheDir(root)
			}
			found, err := magus.FindRoot("")
			if err != nil {
				return "", err
			}
			return magus.ResolveCacheDir(found, magus.WithLoadedConfig(globalCfg))
		},
		NotesShared:      globalCfg.Knowledge.Notes.Shared,
		ShellRules:       shellRules,
		ShellDialect:     shellDialect,
		GraphStaleAdvice: staleGraphAdvice,
		Spells:           project.DefaultSpellRegistry().All,
		SymbolDefined:    symbolDefinedForGuard,
	}
}

// symbolDefinedForGuard answers the one question that lets the guard deny a symbol
// search: does refs return the same sites this grep is reaching for.
//
// Definitive only when no project's index is older than its sources. A stale index
// makes refs answer "unknown, not absent", and a deny resting on that would take grep
// away on the strength of a lookup that admits it may be wrong.
//
// Reached only after precedentIdent has already matched, which the transcript mining
// tuned to fire rarely, so the graph load stays off the path of an ordinary tool call.
// The empty rootOverride is load-bearing: inspectWorkspace is memoized per process and
// panics when a second call names a different root, and every other hook dependency
// resolves through the same empty spelling.
func symbolDefinedForGuard(ident string) (defined, definitive bool) {
	ctx := context.Background()
	g, err := loadKnowledgeGraphForRefs(ctx, "", false, ident)
	if err != nil {
		return false, false
	}
	_, ok := g.Refs(ident)
	return ok, staleGraphAdvice(ctx) == ""
}

// loadWorkspaceShellRules returns additive rules the root magusfile declared via
// magus\guard.shell, and the last non-empty dialect among them for outer parse.
// Missing or unloadable is empty so a magusfile typo cannot take down every
// shell hook (built-ins still apply).
func loadWorkspaceShellRules(ctx context.Context) ([]guard.WorkspaceShellRule, guard.Dialect) {
	ws, err := inspectWorkspace(ctx, "")
	if err != nil {
		return nil, ""
	}
	m, ok := ws.(*magus.Magus)
	if !ok {
		return nil, ""
	}
	src := m.ShellRules()
	if len(src) == 0 {
		return nil, ""
	}
	out := make([]guard.WorkspaceShellRule, len(src))
	var dialect guard.Dialect
	for i, r := range src {
		out[i] = guard.WorkspaceShellRule{
			Name:     r.Name,
			Decision: r.Decision,
			Program:  r.Program,
			Args:     r.Args,
			Reason:   r.Reason,
			Dialect:  r.Dialect,
		}
		if r.Dialect != "" {
			dialect = guard.Dialect(r.Dialect)
		}
	}
	return out, dialect
}

// guardDenyExitCode is what a denied command exits with.
//
// A hook that reports a deny and exits 0 blocks NOTHING: the host sees success
// and runs the command anyway, so the guard looks enforced and is not. 2 rather
// than 1 is what the dominant host reads as "block and show the reason to the
// model". The collision with the usage code is harmless: a guard that could not
// parse its input has not judged the command either.
const guardDenyExitCode = 2

// enforceVerdict turns a deny into a blocking exit. Applies to every format: an
// `-o json` caller with a zero status would be told the same lie in a different
// shape.
//
// The reason reaches stderr only when stdout does not already carry it as prose,
// i.e. every format but text. The guard templates read the verdict off stdout and
// one discards stderr outright, so an unconditional copy printed a kilobyte-plus
// reason twice to an audience with a context budget.
func enforceVerdict(opts OutputOptions, verdict guard.Verdict) error {
	return enforceVerdictTo(os.Stderr, opts, verdict)
}

func enforceVerdictTo(errOut io.Writer, opts OutputOptions, verdict guard.Verdict) error {
	if verdict.Decision != "deny" {
		return nil
	}
	if opts.Format != FormatText {
		fmt.Fprintln(errOut, verdict.Reason)
	}
	return errSilent{exitCode: guardDenyExitCode}
}

// writeGuardVerdict renders a verdict through the standard output arm.
func writeGuardVerdict(out io.Writer, opts OutputOptions, verdict guard.Verdict) error {
	switch opts.Format {
	case FormatText:
		switch verdict.Decision {
		case "deny":
			fmt.Fprintln(out, decisionLabel("deny", verdict.Rule)+" "+verdict.Reason)
		case "advise":
			fmt.Fprintln(out, decisionLabel("advise", verdict.Rule)+" "+verdict.Context)
		default:
			fmt.Fprintln(out, "pass")
		}
		return nil
	case FormatName:
		fmt.Fprintln(out, verdict.Decision)
		return nil
	}
	return writeFormatted(out, opts, verdict)
}

// decisionLabel opens a text verdict with the decision and, where the rule can identify
// itself, its name: `deny [stage-all]`.
//
// The name was reachable only through `-o json` before this, which meant a person could
// read a refusal and still have nothing to look up, grep a trail for, or cite when
// reporting it as a false positive. Every other verdict magus reaches carries a code a
// reader can take somewhere; this was the exception.
//
// Bracketed rather than appended so it survives a line-wrap next to the decision it
// qualifies, and omitted entirely when the rule is anonymous: an empty `[]` would promise
// an identifier that does not exist.
func decisionLabel(decision, rule string) string {
	if rule == "" {
		return decision + ":"
	}
	return decision + " [" + rule + "]:"
}

// guardInputLimit bounds the payload one hook call may carry.
//
// An MCP params object is caller-controlled and can be megabytes, the raw-event wiring
// forwards the WHOLE event rather than one extracted command string, and the template
// holds a second copy of it under the host's hook timeout. Overflow is a read FAILURE
// rather than a truncation, for the reason readGuardInput gives: a truncated payload is
// exactly the "not the command the guard was shown" case.
const guardInputLimit = 1 << 20

// readGuardInput reports the payload, and the read failure separately from an empty one.
// "No input" is a host that sent nothing and "the read broke" is a payload magus was
// supposed to receive, and only the second one means the command about to run is not the
// command the guard was shown.
func readGuardInput(in io.Reader) (string, error) {
	b, err := io.ReadAll(io.LimitReader(in, guardInputLimit+1))
	if err != nil {
		return "", err
	}
	if len(b) > guardInputLimit {
		return "", fmt.Errorf("the payload is larger than the %d-byte limit, so it was not read whole", guardInputLimit)
	}
	return strings.TrimSpace(string(b)), nil
}
