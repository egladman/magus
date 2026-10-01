package main

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/egladman/magus/internal/cli"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/langservice"
)

// buzzRepl opens the REPL behind a bare `magus buzz`: the full magusfile surface
// (host modules, the magus.* namespace, spell and project imports), with the
// magusfile at cwd executed on start so its targets and locals are there to poke
// at. There is no second, magusfile-less REPL and no flag to ask for this one:
// magus reads its context everywhere else, and a REPL opened inside a workspace is
// a REPL on that workspace. Outside one there is simply nothing to autoload, which
// NewBuzzReplSession already treats as ordinary rather than an error.
//
// --no-autoload skips executing the magusfile.
func buzzRepl(ctx context.Context, noAutoload bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("buzz repl: getwd: %w", err)
	}

	sess, err := interp.NewBuzzReplSession(ctx, cwd, !noAutoload)
	if err != nil {
		return fmt.Errorf("buzz repl: %w", err)
	}
	defer func() { _ = sess.Close() }()

	return interp.Repl(ctx, sess, interp.ReplOptions{
		WorkDir:    cwd,
		Stdin:      os.Stdin,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
		Banner:     "magus buzz - Buzz REPL (.help for commands)",
		Candidates: workspaceReplCandidates(ctx, cwd),
		Targets:    workspaceTargetNames(ctx, cwd),
		Flags:      cliFlagCandidates,
	})
}

// workspaceReplCandidates supplies the completion candidates only the CLI can reach: the
// host module names, and this workspace's targets and projects.
//
// It is memoized on first Tab rather than computed at startup, for two reasons. A
// session that never presses Tab pays nothing, which matters because loading the
// workspace is the expensive half; and resolving it lazily means a REPL opened
// outside a workspace still starts, with module names alone, instead of failing on
// a lookup it did not need.
func workspaceReplCandidates(ctx context.Context, cwd string) func() []string {
	var cached []string
	return func() []string {
		if cached != nil {
			return cached
		}
		// Host modules come from the binary, not the workspace, so they are always
		// available, and they are what a workspace REPL is mostly for. Sourced from
		// the same manifest `magus buzz lsp` completes from, spelled the way Buzz
		// actually calls it (module\name), not the dotted form the Go descriptor
		// declares a member under.
		cached = append(cached, langservice.ModuleCallCandidates()...)
		if m, err := loadMagus(ctx, cwd); err == nil {
			// Best-effort completion candidates: this closure has no error path of its
			// own (it feeds a completer, not a command), so a cancelled ctx here just
			// means fewer candidates offered, not a failed Tab press.
			if targets, err := m.ListTargets(ctx); err == nil {
				for _, t := range targets {
					cached = append(cached, t.Name)
				}
			}
			for _, p := range m.All() {
				cached = append(cached, p.Path)
			}
		}
		if cached == nil {
			// Distinguish "resolved to nothing" from "not yet resolved", or every Tab
			// re-runs the workspace load that just came back empty.
			cached = []string{}
		}
		return cached
	}
}

// workspaceTargetNames supplies just this workspace's target names, for
// completion inside magus\run(["<target>", ...]), where offering every module
// and project path alongside them would bury the few names valid there.
func workspaceTargetNames(ctx context.Context, cwd string) func() []string {
	var cached []string
	return func() []string {
		if cached != nil {
			return cached
		}
		if m, err := loadMagus(ctx, cwd); err == nil {
			if targets, err := m.ListTargets(ctx); err == nil {
				for _, t := range targets {
					cached = append(cached, t.Name)
				}
			}
		}
		if cached == nil {
			cached = []string{}
		}
		return cached
	}
}

// cliFlagCandidates returns the CLI's declared flags for verb (e.g. "run"),
// each spelled with its leading dashes, read from the same registry the
// generated binders in cmd/magus/gen and the shell completions come from, so
// this can't drift from what the subcommand actually accepts.
func cliFlagCandidates(verb string) []string {
	for _, c := range cli.All {
		if c.Name != verb {
			continue
		}
		out := make([]string, 0, len(c.Flags))
		for _, f := range c.Flags {
			prefix := "--"
			if len(f.Name) == 1 {
				prefix = "-"
			}
			out = append(out, prefix+f.Name)
		}
		sort.Strings(out)
		return out
	}
	return nil
}
