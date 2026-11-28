package settle

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// AutoResolve runs the merge driver over the content conflicts in source files,
// where the VCS did not already run it during the operation (jj), and returns the
// conflicts still standing. The driver decides each file (Magus.AutoResolve), so a
// comment-only edit settles here even though no glob routes it.
func AutoResolve(ctx context.Context, out io.Writer, m *magus.Magus, drv types.VCSDriver, conflicts []types.Conflict) ([]types.Conflict, error) {
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
			fmt.Fprintf(out, "vcs resolve: auto-resolved %s\n", p)
		}
	}
	if len(left) == 0 {
		fmt.Fprintln(out, "vcs resolve: every conflict is resolved")
	}
	return left, nil
}

// StartMergeAgainst begins the merge that --against settles, and returns the function that
// backs it out again: a no-op unless this is a dry run, since otherwise the merge is the
// whole point and stays in progress for the caller to commit.
//
// A real merge rather than `git merge-tree`: merge-tree reports conflicted PATHS only,
// while PlanConflicts decides on the conflict KIND, and a modify/delete is the shape no
// merge driver is ever invoked for. (The read-only PR advisor uses merge-tree because it
// needs only the names.)
//
// So a dry run merges for real and aborts, which is why a clean tree is required up
// front: `git merge --abort` does not guarantee uncommitted work survives.
func StartMergeAgainst(ctx context.Context, root string, res types.VCSResolution, ref string, dryRun bool) (undo func(), err error) {
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
	if !dryRun {
		// The merge is the point: leave it in progress for the caller to commit.
		return func() {}, nil
	}
	return func() {
		if err := res.VCS.AbortMerge(ctx, root); err != nil {
			// Reported, never swallowed: the tree is NOT as this dry run found it, and a
			// caller told "nothing was touched" would go on to do something else in it.
			slog.ErrorContext(ctx, "could not back out the merge --dry-run started; the tree still has it in progress (git merge --abort)",
				attr.Notice(""), attr.Component("vcs resolve"), attr.Error(err))
		}
	}, nil
}

// Apply performs the plan: clear markers, record deletions, regenerate once,
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
func Apply(ctx context.Context, out io.Writer, m *magus.Magus, driver types.VCSDriver, plan Plan, rebuild func(args []string) error, staleDecls bool) error {
	if err := driver.KeepIncoming(ctx, m.Root(), slices.Concat(plan.Keep, plan.Rederive)); err != nil {
		return fmt.Errorf("vcs resolve: %w", err)
	}
	if err := driver.RemoveConflicts(ctx, m.Root(), plan.Gone); err != nil {
		return fmt.Errorf("vcs resolve, %s: %w", treeState(plan, "the conflict markers were already cleared"), err)
	}
	if staleDecls {
		fmt.Fprintf(out, "vcs resolve: not regenerating - the magusfile is still mid-merge, and a generator it changes would produce bytes matching neither side. Resolve the magusfile, then `%s` to finish.\n", hint.Run.With("generate:rw"))
	} else if err := rebuildTargets(out, rebuild, plan.Rebuild); err != nil {
		return fmt.Errorf("vcs resolve, %s: regenerate: %w", treeState(plan,
			"the conflict markers were cleared and the deletions recorded, but nothing was marked resolved"), err)
	}
	// The registration is derived from the declared outputs, so a conflict in the file
	// holding it is settled by re-deriving. First point the file has no markers.
	EnsureDriver(ctx, m)

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
		return fmt.Errorf("vcs resolve, %s: %w", treeState(plan, "regeneration completed"), err)
	}
	fmt.Fprintf(out, "\nrecorded %d path(s); review before continuing: %s\n", len(staged), driver.ReviewCommand())
	// Named, never silent: a path magus settled and could not record is one the caller has
	// to look at, and the count above would otherwise be the only sign it existed.
	if len(dropped) > 0 {
		fmt.Fprintf(out, "not recorded (gone from disk and untracked, so there is nothing to stage): %s\n",
			strings.Join(dropped, ", "))
	}
	return UnresolvedError(plan)
}

// CommittedMagusfiles returns the COMMITTED content of every conflicted .buzz file, keyed
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
func CommittedMagusfiles(ctx context.Context, root string) map[string]string {
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

// treeState describes how far the resolve got, for an error message.
func treeState(plan Plan, reached string) string {
	return fmt.Sprintf("the working tree has been modified (%s), "+
		"to start over abort the merge (`git rebase --abort` or `git merge --abort`), "+
		"to inspect it `git status` now shows %d kept and %d removed path(s)",
		reached, len(plan.Keep)+len(plan.Rederive), len(plan.Gone))
}

// rebuildTargets runs each target ONCE over every project that needs it. Grouping by
// target keeps this a single build: fifty conflicted files still cost one `magus run
// generate` per distinct target.
//
// A failed target does not stop the rest, so one pass names every stale input.
// The error lists each failed command.
func rebuildTargets(out io.Writer, run func(args []string) error, rebuild map[string][]string) error {
	var failed []error
	for _, target := range slices.Sorted(maps.Keys(rebuild)) {
		projects := rebuild[target]
		slices.Sort(projects)
		command := hint.Run.With(target, strings.Join(projects, " "))
		fmt.Fprintf(out, "regenerating: %s\n", command)
		if err := run(append([]string{target}, projects...)); err != nil {
			failed = append(failed, fmt.Errorf("%s: %w", command, err))
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return fmt.Errorf("%d of %d target(s) failed: %w", len(failed), len(rebuild), errors.Join(failed...))
}

// StageFailed names the stages that did not run. Each reads the conflicts the
// failed one was to settle.
func StageFailed(err error, notRun ...string) error {
	return fmt.Errorf("%w: not run because of it: %s", err, strings.Join(notRun, ", "))
}

func Report(out io.Writer, plan Plan, dryRun bool) {
	verb, goneVerb, rederiveVerb := "resolved", "recorded the deletion of", "re-derived"
	if dryRun {
		verb, goneVerb, rederiveVerb = "would resolve", "would record the deletion of", "would re-derive"
	}
	if len(plan.Keep) > 0 {
		fmt.Fprintf(out, "%s %d generated file(s), then regenerating them:\n", verb, len(plan.Keep))
		printPaths(out, plan.Keep)
	}
	if len(plan.Gone) > 0 {
		fmt.Fprintf(out, "%s %d generated file(s) this workspace no longer tracks:\n", goneVerb, len(plan.Gone))
		printPaths(out, plan.Gone)
	}
	if len(plan.Rederive) > 0 {
		// Reported because it is not a merge. The managed section is rebuilt from the
		// declared outputs; anything outside it comes from the incoming side, so a
		// hand-written rule only the other side has does not survive.
		fmt.Fprintf(out, "%s %d file(s) magus maintains, from the workspace rather than by merging:\n",
			rederiveVerb, len(plan.Rederive))
		printPaths(out, plan.Rederive)
		fmt.Fprintln(out, "  any hand-written rules in these files are taken from the incoming side")
	}
	if len(plan.Manual) > 0 {
		fmt.Fprintf(out, "left for you: %d conflict(s) magus cannot settle:\n", len(plan.Manual))
		printPaths(out, plan.Manual)
	}
}

func printPaths(out io.Writer, paths []string) {
	for _, p := range paths {
		fmt.Fprintf(out, "  %s\n", p)
	}
}

// UnresolvedError exits non-zero when conflicts remain, so `magus vcs resolve && git
// rebase --continue` cannot skip past one you still have to read.
func UnresolvedError(plan Plan) error {
	if len(plan.Manual) == 0 {
		return nil
	}
	return fmt.Errorf("%d conflict(s) still need you, resolve them, stage them, and continue", len(plan.Manual))
}

// OwedByHand is `magus vcs resolve` with nothing conflicted. It runs what the
// branch's range against res.Base owes and what the merge driver recorded, one
// run per target, then clears the record. It stages nothing. A range it cannot
// read, as with no base ref, still runs the recorded work and says so; with
// nothing recorded it refuses.
func OwedByHand(ctx context.Context, out io.Writer, m *magus.Magus, res types.VCSResolution, defines func(path, target string) bool, run RunFunc) error {
	generates := func(project string) bool { return defines(project, "generate") }
	owed, owedErr := changeset.RegenerationOwed(ctx, m.Root(), res.VCS, res.Base, m.ClassifyFiles, generates)
	recorded, err := vcs.OwedRegenerations(ctx, m.Root())
	if err != nil {
		return fmt.Errorf("vcs resolve: %w", err)
	}
	ran := Invocations(owed.Owed.Projects, recorded, defines)
	if owedErr != nil {
		if len(ran) == 0 {
			return fmt.Errorf("vcs resolve: what the branch owes is unknown: %w", owedErr)
		}
		fmt.Fprintf(out, "vcs resolve: what the branch owes is unknown (%v); running only what the merge driver recorded\n", owedErr)
	}
	if len(ran) == 0 {
		fmt.Fprintf(out, "vcs resolve: nothing to resolve; no conflicted paths, no recorded regeneration, and %s..%s owes none\n", owed.Owed.Base, owed.Owed.Head)
		return nil
	}
	runs := make([]string, len(ran))
	for i, inv := range ran {
		if err := run(ctx, inv); err != nil {
			return fmt.Errorf("vcs resolve: %s: %w", hint.Run.With(inv...), err)
		}
		runs[i] = hint.Run.With(inv...)
	}
	if err := vcs.DropOwedRegenerations(ctx, m.Root(), recorded); err != nil {
		return fmt.Errorf("vcs resolve: %w", err)
	}
	fmt.Fprintf(out, "vcs resolve: ran %s; review it with %s\n", strings.Join(runs, " && "), res.VCS.ReviewCommand())
	if owed.Fold != "" {
		fmt.Fprintln(out, owed.Fold)
	}
	return nil
}
