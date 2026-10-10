package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/internal/settle"
	"github.com/egladman/magus/internal/ward"
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

// ----------------------------------------------------------- vcs merge-driver

// mergeDriverCmd dispatches `magus vcs merge-driver %O %A %B %L %P`, which settles one
// conflicted file (settle.File). Args: ancestor result other markerSize path (git and
// hg), plus output when the VCS reads the result from a file of its own (jj). A non-zero
// exit leaves the file conflicted, with markers. Per-clone wiring is installed by `magus
// init`, not here.
func mergeDriverCmd(ctx context.Context, root string, args []string) error {
	if len(args) == 0 {
		_ = mergeDriverUsage()
		// git only ever calls this with all five placeholders, so a bare invocation is
		// a human typing it. Exiting 0 said "merge resolved" for a run that did nothing,
		// and disagreed with the 1-argument case, which already errored.
		return usagef("magus vcs merge-driver: expected 5 arguments (ancestor result other markerSize path), got 0")
	}
	if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		return mergeDriverUsage()
	}
	if len(args) < 5 {
		return usagef("magus vcs merge-driver: expected 5 arguments (ancestor result other markerSize path), got %d", len(args))
	}
	if len(args) > 6 {
		return usagef("magus vcs merge-driver: expected at most 6 arguments (ancestor result other markerSize path output), got %d", len(args))
	}
	f := settle.Files{Base: args[0], Ours: args[1], Theirs: args[2], Output: args[1], MarkerSize: args[3]}
	if len(args) == 6 {
		f.Output = args[5]
	}

	relPath, err := mergeDriverRelPath(root, args[4])
	if err != nil {
		return err
	}
	// Loading the workspace must not re-wire the merge driver: EnsureMergeDriver writes the
	// TRACKED .gitattributes, and doing that here (inside the VCS's index manipulation, once
	// per conflicted file) is the same dirty-tree failure this driver was changed to stop
	// causing.
	m, err := loadMagus(withoutMergeDriverRefresh(ctx), root)
	if err != nil {
		return fmt.Errorf("merge-driver: load workspace: %w", err)
	}
	return settle.File(ctx, m, f, relPath)
}

// mergeDriverUsage prints usage for the merge-driver subcommand.
func mergeDriverUsage() error {
	fmt.Fprintln(os.Stderr, "Usage: magus vcs merge-driver %O %A %B %L %P")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "The VCS merge driver for declared output files and the files magus.yaml's")
	fmt.Fprintln(os.Stderr, "vcs.auto_resolve opts in. git, hg and Sapling invoke it during a merge, and")
	fmt.Fprintln(os.Stderr, "`"+hint.VCSResolve.String()+"` runs it through `jj resolve` on jj. An opted-in file")
	fmt.Fprintln(os.Stderr, "is merged when every region both sides changed is low risk, and otherwise")
	fmt.Fprintln(os.Stderr, "left with conflict markers. A declared output keeps the current version")
	fmt.Fprintln(os.Stderr, "instead of writing conflict markers. On git it records the regeneration it owes,")
	fmt.Fprintln(os.Stderr, "and the settle hooks `"+hint.Init.With("--vcs", "git")+"` writes beside it (`"+hint.VCSResolve.With("--hook")+"`)")
	fmt.Fprintln(os.Stderr, "run it once the merge, rebase, cherry-pick or revert has the whole tree.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "You do not run this by hand. Wire it once per clone with `"+hint.Init.String()+"`.")
	fmt.Fprintln(os.Stderr, "git calls it as:  magus vcs merge-driver %O %A %B %L %P")
	fmt.Fprintln(os.Stderr, "hg calls it as:   magus vcs merge-driver $base $local $other 0 $local")
	fmt.Fprintln(os.Stderr, "jj calls it as:   magus vcs merge-driver $base $left $right $marker_length $path $output")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "To settle a conflicted merge yourself, run `"+hint.VCSResolve.String()+"`: it decides")
	fmt.Fprintln(os.Stderr, "every conflicted path at once, regenerates once, and stages the result -")
	fmt.Fprintln(os.Stderr, "including the files one side deleted, which no VCS calls a driver for.")
	return nil
}

// mergeDriverRelPath normalizes the driver's path argument to a workspace-relative slash
// path. git passes it repo-relative; hg passes an absolute workspace path.
func mergeDriverRelPath(root, pathArg string) (string, error) {
	if !filepath.IsAbs(pathArg) {
		return filepath.ToSlash(pathArg), nil
	}
	wsRoot, err := magus.FindRoot(root)
	if err != nil {
		return "", fmt.Errorf("merge-driver: find workspace root: %w", err)
	}
	rel, err := filepath.Rel(wsRoot, pathArg)
	if err != nil {
		return "", fmt.Errorf("merge-driver: resolve path %q: %w", pathArg, err)
	}
	return filepath.ToSlash(rel), nil
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
	fmt.Fprintln(w, "  --hook <name>    run as the git hook <name>: once the merge, rebase,")
	fmt.Fprintln(w, "                   cherry-pick, revert or am has the whole tree, regenerate")
	fmt.Fprintln(w, "                   only what it changed and stage the result. The merge")
	fmt.Fprintln(w, "                   driver's registration wires these; you do not run them.")
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
	if len(pos) > 0 && rf.Hook == "" {
		return usagef("vcs resolve: takes no paths; it settles every conflicted path in the workspace (got %q)", pos[0])
	}
	if rf.Hook != "" {
		if rf.Against != "" {
			return usagef("vcs resolve: --hook is git's call, and --against is not one it makes")
		}
		return vcsResolveHook(ctx, root, rc, rf.Hook, pos)
	}

	// Not the load dispatch would do: opening a workspace refreshes the merge-driver
	// registration, which writes the tracked .gitattributes. During a merge that file may
	// be unmerged, so the refresh would splice a section between conflict markers. It runs
	// after the markers are gone instead.
	loadCtx := withoutMergeDriverRefresh(ctx)
	m, err := loadMagus(loadCtx, root)
	// A magusfile carrying this merge's conflict markers is not a magusfile magus can parse,
	// and the declarations it holds are the whole basis for settling anything. The committed
	// side is a complete copy of those declarations, so retry against it rather than
	// stopping: the generated conflicts get settled now and the hand-written one waits for a
	// human, instead of both waiting.
	stale := false
	if err != nil {
		overlay := committedMagusfiles(ctx, root)
		if len(overlay) == 0 {
			return err
		}
		m, err = loadMagus(interp.WithOverlay(loadCtx, overlay), root)
		if err != nil {
			return err
		}
		stale = true
		fmt.Printf("vcs resolve: %s still carries this merge's conflict markers, so the committed "+
			"declarations are being used to settle the rest.\n", strings.Join(slices.Sorted(maps.Keys(overlay)), ", "))
	}
	res, err := resolveVCS(ctx, root, m)
	if err != nil {
		return fmt.Errorf("vcs resolve: no VCS resolved for this workspace: %w", err)
	}
	if res.VCS == nil {
		return errors.New("vcs resolve: version control is disabled for this workspace, resolve this merge by hand")
	}

	if rf.Against != "" {
		undo, err := startMergeAgainst(ctx, m.Root(), res, rf.Against)
		if err != nil {
			return err
		}
		defer undo()
	}

	conflicts, err := res.VCS.Conflicts(ctx, m.Root())
	if err != nil {
		return fmt.Errorf("vcs resolve: %w", err)
	}
	if len(conflicts) == 0 {
		if rf.Against != "" {
			fmt.Printf("vcs resolve: %s merged with no conflicts; conclude it with `git commit`\n", rf.Against)
			return nil
		}
		if globalCfg.DryRun {
			fmt.Println("vcs resolve: nothing to resolve; no conflicted paths")
			return nil
		}
		return settleOwedByHand(ctx, root, rc, m, res.VCS)
	}

	if !globalCfg.DryRun {
		if conflicts, err = autoResolveConflicts(ctx, m, res.VCS, conflicts); err != nil {
			return err
		}
		if len(conflicts) == 0 {
			return nil
		}
	}

	plan, err := settle.PlanConflicts(ctx, m, res.VCS, conflicts)
	if err != nil {
		return err
	}
	reportResolution(plan, globalCfg.DryRun)
	if globalCfg.DryRun {
		return unresolvedError(plan)
	}
	return applyResolution(ctx, root, rc, m, res.VCS, plan, stale)
}

// autoResolveConflicts runs the merge driver over the content conflicts in source files,
// where the VCS did not already run it during the operation (jj), and returns the
// conflicts still standing. The driver decides each file (Magus.AutoResolve), so a
// comment-only edit settles here even though no glob routes it.
func autoResolveConflicts(ctx context.Context, m *magus.Magus, drv types.VCSDriver, conflicts []types.Conflict) ([]types.Conflict, error) {
	var eligible []string
	for _, c := range conflicts {
		if c.Kind == types.ConflictKindContent && m.FindOutputProducer(filepath.Join(m.Root(), filepath.FromSlash(c.Path))) == nil {
			eligible = append(eligible, c.Path)
		}
	}
	if len(eligible) == 0 {
		return conflicts, nil
	}
	if err := drv.RunMergeDriver(ctx, m.Root(), eligible); err != nil {
		return nil, fmt.Errorf("vcs resolve: %w", err)
	}
	left, err := drv.Conflicts(ctx, m.Root())
	if err != nil {
		return nil, fmt.Errorf("vcs resolve: %w", err)
	}
	for _, p := range eligible {
		if !slices.ContainsFunc(left, func(c types.Conflict) bool { return c.Path == p }) {
			fmt.Printf("vcs resolve: auto-resolved %s\n", p)
		}
	}
	if len(left) == 0 {
		fmt.Println("vcs resolve: every conflict is resolved")
	}
	return left, nil
}

// startMergeAgainst begins the merge that --against settles, and returns the function that
// backs it out again: a no-op unless this is a dry run, since otherwise the merge is the
// whole point and stays in progress for the caller to commit.
//
// A real merge rather than `git merge-tree`: merge-tree reports conflicted PATHS only,
// while settle.PlanConflicts decides on the conflict KIND, and a modify/delete is the shape no
// merge driver is ever invoked for. (The read-only PR advisor uses merge-tree because it
// needs only the names.)
//
// So a dry run merges for real and aborts, which is why a clean tree is required up
// front: `git merge --abort` does not guarantee uncommitted work survives.
func startMergeAgainst(ctx context.Context, root string, res types.VCSResolution, ref string) (undo func(), err error) {
	dirty, err := res.VCS.DirtyFiles(ctx, root, nil)
	if err != nil {
		return nil, fmt.Errorf("vcs resolve: read tree status: %w", err)
	}
	if len(dirty) > 0 {
		return nil, fmt.Errorf("vcs resolve: --against needs a clean tree, and %d path(s) are uncommitted, commit or stash them first so backing the merge out cannot lose them", len(dirty))
	}
	// The person resolving, as the box knows them: the merge is theirs to conclude.
	if err := res.VCS.StartMerge(ctx, root, ref, types.Person{}); err != nil {
		return nil, fmt.Errorf("vcs resolve: %w", err)
	}
	if !globalCfg.DryRun {
		// The merge is the point: leave it in progress for the caller to commit.
		return func() {}, nil
	}
	return func() {
		if err := res.VCS.AbortMerge(ctx, root); err != nil {
			// Reported, never swallowed: the tree is NOT as this dry run found it, and a
			// caller told "nothing was touched" would go on to do something else in it.
			slog.ErrorContext(ctx, fmt.Sprintf("could not back out the merge --dry-run started; the tree still has it in progress (git merge --abort): %v", err),
				attr.Notice(""), attr.Component("vcs resolve"))
		}
	}, nil
}

// applyResolution performs the plan: clear markers, record deletions, regenerate once,
// then record everything the regeneration touched.
//
// Every step past the first has already mutated the tree, so failures report how far it
// got. A half-applied resolve otherwise looks like a fresh conflict with the markers
// missing, which `git status` alone cannot explain.
// staleDecls says the declarations came from the committed magusfile because the working
// copy's is mid-merge. It suppresses the regeneration step and nothing else: clearing
// markers and recording paths are decisions ABOUT the conflicts, which either side's
// declarations answer the same way, while regenerating PRODUCES bytes, and a merge that
// touched a generator would have this produce output matching neither side.
func applyResolution(ctx context.Context, root string, rc runConfig, m *magus.Magus, driver types.VCSDriver, plan settle.Plan, staleDecls bool) error {
	if err := driver.KeepIncoming(ctx, m.Root(), slices.Concat(plan.Keep, plan.Rederive)); err != nil {
		return fmt.Errorf("vcs resolve: %w", err)
	}
	if err := driver.RemoveConflicts(ctx, m.Root(), plan.Gone); err != nil {
		return fmt.Errorf("vcs resolve, %s: %w", resolveTreeState(plan, "the conflict markers were already cleared"), err)
	}
	if staleDecls {
		fmt.Println("vcs resolve: not regenerating - the magusfile is still mid-merge, and a " +
			"generator it changes would produce bytes matching neither side. Resolve the " +
			"magusfile, then `" + hint.Run.With("generate:rw") + "` to finish.")
	} else if err := runRebuildTargets(ctx, root, rc, plan.Rebuild); err != nil {
		return fmt.Errorf("vcs resolve, %s: regenerate: %w", resolveTreeState(plan,
			"the conflict markers were cleared and the deletions recorded, but nothing was marked resolved"), err)
	}
	// The registration is derived from the declared outputs, so a conflict in the file
	// holding it is settled by re-deriving. First point the file has no markers.
	settle.EnsureDriver(ctx, m)

	settled, err := plan.Paths(ctx, m, driver)
	if err != nil {
		return fmt.Errorf("vcs resolve: %w", err)
	}
	// stagePaths, not MarkResolved directly: one pathspec matching nothing aborts the whole
	// call before staging anything, so a single conflict involving a RENAME took the other
	// forty paths down with it: regeneration complete, index untouched, and an error naming
	// a file the rename had legitimately removed. filterStageable splits those out first.
	staged, dropped, err := stagePaths(ctx, m.Root(), driver, settled)
	if err != nil {
		return fmt.Errorf("vcs resolve, %s: %w", resolveTreeState(plan, "regeneration completed"), err)
	}
	fmt.Printf("\nrecorded %d path(s); review before continuing: %s\n", len(staged), driver.ReviewCommand())
	// Named, never silent: a path magus settled and could not record is one the caller has
	// to look at, and the count above would otherwise be the only sign it existed.
	if len(dropped) > 0 {
		fmt.Printf("not recorded (gone from disk and untracked, so there is nothing to stage): %s\n",
			strings.Join(dropped, ", "))
	}
	return unresolvedError(plan)
}

// committedMagusfiles returns the COMMITTED content of every conflicted .buzz file, keyed
// by absolute path, for interp.WithOverlay.
//
// The committed side, not either merge stage: it is the one version guaranteed to parse,
// and the declarations it carries are the ones the tree had before this merge started.
//
// No error return, deliberately: every way this can fail (no VCS, a backend that cannot
// report conflicts or read a revision, a file the VCS will not hand back) means the same
// thing to the only caller, which is "carry on with the load failure you already have". An
// error here could only be discarded, and returning one alongside a nil map is the
// ambiguity `nilnil` exists to catch. Nothing to overlay is an empty map.
func committedMagusfiles(ctx context.Context, root string) map[string]string {
	res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
	if err != nil || res.VCS == nil {
		return nil
	}
	conflicts, err := res.VCS.Conflicts(ctx, root)
	if err != nil {
		return nil
	}
	overlay := map[string]string{}
	for _, c := range conflicts {
		if !strings.HasSuffix(c.Path, ".buzz") {
			continue
		}
		// "" is the committed revision in whichever backend this is; naming HEAD here
		// would be correct for git alone.
		content, rerr := res.VCS.ReadFileAt(ctx, root, "", c.Path)
		if rerr != nil {
			return nil
		}
		overlay[filepath.Join(root, filepath.FromSlash(c.Path))] = content
	}
	return overlay
}

// resolveTreeState describes how far the resolve got, for an error message.
func resolveTreeState(plan settle.Plan, reached string) string {
	return fmt.Sprintf("the working tree has been modified (%s), "+
		"to start over abort the merge (`git rebase --abort` or `git merge --abort`), "+
		"to inspect it `git status` now shows %d kept and %d removed path(s)",
		reached, len(plan.Keep)+len(plan.Rederive), len(plan.Gone))
}

// runRebuildTargets runs each target ONCE over every project that needs it. Grouping by
// target keeps this a single build: fifty conflicted files still cost one `magus run
// generate` per distinct target.
func runRebuildTargets(ctx context.Context, root string, rc runConfig, rebuild map[string][]string) error {
	for _, target := range slices.Sorted(maps.Keys(rebuild)) {
		projects := rebuild[target]
		slices.Sort(projects)
		fmt.Printf("regenerating: %s\n", hint.Run.With(target, strings.Join(projects, " ")))
		if err := runTarget(ctx, root, rc, append([]string{target}, projects...)); err != nil {
			return err
		}
	}
	return nil
}

func reportResolution(plan settle.Plan, dryRun bool) {
	verb, goneVerb, rederiveVerb := "resolved", "recorded the deletion of", "re-derived"
	if dryRun {
		verb, goneVerb, rederiveVerb = "would resolve", "would record the deletion of", "would re-derive"
	}
	if len(plan.Keep) > 0 {
		fmt.Printf("%s %d generated file(s), then regenerating them:\n", verb, len(plan.Keep))
		printPaths(plan.Keep)
	}
	if len(plan.Gone) > 0 {
		fmt.Printf("%s %d generated file(s) this workspace no longer tracks:\n", goneVerb, len(plan.Gone))
		printPaths(plan.Gone)
	}
	if len(plan.Rederive) > 0 {
		// Reported because it is not a merge. The managed section is rebuilt from the
		// declared outputs; anything outside it comes from the incoming side, so a
		// hand-written rule only the other side has does not survive.
		fmt.Printf("%s %d file(s) magus maintains, from the workspace rather than by merging:\n",
			rederiveVerb, len(plan.Rederive))
		printPaths(plan.Rederive)
		fmt.Println("  any hand-written rules in these files are taken from the incoming side")
	}
	if len(plan.Manual) > 0 {
		fmt.Printf("left for you: %d conflict(s) magus cannot settle:\n", len(plan.Manual))
		printPaths(plan.Manual)
	}
}

func printPaths(paths []string) {
	for _, p := range paths {
		fmt.Printf("  %s\n", p)
	}
}

// unresolvedError exits non-zero when conflicts remain, so `magus vcs resolve && git
// rebase --continue` cannot skip past one you still have to read.
func unresolvedError(plan settle.Plan) error {
	if len(plan.Manual) == 0 {
		return nil
	}
	return fmt.Errorf("%d conflict(s) still need you, resolve them, then `"+hint.VCSAdd.String()+"` and continue", len(plan.Manual))
}

// vcsResolveHook is `magus vcs resolve --hook <name>`, the settle hooks' half of the
// merge driver: once git has the whole tree it regenerates what the operation changed
// (settle.Hook) and stages the result. Which hooks fire when, and what each sees, is
// vcs.SettleHooks.
//
// What happens to the staged output follows the hook. A commit git has not made yet
// carries it. For a clean merge, whose commit git makes on its own, the hook stops that
// commit instead: git leaves the merge in progress, prints "use 'git commit' to complete
// the merge", and the commit the person then makes is the merge commit, regenerated,
// with no amend and no stale commit ever written. After a rebase, cherry-pick, revert or
// am the commits already exist, so the output is staged and the line says how to fold
// it in; magus never amends.
//
// Failure is loud and never leaves stale output behind quietly: the exact command to
// run is printed, a commit not yet made is stopped, and the regeneration stays owed in
// the git dir, where `magus doctor` reports it.
func vcsResolveHook(ctx context.Context, root string, rc runConfig, hook string, args []string) error {
	// A hook runs in the repository's top level, where the workspace root is too; the
	// merge driver relies on the same. Read git's state before loading anything, since
	// most commits are not merge-shaped and load nothing.
	wsRoot, err := magus.FindRoot(root)
	if err != nil {
		return settleFailed(ctx, hook, vcs.HookOperation{}, settle.Outcome{}, err)
	}
	op, ok, err := vcs.GitHookOperation(ctx, wsRoot, vcs.HookEvent{Hook: hook, Args: args, IndexFile: hookIndexFile()})
	if err != nil {
		return settleFailed(ctx, hook, op, settle.Outcome{}, err)
	}
	if !ok || len(op.Changed) == 0 {
		return nil
	}
	if op.CommitPending {
		// The pre-merge-commit stop below leads to a `git commit` whose pre-commit hook
		// lands here again with the very tree that was just settled.
		if settled, err := vcs.HookTreeSettled(ctx, wsRoot, op); err != nil {
			return settleFailed(ctx, hook, op, settle.Outcome{}, err)
		} else if settled {
			return nil
		}
	}

	m, err := loadMagus(withoutMergeDriverRefresh(ctx), root)
	if err != nil {
		return settleFailed(ctx, hook, op, settle.Outcome{}, fmt.Errorf("the workspace did not load, so nothing regenerated: %w", err))
	}
	run := settle.Quietly(func(ctx context.Context, inv []string) error { return runTarget(ctx, root, rc, inv) }, console.WithRunSink)
	out, err := settle.Hook(ctx, m, op, buildDefinesTarget(ctx, m), run)
	if err != nil {
		return settleFailed(ctx, hook, op, out, err)
	}
	if len(out.Ran) == 0 {
		return nil
	}
	slog.InfoContext(ctx, out.Notice(hook, settle.FoldCommand(ctx, m)), attr.Notice(""))
	if hook == vcs.HookPreMergeCommit && len(out.Staged) > 0 {
		return errSilent{exitCode: 1}
	}
	return nil
}

// settleOwedByHand is `magus vcs resolve` with nothing conflicted: it runs what the
// merge driver left owed, which a settle hook that failed or was bypassed with
// --no-verify leaves behind and `magus doctor` reports, stages the result and clears the
// record. With nothing owed it says so.
func settleOwedByHand(ctx context.Context, root string, rc runConfig, m *magus.Magus, driver types.VCSDriver) error {
	owed, err := vcs.OwedRegenerations(ctx, m.Root())
	if err != nil {
		return fmt.Errorf("vcs resolve: %w", err)
	}
	ran := settle.Invocations(nil, owed, buildDefinesTarget(ctx, m))
	if len(ran) == 0 {
		fmt.Println("vcs resolve: nothing to resolve; no conflicted paths and no regeneration owed")
		return nil
	}
	plan := settle.Plan{Rebuild: map[string][]string{}}
	for _, inv := range ran {
		plan.Rebuild[strings.TrimSuffix(inv[0], ":rw")] = inv[1:]
	}
	if err := runRebuildTargets(ctx, root, rc, plan.Rebuild); err != nil {
		return fmt.Errorf("vcs resolve: regenerate: %w", err)
	}
	settled, err := plan.Paths(ctx, m, driver)
	if err != nil {
		return fmt.Errorf("vcs resolve: %w", err)
	}
	staged, _, err := stagePaths(ctx, m.Root(), driver, settled)
	if err != nil {
		return fmt.Errorf("vcs resolve: %w", err)
	}
	if err := vcs.DropOwedRegenerations(ctx, m.Root(), owed); err != nil {
		return fmt.Errorf("vcs resolve: %w", err)
	}
	fmt.Printf("settled what the merge driver left owed; staged %d path(s): %s\n", len(staged), driver.ReviewCommand())
	return nil
}

// hookIndexFile is the index git exported to this hook, absolute; "" when it exported
// none. git spells the default one relative to the hook's working directory.
func hookIndexFile() string {
	index := os.Getenv("GIT_INDEX_FILE")
	if index == "" || filepath.IsAbs(index) {
		return index
	}
	wd, err := os.Getwd()
	if err != nil {
		return index
	}
	return filepath.Join(wd, index)
}

// settleFailed prints what did not happen and the command that does it, then fails
// the hook. A commit git has not made yet is stopped by that failure; one it has made
// carries stale output, and the line says so.
func settleFailed(ctx context.Context, hook string, op vcs.HookOperation, out settle.Outcome, err error) error {
	regenerate := hint.VCSResolve.With("--hook", hook)
	if len(out.Ran) > 0 {
		regenerate = out.Command()
	}
	slog.ErrorContext(ctx, fmt.Sprintf("could not regenerate after this %s: %v", cmp.Or(op.Kind, "operation"), err),
		attr.Notice(""), attr.Component("magus"))
	switch {
	case op.CommitPending:
		slog.ErrorContext(ctx, fmt.Sprintf("the commit is stopped; regenerate with `%s`, stage the result, and commit again (`git commit --no-verify` commits without it, and the output stays stale)", regenerate),
			attr.Notice(""), attr.Component("magus"))
	case op.Kind != "":
		slog.ErrorContext(ctx, fmt.Sprintf("HEAD carries stale generated output; regenerate with `%s`, then `git commit --amend --no-edit`", regenerate),
			attr.Notice(""), attr.Component("magus"))
	}
	return errSilent{exitCode: 1}
}

// resolveVCS returns the active VCS resolution for the workspace.
func resolveVCS(ctx context.Context, root string, m *magus.Magus) (types.VCSResolution, error) {
	wsRoot := m.Root()
	if wsRoot == "" {
		wsRoot = root
	}
	return vcs.Resolve(ctx, wsRoot, "", m.VCSOptions())
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
	fmt.Fprintln(w, "--preserve is the exception, and the reason it is a flag. An identity tells")
	fmt.Fprintln(w, "you whether two trees match; it cannot rebuild either one. --preserve also")
	fmt.Fprintln(w, "captures the uncommitted work, tracked edits and untracked files alike, and")
	fmt.Fprintln(w, "prints a handle that restores it. The working copy is untouched either way.")
	fmt.Fprintln(w, "On git and Mercurial a capture is dropped at 30 days, by the next preserve")
	fmt.Fprintln(w, "and by the server's `"+hint.JobRun.With("prune-preserved")+"`. Without a server")
	fmt.Fprintln(w, "that job is a no-op; run `magus server prune-preserved` instead. On Sapling a")
	fmt.Fprintln(w, "capture is a hidden commit no Sapling command can drop, so it stays until you")
	fmt.Fprintln(w, "remove it. Jujutsu mints nothing, so nothing accumulates.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Feed the revision to anything that takes one ("+hint.GraphDiff.With("--rev", "<rev>")+").")
	fmt.Fprintln(w, "Compare two digests to learn whether two workers saw the same uncommitted")
	fmt.Fprintln(w, "tree, which the revision alone cannot tell you: a dirty tree's revision is")
	fmt.Fprintln(w, "the same one everybody else has.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --preserve capture the uncommitted work too, and print a handle for it")
	fmt.Fprintln(w, "  -o name    the citable token: the revision, or <revision>+<digest> when dirty")
	fmt.Fprintln(w, "  -o json    the whole record (global flag; yaml, jsonl and template too)")
}

// vcsCheckpointCmd reads the working state's identity and prints it.
func vcsCheckpointCmd(ctx context.Context, root string, args []string) error {
	var flags *gen.VCSCheckpointFlags
	pos, err := cmdParse("vcs checkpoint", args, func(fs *flag.FlagSet) {
		fs.Usage = func() { vcsCheckpointUsage(os.Stderr) }
		flags = gen.BindVCSCheckpoint(fs)
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
	// every VCS call to the process cwd, which is a different repository the moment you
	// run this from anywhere but the root.
	wsRoot := ws.Root()
	res, err := vcs.Resolve(ctx, wsRoot, "", ws.VCSOptions())
	if err != nil {
		return fmt.Errorf("vcs checkpoint: %w", err)
	}
	cp, err := vcs.Checkpoint(ctx, wsRoot, res, flags.Preserve)
	if err != nil {
		// A failed --preserve can still have MINTED. Mercurial's shelf and Sapling's
		// snapshot commit both exist before the step that reports the failure, and the
		// handle in cp is the only thing that reaches the object, so it is rendered before
		// the failure is returned: the record on stdout, the reason on stderr, and an exit
		// status that still says the command failed. git returns no handle on any of its
		// failure paths, so it never arrives here.
		if cp.Preserved == "" {
			return err
		}
		if emitErr := emitCheckpoint(cp); emitErr != nil {
			return errors.Join(err, emitErr)
		}
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
		return emitNames([]string{cp.Token()})
	}
	fmt.Println(checkpointLine(cp))
	return nil
}

// checkpointLine is the human reading: "<rev> <branch> clean" or "<rev> <branch> dirty
// <digest>", with " preserved <handle>" appended when --preserve minted one. The field
// count is fixed through the dirty word, so a branchless revision (a detached head, jj's
// usual anonymous change) renders "-" rather than collapsing the column and silently
// shifting everything after it.
//
// The handle is on this line because --preserve promises to print one and this is the
// default rendering. Without it the two spellings of the command produced identical
// output while one of them left an object in the user's repository, and the only way back
// to that object was a format flag nobody was told to pass.
func checkpointLine(cp types.VCSCheckpoint) string {
	branch := cp.Branch
	if branch == "" {
		branch = "-"
	}
	if !cp.Dirty {
		return fmt.Sprintf("%s %s clean", cp.Revision, branch)
	}
	line := fmt.Sprintf("%s %s dirty %s", cp.Revision, branch, cp.PatchDigest)
	if cp.Preserved != "" {
		line += " preserved " + cp.Preserved
	}
	return line
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
	fmt.Fprintln(w, "  --reason     why they belong in this change; required with --untracked")
}

// untrackedOverride phrases the refusal a bare --untracked earns.
//
// The flag clears the one report that separates this command from `git add -A`, and
// what it sweeps in lands in a commit everybody pulls. Naming a path stays free of the
// requirement: that says yes to one file the caller looked at.
var untrackedOverride = ward.Override{
	Name:     "magus vcs add --untracked",
	Silences: "stages every undeclared file at once, dropping the report that is the difference between this and git add -A",
	Spelling: `magus vcs add --untracked --reason "<why>"`,
	Records:  "is kept with the staging verdict, in the report and in -o json",
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
	// Before the workspace loads: a refusal about the flags must not need a resolvable
	// tree, and must not land after anything is staged.
	if err := ward.RequireReason(untrackedOverride, af.Untracked, af.Reason); err != nil {
		return usagef("%s", err)
	}

	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}
	// The resolved workspace root, not the --root OVERRIDE this was handed. The override
	// is empty unless the user passed --root, and everything below is workspace-relative:
	// with "" the path math produced `vcs add: "x" is outside the workspace at ` (naming
	// no workspace at all), so naming a path explicitly, which this command's own
	// undeclared-file message tells you to do, could never work. The whole-tree form only
	// appeared to work because an empty dir sends every VCS call to the process cwd.
	// vcsResolveCmd already goes through m.Root() for the same reason.
	root = ws.Root()
	res, err := vcs.Resolve(ctx, root, "", ws.VCSOptions())
	if err != nil || res.VCS == nil {
		return fmt.Errorf("vcs add: no VCS resolved for this workspace")
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
	// it was the flag, or plain git, which is what the command exists to replace.
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
		Reason:      strings.TrimSpace(af.Reason),
	}
	if len(unexplained) > 0 {
		// inputDirty is false by construction: an output is only unexplained BECAUSE no
		// declared input of its project moved. So this is ClassifyDrift's second fork:
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
		staged, gone, err := stagePaths(ctx, root, res.VCS, stage)
		if err != nil {
			return err
		}
		verdict.Staged, dropped = staged, gone
	}
	return emitStaging(verdict, dropped, af.Untracked, globalCfg.DryRun, res.VCS.ReviewCommand())
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
		return nil, fmt.Errorf("vcs add: no workspace root resolved, cannot place %q", paths[0])
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
// types.IsMagusMaintained) show up, so they are reported rather than dropped.
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
//
// review is the backend's own read-back command (VCSDriver.ReviewCommand), passed in
// rather than composed: this line used to say `git diff --cached --stat` to everyone, and
// three of the four backends have no index to read.
func reportStaging(v types.StagingPlan, dropped []string, untracked, dryRun bool, review string) {
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
			fmt.Printf("  reason: %s\n", v.Reason)
		}
	} else {
		if len(v.Undeclared) > 0 {
			fmt.Printf("skipped %d undeclared file(s); no target claims them:\n", len(v.Undeclared))
			printPaths(v.Undeclared)
			fmt.Println("  if one is a new source file, name it explicitly or pass --untracked --reason \"<why>\";")
			fmt.Println("  if it is build residue, add it to your VCS ignore rules")
		}
		if len(v.Maintained) > 0 {
			fmt.Printf("skipped %d file(s) magus itself maintains outside any target's declared outputs:\n", len(v.Maintained))
			printPaths(v.Maintained)
			fmt.Println("  these are not residue; name them explicitly or pass --untracked --reason \"<why>\" to stage them")
		}
	}
	if len(v.Staged) > 0 {
		fmt.Println("\nreview before committing: " + review)
	}
}

// splitMaintained separates paths magus's own core maintains from everything
// else undeclared, so reportStaging can describe each group accurately instead
// of asserting every undeclared path "affects nothing".
//
// A maintained path is one magus writes directly, rather than a target through a
// declared output glob, so ClassifyFiles has nothing to match it against and it
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
// `describe file` classifies the same paths and the two answers must not diverge,
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
func stagePaths(ctx context.Context, root string, driver types.VCSDriver, paths []string) (staged, dropped []string, err error) {
	stageable, dropped, err := filterStageable(ctx, root, driver, paths)
	if err != nil {
		return nil, nil, err
	}
	if len(stageable) == 0 {
		return []string{}, dropped, nil
	}
	// MarkResolved batches the pathspecs, which matters here: `vcs add` over a whole
	// dirty tree is the largest path list these commands hand to the VCS.
	if err := driver.MarkResolved(ctx, root, stageable); err != nil {
		return nil, nil, fmt.Errorf("vcs add: %w", err)
	}
	return stageable, dropped, nil
}

// emitStaging renders the verdict: the structured formats get the value itself, and the
// terminal gets the prose. One decision, several audiences, which is the whole reason
// the verdict is a value. `-o json` used to be accepted here and answer in text.
func emitStaging(v types.StagingPlan, dropped []string, untracked, dryRun bool, review string) error {
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, v)
	case outputName:
		// The paths that were staged, which is what the plan is a plan OF. Dropped paths
		// are deliberately absent: this is the list a caller feeds forward.
		return emitNames(v.Staged)
	}
	reportStaging(v, dropped, untracked, dryRun, review)
	return nil
}

// filterStageable separates paths git add can actually act on from ones that
// would abort the whole `git add` call. See stagePaths for why a missing-but-
// tracked path (a deletion or the old half of a rename) must still be staged.
func filterStageable(ctx context.Context, root string, driver types.VCSDriver, paths []string) (stageable, dropped []string, err error) {
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
	// backends get the paths passed through rather than probed.
	if driver.Name() != "git" {
		return append(stageable, maybeGone...), nil, nil
	}

	// The tracked listing does not care whether a file is still on disk, so a
	// tracked-but-deleted path is kept and its deletion gets recorded.
	known, err := driver.TrackedFiles(ctx, root, maybeGone)
	if err != nil {
		return nil, nil, fmt.Errorf("vcs add: %w", err)
	}
	tracked := make(map[string]bool, len(known))
	for _, p := range known {
		tracked[p] = true
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
