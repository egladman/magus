package interp

import (
	"context"
	"sort"

	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/langservice"
)

// WorkspaceNames lists the target names and project paths of the workspace at dir.
// The root package opens workspaces and this package cannot import it, so the
// caller supplies the lookup.
type WorkspaceNames func(ctx context.Context, dir string) (targets, projects []string, err error)

// ReplCandidates supplies the completion candidates only the CLI can reach: the
// host module names, and this workspace's targets and projects.
//
// It is memoized on first Tab rather than computed at startup, for two reasons. A
// session that never presses Tab pays nothing, which matters because loading the
// workspace is the expensive half; and resolving it lazily means a REPL opened
// outside a workspace still starts, with module names alone, instead of failing on
// a lookup it did not need.
func ReplCandidates(names WorkspaceNames) func(ctx context.Context, cwd string) func() []string {
	return func(ctx context.Context, cwd string) func() []string {
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
			// Best-effort completion candidates: this closure has no error path of its
			// own (it feeds a completer, not a command), so a failed lookup here just
			// means fewer candidates offered, not a failed Tab press.
			if targets, projects, err := names(ctx, cwd); err == nil {
				cached = append(cached, targets...)
				cached = append(cached, projects...)
			}
			if cached == nil {
				// Distinguish "resolved to nothing" from "not yet resolved", or every Tab
				// re-runs the workspace load that just came back empty.
				cached = []string{}
			}
			return cached
		}
	}
}

// ReplTargetNames supplies just this workspace's target names, for
// completion inside magus\run(["<target>", ...]), where offering every module
// and project path alongside them would bury the few names valid there.
func ReplTargetNames(ctx context.Context, cwd string, names WorkspaceNames) func() []string {
	var cached []string
	return func() []string {
		if cached != nil {
			return cached
		}
		if targets, _, err := names(ctx, cwd); err == nil {
			cached = append(cached, targets...)
		}
		if cached == nil {
			cached = []string{}
		}
		return cached
	}
}

// CLIFlagCandidates returns the CLI's declared flags for verb (e.g. "run"),
// each spelled with its leading dashes, read from the same registry the
// generated binders in cmd/magus/gen and the shell completions come from, so
// this can't drift from what the subcommand actually accepts.
func CLIFlagCandidates(verb string) []string {
	for _, c := range commands.Root.Spec.Children {
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
