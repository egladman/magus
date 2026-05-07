package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/egladman/magus/internal/interp"
)

func replCmd(ctx context.Context, _ string, args []string) error {
	var (
		noAutoload *bool
		workDir    *string
		engineName *string
	)
	_, err := cmdParse("repl", args, func(fs *flag.FlagSet) {
		noAutoload = fs.Bool("no-autoload", false, "Skip executing the magusfile on start")
		workDir = fs.String("C", "", "Working directory for require() resolution (default: cwd)")
		engineName = fs.String("engine", "lua", "REPL language engine: lua or buzz")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus repl [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Open an interactive REPL with the same bindings available to magusfile")
			fmt.Fprintln(os.Stderr, "scripts (magus, sh, fs, vcs, platform, spells).")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "The default engine is Teal/Lua. Pass --engine buzz for a Buzz REPL.")
			fmt.Fprintln(os.Stderr, "If a matching magusfile is present in or above the cwd, it is executed on startup.")
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

	if *engineName == "buzz" {
		return buzzRepl(ctx, cwd, *noAutoload)
	}
	if *engineName != "lua" {
		return fmt.Errorf("repl: unknown engine %q (want lua or buzz)", *engineName)
	}

	r, err := interp.NewLuaSession(ctx)
	if err != nil {
		return fmt.Errorf("repl: new vm: %w", err)
	}
	defer func() { _ = r.Close() }()

	if err := interp.InstallReplPrelude(ctx, r); err != nil {
		return fmt.Errorf("repl: %w", err)
	}

	if !*noAutoload {
		src, findErr := interp.Find(cwd)
		if findErr != nil && !errors.Is(findErr, interp.ErrNoMagusfile) {
			fmt.Fprintf(os.Stderr, "magus: repl: find magusfile.tl: %v\n", findErr)
		} else if src != nil {
			for _, path := range src.Files {
				code, err := interp.CompileFile(ctx, r, path)
				if err == nil {
					err = r.DoString(string(code))
				}
				if err != nil {
					fmt.Fprintf(os.Stderr, "magus: repl: autoload %s: %v\n", path, err)
					break
				}
			}
		}
	}

	return interp.Repl(ctx, r, interp.ReplOptions{
		WorkDir: cwd,
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Banner:  "magus repl (teal mode; .help for commands)",
	})
}

// buzzRepl opens an interactive Buzz REPL with the magus host bindings, loading
// magusfile.bzz from (or above) cwd unless noAutoload is set.
func buzzRepl(ctx context.Context, cwd string, noAutoload bool) error {
	autoloadDir := cwd
	if noAutoload {
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
		Banner:  "magus repl (buzz mode; .help for commands)",
	})
}
