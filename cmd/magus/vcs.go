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
// which target rebuilds them. `add` classifies paths before staging, `resolve` settles a
// conflicted merge, `merge-driver` is the per-file callback git invokes.
//
// `add` is the replacement for the `git add -A` the agent guard denies. The guard reads a
// command STRING and infers intent; a magus verb gets the paths as arguments and
// classifies them against the declared globs before touching the index.
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
	fmt.Fprintln(w, "  add            stage a change the way this workspace's declarations say it should be staged")
	fmt.Fprintln(w, "  resolve        settle an in-progress merge/rebase's conflicted generated files, then regenerate once")
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
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --dry-run   classify and report; touch nothing (global flag)")
}

// vcsResolveCmd classifies every conflicted path, settles the generated ones in bulk,
// regenerates once, and records the result.
func vcsResolveCmd(ctx context.Context, root string, rc runConfig, args []string) error {
	pos, err := cmdParse("vcs resolve", args, func(fs *flag.FlagSet) {
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
		// The project PATH, not its display label: this string becomes an argument to
		// `magus run <target> <project>`, and ProjectRef.Display renders the root as its
		// directory BASENAME so a bare "." never reaches a human-facing log. In a git
		// worktree that basename is the worktree's own directory name, which is not a
		// project any workspace knows - so resolve regenerated nothing and died with
		// `unknown project: "<worktree-dir>"`. Display's own doc draws this line: labels
		// for reading, the path for anything the user (or this code) feeds back to magus.
		proj := p.Path
		if proj == "" {
			proj = "."
		}
		if !slices.Contains(plan.rebuild[target], proj) {
			plan.rebuild[target] = append(plan.rebuild[target], proj)
		}
	}
	slices.Sort(plan.keep)
	slices.Sort(plan.gone)
	slices.Sort(plan.rederive)
	slices.Sort(plan.manual)
	return plan, nil
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
		if producer == nil || !rebuilt[types.ProjectLabel(producer.Path, producer.Dir)] {
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
	return fmt.Errorf("%d conflict(s) still need you; resolve them, then `magus vcs add` and continue", len(plan.manual))
}

// -------------------------------------------------------------------- vcs add

func vcsAddUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus vcs add [<path>...] [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Stage a change the way this workspace's declarations say it should be")
	fmt.Fprintln(w, "staged: sources and the generated outputs a source change produced go")
	fmt.Fprintln(w, "together, and anything undeclared is reported rather than swept in.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "With no paths, the whole dirty tree is classified. This is the safe")
	fmt.Fprintln(w, "replacement for `git add -A`, which stages undeclared files silently.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --dry-run    classify and report; touch nothing (global flag)")
	fmt.Fprintln(w, "  --untracked  also stage undeclared files (the ones add -A would sweep in)")
}

// vcsAddCmd classifies the paths, stages what is declared, and reports the rest.
func vcsAddCmd(ctx context.Context, root string, args []string) error {
	// --dry-run is the GLOBAL config flag, not a local one: it already means
	// "show me what would happen" on every other command, and redefining it here
	// panics the FlagSet anyway.
	var untracked bool
	pos, err := cmdParse("vcs add", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&untracked, "untracked", false, "Also stage undeclared files")
		fs.Usage = func() { vcsAddUsage(os.Stderr) }
	})
	if err != nil {
		return err
	}

	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}
	res, err := vcs.Resolve(ctx, root, "", ws.VCSOptions())
	if err != nil || res.VCS == nil {
		return fmt.Errorf("vcs add: no VCS resolved for this workspace")
	}
	// The write goes through the capability `vcs resolve` uses, so a backend that cannot
	// record paths is refused up front rather than part-way through.
	recorder, ok := res.VCS.(types.ConflictResolver)
	if !ok {
		return fmt.Errorf("vcs add: %s cannot record paths through magus; stage with %s directly", res.Name, res.Name)
	}

	paths, err := workspaceRelPaths(root, pos)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		lines, err := res.VCS.DirtyFiles(ctx, root, nil)
		if err != nil {
			return fmt.Errorf("vcs add: list dirty files: %w", err)
		}
		paths = statusPaths(lines)
	}
	if len(paths) == 0 {
		fmt.Println("vcs add: nothing to stage; the tree is clean")
		return nil
	}

	// One classification call for every path: the same declared-glob answer
	// `magus describe file` gives, so the two can never disagree.
	files, err := ws.ClassifyFiles(ctx, paths)
	if err != nil {
		return err
	}
	sources, outputs, undeclared := classifyForStaging(files)

	stage := slices.Concat(sources, outputs)
	if untracked {
		stage = append(stage, undeclared...)
	}
	slices.Sort(stage)

	reportStaging(sources, outputs, undeclared, untracked, globalCfg.DryRun)
	if globalCfg.DryRun || len(stage) == 0 {
		return nil
	}
	return stagePaths(ctx, root, res.Name, recorder, stage)
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

func reportStaging(sources, outputs, undeclared []string, untracked, dryRun bool) {
	verb := "staged"
	if dryRun {
		verb = "would stage"
	}
	if len(sources) > 0 {
		fmt.Printf("%s %d source file(s):\n", verb, len(sources))
		for _, p := range sources {
			fmt.Printf("  %s\n", p)
		}
	}
	if len(outputs) > 0 {
		fmt.Printf("%s %d generated output(s), which belong with the source change that produced them:\n", verb, len(outputs))
		for _, p := range outputs {
			fmt.Printf("  %s\n", p)
		}
	}
	if len(undeclared) == 0 {
		return
	}
	if untracked {
		fmt.Printf("%s %d undeclared file(s) (--untracked):\n", verb, len(undeclared))
		for _, p := range undeclared {
			fmt.Printf("  %s\n", p)
		}
		return
	}
	maintained, unclaimed := splitMaintained(undeclared)
	if len(unclaimed) > 0 {
		fmt.Printf("skipped %d undeclared file(s) - no target claims them:\n", len(unclaimed))
		for _, p := range unclaimed {
			fmt.Printf("  %s\n", p)
		}
		fmt.Println("  if one is a new source file, name it explicitly or pass --untracked;")
		fmt.Println("  if it is build residue, add it to your VCS ignore rules")
	}
	if len(maintained) > 0 {
		fmt.Printf("skipped %d file(s) magus itself maintains outside any target's declared outputs:\n", len(maintained))
		for _, p := range maintained {
			fmt.Printf("  %s\n", p)
		}
		fmt.Println("  these are not residue - name them explicitly or pass --untracked to stage them")
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

// stagePaths shells out to git for the index write itself.
//
// A declared output (or source) whose path no longer exists on disk - a
// directory renamed out from under it, a stale declaration nobody updated -
// makes `git add` fail on that ONE pathspec with "did not match any files".
// `git add` does not skip the bad pathspec and keep going: it aborts the whole
// invocation before staging anything, so one stale path silently loses every
// other path handed to the same call. filterStageable splits paths first so
// that never happens: a path that exists on disk is always staged; a path
// that is gone from disk but still known to git is ALSO staged, because that
// is precisely how a deletion or a rename gets recorded - dropping it would
// make `vcs add` unable to ever commit a removal. Only a path that is neither
// on disk nor tracked - a declaration that no longer corresponds to anything
// real - is dropped, and reported rather than silently discarded.
//
// Paths are passed after `--` so one that begins with a dash, or collides with a
// revision name, is unambiguously a path.
func stagePaths(ctx context.Context, root, vcsName string, recorder types.ConflictResolver, paths []string) error {
	stageable, dropped, err := filterStageable(ctx, root, vcsName, paths)
	if err != nil {
		return err
	}
	for _, p := range dropped {
		fmt.Printf("skipping %s: declared but missing from disk and not tracked\n", p)
	}
	if len(stageable) == 0 {
		return nil
	}
	// MarkResolved batches the pathspecs, which matters here: `vcs add` over a whole
	// dirty tree is the largest path list these commands hand to the VCS.
	if err := recorder.MarkResolved(ctx, root, stageable); err != nil {
		return fmt.Errorf("vcs add: %w", err)
	}
	fmt.Println("\nreview before committing: git diff --cached --stat")
	return nil
}

// filterStageable separates paths git add can actually act on from ones that
// would abort the whole `git add` call. See stagePaths for why a missing-but-
// tracked path (a deletion or the old half of a rename) must still be staged.
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
