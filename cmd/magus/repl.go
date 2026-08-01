package main

import (
	"context"
	"fmt"
	"os"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/std"
)

// workspaceBuzzRepl opens the magusfile-aware REPL behind `magus buzz --workspace`.
func workspaceBuzzRepl(ctx context.Context, workDir string, noAutoload bool) error {
	cwd := workDir
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("buzz --workspace: getwd: %w", err)
		}
	}

	autoloadDir := cwd
	if noAutoload {
		autoloadDir = ""
	}
	sess, err := interp.NewBuzzReplSession(ctx, autoloadDir)
	if err != nil {
		return fmt.Errorf("buzz --workspace: %w", err)
	}
	defer func() { _ = sess.Close() }()

	return interp.Repl(ctx, sess, interp.ReplOptions{
		WorkDir:    cwd,
		Stdin:      os.Stdin,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
		Banner:     "magus buzz --workspace (.help for commands)",
		Candidates: workspaceReplCandidates(ctx, cwd),
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
		// available - and they are what a workspace REPL is mostly for. Each
		// module contributes its own name plus every `mod.method`, which is the
		// difference between completing "fs" and completing "fs.writeFile".
		for _, mod := range std.DescribeModules("") {
			cached = append(cached, mod.Name)
			for _, meth := range std.DescribeModules(mod.Name)[0].Methods {
				cached = append(cached, mod.Name+"."+meth.Name)
			}
		}
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
