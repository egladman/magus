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
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// vcsCmd implements `magus vcs <subcommand>`.
//
// Three of the verbs rest on one fact git does not have: which files are generated, and
// which target rebuilds them. `add` classifies paths before staging, `resolve` settles a
// conflicted merge, `merge-driver` is the per-file callback git invokes. `checkpoint` is
// the odd one out and rests on nothing: it reads the working state's identity back.
//
// `add` replaces the `git add -A` the agent guard denies: the guard infers intent from a
// command STRING, while a magus verb gets the paths as arguments and classifies them
// against the declared globs before touching the index.
//
// Not a general git proxy.
func vcsCmd(ctx context.Context, root string, rc runConfig, args []string) error {
	if len(args) == 0 {
		vcsUsage(os.Stderr)
		return usagef("magus vcs: a subcommand is required (try: add, resolve, checkpoint)")
	}
	verb, rest := splitVCSVerb(args)
	switch verb {
	case "add":
		return vcsAddCmd(ctx, root, rest)
	case "resolve":
		return vcsResolveCmd(ctx, root, rc, rest)
	case "checkpoint":
		return vcsCheckpointCmd(ctx, root, rest)
	case "merge-driver":
		return mergeDriverCmd(ctx, root, rest)
	case "-h", "--help", "help":
		vcsUsage(os.Stderr)
		return nil
	default:
		return usagef("magus vcs: unknown subcommand %q (want add, resolve, checkpoint, or merge-driver)", verb)
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
	fmt.Fprintln(w, "  checkpoint     print the identity of the working state right now; writes nothing")
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
	fmt.Fprintln(w, "  --against <ref>  merge <ref> first, then settle what it conflicts with.")
	fmt.Fprintln(w, "                   Needs a clean tree. Leaves the merge in progress to")
	fmt.Fprintln(w, "                   commit; with --dry-run it is backed out again.")
	fmt.Fprintln(w, "  --dry-run        classify and report; touch nothing (global flag)")
}

// vcsResolveCmd classifies every conflicted path, settles the generated ones in bulk,
// regenerates once, and records the result.
func vcsResolveCmd(ctx context.Context, root string, rc runConfig, args []string) error {
	var rf *gen.VCSResolveFlags
	pos, err := cmdParse("vcs resolve", args, func(fs *flag.FlagSet) {
		rf = gen.BindVCSResolve(fs)
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

	if rf.Against != "" {
		undo, err := startMergeAgainst(ctx, m.Root(), res, rf.Against)
		if err != nil {
			return err
		}
		defer undo()
	}

	conflicts, err := resolver.Conflicts(ctx, m.Root())
	if err != nil {
		return fmt.Errorf("vcs resolve: %w", err)
	}
	if len(conflicts) == 0 {
		if rf.Against != "" {
			fmt.Printf("vcs resolve: %s merged with no conflicts; conclude it with `git commit`\n", rf.Against)
			return nil
		}
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

// startMergeAgainst begins the merge that --against settles, and returns the function that
// backs it out again - a no-op unless this is a dry run, since otherwise the merge is the
// whole point and stays in progress for the caller to commit.
//
// A real merge rather than `git merge-tree`: merge-tree reports conflicted PATHS only,
// while planResolution decides on the conflict KIND, and a modify/delete is the shape no
// merge driver is ever invoked for. (The read-only PR advisor uses merge-tree because it
// needs only the names.)
//
// So a dry run merges for real and aborts, which is why a clean tree is required up
// front - `git merge --abort` does not guarantee uncommitted work survives.
func startMergeAgainst(ctx context.Context, root string, res types.VCSResolution, ref string) (undo func(), err error) {
	starter, ok := res.VCS.(types.MergeStarter)
	if !ok {
		return nil, fmt.Errorf("vcs resolve: %s cannot start a merge through magus; merge %q yourself, then run `magus vcs resolve`", res.Name, ref)
	}
	dirty, err := res.VCS.DirtyFiles(ctx, root, nil)
	if err != nil {
		return nil, fmt.Errorf("vcs resolve: read tree status: %w", err)
	}
	if len(dirty) > 0 {
		return nil, fmt.Errorf("vcs resolve: --against needs a clean tree, and %d path(s) are uncommitted; commit or stash them first so backing the merge out cannot lose them", len(dirty))
	}
	if err := starter.StartMerge(ctx, root, ref); err != nil {
		return nil, fmt.Errorf("vcs resolve: %w", err)
	}
	if !globalCfg.DryRun {
		// The merge is the point: leave it in progress for the caller to commit.
		return func() {}, nil
	}
	return func() {
		if err := starter.AbortMerge(ctx, root); err != nil {
			// Reported, never swallowed: the tree is NOT as this dry run found it, and a
			// caller told "nothing was touched" would go on to do something else in it.
			fmt.Fprintf(os.Stderr, "vcs resolve: could not back out the merge --dry-run started; the tree still has it in progress (git merge --abort): %v\n", err)
		}
	}, nil
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

// rebuiltProjects returns every project any rebuild target will run over, keyed by
// projectKey.
func (p resolutionPlan) rebuiltProjects() map[string]bool {
	out := map[string]bool{}
	for _, projects := range p.rebuild {
		for _, key := range projects {
			out[key] = true
		}
	}
	return out
}

// projectKey is the one spelling of a project used to key the rebuild set, on BOTH the
// filling and the reading side.
//
// The PATH, never the label. Every nested project agrees on both spellings, so filling
// with paths and reading back with types.ProjectLabel looked correct - but the ROOT can
// never agree: ProjectLabel rejects "" and ".", resolving the root to its directory
// basename while the set holds ".". The lookup missed every time, leaving every
// root-owned regenerated output unstaged, which is the dirty tree settledPaths exists to
// prevent. One helper on both sides makes that divergence unrepresentable.
func projectKey(p *types.Project) string {
	if p.Path == "" {
		return "."
	}
	return p.Path
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
			if types.IsMagusMaintained(c.Path) && c.Kind == types.ConflictKindContent {
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
		proj := projectKey(p)
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
	dirty, err := driver.DirtyFiles(ctx, m.Root(), nil)
	if err != nil {
		return nil, fmt.Errorf("list regenerated files: %w", err)
	}
	rebuilt := plan.rebuiltProjects()
	for _, path := range dirty {
		if settled[path] {
			continue
		}
		producer := m.FindOutputProducer(filepath.Join(m.Root(), filepath.FromSlash(path)))
		if producer == nil || !rebuilt[projectKey(producer)] {
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

// ------------------------------------------------------------- vcs checkpoint

func vcsCheckpointUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus vcs checkpoint [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Print the identity of the working state right now: the head revision, the")
	fmt.Fprintln(w, "branch carrying it, whether the tree is dirty, and a digest of the")
	fmt.Fprintln(w, "uncommitted patch.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "It RESOLVES AND RECORDS; it never MINTS. No tag, no stash, no ref, no file,")
	fmt.Fprintln(w, "nothing changed anywhere - so taking one costs the tree nothing and one you")
	fmt.Fprintln(w, "do not keep costs nothing either. Record it when you hand work out, so a")
	fmt.Fprintln(w, "later reader knows what that work was looking at.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Feed the revision to anything that takes one (magus graph diff --rev <rev>).")
	fmt.Fprintln(w, "Compare two digests to learn whether two workers saw the same uncommitted")
	fmt.Fprintln(w, "tree, which the revision alone cannot tell you: a dirty tree's revision is")
	fmt.Fprintln(w, "the same one everybody else has.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  -o name    the citable token: the revision, or <revision>+<digest> when dirty")
	fmt.Fprintln(w, "  -o json    the whole record (global flag; yaml, jsonl and template too)")
}

// vcsCheckpointCmd reads the working state's identity and prints it.
func vcsCheckpointCmd(ctx context.Context, root string, args []string) error {
	pos, err := cmdParse("vcs checkpoint", args, func(fs *flag.FlagSet) {
		fs.Usage = func() { vcsCheckpointUsage(os.Stderr) }
	})
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usagef("vcs checkpoint: takes no arguments; it reports the whole workspace's working state (got %q)", pos[0])
	}

	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}
	// The RESOLVED workspace root, not the --root override, for the reason vcsAddCmd
	// spells out: the override is empty unless you passed --root, and an empty dir sends
	// every VCS call to the process cwd - which is a different repository the moment you
	// run this from anywhere but the root.
	wsRoot := ws.Root()
	res, err := vcs.Resolve(ctx, wsRoot, "", ws.VCSOptions())
	if err != nil {
		return fmt.Errorf("vcs checkpoint: %w", err)
	}
	cp, err := vcs.Checkpoint(ctx, wsRoot, res)
	if err != nil {
		return err
	}
	return emitCheckpoint(cp)
}

// emitCheckpoint renders the checkpoint: the structured formats get the record, -o name
// the one citable token, and the terminal the one-line reading of the same value.
func emitCheckpoint(cp types.VCSCheckpoint) error {
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, cp)
	case outputName:
		fmt.Println(checkpointToken(cp))
		return nil
	}
	fmt.Println(checkpointLine(cp))
	return nil
}

// checkpointToken is the single most citable thing about a checkpoint, for the one cell a
// ledger gives it. A clean tree IS its revision. A dirty one is not - every worker on this
// branch shares that revision - so the digest joins it, and the "+" marks the identity as
// a revision PLUS uncommitted work rather than a revision anyone can check out.
func checkpointToken(cp types.VCSCheckpoint) string {
	if !cp.Dirty {
		return cp.Revision
	}
	return cp.Revision + "+" + cp.PatchDigest
}

// checkpointLine is the human reading: "<rev> <branch> clean" or "<rev> <branch> dirty
// <digest>". The field count is fixed through the dirty word, so a branchless revision (a
// detached head, jj's usual anonymous change) renders "-" rather than collapsing the
// column and silently shifting everything after it.
func checkpointLine(cp types.VCSCheckpoint) string {
	branch := cp.Branch
	if branch == "" {
		branch = "-"
	}
	if !cp.Dirty {
		return fmt.Sprintf("%s %s clean", cp.Revision, branch)
	}
	return fmt.Sprintf("%s %s dirty %s", cp.Revision, branch, cp.PatchDigest)
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
	var af *gen.VCSAddFlags
	pos, err := cmdParse("vcs add", args, func(fs *flag.FlagSet) {
		af = gen.BindVCSAdd(fs)
		fs.Usage = func() { vcsAddUsage(os.Stderr) }
	})
	if err != nil {
		return err
	}

	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}
	// The resolved workspace root, not the --root OVERRIDE this was handed. The override
	// is empty unless the user passed --root, and everything below is workspace-relative:
	// with "" the path math produced `vcs add: "x" is outside the workspace at ` - naming
	// no workspace at all - so naming a path explicitly, which this command's own
	// undeclared-file message tells you to do, could never work. The whole-tree form only
	// appeared to work because an empty dir sends every VCS call to the process cwd.
	// vcsResolveCmd already goes through m.Root() for the same reason.
	root = ws.Root()
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
		paths, err = res.VCS.DirtyFiles(ctx, root, nil)
		if err != nil {
			return fmt.Errorf("vcs add: list dirty files: %w", err)
		}
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

	// Only when classifying the whole dirty tree. Naming a path is an explicit statement
	// of intent about that path, and this check is an inference about paths nobody named.
	var unexplained []string
	if len(pos) == 0 {
		outputs, unexplained = types.SplitExplainedOutputs(files, types.SourcesChangedSinceBase(ctx, ws, res, root))
	}
	// Naming a path is an explicit statement of intent about that path, so an undeclared
	// one the caller ASKED for is staged rather than reported as skipped. Without this,
	// `magus vcs add .gitattributes` refused the very file its own message had just told
	// you to name ("name them explicitly or pass --untracked"), and the only way to stage
	// it was the flag - or plain git, which is what the command exists to replace.
	//
	// --untracked stays the whole-tree form of the same permission: it says yes to every
	// undeclared path at once, which is the one that needs a flag because nobody named
	// them one by one.
	explicit := len(pos) > 0
	maintained, unclaimed := splitMaintained(undeclared)
	if explicit {
		// They were asked for by name, so they are not "skipped" and must not be
		// reported as if they were.
		maintained, unclaimed = nil, nil
	}

	verdict := types.StagingPlan{
		Sources:     sources,
		Outputs:     outputs,
		Unexplained: unexplained,
		Undeclared:  unclaimed,
		Maintained:  maintained,
		Staged:      []string{},
	}
	if len(unexplained) > 0 {
		// inputDirty is false by construction: an output is only unexplained BECAUSE no
		// declared input of its project moved. So this is ClassifyDrift's second fork -
		// skew against a differently-versioned magus, or a non-deterministic generator.
		code, msg := types.ClassifyDrift(false, version)
		verdict.Code, verdict.Message, verdict.URL = string(code), msg, types.CodeURL(code)
	}

	stage := slices.Concat(sources, outputs)
	if af.Untracked || explicit {
		stage = append(stage, undeclared...)
	}
	slices.Sort(stage)

	var dropped []string
	if !globalCfg.DryRun && len(stage) > 0 {
		staged, gone, err := stagePaths(ctx, root, res.Name, recorder, stage)
		if err != nil {
			return err
		}
		verdict.Staged, dropped = staged, gone
	}
	return emitStaging(verdict, dropped, af.Untracked, globalCfg.DryRun)
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
	// An empty root is a CALLER bug, not a path the user typed wrong. Left to run, it
	// reports every path as "outside the workspace at " with nothing after "at", which
	// blames the argument for a mistake it did not make.
	if root == "" {
		return nil, fmt.Errorf("vcs add: no workspace root resolved; cannot place %q", paths[0])
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

// classifyForStaging splits classified files into the three groups staging cares
// about.
//
// Sources and outputs are BOTH staged deliberately: regenerated outputs belong in the
// same commit as the source that moved them, and committing the source alone is what
// makes CI fail on drift.
//
// Undeclared paths are the hazard `git add -A` poses. Usually build residue, but also
// where a genuinely new source file and anything magus's core writes directly (see
// types.IsMagusMaintained) show up - so they are reported rather than dropped.
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

// reportStaging renders the verdict as prose. It reads the value and prints; it decides
// nothing, so the terminal and `-o json` cannot disagree about what happened.
func reportStaging(v types.StagingPlan, dropped []string, untracked, dryRun bool) {
	verb := "staged"
	if dryRun {
		verb = "would stage"
	}
	for _, p := range dropped {
		fmt.Printf("skipping %s: declared but missing from disk and not tracked\n", p)
	}
	if len(v.Sources) > 0 {
		fmt.Printf("%s %d source file(s):\n", verb, len(v.Sources))
		printPaths(v.Sources)
	}
	if len(v.Outputs) > 0 {
		fmt.Printf("%s %d generated output(s), which belong with the source change that produced them:\n", verb, len(v.Outputs))
		printPaths(v.Outputs)
	}
	if len(v.Unexplained) > 0 {
		fmt.Printf("skipped %d generated output(s) no source change here accounts for:\n", len(v.Unexplained))
		printPaths(v.Unexplained)
		// The classification, not a second hand-written telling of it: the same code and
		// sentence the generate gate reports for this condition (types.ClassifyDrift).
		fmt.Printf("  %s: %s\n", v.Code, v.Message)
		fmt.Printf("  %s\n", v.URL)
		fmt.Println("  name a path explicitly to stage it anyway")
	}
	if untracked {
		all := slices.Concat(v.Undeclared, v.Maintained)
		if len(all) > 0 {
			slices.Sort(all)
			fmt.Printf("%s %d undeclared file(s) (--untracked):\n", verb, len(all))
			printPaths(all)
		}
	} else {
		if len(v.Undeclared) > 0 {
			fmt.Printf("skipped %d undeclared file(s); no target claims them:\n", len(v.Undeclared))
			printPaths(v.Undeclared)
			fmt.Println("  if one is a new source file, name it explicitly or pass --untracked;")
			fmt.Println("  if it is build residue, add it to your VCS ignore rules")
		}
		if len(v.Maintained) > 0 {
			fmt.Printf("skipped %d file(s) magus itself maintains outside any target's declared outputs:\n", len(v.Maintained))
			printPaths(v.Maintained)
			fmt.Println("  these are not residue - name them explicitly or pass --untracked to stage them")
		}
	}
	if len(v.Staged) > 0 {
		fmt.Println("\nreview before committing: git diff --cached --stat")
	}
}

// splitMaintained separates paths magus's own core maintains from everything
// else undeclared, so reportStaging can describe each group accurately instead
// of asserting every undeclared path "affects nothing".
//
// A maintained path is one magus writes directly, rather than a target through a
// declared output glob - so ClassifyFiles has nothing to match it against and it
// lands in "undeclared" alongside genuine residue. Calling it undeclared is
// accurate; claiming it "affects nothing" is not, so it gets its own report line
// instead of being folded into the blanket message.
//
// .gitattributes is written by gitVCS.InstallMergeDriver (vcs/git.go) to register
// magus's own merge driver for generated-output conflicts. It is also why `vcs
// resolve` can settle a conflict in it: its content is derived from the workspace's
// declared outputs, so it is re-deriveable rather than mergeable.
//
// The set itself is types.IsMagusMaintained rather than a local one, because
// `describe file` classifies the same paths and the two answers must not diverge -
// which they did, describe calling .gitattributes unclaimed and suggesting the
// ignore rules while staging reported it as maintained.
func splitMaintained(undeclared []string) (maintained, unclaimed []string) {
	for _, p := range undeclared {
		if types.IsMagusMaintained(p) {
			maintained = append(maintained, p)
		} else {
			unclaimed = append(unclaimed, p)
		}
	}
	return maintained, unclaimed
}

// stagePaths shells out to git for the index write itself.
//
// One pathspec that matches nothing aborts the WHOLE `git add` before staging anything,
// so a single stale declaration silently loses every other path in the call.
// filterStageable splits them first: on disk is staged; gone from disk but still tracked
// is ALSO staged, since that is how a deletion or rename is recorded. Only a path that is
// neither is dropped, and reported rather than discarded.
//
// Paths are passed after `--` so one beginning with a dash is unambiguously a path.
//
// It returns what it staged and dropped rather than printing either, so `-o json` gets
// the same answer the terminal does.
func stagePaths(ctx context.Context, root, vcsName string, recorder types.ConflictResolver, paths []string) (staged, dropped []string, err error) {
	stageable, dropped, err := filterStageable(ctx, root, vcsName, paths)
	if err != nil {
		return nil, nil, err
	}
	if len(stageable) == 0 {
		return []string{}, dropped, nil
	}
	// MarkResolved batches the pathspecs, which matters here: `vcs add` over a whole
	// dirty tree is the largest path list these commands hand to the VCS.
	if err := recorder.MarkResolved(ctx, root, stageable); err != nil {
		return nil, nil, fmt.Errorf("vcs add: %w", err)
	}
	return stageable, dropped, nil
}

// emitStaging renders the verdict: the structured formats get the value itself, and the
// terminal gets the prose. One decision, several audiences - which is the whole reason
// the verdict is a value. `-o json` used to be accepted here and answer in text.
func emitStaging(v types.StagingPlan, dropped []string, untracked, dryRun bool) error {
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, v)
	}
	reportStaging(v, dropped, untracked, dryRun)
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
