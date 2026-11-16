package guard

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// rewriteDenial words the refusal of a run the rw charm would have write
// past the row's write paths. Say names the files, since a host shows the
// agent Say and leaves Why behind the verdict's ref.
func rewriteDenial(v runVerdict, me types.Job, defaults []string) denial {
	r, next := v.run, v.run.readOnly(defaults)
	return denial{
		Say: fmt.Sprintf("magus workspace: `%s` rewrites %s outside lease %s's write paths; run `%s`.", r.target, outsideShown(v.outside), me.ID, next),
		Why: fmt.Sprintf("Under the rw charm `%s` writes `%s`, which lease %s's write paths (%s) do not cover.\n"+
			"`%s` reports the same drift without writing it. The checkout's integrator, "+integratorIs+", rewrites the rest after it integrates the units; name the files in your result's unresolved risks if the unit needs them.",
			r.target, strings.Join(v.outside, "`, `"), me.ID, writePathsShown(me), next),
	}
}

// outsideShown quotes the first writePathsShownMax of globs, then counts the
// rest, which the refusal's Why lists.
func outsideShown(globs []string) string {
	if len(globs) <= writePathsShownMax {
		return fmt.Sprintf("`%s`", strings.Join(globs, "`, `"))
	}
	return fmt.Sprintf("`%s` and %d more", strings.Join(globs[:writePathsShownMax], "`, `"), len(globs)-writePathsShownMax)
}

// writePathsShown lists row's write paths, the first writePathsShownMax of
// them, then names how many more and where they are.
func writePathsShown(row types.Job) string {
	if len(row.Paths.Write) <= writePathsShownMax {
		return strings.Join(row.Paths.Write, ", ")
	}
	return fmt.Sprintf("%s and %d more (%s)", strings.Join(row.Paths.Write[:writePathsShownMax], ", "),
		len(row.Paths.Write)-writePathsShownMax, hint.DescribeJob.With(row.ID))
}

// denyLeaseCheckout refuses a row's holder a magus call outside the row's
// standing checkout. A cd, -C or --root on the line places the call; an
// unreadable directory places it where the host started. Reports pass.
func denyLeaseCheckout(ctx context.Context, deps Dependencies, actingLease, command string) denial {
	if actingLease == "" {
		return denial{}
	}
	runs := func(c hint.Invocation) bool { return isMagusInvocation(c) && !reportsOnly(c.Args) }
	calls, ok := placeMagusCalls(ctx, deps, command, runs)
	if !ok || len(calls) == 0 {
		return denial{}
	}
	me, ok := actingLiveLease(ctx, deps, actingLease)
	if !ok || me.Worktree == nil || me.Worktree.Root == "" || workerRole(me, deps.caller) == "" {
		return denial{}
	}
	// An adopted checkout nobody took yet is bound by the worker's exec
	// (job.Store.Exec), so no tree is its own yet.
	if !me.Worktree.Made && me.CheckoutRoot == "" {
		return denial{}
	}
	root := me.Worktree.Root
	envelope := hookLocation(ctx, deps).workspace
	for _, c := range calls {
		if there := cmp.Or(c.root, envelope); there == "" || samePath(there, root) || scratchWorkspace(there, root) {
			continue
		}
		say := fmt.Sprintf("magus workspace: `%s` runs outside lease %s's checkout %s; ", elideCommand(command), me.ID, root)
		if _, err := os.Stat(filepath.Join(root, "magus")); err != nil {
			say += fmt.Sprintf("run it there, which has no ./magus yet (%v): build or place one first.", err)
		} else {
			say += fmt.Sprintf("run it there: `%s`.", strings.Join(slices.Concat([]string{"cd", root, "&&", "./magus"}, withoutRelocation(c.inv.Args)), " "))
		}
		return denial{
			Say: say,
			Why: "A host shell can start a subagent's call in its parent's directory, and a cd does not persist between calls, so a worker's magus run lands in a tree its row does not own and is graded against that tree. " +
				"Naming the checkout on the line places the call there, and that checkout's binary judges it with the sources it was linked from.",
		}
	}
	return denial{}
}
