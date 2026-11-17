package main

import (
	"context"
	"fmt"
	"os"

	"github.com/egladman/magus/internal/interp"
)

// buzzRepl opens the REPL behind a bare `magus buzz`: the full magusfile API
// (host modules, the magus.* namespace, spell and project imports), with the
// magusfile at cwd executed on start so its targets and locals are there to poke
// at. There is no second, magusfile-less REPL and no flag to ask for this one:
// magus reads its context everywhere else, and a REPL opened inside a workspace is
// a REPL on that workspace. Outside one there is nothing to autoload, which
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
		Targets:    interp.ReplTargetNames(ctx, cwd, workspaceNames),
		Flags:      interp.CLIFlagCandidates,

		ShellIntegration: globalCfg.ShellIntegration == "osc133",
	})
}

// workspaceNames lists the open workspace's target names and project paths, for the REPL's
// completion. A workspace that will not load offers none; targets that fail to list leave
// the project paths.
func workspaceNames(ctx context.Context, dir string) (targets, projects []string, err error) {
	m, err := loadMagus(ctx, dir)
	if err != nil {
		return nil, nil, err
	}
	if listed, err := m.ListTargets(ctx); err == nil {
		for _, t := range listed {
			targets = append(targets, t.Name)
		}
	}
	for _, p := range m.All() {
		projects = append(projects, p.Path)
	}
	return targets, projects, nil
}

// workspaceReplCandidates is the completion source the step gate and the REPL take.
var workspaceReplCandidates = interp.ReplCandidates(workspaceNames)
