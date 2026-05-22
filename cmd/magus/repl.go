package main

import (
	"context"
	"flag"
	"fmt"
	"os"

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
			fmt.Fprintln(os.Stderr, "If a magusfile.bzz is present in or above the cwd, it is executed on startup.")
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
		WorkDir: cwd,
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Banner:  "magus repl (.help for commands)",
	})
}
