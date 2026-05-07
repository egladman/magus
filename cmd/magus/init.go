package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/interactive"
)

// Starter magusfiles written by `magus init` when a directory has none.
// They double as the canonical example magusfiles referenced in the docs, so
// edits here are user-facing in both places.
//
//go:embed starter/magusfile.tl
var starterMagusfileTeal string

//go:embed starter/magusfile.bzz
var starterMagusfileBuzz string

// The starter magusfiles load a workspace-local "hello" spell. It is authored
// in Teal regardless of the magusfile language and scaffolded under the spell
// directory convention at spells/hello/spell.tl.
//
//go:embed starter/spells/hello/spell.tl
var starterSpellHello string

// initCmd implements `magus init`: bootstrap a magus workspace in the
// current directory. It writes a magus.yaml to $XDG_CONFIG_HOME/magus/
// by default (use --local to write into the repo instead), stubs a
// magusfile (teal by default, or buzz via --lang) when the directory has
// none, and wires the VCS merge driver so conflicts in declared outputs
// regenerate instead of producing conflict markers.
//
// With --global only the global XDG config is written; the workspace
// bootstrap (magusfile stub + merge driver) is per-clone and skipped.
// With --local the config is written into the repo (CWD) instead of XDG.
func initCmd(ctx context.Context, root string, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	useGlobal := fs.Bool("global", false, "Write only the global config ($XDG_CONFIG_HOME/magus/magus.yaml); skips workspace bootstrap")
	useLocal := fs.Bool("local", false, "Write config into the repo (CWD) instead of $XDG_CONFIG_HOME/magus/")
	force := fs.Bool("force", false, "Overwrite an existing config file")
	vcsName := fs.String("vcs", "", "VCS to wire the merge driver for (git|hg); prompts when omitted on a TTY")
	lang := fs.String("lang", "teal", "magusfile language to scaffold (teal|buzz)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus init [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Bootstrap a magus workspace in the current directory:")
		fmt.Fprintln(os.Stderr, "  - write magus.yaml to $XDG_CONFIG_HOME/magus/ (default) or CWD (--local)")
		fmt.Fprintln(os.Stderr, "  - stub a magusfile (teal or buzz via --lang) when none exists")
		fmt.Fprintln(os.Stderr, "  - wire the VCS merge driver so conflicts in declared outputs regenerate")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "The VCS is taken from --vcs, or picked interactively when stdin is a")
		fmt.Fprintln(os.Stderr, "terminal. With --global only the global config is written.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *useGlobal && *useLocal {
		return fmt.Errorf("init: --global and --local are mutually exclusive")
	}

	switch *lang {
	case "teal", "buzz":
	default:
		return fmt.Errorf("init: unknown --lang %q (choose teal or buzz)", *lang)
	}

	// --global: write XDG config only, skip workspace bootstrap.
	if *useGlobal {
		cfgPath, err := xdgConfigPath()
		if err != nil {
			return fmt.Errorf("init --global: %w", err)
		}
		if err := config.Init(cfgPath, *force); err != nil {
			return err
		}
		slog.InfoContext(ctx, "init: wrote global config", slog.String("path", cfgPath))
		printInitNextSteps(ctx, cfgPath, "", false)
		return nil
	}

	// Resolve config path: XDG (default) or CWD (--local).
	var cfgPath string
	var isLocal bool
	if *useLocal {
		cfgPath = config.Filename
		isLocal = true
	} else {
		p, err := xdgConfigPath()
		if err != nil {
			return fmt.Errorf("init: %w", err)
		}
		cfgPath = p
	}

	if err := config.Init(cfgPath, *force); err != nil {
		return err
	}
	slog.InfoContext(ctx, "init: wrote config", slog.String("path", cfgPath))

	if err := writeMagusfileStub(".", *lang); err != nil {
		return err
	}

	if err := installMergeDriverForInit(ctx, root, *vcsName); err != nil {
		return err
	}

	printInitNextSteps(ctx, cfgPath, *lang, isLocal)
	return nil
}

// xdgConfigPath returns $XDG_CONFIG_HOME/magus/magus.yaml, creating the
// directory if it does not exist.
func xdgConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine config directory: %w", err)
	}
	magusDir := filepath.Join(dir, "magus")
	if err := os.MkdirAll(magusDir, 0o755); err != nil {
		return "", fmt.Errorf("create config dir %s: %w", magusDir, err)
	}
	return filepath.Join(magusDir, config.Filename), nil
}

// printInitNextSteps prints actionable hints after a successful init.
// Gated on interactive.Enabled() so MAGUS_HINTS_ENABLED=false silences it.
// cfgPath is where magus.yaml was written; lang is the scaffolded language
// (empty when --global was used); isLocal is true when --local was used.
func printInitNextSteps(ctx context.Context, cfgPath, lang string, isLocal bool) {
	if !interactive.Enabled() {
		return
	}

	interactive.Emit(os.Stderr, fmt.Sprintf("config written to %s", cfgPath))

	if lang != "" {
		magusfile := "magusfile.tl"
		if lang == "buzz" {
			magusfile = "magusfile.bzz"
		}
		interactive.Emit(os.Stderr, fmt.Sprintf("magusfile scaffolded: %s", magusfile))
		interactive.Emit(os.Stderr, "run your first target:  magus run build")
	}

	if isLocal {
		interactive.Emit(os.Stderr, "tip: commit magus.yaml to share config with your team")
	}

	// Daemon rc snippet — clearly optional, copy/paste only.
	interactive.Emit(os.Stderr, "")
	interactive.Emit(os.Stderr, "optional: start the daemon automatically on login")
	interactive.Emit(os.Stderr, "add to ~/.bashrc, ~/.zshrc, or ~/.profile:")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  # start magus daemon on login (magus works fine without it)")
	fmt.Fprintln(os.Stderr, "  if command -v magus >/dev/null 2>&1 && \\")
	fmt.Fprintln(os.Stderr, "     ! magus status --probe=liveness >/dev/null 2>&1; then")
	fmt.Fprintln(os.Stderr, "    magus server start &")
	fmt.Fprintln(os.Stderr, "  fi")
	fmt.Fprintln(os.Stderr, "")
	interactive.Emit(os.Stderr, "stop with:  magus server stop")
}

// writeMagusfileStub writes a starter magusfile in dir when the directory has
// no magus declaration file yet. lang selects the scaffold language: "teal"
// writes magusfile.tl, "buzz" writes magusfile.bzz. A pre-existing
// magusfile.{tl,bzz} or magusfiles/ directory is left untouched.
func writeMagusfileStub(dir, lang string) error {
	if magusfilePresent(dir) {
		return nil
	}
	filename, content := "magusfile.tl", starterMagusfileTeal
	if lang == "buzz" {
		filename, content = "magusfile.bzz", starterMagusfileBuzz
	}
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("init: write %s: %w", path, err)
	}
	slog.Info("init: wrote magusfile", slog.String("path", path))

	// The magusfile loads the "hello" spell; scaffold it under the directory
	// convention (spells/<name>/spell.tl). Leave an existing one untouched so
	// re-running init never clobbers edits.
	spellPath := filepath.Join(dir, "spells", "hello", "spell.tl")
	if _, err := os.Stat(spellPath); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(spellPath), 0o755); err != nil {
		return fmt.Errorf("init: create %s: %w", filepath.Dir(spellPath), err)
	}
	if err := os.WriteFile(spellPath, []byte(starterSpellHello), 0o644); err != nil {
		return fmt.Errorf("init: write %s: %w", spellPath, err)
	}
	slog.Info("init: wrote spell", slog.String("path", spellPath))
	return nil
}

// magusfilePresent reports whether dir already holds a magus project
// declaration: a magusfile.{tl,bzz} file or a magusfiles/ directory.
func magusfilePresent(dir string) bool {
	for _, name := range []string{"magusfile.tl", "magusfile.bzz"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	if fi, err := os.Stat(filepath.Join(dir, "magusfiles")); err == nil && fi.IsDir() {
		return true
	}
	return false
}
