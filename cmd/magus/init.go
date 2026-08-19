package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/interactive"
)

// starterMagusfileBuzz is the starter magusfile written by `magus init` when a
// directory has none. It doubles as the canonical example magusfile referenced in
// the docs, so edits here are user-facing in both places.
//
// Named magusfile.tmpl.buzz, NOT magusfile.buzz: this is template data that ships
// inside the binary, and "magusfile.buzz" is a project marker (see projectMarkers),
// so the old name made discovery register magus's own repository as owning a project
// here. It then failed doctor's language-coverage check, and the only ways to quiet
// that were to bind a spell the template must not have or to write an opt-out into
// the very file `magus init` hands new users - ceremony in the one file whose whole
// point is that a magusfile's mere presence is enough. The .buzz suffix stays so the
// shipped template is still linted and syntax-checked as Buzz.
//
//go:embed starter/magusfile.tmpl.buzz
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
	bindDisplayFlags(fs)
	inf := gen.BindInit(fs)
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

	if inf.Global && inf.Local {
		return fmt.Errorf("init: --global and --local are mutually exclusive")
	}

	// Ahead of every write, because init is the one command that touches three
	// places at once - a config file usually OUTSIDE the repo, a magusfile in it,
	// and the VCS merge driver in git's own config. Seeing those three named is
	// the difference between bootstrapping a workspace and discovering later that
	// something rewired your repository.
	if inf.DryRun {
		return printInitPlan(inf.Global, inf.Local, inf.Force)
	}

	// --global: write XDG config only, skip workspace bootstrap.
	if inf.Global {
		cfgPath, err := xdgConfigPath()
		if err != nil {
			return fmt.Errorf("init --global: %w", err)
		}
		if err := config.Init(cfgPath, inf.Force); err != nil {
			return err
		}
		slog.InfoContext(ctx, "init: wrote global config", slog.String("path", cfgPath))
		printInitNextSteps(ctx, cfgPath, false, false)
		return nil
	}

	// Resolve config path: XDG (default) or CWD (--local).
	var cfgPath string
	var isLocal bool
	if inf.Local {
		cfgPath = config.Filename
		isLocal = true
	} else {
		p, err := xdgConfigPath()
		if err != nil {
			return fmt.Errorf("init: %w", err)
		}
		cfgPath = p
	}

	if err := config.Init(cfgPath, inf.Force); err != nil {
		return err
	}
	slog.InfoContext(ctx, "init: wrote config", slog.String("path", cfgPath))

	if err := writeMagusfileStub("."); err != nil {
		return err
	}

	if err := installMergeDriverForInit(ctx, root, inf.VCS); err != nil {
		return err
	}

	printInitNextSteps(ctx, cfgPath, true, isLocal)
	return nil
}

// xdgConfigTarget returns $XDG_CONFIG_HOME/magus/magus.yaml and creates
// nothing, so --dry-run can name the destination without making a directory
// on the way to describing it.
func xdgConfigTarget() (string, error) {
	dir, err := config.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine config directory: %w", err)
	}
	return filepath.Join(dir, "magus", config.Filename), nil
}

// xdgConfigPath returns the same path, creating the directory if it does not
// exist. This is the form a real run uses.
func xdgConfigPath() (string, error) {
	target, err := xdgConfigTarget()
	if err != nil {
		return "", err
	}
	magusDir := filepath.Dir(target)
	if err := os.MkdirAll(magusDir, 0o755); err != nil {
		return "", fmt.Errorf("create config dir %s: %w", magusDir, err)
	}
	return target, nil
}

// printInitPlan names every destination a real init would touch, and reports
// which of them already exist, without creating any of them.
//
// It reads state rather than assuming: a config that is already there is only
// overwritten with --force, and a magusfile is never overwritten at all, so a
// plan that said "would write" for both would overstate what happens.
func printInitPlan(global, local, force bool) error {
	target, err := xdgConfigTarget()
	if err != nil {
		return err
	}
	cfgPath := target
	if local {
		cfgPath = config.Filename
	}

	verb := "would write"
	if _, err := os.Stat(cfgPath); err == nil {
		verb = "exists, would be left alone (pass --force to overwrite)"
		if force {
			verb = "exists, would be OVERWRITTEN (--force)"
		}
	}
	fmt.Fprintf(os.Stdout, "config:       %s - %s\n", cfgPath, verb)

	if global {
		fmt.Fprintln(os.Stdout, "magusfile:    skipped (--global writes only the config)")
		fmt.Fprintln(os.Stdout, "merge driver: skipped (--global writes only the config)")
		fmt.Fprintln(os.Stdout, "dry run: nothing was changed")
		return nil
	}

	if magusfilePresent(".") {
		fmt.Fprintln(os.Stdout, "magusfile:    already present, would be left alone")
	} else {
		fmt.Fprintln(os.Stdout, "magusfile:    magusfile.buzz - would write a starter")
	}
	// Named as a config write rather than a file write: this is the step that
	// edits the repository's VCS configuration, and it is the least expected one.
	fmt.Fprintln(os.Stdout, "merge driver: would wire the magus merge driver into your VCS config")
	fmt.Fprintln(os.Stdout, "dry run: nothing was changed")
	return nil
}

// printInitNextSteps prints actionable hints after a successful init.
// Gated on the user-controlled hints preference, not terminal interactivity.
// cfgPath is where magus.yaml was written; scaffolded is true when a magusfile.buzz
// was stubbed (false when --global was used); isLocal is true when --local was used.
func printInitNextSteps(_ context.Context, cfgPath string, scaffolded, isLocal bool) {
	if !interactive.HintsEnabled() {
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

	// Point users at the agent surface. A hint, not a step: connecting a client
	// is per-user and per-machine (it writes the client's config, not the repo),
	// so it does not belong in repo bootstrap - init just says where to look.
	interactive.Emit(os.Stderr, "")
	interactive.Emit(os.Stderr, "let an AI agent use this workspace over the daemon (graph-aware skills + MCP tools):")
	interactive.Emit(os.Stderr, "  magus agent install .agents/skills  # Agent Skills; it also prints the AGENTS.md block to paste")
	interactive.Emit(os.Stderr, "  magus config mcp connector create --name <client>  # mint a token, then configure the client")
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
