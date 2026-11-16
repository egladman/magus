package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/egladman/magus"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/scaffold"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/vcs"
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
// the very file `magus init` hands new users: ceremony in the one file whose whole
// point is that a magusfile's mere presence is enough. The .buzz suffix stays so the
// shipped template is still linted and syntax-checked as Buzz.
//
//go:embed starter/magusfile.tmpl.buzz
var starterMagusfileBuzz string

// initCommand implements `magus init`: bootstrap a magus workspace in the
// current directory. It writes a magus.yaml to $XDG_CONFIG_HOME/magus/
// by default (use --local to write into the repo instead), stubs a
// magusfile.buzz when the directory has none, and wires the VCS merge driver
// so conflicts in declared outputs regenerate instead of producing conflict
// markers.
//
// With --global only the global XDG config is written; the workspace
// bootstrap (magusfile stub + merge driver) is per-clone and skipped.
// With --local the config is written into the repo (root) instead of XDG.
// The magusfile stub and merge driver are always scoped to root; the default
// (non-local) config path is not, so --root without --local or --global is
// refused rather than silently writing the real global config.
func initCommand(ctx context.Context, root string, args []string) error {
	// `magus init spell <name>` is a noun subcommand: scaffold a spell rather than
	// bootstrap the workspace. Consistent with the describe/config noun grammar.
	if len(args) > 0 && args[0] == "spell" {
		return initSpellCommand(ctx, args[1:])
	}

	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	bindDisplayFlags(fs)
	inf := flaggen.BindInit(fs)
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
		fmt.Fprintln(os.Stderr, "--root scopes the magusfile stub and merge driver; combine it with --local")
		fmt.Fprintln(os.Stderr, "or --global too, since --root alone does not change the default config path.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Subcommands:")
		fmt.Fprintln(os.Stderr, "  magus init spell <name>   scaffold a new spell at spells/<name>/spell.buzz")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		printOwnDefaults(fs)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if inf.Global && inf.Local {
		return fmt.Errorf("init: --global and --local are mutually exclusive")
	}
	if inf.DryRun && opts.Format != outputText {
		return exit.Usagef("magus init: -o is not supported with --dry-run, the plan is prose")
	}

	// --root only ever scoped the merge-driver workspace load; the config write (the
	// default target) stayed at $XDG_CONFIG_HOME regardless. An agent pointing --root
	// at a scratch workspace has no reason to expect that combination to touch the
	// real user's global config, so refuse rather than silently writing there: make
	// the caller say which config --root's scoping should mean.
	if root != "" && !inf.Global && !inf.Local {
		return fmt.Errorf("init: --root requires --local or --global (--root does not scope the default config path, " +
			"pass --local to write magus.yaml into --root, or --global to confirm the write to $XDG_CONFIG_HOME/magus/)")
	}

	// Ahead of every write: init touches three places at once, and two are outside
	// the repo: the config, and the merge driver in the VCS's own config.
	if inf.DryRun {
		return scaffold.PrintPlan(root, inf.Global, inf.Local, inf.Force)
	}

	// --global: write XDG config only, skip workspace bootstrap.
	if inf.Global {
		cfgPath, err := scaffold.XDGConfigPath()
		if err != nil {
			return fmt.Errorf("init --global: %w", err)
		}
		wrote, err := config.Init(vcs.WriteJudge(ctx, scaffold.JudgeDir(root)), cfgPath, inf.Force)
		if err != nil {
			return err
		}
		scaffold.LogConfigInit(ctx, cfgPath, wrote)
		if opts.Format != outputText {
			return emitRecord(opts, initOutput{Config: cfgPath, Wrote: wrote, Global: true}, []string{cfgPath})
		}
		scaffold.PrintNextSteps(ctx, cfgPath, false, false)
		return nil
	}

	// Resolve config path: XDG (default) or --root/CWD (--local).
	var cfgPath string
	var isLocal bool
	if inf.Local {
		cfgPath = filepath.Join(root, config.Filename)
		isLocal = true
	} else {
		p, err := scaffold.XDGConfigPath()
		if err != nil {
			return fmt.Errorf("init: %w", err)
		}
		cfgPath = p
	}

	// Workspace bootstrap (magusfile stub + merge driver) before the config write:
	// both are scoped to root and neither destroys anything on retry, while the
	// default config target sits outside the repo entirely ($XDG_CONFIG_HOME).
	// Writing the config first left an orphaned global config behind whenever a
	// later step (e.g. a bad --root) failed.
	if err := scaffold.WriteMagusfileStub(ctx, root, starterMagusfileBuzz); err != nil {
		return err
	}

	if err := scaffold.InstallMergeDriver(ctx, initLoaders, root, inf.VCS); err != nil {
		return err
	}

	wrote, err := config.Init(vcs.WriteJudge(ctx, scaffold.JudgeDir(root)), cfgPath, inf.Force)
	if err != nil {
		return err
	}
	scaffold.LogConfigInit(ctx, cfgPath, wrote)

	if opts.Format != outputText {
		return emitRecord(opts, initOutput{Config: cfgPath, Wrote: wrote, Local: isLocal}, []string{cfgPath})
	}
	scaffold.PrintNextSteps(ctx, cfgPath, true, isLocal)
	return nil
}

// initLoaders hands scaffold the process's memoized workspace load and VCS resolution.
var initLoaders = scaffold.Loaders{
	Load:       func(ctx context.Context, root string) (*magus.Magus, error) { return loadMagus(ctx, root) },
	ResolveVCS: resolveVCS,
}

type initOutput struct {
	Config string `json:"config"`
	Wrote  bool   `json:"wrote"`
	Global bool   `json:"global"`
	Local  bool   `json:"local"`
}
