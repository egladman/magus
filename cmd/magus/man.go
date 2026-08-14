package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/egladman/magus/internal/clispec"
)

func manCmd(args []string) error {
	if len(args) == 0 {
		manUsage()
		return usagef("magus man: subcommand required (want install)")
	}
	if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		manUsage()
		return nil
	}
	if args[0] != "install" {
		manUsage()
		return usagef("magus man: unknown subcommand %q (want install)", args[0])
	}

	fs := flag.NewFlagSet("man install", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.SetOutput(os.Stderr)
	dir := fs.String("dir", defaultManDir(), "directory for section 1 man pages")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("magus man install: unexpected argument %q", fs.Arg(0))
	}
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		return fmt.Errorf("magus man install: create %s: %w", *dir, err)
	}
	pages := clispec.RoffPages("", version)
	for _, page := range pages {
		path := filepath.Join(*dir, page.Name)
		if err := os.WriteFile(path, page.Content, 0o644); err != nil {
			return fmt.Errorf("magus man install: write %s: %w", path, err)
		}
	}
	fmt.Fprintf(os.Stdout, "Installed %d man pages to %s\n", len(pages), *dir)
	return nil
}

func defaultManDir() string {
	if dataHome := os.Getenv("XDG_DATA_HOME"); dataHome != "" {
		return filepath.Join(dataHome, "man", "man1")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "share/man/man1"
	}
	return filepath.Join(home, ".local", "share", "man", "man1")
}

func manUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus man install [--dir <path>]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Write the man pages embedded in this binary.")
}
