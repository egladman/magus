package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/libs/cmdline/generate"
	"github.com/egladman/magus/libs/xdg"
)

func manCmd(args []string) error {
	if len(args) == 0 {
		manUsage()
		return exit.Usagef("magus man: subcommand required (want install)")
	}
	if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		manUsage()
		return nil
	}
	if args[0] != "install" {
		manUsage()
		return exit.Usagef("magus man: unknown subcommand %q (want install)", args[0])
	}

	fs := flag.NewFlagSet("man install", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.SetOutput(os.Stderr)
	dir := fs.String("dir", "", "directory for section 1 man pages (default: $XDG_DATA_HOME/man/man1)")
	dryRun := fs.Bool("dry-run", false, "print what would be written without touching the filesystem")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("magus man install: unexpected argument %q", fs.Arg(0))
	}
	if *dir == "" {
		def, err := defaultManDir()
		if err != nil {
			return fmt.Errorf("magus man install: pass --dir: %w", err)
		}
		*dir = def
	}
	root := commands.Root
	root.Env = config.EnvVars()
	pages := generate.Roff(root, "", version)
	// Before MkdirAll: the destination is usually outside the repo, so describing
	// it must not create it.
	if *dryRun {
		for _, page := range pages {
			fmt.Fprintf(os.Stdout, "would write %s\n", filepath.Join(*dir, page.Name))
		}
		fmt.Fprintf(os.Stdout, "dry run: %d man page(s) would be written to %s; nothing was changed\n", len(pages), *dir)
		return nil
	}
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		return fmt.Errorf("magus man install: create %s: %w", *dir, err)
	}
	for _, page := range pages {
		path := filepath.Join(*dir, page.Name)
		if err := os.WriteFile(path, page.Content, 0o644); err != nil {
			return fmt.Errorf("magus man install: write %s: %w", path, err)
		}
	}
	fmt.Fprintf(os.Stdout, "Installed %d man pages to %s\n", len(pages), *dir)
	return nil
}

func defaultManDir() (string, error) {
	data, err := xdg.DataDir()
	if err != nil {
		return "", fmt.Errorf("resolve the data directory: %w", err)
	}
	return filepath.Join(data, "man", "man1"), nil
}

func manUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus man install [--dir <path>] [--dry-run]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Write the man pages embedded in this binary.")
}
