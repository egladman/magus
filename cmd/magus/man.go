package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/generate"
	"github.com/egladman/magus/libs/xdg"
)

func manCommand(args []string) error {
	fs := flag.NewFlagSet("self man", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.SetOutput(os.Stderr)
	fs.Usage = manUsage
	mf := flaggen.BindSelfMan(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		manUsage()
		return exit.Usagef("magus self man: unexpected argument %q", fs.Arg(0))
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	dir := mf.Dir
	if dir == "" {
		def, err := defaultManDir()
		if err != nil {
			return fmt.Errorf("magus self man: pass --dir: %w", err)
		}
		dir = def
	}
	root := commands.Root
	root.Env = config.EnvVars()
	pages := generate.Roff(root, "", version)
	names := make([]string, 0, len(pages))
	for _, page := range pages {
		names = append(names, page.Name)
	}
	// Before MkdirAll: the destination is usually outside the repo, so describing
	// it must not create it.
	if mf.DryRun && opts.Format != outputText {
		return emitRecord(opts, manInstallOutput{Dir: dir, DryRun: true, Pages: names}, names)
	}
	if mf.DryRun {
		for _, page := range pages {
			fmt.Fprintf(os.Stdout, "would write %s\n", filepath.Join(dir, page.Name))
		}
		fmt.Fprintf(os.Stdout, "dry run: %d man page(s) would be written to %s; nothing was changed\n", len(pages), dir)
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("magus self man: create %s: %w", dir, err)
	}
	for _, page := range pages {
		path := filepath.Join(dir, page.Name)
		if err := os.WriteFile(path, page.Content, 0o644); err != nil {
			return fmt.Errorf("magus self man: write %s: %w", path, err)
		}
	}
	if opts.Format != outputText {
		return emitRecord(opts, manInstallOutput{Dir: dir, Pages: names}, names)
	}
	fmt.Fprintf(os.Stdout, "Installed %d man pages to %s\n", len(pages), dir)
	return nil
}

type manInstallOutput struct {
	Dir    string   `json:"dir"`
	DryRun bool     `json:"dry_run"`
	Pages  []string `json:"pages"`
}

func defaultManDir() (string, error) {
	data, err := xdg.DataDir()
	if err != nil {
		return "", fmt.Errorf("resolve the data directory: %w", err)
	}
	return filepath.Join(data, "man", "man1"), nil
}

func manUsage() {
	page, _ := commandAt("self man")
	fmt.Fprintf(os.Stderr, "Usage: %s\n\n%s\n\nFlags:\n", page.Usage, page.Short)
	for _, f := range page.Flags {
		fmt.Fprintf(os.Stderr, "  --%-10s %s\n", f.Name, f.Doc)
	}
}
