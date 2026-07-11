package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"golang.org/x/term"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/interactive/clihint"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/types"
)

// xTargets is the target set offered after the project picker. Mirrors
// manpage.CommonSubcommands minus "ls" (no-op for the shorthand UX).
var xTargets = []string{"build", "test", "lint", "format", "clean", "generate", "ci"}

// x dispatches `magus x [filter...]`: TTY-only fuzzy project + target
// shorthand for `magus run`.
func x(ctx context.Context, root string, _ runConfig, args []string) error {
	var step *bool
	filters, err := cmdParse("x", args, func(fs *flag.FlagSet) {
		step = fs.Bool("step", false, "Pause before each subprocess for interactive stepping (implies --concurrency=1)")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus x [filter...]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Interactive project + target picker. Filters are AND-combined")
			fmt.Fprintln(os.Stderr, "substrings; leaf-anchored longest match wins ranking.")
			fmt.Fprintln(os.Stderr, "Requires an interactive terminal — for scripts use `magus run`.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}

	if !isInteractiveTTY() && !globalCfg.AssumeInteractive {
		fmt.Fprintf(os.Stderr, "magus: x requires an interactive terminal; use `%s` instead\n", clihint.Run.With("<target>", "<project>"))
		fmt.Fprintln(os.Stderr, "       (set assume_interactive: true in magus.yaml or MAGUS_ASSUME_INTERACTIVE=1 to override)")
		return errSilent{exitCode: 2}
	}

	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}
	all := m.All()
	if len(all) == 0 {
		return errors.New("no projects in workspace")
	}

	chosen, err := pickProject(all, filters)
	if err != nil {
		if errors.Is(err, tty.ErrAborted) {
			return nil
		}
		return err
	}

	state, _ := interactive.LoadState()
	last := state.LastTarget[chosen.Dir]
	targetName, err := pickTarget(last)
	if err != nil {
		if errors.Is(err, tty.ErrAborted) {
			return nil
		}
		return err
	}

	if state.LastTarget == nil {
		state.LastTarget = make(map[string]string)
	}
	state.LastTarget[chosen.Dir] = targetName
	_ = interactive.SaveState(state)

	m.LogScope(chosen.Path, "")

	if *step {
		ctx = withStepGate(ctx)
	}

	if targetName == "ci" {
		ciTargets := []types.Target{{Path: chosen.Path, Name: "ci"}}
		var ciOpts []magus.RunOption
		if globalCfg.DryRun {
			ciOpts = append(ciOpts, magus.WithDryRun())
		}
		if *step {
			ciOpts = append(ciOpts, magus.WithStep())
		}
		return m.RunCI(ctx, ciTargets, ciOpts...)
	}
	// Expand short aliases.
	targetName = canonicalTarget(targetName)
	targets := []types.Target{{Path: chosen.Path, Name: targetName}}
	var xOpts []magus.RunOption
	if globalCfg.DryRun {
		xOpts = append(xOpts, magus.WithDryRun())
	}
	if *step {
		xOpts = append(xOpts, magus.WithStep())
	}
	if charms := withDefaultCharms(nil, globalCfg.DefaultCharms, false); len(charms) > 0 {
		xOpts = append(xOpts, magus.WithCharms(charms...))
	}
	return m.Run(ctx, targets, xOpts...)
}

// pickProject filters all projects by the AND-substring rule, ranks the
// survivors, and either returns the unique top scorer or prompts the picker.
func pickProject(all []*types.Project, filters []string) (*types.Project, error) {
	scored := interactive.ScoreProjects(all, filters)
	if len(scored) == 0 {
		// No matches even before the picker: open the picker over the
		// full set so the user can adjust their filter without re-running.
		scored = interactive.ScoreProjects(all, nil)
	}

	if len(scored) == 1 || (len(scored) > 1 && scored[0].Score > scored[1].Score && len(filters) > 0) {
		// Unique-by-score: skip the picker. The "len(filters) > 0" guard
		// stops us from auto-picking a project when the user typed `x`
		// with no filters and just wants to browse.
		if len(scored) == 1 || len(filters) > 0 {
			return scored[0].P, nil
		}
	}

	items := make([]string, len(scored))
	for i, s := range scored {
		items[i] = s.P.Path
	}
	idx, err := tty.Pick(items, tty.Options{
		Prompt:        "project",
		InitialFilter: "",
	})
	if err != nil {
		return nil, err
	}
	return scored[idx].P, nil
}

// pickTarget shows the target list with last (if any) pre-highlighted.
func pickTarget(last string) (string, error) {
	initial := 0
	for i, v := range xTargets {
		if v == last {
			initial = i
			break
		}
	}
	idx, err := tty.Pick(xTargets, tty.Options{
		Prompt:  "target",
		Initial: initial,
		MaxRows: len(xTargets),
	})
	if err != nil {
		return "", err
	}
	return xTargets[idx], nil
}

// isInteractiveTTY reports whether stdin and stderr are both terminals.
func isInteractiveTTY() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
}
