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

// starterMagusfileBuzz is the starter magusfile written by `magus init` when a
// directory has none. It doubles as the canonical example magusfile referenced in
// the docs, so edits here are user-facing in both places.
//
//go:embed starter/magusfile.buzz
var starterMagusfileBuzz string

// initCmd implements `magus init`: bootstrap a magus workspace in the
// current directory. It writes a magus.yaml to $XDG_CONFIG_HOME/magus/
// by default (use --local to write into the repo instead), stubs a
// magusfile.buzz when the directory has none, and wires the VCS merge driver
// so conflicts in declared outputs regenerate instead of producing conflict
// markers.
//
// With --global only the global XDG config is written; the workspace
// bootstrap (magusfile stub + merge driver) is per-clone and skipped.
// With --local the config is written into the repo (CWD) instead of XDG.
func initCmd(ctx context.Context, root string, args []string) error {
	// `magus init spell <name>` is a noun subcommand: scaffold a spell rather than
	// bootstrap the workspace. Consistent with the describe/config noun grammar.
	if len(args) > 0 && args[0] == "spell" {
		return initSpellCmd(ctx, args[1:])
	}

	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	useGlobal := fs.Bool("global", false, "Write only the global config ($XDG_CONFIG_HOME/magus/magus.yaml); skips workspace bootstrap")
	useLocal := fs.Bool("local", false, "Write config into the repo (CWD) instead of $XDG_CONFIG_HOME/magus/")
	force := fs.Bool("force", false, "Overwrite an existing config file")
	vcsName := fs.String("vcs", "", "VCS to wire the merge driver for (git|hg); prompts when omitted on a TTY")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus init [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Bootstrap a magus workspace in the current directory:")
		fmt.Fprintln(os.Stderr, "  - write magus.yaml to $XDG_CONFIG_HOME/magus/ (default) or CWD (--local)")
		fmt.Fprintln(os.Stderr, "  - stub a magusfile.buzz when none exists")
		fmt.Fprintln(os.Stderr, "  - wire the VCS merge driver so conflicts in declared outputs regenerate")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "The VCS is taken from --vcs, or picked interactively when stdin is a")
		fmt.Fprintln(os.Stderr, "terminal. With --global only the global config is written.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Subcommands:")
		fmt.Fprintln(os.Stderr, "  magus init spell <name>   scaffold a new spell at spells/<name>/spell.buzz")
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
		printInitNextSteps(ctx, cfgPath, false, false)
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

	if err := writeMagusfileStub("."); err != nil {
		return err
	}

	if err := installMergeDriverForInit(ctx, root, *vcsName); err != nil {
		return err
	}

	printInitNextSteps(ctx, cfgPath, true, isLocal)
	return nil
}

// xdgConfigPath returns $XDG_CONFIG_HOME/magus/magus.yaml, creating the
// directory if it does not exist.
func xdgConfigPath() (string, error) {
	dir, err := config.UserConfigDir()
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
// cfgPath is where magus.yaml was written; scaffolded is true when a magusfile.buzz
// was stubbed (false when --global was used); isLocal is true when --local was used.
func printInitNextSteps(_ context.Context, cfgPath string, scaffolded, isLocal bool) {
	if !interactive.Enabled() {
		return
	}

	interactive.Emit(os.Stderr, fmt.Sprintf("config written to %s", cfgPath))

	if scaffolded {
		interactive.Emit(os.Stderr, "magusfile scaffolded: magusfile.buzz")
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

// writeMagusfileStub writes a starter magusfile.buzz in dir when the directory has
// no magus declaration file yet. A pre-existing magusfile.buzz or magusfiles/
// directory is left untouched.
func writeMagusfileStub(dir string) error {
	if magusfilePresent(dir) {
		return nil
	}
	path := filepath.Join(dir, "magusfile.buzz")
	if err := os.WriteFile(path, []byte(starterMagusfileBuzz), 0o644); err != nil {
		return fmt.Errorf("init: write %s: %w", path, err)
	}
	slog.Info("init: wrote magusfile", slog.String("path", path))
	return nil
}

// magusfilePresent reports whether dir already holds a magus project
// declaration: a magusfile.buzz file or a magusfiles/ directory.
func magusfilePresent(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "magusfile.buzz")); err == nil {
		return true
	}
	if fi, err := os.Stat(filepath.Join(dir, "magusfiles")); err == nil && fi.IsDir() {
		return true
	}
	return false
}
