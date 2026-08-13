package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/interactive/clihint"
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
			fmt.Fprintln(os.Stderr, "Requires an interactive terminal - for scripts use `magus run`.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}

	// No override: x draws a picker and reads keystrokes, so without a terminal
	// there is nothing for it to do. A config key that claims otherwise only
	// moves the failure later, into a redraw against a pipe.
	if !isInteractiveTTY() {
		fmt.Fprintf(os.Stderr, "magus: x requires an interactive terminal; use `%s` instead\n", clihint.Run.With("<target>", "<project>"))
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

	chosen, err := pickProject(ctx, root, all, filters)
	if err != nil {
		if errors.Is(err, tty.ErrAborted) {
			return nil
		}
		return err
	}

	state, _ := interactive.LoadState()
	last := state.LastTarget[chosen.Dir]
	targetName, err := pickTarget(ctx, last)
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

	m.LogScope(ctx, chosen.Path, "")

	if *step && !isInteractiveTTY() {
		fmt.Fprintln(os.Stderr, "magus: --step requires an interactive terminal")
		return errSilent{exitCode: 2}
	}
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
// graphLookup returns a live search over the knowledge graph, resolving typed
// text to the PROJECT that owns whatever it names, or nil when no graph is
// available.
//
// This is what makes `x` graph-aware rather than a list filter: the graph knows
// files, symbols, docs, targets and spells, so typing "hasher" finds the project
// that defines it, not just the projects whose PATH happens to contain those
// letters. Everything the picker already does - the mouse, the inline drawing,
// the way out - is unchanged; only where the candidates come from moves.
//
// Degrades to nil rather than failing. A workspace whose graph has never been
// built, or a daemon that is not running, still gets the path filter it always
// had; a picker that refused to open because a search index was cold would be
// strictly worse than one that searches less.
func graphLookup(ctx context.Context, root string, byPath map[string]*types.Project) func(string) []string {
	// Loaded in the BACKGROUND, because loading it takes most of a second and
	// the picker has to be on screen before then. Blocking the open on a search
	// index means typing `magus x` and watching nothing happen - which is worse
	// than searching less, and is the exact input lag this surface exists to
	// avoid.
	//
	// Until it arrives, the caller's path filter answers. From the first
	// keystroke after it lands, the graph does. Nothing waits.
	var (
		mu    sync.Mutex
		g     *knowledge.Graph
		ready bool
	)
	go func() {
		loaded, err := loadKnowledgeGraph(ctx, root, false, false, false)
		mu.Lock()
		defer mu.Unlock()
		if err == nil {
			g = loaded
		}
		ready = true
	}()

	return func(filter string) []string {
		mu.Lock()
		graph, done := g, ready
		mu.Unlock()
		if !done || graph == nil {
			return nil // not yet, or never: the path filter carries it
		}
		return resolveProjects(graph, filter, byPath)
	}
}

// resolveProjects turns one query into the projects that own its matches.
func resolveProjects(g *knowledge.Graph, filter string, byPath map[string]*types.Project) []string {
	return func(filter string) []string {
		if strings.TrimSpace(filter) == "" {
			return nil // no query yet: the caller's own ordering stands
		}
		seen := map[string]bool{}
		var out []string
		for _, m := range g.Resolve(filter, graphMatchLimit) {
			p, ok := projectForNode(m, byPath)
			if !ok || seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
		return out
	}(filter)
}

// graphMatchLimit bounds one keystroke's search. The picker shows ten rows, and
// resolving far past what can be drawn spends time on results nobody sees.
const graphMatchLimit = 60

// projectForNode maps a graph match back to the project path the picker deals
// in, or reports that it belongs to none.
func projectForNode(m types.KnowledgeMatch, byPath map[string]*types.Project) (string, bool) {
	// The node id carries its owner as a prefix for every kind that has one
	// (target:<project>:<name>, file:<path>, ...), so the longest declared
	// project path that prefixes it is the owner.
	best := ""
	for path := range byPath {
		if path == "." {
			continue
		}
		if strings.Contains(m.ID, path) && len(path) > len(best) {
			best = path
		}
	}
	if best == "" {
		if _, ok := byPath["."]; ok && m.ID != "" {
			return ".", true
		}
		return "", false
	}
	return best, true
}

func pickProject(ctx context.Context, root string, all []*types.Project, filters []string) (*types.Project, error) {
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
	byPath := make(map[string]*types.Project, len(all))
	for _, p := range all {
		byPath[p.Path] = p
	}

	opts := tty.PickOptions{Prompt: "project", InitialFilter: ""}
	// shown is the list the picker last drew. A live query REPLACES the items,
	// so the index it returns is into whatever the final keystroke produced -
	// not into `scored`. Recording it is how the label maps back to a project.
	shown := items
	if q := graphLookup(ctx, root, byPath); q != nil {
		opts.Query = func(_ context.Context, filter string) ([]string, error) {
			hits := q(filter)
			if len(hits) == 0 {
				// No graph match: fall back to the path filter, so typing
				// something the graph has never heard of still narrows the
				// list rather than emptying it.
				hits = filterPaths(items, filter)
			}
			shown = hits
			return hits, nil
		}
	}

	idx, err := tty.Pick(ctx, os.Stdin, os.Stderr, tty.SystemProbe, items, opts)
	if err != nil {
		return nil, err
	}
	if opts.Query == nil {
		return scored[idx].P, nil
	}
	if idx < 0 || idx >= len(shown) {
		return nil, fmt.Errorf("picker returned %d for %d shown", idx, len(shown))
	}
	return byPath[shown[idx]], nil
}

// filterPaths is the path-substring narrowing the picker has always done, kept
// as the fallback for text the graph does not know.
func filterPaths(items []string, filter string) []string {
	f := strings.ToLower(strings.TrimSpace(filter))
	if f == "" {
		return items
	}
	var out []string
	for _, it := range items {
		if strings.Contains(strings.ToLower(it), f) {
			out = append(out, it)
		}
	}
	return out
}

// pickTarget shows the target list with last (if any) pre-highlighted.
func pickTarget(ctx context.Context, last string) (string, error) {
	initial := 0
	for i, v := range xTargets {
		if v == last {
			initial = i
			break
		}
	}
	idx, err := tty.Pick(ctx, os.Stdin, os.Stderr, tty.SystemProbe, xTargets, tty.PickOptions{
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
	return tty.StdinIsTerminal() && tty.IsTerminalWriter(os.Stderr, tty.SystemProbe)
}
