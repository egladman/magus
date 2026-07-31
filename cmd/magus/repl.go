package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/egladman/magus/host"
	"github.com/egladman/magus/internal/interp"
)

func replCmd(ctx context.Context, _ string, args []string) error {
	var (
		noAutoload *bool
		workDir    *string
	)
	_, err := cmdParse("repl", args, func(fs *flag.FlagSet) {
		noAutoload = fs.Bool("no-autoload", false, "Skip executing the magusfile on start")
		workDir = fs.String("C", "", "Working directory for import resolution (default: cwd)")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus repl [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Open an interactive Buzz REPL with the same bindings available to magusfile")
			fmt.Fprintln(os.Stderr, "scripts (magus, sh, fs, vcs, platform, spells).")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "If a magusfile.buzz is present in or above the cwd, it is executed on startup.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags:")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}

	cwd := *workDir
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("repl: getwd: %w", err)
		}
	}

	autoloadDir := cwd
	if *noAutoload {
		autoloadDir = ""
	}
	sess, err := interp.NewBuzzReplSession(ctx, autoloadDir)
	if err != nil {
		return fmt.Errorf("repl: %w", err)
	}
	defer func() { _ = sess.Close() }()

	return interp.Repl(ctx, sess, interp.ReplOptions{
		WorkDir:    cwd,
		Stdin:      os.Stdin,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
		Banner:     "magus repl (.help for commands)",
		Candidates: replCandidates(ctx, cwd),
	})
}

// replCandidates supplies the completion candidates only the CLI can reach: the
// host module names, and this workspace's targets and projects.
//
// It is memoized on first Tab rather than computed at startup, for two reasons. A
// session that never presses Tab pays nothing, which matters because loading the
// workspace is the expensive half; and resolving it lazily means a REPL opened
// outside a workspace still starts, with module names alone, instead of failing on
// a lookup it did not need.
func replCandidates(ctx context.Context, cwd string) func() []string {
	var cached []string
	return func() []string {
		if cached != nil {
			return cached
		}
		// Host modules come from the binary, not the workspace, so they are always
		// available - and they are what a bare `magus repl` is mostly for. Each
		// module contributes its own name plus every `mod.method`, which is the
		// difference between completing "fs" and completing "fs.writeFile".
		for _, mod := range host.Modules("") {
			cached = append(cached, mod.Name)
			for _, meth := range host.Modules(mod.Name)[0].Methods {
				cached = append(cached, mod.Name+"."+meth.Name)
			}
		}
		if m, err := loadMagus(ctx, cwd); err == nil {
			for _, t := range m.DescribeTargets() {
				cached = append(cached, t.Name)
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
