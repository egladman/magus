package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// vcsCmd implements `magus vcs <subcommand>`.
//
// All three verbs rest on one fact git does not have: which files are generated, and
// which target rebuilds them. `add` classifies paths and emits the ones worth staging,
// `resolve` settles a conflicted merge (and predicts one with --base), `merge-driver` is
// the per-file callback git invokes.
//
// `add` replaces the `git add -A` the agent guard denies by computing the selection
// rather than performing it: magus's knowledge here IS a list of paths, so it emits the
// list and git writes its own index (`magus vcs add -o name | git add
// --pathspec-from-file=-`). The line that separates it from `resolve`: magus mutates the
// recorded state only where the action is NOT expressible as a path list - resolve must
// regenerate between clearing markers and recording, and what regeneration touches
// cannot be known in advance.
//
// Not a general git proxy: wrapping every VCS verb would put magus on the critical path
// of operations it has no opinion about.
func vcsCmd(ctx context.Context, root string, rc runConfig, args []string) error {
	if len(args) == 0 {
		vcsUsage(os.Stderr)
		return usagef("magus vcs: a subcommand is required (try: add, resolve)")
	}
	verb, rest := splitVCSVerb(args)
	switch verb {
	case "add":
		return vcsAddCmd(ctx, root, rest)
	case "resolve":
		return vcsResolveCmd(ctx, root, rc, rest)
	case "merge-driver":
		return mergeDriverCmd(ctx, root, rest)
	case "-h", "--help", "help":
		vcsUsage(os.Stderr)
		return nil
	default:
		return usagef("magus vcs: unknown subcommand %q (want add, resolve, or merge-driver)", verb)
	}
}

// splitVCSVerb returns the subcommand and the remaining args with it removed.
//
// The verb is the first non-flag token, not args[0]: global flags are allowed on either
// side of a subcommand elsewhere in this CLI, and a wrapper can prefix the merge driver's
// registration string. A help flag is reported as the verb.
func splitVCSVerb(args []string) (verb string, rest []string) {
	for i, a := range args {
		if a == "-h" || a == "--help" || !strings.HasPrefix(a, "-") {
			return a, slices.Concat(args[:i], args[i+1:])
		}
	}
	return "", args
}

func vcsUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus vcs <subcommand> [args]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Subcommands:")
	fmt.Fprintln(w, "  add            classify a change and emit the paths worth staging (pipe into `git add --pathspec-from-file=-`)")
	fmt.Fprintln(w, "  resolve        settle an in-progress merge/rebase's conflicted generated files, then regenerate once; --base predicts conflicts before one starts")
	fmt.Fprintln(w, "  merge-driver   the per-file merge driver git and hg invoke; you do not run this by hand")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Run `magus vcs <subcommand> -h` for its own flags.")
}

// ---------------------------------------------------------------- vcs resolve

func vcsResolveUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus vcs resolve [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Settle the conflicted GENERATED files of an in-progress merge, rebase, or")
	fmt.Fprintln(w, "cherry-pick, then regenerate them once and stage the result. Conflicts in")
	fmt.Fprintln(w, "files magus does not generate are reported and left for you.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "This is the bulk counterpart to the merge driver. git invokes a driver once")
	fmt.Fprintln(w, "per conflicted path, inside its own index manipulation, so the cost scales")
	fmt.Fprintln(w, "with the conflict count and no regeneration can run there at all. Deciding")
	fmt.Fprintln(w, "every path first and regenerating once is both faster and the only way to")
	fmt.Fprintln(w, "settle a file one side deleted, which no VCS calls a merge driver for.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "With --base, nothing has to be in progress: the conflicts merging that")
	fmt.Fprintln(w, "revision would produce are PREDICTED (an in-memory 3-way merge; the tree is")
	fmt.Fprintln(w, "not touched) and classified the same way. A hosting service computes a pull")
	fmt.Fprintln(w, "request's mergeability with exactly that merge and never runs a merge")
	fmt.Fprintln(w, "driver, so this is the conflict banner before the push instead of after.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --base <rev>  predict the conflicts a merge with <rev> would produce; run nothing")
	fmt.Fprintln(w, "  --dry-run     classify and report; touch nothing (global flag)")
}

// vcsResolveCmd classifies every conflicted path, settles the generated ones in bulk,
// regenerates once, and records the result.
func vcsResolveCmd(ctx context.Context, root string, rc runConfig, args []string) error {
	var base string
	pos, err := cmdParse("vcs resolve", args, func(fs *flag.FlagSet) {
		fs.StringVar(&base, "base", "", "Predict the conflicts a merge with this revision would produce, without touching the tree")
		fs.Usage = func() { vcsResolveUsage(os.Stderr) }
	})
	if err != nil {
		return err
	}
	// resolve mutates every conflicted path in the workspace. Widening
	// `magus vcs resolve one/file.go` from one path to all of them would cost a tree.
	if len(pos) > 0 {
		return usagef("vcs resolve: takes no paths; it settles every conflicted path in the workspace (got %q)", pos[0])
	}

	// Not the load dispatch would do: opening a workspace refreshes the merge-driver
	// registration, which writes the tracked .gitattributes. During a merge that file may
	// be unmerged, so the refresh would splice a section between conflict markers. It runs
	// after the markers are gone instead.
	m, err := loadMagus(withoutMergeDriverRefresh(ctx), root)
	if err != nil {
		return err
	}
	res, err := resolveVCS(ctx, root, m)
	if err != nil {
		return fmt.Errorf("vcs resolve: no VCS resolved for this workspace: %w", err)
	}
	resolver, ok := res.VCS.(types.ConflictResolver)
	if !ok {
		return fmt.Errorf("vcs resolve: %s cannot report conflicts; resolve this merge by hand", res.Name)
	}
	if base != "" {
		return vcsPreflight(ctx, m, res, resolver, base)
	}

	conflicts, err := resolver.Conflicts(ctx, m.Root())
	if err != nil {
		return fmt.Errorf("vcs resolve: %w", err)
	}
	if len(conflicts) == 0 {
		fmt.Println("vcs resolve: nothing to resolve; no conflicted paths")
		return nil
	}

	plan, err := planResolution(ctx, m, resolver, conflicts)
	if err != nil {
		return err
	}
	reportResolution(plan, globalCfg.DryRun)
	if globalCfg.DryRun {
		return unresolvedError(plan)
	}
	return applyResolution(ctx, root, rc, m, res.VCS, resolver, plan)
}

// applyResolution performs the plan: clear markers, record deletions, regenerate once,
// then record everything the regeneration touched.
//
// Every step past the first has already mutated the tree, so failures report how far it
// got. A half-applied resolve otherwise looks like a fresh conflict with the markers
// missing, which `git status` alone cannot explain.
func applyResolution(ctx context.Context, root string, rc runConfig, m *magus.Magus, driver types.VCSDriver, resolver types.ConflictResolver, plan resolutionPlan) error {
	if err := resolver.KeepIncoming(ctx, m.Root(), slices.Concat(plan.keep, plan.rederive)); err != nil {
		return fmt.Errorf("vcs resolve: %w", err)
	}
	if err := resolver.RemoveConflicts(ctx, m.Root(), plan.gone); err != nil {
		return fmt.Errorf("vcs resolve: %w\n%s", err, resolveTreeState(plan, "the conflict markers were already cleared"))
	}
	if err := runRebuildTargets(ctx, root, rc, plan.rebuild); err != nil {
		return fmt.Errorf("vcs resolve: regenerate: %w\n%s", err, resolveTreeState(plan,
			"the conflict markers were cleared and the deletions recorded, but nothing was marked resolved"))
	}
	// The registration is derived from the declared outputs, so a conflict in the file
	// holding it is settled by re-deriving. First point the file has no markers.
	ensureMergeDriver(ctx, m)

	settled, err := settledPaths(ctx, m, driver, plan)
	if err != nil {
		return fmt.Errorf("vcs resolve: %w", err)
	}
	if err := resolver.MarkResolved(ctx, m.Root(), settled); err != nil {
		return fmt.Errorf("vcs resolve: %w\n%s", err, resolveTreeState(plan, "regeneration completed"))
	}
	fmt.Printf("\nrecorded %d path(s); review before continuing: git diff --cached --stat\n", len(settled))
	return unresolvedError(plan)
}

// resolveTreeState describes how far the resolve got, for an error message.
func resolveTreeState(plan resolutionPlan, reached string) string {
	return fmt.Sprintf("the working tree has been modified: %s. "+
		"To start over, abort the merge (`git rebase --abort` or `git merge --abort`); "+
		"to inspect it, `git status` now shows %d kept and %d removed path(s).",
		reached, len(plan.keep)+len(plan.rederive), len(plan.gone))
}

// resolutionPlan is what resolve decided for each conflicted path, before it acts.
type resolutionPlan struct {
	// keep are generated paths settled by taking a side and regenerating over it.
	keep []string
	// gone are paths whose deletion is the answer: both sides deleted them, or one did
	// and the workspace now ignores them.
	gone []string
	// rederive are paths magus maintains whose content is a function of the workspace, so
	// they are rebuilt rather than merged. Split from keep so the report can say which.
	rederive []string
	// manual are the conflicts magus has no claim over; a human resolves them.
	manual []string
	// rebuild maps a target name to the projects that must run it to rebuild the kept
	// paths. Keyed by target so one `magus run generate` covers every project at once
	// instead of one run per file.
	rebuild map[string][]string
}

// rebuiltProjects returns every project label any rebuild target will run over.
func (p resolutionPlan) rebuiltProjects() map[string]bool {
	out := map[string]bool{}
	for _, projects := range p.rebuild {
		for _, label := range projects {
			out[label] = true
		}
	}
	return out
}

// planResolution classifies every conflict without touching the tree.
//
// A path is settled automatically only when magus can name the target that rebuilds it.
// A VCS reports a conflict when BOTH sides changed a path, so taking a side always
// discards a real change: safe when a later run rewrites the file from source, data loss
// when nothing does.
func planResolution(ctx context.Context, m *magus.Magus, resolver types.ConflictResolver, conflicts []types.Conflict) (resolutionPlan, error) {
	paths := make([]string, len(conflicts))
	for i, c := range conflicts {
		paths[i] = c.Path
	}
	// A generated file that is now ignored was removed from version control on purpose;
	// the other side carries a mechanical regeneration of it. Without this check both
	// sides look like declared outputs and the delete gets reverted every merge.
	ignored, err := resolver.IgnoredPaths(ctx, m.Root(), paths)
	if err != nil {
		return resolutionPlan{}, fmt.Errorf("vcs resolve: %w", err)
	}

	plan := resolutionPlan{rebuild: map[string][]string{}}
	for _, c := range conflicts {
		abs := filepath.Join(m.Root(), filepath.FromSlash(c.Path))
		p := m.FindOutputProducer(abs)
		if p == nil {
			// Not a declared output. The merge-driver registration is the exception:
			// magus writes it, no target declares it, and it is re-derived.
			if vcsMaintainedFiles[c.Path] && c.Kind == types.ConflictKindContent {
				plan.rederive = append(plan.rederive, c.Path)
				continue
			}
			plan.manual = append(plan.manual, c.Path)
			continue
		}
		target, ok := settleTarget(p, abs)
		if !ok {
			plan.manual = append(plan.manual, c.Path)
			continue
		}
		switch {
		case c.Kind == types.ConflictKindBothDeleted:
			// Neither side has content. Record the removal and stop.
			plan.gone = append(plan.gone, c.Path)
			continue
		case c.Kind == types.ConflictKindDeleted && ignored[c.Path]:
			plan.gone = append(plan.gone, c.Path)
			continue
		case c.Kind == types.ConflictKindDeleted:
			// One side deleted a file that is STILL a tracked declared output. Keeping it
			// resurrects a deletion someone meant; dropping it loses an output the
			// workspace declares. Neither is magus's call.
			plan.manual = append(plan.manual, c.Path)
			continue
		}
		plan.keep = append(plan.keep, c.Path)
		if proj := projectRunArg(p); !slices.Contains(plan.rebuild[target], proj) {
			plan.rebuild[target] = append(plan.rebuild[target], proj)
		}
	}
	slices.Sort(plan.keep)
	slices.Sort(plan.gone)
	slices.Sort(plan.rederive)
	slices.Sort(plan.manual)
	return plan, nil
}

// projectRunArg is the string this code feeds back to `magus run <target> <project>`
// for p, and the key rebuiltProjects/settledPaths agree on: the project PATH, with
// the root spelled ".". NOT the display label: ProjectRef.Display renders the root
// project as its directory BASENAME so a bare "." never reaches a human-facing log,
// and in a git worktree that basename is the worktree's own directory name - not a
// project any workspace knows. Feeding the label back to magus made resolve die with
// `unknown project: "<worktree-dir>"`, and using it as the rebuilt-set lookup key made
// settledPaths silently skip every OTHER regenerated root-project output, which is
// exactly the leftover dirty tree that blocks `git rebase --continue`. Display's own
// doc draws this line: labels for reading, the path for anything fed back to magus.
func projectRunArg(p *types.Project) string {
	if p.Path == "" {
		return "."
	}
	return p.Path
}

// runRebuildTargets runs each target ONCE over every project that needs it. Grouping by
// target keeps this a single build: fifty conflicted files still cost one `magus run
// generate` per distinct target.
func runRebuildTargets(ctx context.Context, root string, rc runConfig, rebuild map[string][]string) error {
	for _, target := range slices.Sorted(maps.Keys(rebuild)) {
		projects := rebuild[target]
		slices.Sort(projects)
		fmt.Printf("regenerating: magus run %s %s\n", target, strings.Join(projects, " "))
		if err := runTarget(ctx, root, rc, append([]string{target}, projects...)); err != nil {
			return err
		}
	}
	return nil
}

// settledPaths returns everything to record: the kept paths, plus any OTHER declared
// output the regeneration rewrote.
//
// The second half is the normal case. A generate target writes every output its project
// declares, so recording only the conflicted paths leaves the rest modified and
// unrecorded - the dirty tree that makes `git rebase --continue` refuse.
//
// Limited to outputs of the projects that were rebuilt, so a file you had already
// modified elsewhere is not swept in.
func settledPaths(ctx context.Context, m *magus.Magus, driver types.VCSDriver, plan resolutionPlan) ([]string, error) {
	settled := map[string]bool{}
	for _, p := range slices.Concat(plan.keep, plan.rederive) {
		settled[p] = true
	}
	lines, err := driver.DirtyFiles(ctx, m.Root(), nil)
	if err != nil {
		return nil, fmt.Errorf("list regenerated files: %w", err)
	}
	rebuilt := plan.rebuiltProjects()
	for _, path := range statusPaths(lines) {
		if settled[path] {
			continue
		}
		producer := m.FindOutputProducer(filepath.Join(m.Root(), filepath.FromSlash(path)))
		if producer == nil || !rebuilt[projectRunArg(producer)] {
			continue
		}
		settled[path] = true
	}
	return slices.Sorted(maps.Keys(settled)), nil
}

func reportResolution(plan resolutionPlan, dryRun bool) {
	verb, goneVerb, rederiveVerb := "resolved", "recorded the deletion of", "re-derived"
	if dryRun {
		verb, goneVerb, rederiveVerb = "would resolve", "would record the deletion of", "would re-derive"
	}
	if len(plan.keep) > 0 {
		fmt.Printf("%s %d generated file(s), then regenerating them:\n", verb, len(plan.keep))
		printPaths(plan.keep)
	}
	if len(plan.gone) > 0 {
		fmt.Printf("%s %d generated file(s) this workspace no longer tracks:\n", goneVerb, len(plan.gone))
		printPaths(plan.gone)
	}
	if len(plan.rederive) > 0 {
		// Reported because it is not a merge. The managed section is rebuilt from the
		// declared outputs; anything outside it comes from the incoming side, so a
		// hand-written rule only the other side has does not survive.
		fmt.Printf("%s %d file(s) magus maintains, from the workspace rather than by merging:\n",
			rederiveVerb, len(plan.rederive))
		printPaths(plan.rederive)
		fmt.Println("  any hand-written rules in these files are taken from the incoming side")
	}
	if len(plan.manual) > 0 {
		fmt.Printf("left for you: %d conflict(s) magus cannot settle:\n", len(plan.manual))
		printPaths(plan.manual)
	}
}

func printPaths(paths []string) {
	for _, p := range paths {
		fmt.Printf("  %s\n", p)
	}
}

// unresolvedError exits non-zero when conflicts remain, so `magus vcs resolve && git
// rebase --continue` cannot skip past one you still have to read.
func unresolvedError(plan resolutionPlan) error {
	if len(plan.manual) == 0 {
		return nil
	}
	return fmt.Errorf("%d conflict(s) still need you; resolve them, `git add` them, and continue", len(plan.manual))
}

// vcsPreflight predicts the conflicts merging base would produce and classifies
// them exactly as resolve would classify the real thing. Read-only by
// construction: the prediction is an in-memory merge and the plan is never
// applied. The value is timing - a hosting service runs the same 3-way merge to
// decide mergeability and never runs a merge driver, so without this the first
// conflict signal is the service's banner, after the push.
func vcsPreflight(ctx context.Context, m *magus.Magus, res types.VCSResolution, resolver types.ConflictResolver, base string) error {
	predictor, ok := res.VCS.(types.ConflictPredictor)
	if !ok {
		return fmt.Errorf("vcs resolve: %s cannot predict conflicts; merge or rebase, then resolve as usual", res.Name)
	}
	conflicts, err := predictor.PredictConflicts(ctx, m.Root(), base)
	if err != nil {
		return fmt.Errorf("vcs resolve --base: %w", err)
	}
	if len(conflicts) == 0 {
		fmt.Printf("vcs resolve: merging %s would produce no conflicts\n", base)
		return nil
	}
	plan, err := planResolution(ctx, m, resolver, conflicts)
	if err != nil {
		return err
	}
	reportPreflight(plan, base)
	if len(plan.manual) == 0 {
		return nil
	}
	// Non-zero mirrors unresolvedError: only conflicts a human must read fail the
	// preflight, so `magus vcs resolve --base <rev> && git push` gates on exactly them.
	return fmt.Errorf("%d predicted conflict(s) would need you; the rest settle with `magus vcs resolve` once the merge or rebase stops", len(plan.manual))
}

func reportPreflight(plan resolutionPlan, base string) {
	total := len(plan.keep) + len(plan.gone) + len(plan.rederive) + len(plan.manual)
	fmt.Printf("merging %s would conflict on %d path(s)\n", base, total)
	if settled := slices.Sorted(slices.Values(slices.Concat(plan.keep, plan.rederive))); len(settled) > 0 {
		fmt.Printf("settled for you by `magus vcs resolve` once the merge stops - generated, so regenerated rather than merged (%d):\n", len(settled))
		printPaths(settled)
	}
	if len(plan.gone) > 0 {
		fmt.Printf("recorded as deletions (%d):\n", len(plan.gone))
		printPaths(plan.gone)
	}
	if len(plan.manual) > 0 {
		fmt.Printf("left for you - conflicts magus cannot settle (%d):\n", len(plan.manual))
		printPaths(plan.manual)
	}
}

// -------------------------------------------------------------------- vcs add

func vcsAddUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus vcs add [<path>...] [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Classify a change and emit the selection worth staging: sources and the")
	fmt.Fprintln(w, "generated outputs a source change produced go together, and anything")
	fmt.Fprintln(w, "undeclared is reported rather than swept in. magus computes the selection;")
	fmt.Fprintln(w, "your VCS stages it:")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "  magus vcs add -o name | git add --pathspec-from-file=-")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "With no paths, the whole dirty tree is classified. This replaces the")
	fmt.Fprintln(w, "`git add -A` reflex - which stages undeclared files silently - without")
	fmt.Fprintln(w, "magus ever writing the index itself.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  -o name      emit the selection as bare paths, one per line (the pipe form)")
	fmt.Fprintln(w, "  --untracked  include undeclared files in the selection (the ones add -A would sweep in)")
}

// vcsAddCmd classifies the paths and emits what is worth staging; it never
// writes the index. The classification is magus's knowledge (declared globs);
// the staging is git's job, reached through the pipe the usage text shows.
func vcsAddCmd(ctx context.Context, root string, args []string) error {
	var untracked bool
	pos, err := cmdParse("vcs add", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&untracked, "untracked", false, "Include undeclared files in the emitted selection")
		fs.Usage = func() { vcsAddUsage(os.Stderr) }
	})
	if err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format != outputText && opts.Format != outputName {
		return usagef("vcs add: -o %s is not supported (text reports, name emits the selection)", opts.Format)
	}
	// In the pipe form stdout is the selection and nothing else; the human-facing
	// report moves to stderr so `| git add --pathspec-from-file=-` reads paths only.
	report := os.Stdout
	if opts.Format == outputName {
		report = os.Stderr
	}

	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}
	// The --root flag is "" by default; every path comparison below needs the
	// RESOLVED root, or relative-path math silently keys off the CWD.
	wsRoot := ws.Root()
	res, err := vcs.Resolve(ctx, wsRoot, "", ws.VCSOptions())
	if err != nil || res.VCS == nil {
		return fmt.Errorf("vcs add: no VCS resolved for this workspace")
	}

	paths, err := workspaceRelPaths(wsRoot, pos)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		lines, err := res.VCS.DirtyFiles(ctx, wsRoot, nil)
		if err != nil {
			return fmt.Errorf("vcs add: list dirty files: %w", err)
		}
		paths = statusPaths(lines)
	}
	if len(paths) == 0 {
		fmt.Fprintln(report, "vcs add: nothing to stage; the tree is clean")
		return nil
	}

	// One classification call for every path: the same declared-glob answer
	// `magus describe file` gives, so the two can never disagree.
	files, err := ws.ClassifyFiles(ctx, paths)
	if err != nil {
		return err
	}
	sources, outputs, undeclared := classifyForStaging(files)

	selection := slices.Concat(sources, outputs)
	if untracked {
		selection = append(selection, undeclared...)
	}
	slices.Sort(selection)
	// A declared path that is gone from disk AND unknown to the VCS would abort
	// git's whole staging call; filter it out of the selection here so the emitted
	// list is always safe to feed to `git add`.
	selection, dropped, err := filterStageable(ctx, wsRoot, res.Name, selection)
	if err != nil {
		return err
	}

	reportStaging(report, sources, outputs, undeclared, untracked)
	for _, p := range dropped {
		fmt.Fprintf(report, "left out %s: declared but missing from disk and not tracked\n", p)
	}
	if opts.Format == outputName {
		for _, p := range selection {
			fmt.Println(p)
		}
		return nil
	}
	if len(selection) > 0 {
		fmt.Printf("\nstage this selection: %s\n", stagePipeHint(wsRoot, untracked, pos))
	}
	return nil
}

// stagePipeHint reconstructs the exact pipe that stages what this invocation
// reported, preserving the caller's own path arguments and flags. When the
// caller is not at the workspace root, git is pointed there with -C so the
// emitted workspace-relative paths resolve as pathspecs.
func stagePipeHint(root string, untracked bool, pos []string) string {
	emit := "magus vcs add -o name"
	if untracked {
		emit += " --untracked"
	}
	for _, p := range pos {
		emit += " " + p
	}
	gitCmd := "git add --pathspec-from-file=-"
	if cwd, err := os.Getwd(); err == nil && cwd != root {
		gitCmd = fmt.Sprintf("git -C %s add --pathspec-from-file=-", root)
	}
	return emit + " | " + gitCmd
}

// workspaceRelPaths turns the paths you typed into workspace-relative ones.
//
// ClassifyFiles, the declared-output globs, and the index write are all
// workspace-relative, but you run this from wherever you are. Passing the typed path
// through made `magus vcs add foo.go` from a subdirectory miss, or address a different
// file, with no error: an unmatched path just reports as undeclared.
func workspaceRelPaths(root string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("vcs add: resolve working directory: %w", err)
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(cwd, p)
		}
		rel, err := filepath.Rel(root, abs)
		if err != nil || strings.HasPrefix(rel, "..") {
			return nil, fmt.Errorf("vcs add: %q is outside the workspace at %s", p, root)
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return out, nil
}

// statusPaths extracts the path from each entry DirtyFiles returns.
//
// DirtyFiles returns status LINES, not paths, despite the name - every existing caller
// only tests whether the result is empty, so nothing noticed. Handing those lines to the
// classifier unparsed made every entry look like " M foo", which matched no declared glob
// and would have staged the workspace blind.
//
// The status prefix is DETECTED rather than assumed. git and hg emit two status columns
// then the path, but jj's DirtyFiles runs `jj diff --name-only` and returns bare paths -
// so blindly slicing off two characters turned every jj path into a corrupted one
// ("docs/foo.md" -> "cs/foo.md") that then matched nothing and staged nothing.
func statusPaths(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		path := line
		if rest, ok := strings.CutPrefix(line, statusPrefix(line)); ok {
			path = rest
		}
		// A rename reads "old -> new"; the new name is what to stage.
		if _, after, ok := strings.Cut(path, " -> "); ok {
			path = after
		}
		// git quotes a path with unusual bytes, always with double quotes. Unquote also
		// accepts Go rune and raw-string literals, so it is gated on the quoting form git
		// actually emits; without that, a file literally named `x` loses its backquotes.
		if strings.HasPrefix(path, `"`) {
			if unquoted, err := strconv.Unquote(path); err == nil {
				path = unquoted
			}
		}
		if path != "" {
			out = append(out, path)
		}
	}
	return out
}

// statusPrefix returns the leading status columns of a status line, or "" when the line
// carries none.
//
// Three shapes have to be told apart, and the width is not constant:
//   - git porcelain is two columns then a space (" M path", "?? path", "A  path")
//   - the FIRST line loses its leading space, because DirtyFiles reads the output through
//     a trimming helper, so " M path" arrives as "M path" - one column then a space
//   - hg status is one column then a space ("M path")
//   - jj returns bare paths with no columns at all
//
// Stripping a fixed two characters covered the first three by accident and corrupted
// every jj path; requiring a fixed three covered git only, and left the trimmed first
// line and all of hg with a "M " glued to the front of the path, where it matched no
// declared glob and was reported as undeclared. Detecting the width is what handles all
// four.
func statusPrefix(line string) string {
	if len(line) < 3 {
		return ""
	}
	if isStatusColumn(line[0]) && isStatusColumn(line[1]) && line[2] == ' ' {
		return line[:3]
	}
	// A trimmed git line, or hg: one column then the separating space.
	if isStatusColumn(line[0]) && line[0] != ' ' && line[1] == ' ' {
		return line[:2]
	}
	return ""
}

// isStatusColumn reports whether c can appear in a status column. Lowercase is excluded
// deliberately: it is what keeps an ordinary bare path ("docs/foo.md") from being read as
// a status line.
func isStatusColumn(c byte) bool {
	return c == ' ' || c == '?' || c == '!' || (c >= 'A' && c <= 'Z')
}

// classifyForStaging splits classified files into the three groups staging cares
// about.
//
// Sources and outputs are BOTH staged, and that is the point rather than an
// oversight: a generate target rewriting its declared outputs is the system
// working, and those outputs belong in the same commit as the source that moved
// them. Committing the source alone is what makes CI fail on drift.
//
// Undeclared paths are the actual hazard `git add -A` poses. No target claims
// them, so they are usually build residue or a scratch file - but they are also
// where a genuinely new, not-yet-declared source file lives, and where a file
// magus's own core writes directly (see vcsMaintainedFiles) shows up, since
// neither has a target's declared-output glob to match against. They are
// reported rather than dropped or assumed inert.
func classifyForStaging(out []types.FileEntry) (sources, outputs, undeclared []string) {
	for _, f := range out {
		switch f.Role {
		case "source":
			sources = append(sources, f.Path)
		case "output":
			outputs = append(outputs, f.Path)
		default:
			undeclared = append(undeclared, f.Path)
		}
	}
	return sources, outputs, undeclared
}

func reportStaging(w io.Writer, sources, outputs, undeclared []string, untracked bool) {
	if len(sources) > 0 {
		fmt.Fprintf(w, "%d source file(s) to stage:\n", len(sources))
		for _, p := range sources {
			fmt.Fprintf(w, "  %s\n", p)
		}
	}
	if len(outputs) > 0 {
		fmt.Fprintf(w, "%d generated output(s) to stage - they belong with the source change that produced them:\n", len(outputs))
		for _, p := range outputs {
			fmt.Fprintf(w, "  %s\n", p)
		}
	}
	if len(undeclared) == 0 {
		return
	}
	if untracked {
		fmt.Fprintf(w, "%d undeclared file(s) in the selection (--untracked):\n", len(undeclared))
		for _, p := range undeclared {
			fmt.Fprintf(w, "  %s\n", p)
		}
		return
	}
	maintained, unclaimed := splitMaintained(undeclared)
	if len(unclaimed) > 0 {
		fmt.Fprintf(w, "left out %d undeclared file(s) - no target claims them:\n", len(unclaimed))
		for _, p := range unclaimed {
			fmt.Fprintf(w, "  %s\n", p)
		}
		fmt.Fprintln(w, "  if one is a new source file, name it explicitly or pass --untracked;")
		fmt.Fprintln(w, "  if it is build residue, add it to your VCS ignore rules")
	}
	if len(maintained) > 0 {
		fmt.Fprintf(w, "left out %d file(s) magus itself maintains outside any target's declared outputs:\n", len(maintained))
		for _, p := range maintained {
			fmt.Fprintf(w, "  %s\n", p)
		}
		fmt.Fprintln(w, "  these are not residue - name them explicitly or pass --untracked to include them")
	}
}

// vcsMaintainedFiles are paths magus's own core writes directly, rather than a
// target through a declared output glob - so ClassifyFiles has nothing to match
// them against and they land in "undeclared" alongside genuine residue. Calling
// them undeclared is accurate; claiming they "affect nothing" is not, so they
// get their own report line instead of being folded into the blanket message.
//
// .gitattributes is written by gitVCS.InstallMergeDriver (vcs/git.go) to
// register magus's own merge driver for generated-output conflicts. It is also
// why `vcs resolve` can settle a conflict in it: its content is derived from the
// workspace's declared outputs, so it is re-deriveable rather than mergeable.
var vcsMaintainedFiles = map[string]bool{
	".gitattributes": true,
}

// splitMaintained separates paths magus's own core maintains from everything
// else undeclared, so reportStaging can describe each group accurately instead
// of asserting every undeclared path "affects nothing".
func splitMaintained(undeclared []string) (maintained, unclaimed []string) {
	for _, p := range undeclared {
		if vcsMaintainedFiles[p] {
			maintained = append(maintained, p)
		} else {
			unclaimed = append(unclaimed, p)
		}
	}
	return maintained, unclaimed
}

// filterStageable separates paths git add can actually act on from ones that
// would abort the whole `git add` call.
//
// A declared output (or source) whose path no longer exists on disk - a
// directory renamed out from under it, a stale declaration nobody updated -
// makes `git add` fail on that ONE pathspec with "did not match any files".
// `git add` does not skip the bad pathspec and keep going: it aborts the whole
// invocation before staging anything, so one stale path silently loses every
// other path handed to the same call. The emitted selection must never carry
// one: a path that exists on disk is always kept; a path that is gone from
// disk but still known to git is ALSO kept, because that is precisely how a
// deletion or a rename gets recorded - dropping it would make the selection
// unable to ever carry a removal. Only a path that is neither on disk nor
// tracked - a declaration that no longer corresponds to anything real - is
// dropped, and reported rather than silently discarded.
func filterStageable(ctx context.Context, root, vcsName string, paths []string) (stageable, dropped []string, err error) {
	var maybeGone []string
	for _, p := range paths {
		if _, statErr := os.Stat(filepath.Join(root, p)); statErr == nil {
			stageable = append(stageable, p)
			continue
		}
		maybeGone = append(maybeGone, p)
	}
	if len(maybeGone) == 0 {
		return stageable, nil, nil
	}
	// The abort-the-whole-invocation behavior this guards against is git's. Other
	// backends get the paths passed through rather than probed with a missing command.
	if vcsName != "git" {
		return append(stageable, maybeGone...), nil, nil
	}

	tracked, err := gitTrackedPaths(ctx, root, maybeGone)
	if err != nil {
		return nil, nil, err
	}
	for _, p := range maybeGone {
		if tracked[p] {
			stageable = append(stageable, p)
		} else {
			dropped = append(dropped, p)
		}
	}
	return stageable, dropped, nil
}

// gitTrackedPaths reports which of paths git already has in its index. That
// index listing is unaffected by whether the file still exists on disk, which
// is exactly the property filterStageable needs: a tracked-but-deleted path
// must still be staged so the deletion gets recorded.
func gitTrackedPaths(ctx context.Context, root string, paths []string) (map[string]bool, error) {
	tracked := make(map[string]bool)
	// Batched like the vcs package's own pathspec calls: a dirty tree runs to hundreds of
	// paths and an unbounded argv hits E2BIG.
	for start := 0; start < len(paths); start += gitLsFilesChunk {
		chunk := paths[start:min(start+gitLsFilesChunk, len(paths))]
		cmd := exec.CommandContext(ctx, "git", append([]string{"ls-files", "-z", "--"}, chunk...)...)
		cmd.Dir = root
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("vcs add: git ls-files: %w", err)
		}
		for _, p := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
			if p != "" {
				tracked[p] = true
			}
		}
	}
	return tracked, nil
}

// gitLsFilesChunk bounds pathspecs per `git ls-files` call; see gitTrackedPaths.
const gitLsFilesChunk = 256
