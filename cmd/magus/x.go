package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"slices"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/termkit/pick"
	"github.com/egladman/magus/types"
)

// x dispatches `magus x [filter...]`: TTY-only fuzzy project + target
// shorthand for `magus run`.
func x(ctx context.Context, root string, _ runConfig, args []string) error {
	var step *bool
	filters, err := commandParse("x", args, func(fs *flag.FlagSet) {
		step = fs.Bool("step", false, "Pause before each subprocess for interactive stepping (implies --concurrency=1)")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus x <ref>          reproduce the run that output ref recorded")
			fmt.Fprintln(os.Stderr, "       magus x [filter...]   interactive project + target picker")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Given an output ref (out1a2b3c) x reproduces that invocation - project,")
			fmt.Fprintln(os.Stderr, "target and charms all come from the ref, so it needs no terminal and")
			fmt.Fprintln(os.Stderr, "works on a ref copied from CI.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Given filters, it is the picker: AND-combined substrings, leaf-anchored")
			fmt.Fprintln(os.Stderr, "longest match wins. That form requires an interactive terminal;")
			fmt.Fprintf(os.Stderr, "for scripts use `%s`.\n", hint.Run.String())
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}

	// A ref names the project and target outright, so there is nothing to pick and
	// the TTY gate below does not apply. Checked first for that reason: the gate
	// would otherwise reject the one form of x that needs no terminal.
	if len(filters) == 1 && interactive.OutputRefShape.MatchString(filters[0]) {
		return reproduceRef(ctx, root, filters[0], *step)
	}

	// No override: x draws a picker and reads keystrokes, so without a terminal
	// there is nothing for it to do. A config key that claims otherwise only
	// moves the failure later, into a redraw against a pipe.
	if !interactive.IsInteractiveTTY() {
		slog.ErrorContext(ctx, fmt.Sprintf("x requires an interactive terminal; use `%s` instead", hint.Run.With("<target>", "<project>")),
			attr.Notice(""), attr.Component("magus"))
		return exit.Reported(exit.Usage)
	}

	m, sink, cleanup, err := openXRun(ctx, root)
	if err != nil {
		return err
	}
	defer func() { _ = cleanup() }()
	all := m.All()
	if len(all) == 0 {
		return fmt.Errorf("magus x: no projects in workspace (a project is a directory with a magusfile.buzz declaring magus\\project), run `%s` to bootstrap one", hint.Init.String())
	}

	chosen, err := interactive.PickProject(ctx, func(ctx context.Context) (interactive.GraphSearch, error) {
		g, err := loadKnowledgeGraph(ctx, root, false, false, false)
		if err != nil {
			return nil, err
		}
		return g.Resolve, nil
	}, all, filters)
	if err != nil {
		if errors.Is(err, pick.ErrAborted) {
			return nil
		}
		return err
	}

	targetName, err := interactive.PickTarget(ctx, interactive.LastTarget(chosen.Dir))
	if err != nil {
		if errors.Is(err, pick.ErrAborted) {
			return nil
		}
		return err
	}
	_ = interactive.SaveLastTarget(chosen.Dir, targetName)

	sink.EmitScope(ctx, chosen.Path, "", []string{chosen.Path})

	if *step {
		ctx = interp.WithStepGate(ctx, workspaceReplCandidates)
	}

	if targetName == "ci" {
		ciTargets := []types.Target{{Path: chosen.Path, Name: "ci"}}
		ciOpts := []magus.RunOption{magus.WithSink(sink), magus.WithObserver(sink.GraphObserver())}
		if globalCfg.DryRun {
			ciOpts = append(ciOpts, magus.WithDryRun())
		}
		if *step {
			ciOpts = append(ciOpts, magus.WithStep())
		}
		charms, note := execCICharms(withDefaultCharms(nil, globalCfg.DefaultCharms, false))
		if note != "" {
			slog.InfoContext(ctx, note, attr.Notice(""), attr.Component("magus"))
		}
		return runAndEmit(ctx, m, "ci", nil, ciTargets, withCharms(ciOpts, charms))
	}
	// Expand short aliases.
	targetName = canonicalTarget(targetName)
	targets := []types.Target{{Path: chosen.Path, Name: targetName}}
	xOpts := []magus.RunOption{magus.WithSink(sink), magus.WithObserver(sink.GraphObserver())}
	if globalCfg.DryRun {
		xOpts = append(xOpts, magus.WithDryRun())
	}
	if *step {
		xOpts = append(xOpts, magus.WithStep())
	}
	if charms := withDefaultCharms(nil, globalCfg.DefaultCharms, false); len(charms) > 0 {
		xOpts = append(xOpts, magus.WithCharms(charms...))
	}
	return runAndEmit(ctx, m, targetName, nil, targets, xOpts)
}

// execCICharms is the set `magus x ci` runs under: charms without the
// write-granting ones, since x verifies only and never repairs. note says
// so when rw was carried, and is empty otherwise.
func execCICharms(charms []string) (verify []string, note string) {
	verify = magus.CharmsForCI(charms)
	if slices.ContainsFunc(charms, func(c string) bool {
		return types.NormalizeCharm(c) == types.CharmReadWrite
	}) {
		note = "rw is carried, but magus x verifies only, so this ci run repairs nothing; `magus run ci` repairs"
	}
	return verify, note
}

// runAndEmit runs what x picked and renders it as `magus run` does.
// Under jsonl the sink has already streamed it.
func runAndEmit(ctx context.Context, m *magus.Magus, target string, charms []string, targets []types.Target, runOpts []magus.RunOption) error {
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	run := m.Run
	if target == "ci" {
		run = m.RunCI
	}
	if err := run(ctx, targets, runOpts...); err != nil {
		return err
	}
	switch opts.Format {
	case outputJSON, outputYAML, outputTemplate:
		return emitRunResult(ctx, m, opts, target, charms, targets, nil, nil, nil)
	case outputName:
		return emitProjectNames(m, targets)
	case outputJSONL, outputText:
	}
	return nil
}

// openXRun loads the workspace and the sink an x run reports through; cleanup flushes
// the sink.
func openXRun(ctx context.Context, root string) (*magus.Magus, *magus.Sink, func() error, error) {
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return nil, nil, nil, err
	}
	m, err := loadMagus(ctx, root)
	if err != nil {
		return nil, nil, nil, err
	}
	sink, cleanup, err := openSink(ctx, opts)
	if err != nil {
		return nil, nil, nil, err
	}
	return m, sink, cleanup, nil
}

// reproduceRef re-runs the invocation an output ref recorded.
//
// The ref is the whole point: it comes off a CI log, names a run on a machine you
// do not have, and the descriptor carries what it takes to run it here: project,
// target (charms folded in by reproTarget), and the revision its inputs were read
// at. When the artifact is in reach the cache replays it; when it is not, this runs
// the same invocation rather than a similar one.
func reproduceRef(ctx context.Context, root, ref string, step bool) error {
	m, sink, cleanup, err := openXRun(ctx, root)
	if err != nil {
		return err
	}
	defer func() { _ = cleanup() }()
	d, err := m.OutputDescriptorByRef(ctx, ref)
	if err != nil {
		// Not here yet, which is the ordinary case for a ref copied out of CI: ask the
		// remote published store before giving up.
		_, remote, rerr := m.OutputByRefRemote(ctx, ref)
		if rerr != nil {
			return err
		}
		d = remote
	}
	if d.Target == "" || d.Project == "" {
		return fmt.Errorf("magus x: ref %s records no target to reproduce", ref)
	}

	parsed, err := types.ParseTarget(d.Target)
	if err != nil {
		return fmt.Errorf("magus x: ref %s records target %q: %w", ref, d.Target, err)
	}

	// Said before running, not after: reproduction that cannot be exact should say so
	// while you can still act on it. Neither condition blocks the run: a rebuild at
	// the wrong revision is still often what you want, and deciding otherwise for you
	// would make the ref useless the moment it is a day old.
	if d.Dirty {
		sink.EmitNotice(ctx, slog.LevelInfo, "", fmt.Sprintf(
			"ref %s was produced from a working tree with uncommitted changes; its revision alone cannot reproduce it", ref))
	}
	// Compared by KEY, not by revision. A different commit is not a different
	// invocation: most commits touch none of a given target's sources, and comparing
	// revisions warned "this will rebuild" for runs that then replayed in 69ms. The
	// key IS the identity the cache decides on, so equality here is not a prediction.
	if d.Key != "" {
		switch live, _, kerr := m.ComputeTargetKey(ctx, d.Project, parsed.Name, parsed.Charms); {
		case kerr != nil:
			// Best-effort: a key that will not compute is not a reason to refuse the run.
			slog.With(attr.Component("x")).DebugContext(ctx, "could not compute the local key", attr.Error(kerr))
		case live == d.Key:
			sink.EmitNotice(ctx, slog.LevelInfo, "", fmt.Sprintf(
				"ref %s reproduces exactly here: same cache key, so this replays the recorded run", ref))
		default:
			reportForeignMachine(ctx, m, sink, ref, d)
		}
	}

	sink.EmitScope(ctx, d.Project, "ref "+ref, []string{d.Project})
	if step {
		ctx = interp.WithStepGate(ctx, workspaceReplCandidates)
	}

	targets := []types.Target{{Path: d.Project, Name: parsed.Name, Charms: parsed.Charms}}
	opts := []magus.RunOption{magus.WithCharms(parsed.Charms...), magus.WithSink(sink), magus.WithObserver(sink.GraphObserver())}
	if d.Spell != "" {
		opts = append(opts, magus.WithSpellFilter(d.Spell))
	}
	if len(d.ExtraArgs) > 0 {
		opts = append(opts, magus.WithExtraArgs(d.ExtraArgs))
	}
	if globalCfg.DryRun {
		opts = append(opts, magus.WithDryRun())
	}
	if step {
		opts = append(opts, magus.WithStep())
	}
	return runAndEmit(ctx, m, parsed.Name, parsed.Charms, targets, opts)
}

// elide keeps a value inside the aligned column, trimming from the LEFT because the
// distinguishing part of a describe-style version is its tail.
func elide(v string) string {
	const max = 22
	if len(v) <= max {
		return v
	}
	return "..." + v[len(v)-(max-3):]
}

func shortRev(r string) string {
	if len(r) > 12 {
		return r[:12]
	}
	return r
}

// reportForeignMachine explains WHY a ref will not replay, naming each differing
// field and both values.
//
// "different inputs" is true and useless: the reader already knows it did not
// replay, and what they need is which of the handful of possible causes it was.
// Only differing fields are printed, so the rows that appear are the diagnosis.
func reportForeignMachine(ctx context.Context, m *magus.Magus, sink *magus.Sink, ref string, d magus.OutputDescriptor) {
	type row struct{ field, recorded, here string }
	var rows []row
	add := func(field, recorded, here string) {
		// Both sides must be known: a field missing from either is unrecorded, not
		// different, and reporting it as a difference invents a cause.
		if recorded != "" && here != "" && recorded != here {
			rows = append(rows, row{field, recorded, here})
		}
	}

	add("platform", d.Platform, runtime.GOOS+"/"+runtime.GOARCH)
	if d.KeyVersion != "" && d.KeyVersion != magus.CacheKeyVersion {
		rows = append(rows, row{"key version", d.KeyVersion, magus.CacheKeyVersion})
	}
	add("magus", d.MagusVersion, version)
	vcsName, head, _ := m.CurrentRevision(ctx)
	add("vcs", d.VCSName, vcsName)
	// Revisions only when the PROVIDERS agree: a git SHA and an hg node id are both
	// 40 hex, so comparing across them answers a question nobody asked.
	if d.VCSName == "" || vcsName == "" || d.VCSName == vcsName {
		add("revision", shortRev(d.Revision), shortRev(head))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "ref %s was produced on a different machine, so this runs the target rather than replaying it\n", ref)
	for _, r := range rows {
		fmt.Fprintf(&b, "  %-11s recorded %-22s here %s\n", r.field, elide(r.recorded), elide(r.here))
	}
	// The insight, not a restatement: source hashes agree across platforms, which is
	// exactly why the key cannot catch this and the import guard has to.
	if d.Platform != "" && d.Platform != runtime.GOOS+"/"+runtime.GOARCH {
		b.WriteString("  sources hash identically across platforms, so the artifact would be refused on import\n")
	}
	if d.KeyVersion != "" && d.KeyVersion != magus.CacheKeyVersion {
		b.WriteString("  the two keys were computed by different key versions and can never match\n")
	}

	fmt.Fprintf(&b, "  see %s", types.CodeURL(types.OutputRefForeignMachine))
	sink.EmitNotice(ctx, slog.LevelWarn, types.OutputRefForeignMachine, b.String())
}
