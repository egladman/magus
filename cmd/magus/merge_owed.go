package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// How long the job waits for the operation that submitted it to let go of the tree.
// post-rewrite fires before a rebase removes its state dir; a stop for conflicts is
// longer than this, and the hook that fires when it finishes submits the job again.
const (
	owedOperationWait = 30 * time.Second
	owedOperationPoll = 250 * time.Millisecond
)

// serverRegenerateOwed is the worker for the regenerate-owed job: it runs the
// regenerations the merge driver recorded, once, over the finished tree, and stages what
// they wrote. The post-merge, post-rewrite and post-commit hooks installed by
// installRegenHooks submit it and return, so the work happens on the daemon.
//
// It stages and never amends. The job runs after git has returned and the person may
// already be typing the next command, so moving HEAD under them is a race, and the
// drift notice set the convention: print the command that folds the fix in.
//
// A failed regeneration leaves the record in place for the next hook or a manual run,
// and `magus doctor` reports it until then.
func serverRegenerateOwed(ctx context.Context, root string, args []string) error {
	if _, err := cmdParse("server "+job.NameRegenerateOwed, args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "usage: magus server "+job.NameRegenerateOwed)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Regenerate and stage the generated files a finished merge or rebase kept one")
			fmt.Fprintln(os.Stderr, "side of. This is the worker for `"+hint.JobRun.With(job.NameRegenerateOwed)+"`; prefer that form.")
		}
	}); err != nil {
		return err
	}
	// Checked before the workspace loads: post-commit submits this job on every commit.
	owed, err := vcs.OwedRegenerations(ctx, root)
	if err != nil {
		return fmt.Errorf("server %s: %w", job.NameRegenerateOwed, err)
	}
	if len(owed) == 0 {
		return nil
	}
	finished, err := waitForFinishedOperation(ctx, root, owedOperationWait, owedOperationPoll)
	if err != nil {
		return fmt.Errorf("server %s: %w", job.NameRegenerateOwed, err)
	}
	if !finished {
		slog.InfoContext(ctx, "regenerate-owed: a merge or rebase is still in progress; the regeneration stays owed until it finishes")
		return nil
	}

	m, err := loadMagus(ctx, root)
	if err != nil {
		return fmt.Errorf("server %s: %w", job.NameRegenerateOwed, err)
	}
	res, err := resolveVCS(ctx, root, m)
	if err != nil {
		return fmt.Errorf("server %s: %w", job.NameRegenerateOwed, err)
	}
	if res.VCS == nil {
		return fmt.Errorf("server %s: a regeneration is owed but no VCS resolved for %s", job.NameRegenerateOwed, m.Root())
	}
	s, err := settleOwedRegeneration(ctx, m, res.VCS, func(ctx context.Context, args []string) error {
		// root, not m.Root(): loadMagus is a singleton keyed on the override it first saw.
		return runTarget(ctx, root, runConfig{}, args)
	})
	if err != nil {
		notice := fmt.Sprintf("regenerate-owed: could not regenerate what the merge owes, so it stays owed; retry with `%s`: %v",
			hint.JobRun.With(job.NameRegenerateOwed), err)
		fmt.Fprintln(os.Stderr, notice)
		noteJobDesktop(ctx, job.NameRegenerateOwed, notice)
		return err
	}
	notice := s.notice(foldCommand(ctx, m.Root(), res.VCS))
	fmt.Fprintln(os.Stderr, notice)
	noteJobDesktop(ctx, job.NameRegenerateOwed, notice)
	return nil
}

// waitForFinishedOperation polls until root's worktree is no longer mid merge or rebase,
// reporting false when wait elapses first.
func waitForFinishedOperation(ctx context.Context, root string, wait, poll time.Duration) (bool, error) {
	deadline := time.Now().Add(wait)
	for {
		underway, err := vcs.OperationUnderway(ctx, root)
		if err != nil || !underway {
			return err == nil, err
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(poll):
		}
	}
}

// owedSettlement is what one settle did.
type owedSettlement struct {
	// ran is each invocation's args, in run order.
	ran [][]string
	// staged are the regenerated paths recorded in the index.
	staged []string
	// undeclared are entries whose project or target no longer exists; they are dropped,
	// since no run can ever settle them.
	undeclared []vcs.OwedRegeneration
}

// settleOwedRegeneration runs every owed regeneration once through regenerate, stages
// the declared outputs of the rebuilt projects that changed, and drops what it settled
// from the record. On a regeneration error the record is left untouched.
func settleOwedRegeneration(ctx context.Context, m *magus.Magus, driver types.VCSDriver, regenerate func(context.Context, []string) error) (owedSettlement, error) {
	owed, err := vcs.OwedRegenerations(ctx, m.Root())
	if err != nil || len(owed) == 0 {
		return owedSettlement{}, err
	}
	var s owedSettlement
	var live []vcs.OwedRegeneration
	for _, o := range owed {
		if p := m.Get(o.Project); p != nil {
			if _, ok := p.TargetOutputs[o.Target]; ok {
				live = append(live, o)
				continue
			}
		}
		s.undeclared = append(s.undeclared, o)
	}

	plan := resolutionPlan{rebuild: map[string][]string{}}
	for _, inv := range owedInvocations(live) {
		if err := regenerate(ctx, inv); err != nil {
			return owedSettlement{}, fmt.Errorf("%s: %w", hint.Run.With(inv...), err)
		}
		s.ran = append(s.ran, inv)
	}
	for _, o := range live {
		plan.rebuild[o.Target] = append(plan.rebuild[o.Target], o.Project)
	}
	paths, err := settledPaths(ctx, m, driver, plan)
	if err != nil {
		return owedSettlement{}, err
	}
	if resolver, ok := driver.(types.ConflictResolver); ok && len(paths) > 0 {
		if s.staged, _, err = stagePaths(ctx, m.Root(), driver.Name(), resolver, paths); err != nil {
			return owedSettlement{}, err
		}
	}
	if err := vcs.DropOwedRegenerations(ctx, m.Root(), slices.Concat(live, s.undeclared)); err != nil {
		return owedSettlement{}, err
	}
	return s, nil
}

// owedInvocations groups owed entries into `<target>:rw <projects...>` runs, deepest
// projects first. An ancestor's generators read what its nested projects generate (the
// root's knowledge graph indexes docs/ pages), and docs is not declared a dependency of
// the root, so one run across both could build the root first and index stale pages.
func owedInvocations(owed []vcs.OwedRegeneration) [][]string {
	type group struct {
		depth  int
		target string
	}
	groups := map[group][]string{}
	for _, o := range owed {
		g := group{projectDepth(o.Project), o.Target}
		groups[g] = append(groups[g], o.Project)
	}
	order := slices.SortedFunc(maps.Keys(groups), func(a, b group) int {
		return cmp.Or(cmp.Compare(b.depth, a.depth), cmp.Compare(a.target, b.target))
	})
	out := make([][]string, 0, len(order))
	for _, g := range order {
		projects := slices.Sorted(slices.Values(groups[g]))
		out = append(out, append([]string{g.target + ":rw"}, slices.Compact(projects)...))
	}
	return out
}

func projectDepth(path string) int {
	if path == "." || path == "" {
		return 0
	}
	return strings.Count(path, "/") + 1
}

// foldCommand is how to fold staged output into HEAD: an amend when HEAD is an unpushed
// git commit, "" when it may already be published or the backend cannot say.
func foldCommand(ctx context.Context, root string, driver types.VCSDriver) string {
	pr, ok := driver.(types.PushStatusReporter)
	if !ok || driver.Name() != "git" {
		return ""
	}
	head, err := driver.Metadata(ctx, root)
	if err != nil {
		return ""
	}
	if pushed, known, err := pr.CommitPushed(ctx, root, head.ID); err != nil || !known || pushed {
		return ""
	}
	return "git commit --amend --no-edit"
}

// notice is the one line the job prints: what ran, what it staged, and how to finish.
func (s owedSettlement) notice(fold string) string {
	runs := make([]string, len(s.ran))
	for i, inv := range s.ran {
		runs[i] = hint.Run.With(inv...)
	}
	var b strings.Builder
	if len(s.ran) > 0 {
		fmt.Fprintf(&b, "regenerate-owed: ran %s", strings.Join(runs, ", "))
	}
	switch {
	case len(s.ran) == 0:
		b.WriteString("regenerate-owed: nothing left to run")
	case len(s.staged) == 0:
		b.WriteString("; the kept versions were current, nothing to stage")
	case fold != "":
		fmt.Fprintf(&b, "; staged %d file(s); fold them into HEAD with `%s`", len(s.staged), fold)
	default:
		fmt.Fprintf(&b, "; staged %d file(s); HEAD may already be pushed, so commit them as a new commit", len(s.staged))
	}
	if len(s.undeclared) > 0 {
		gone := make([]string, len(s.undeclared))
		for i, o := range s.undeclared {
			gone[i] = o.Target + " in " + o.Project
		}
		fmt.Fprintf(&b, "; dropped, no longer declared: %s", strings.Join(gone, ", "))
	}
	return b.String()
}

// installRegenHooks installs the hook that submits the regenerate-owed job after a
// merge, rebase, amend or merge-concluding commit. Same guarantees as installDriftHooks:
// best-effort, never fatal to starting the daemon, a no-op on a backend without it.
func installRegenHooks(ctx context.Context) {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	res, err := vcs.Resolve(ctx, cwd, "", types.VCSOptions{})
	if err != nil || res.VCS == nil {
		return
	}
	installer, ok := res.VCS.(types.RegenHookInstaller)
	if !ok {
		return
	}
	root, err := res.VCS.Root(ctx, cwd)
	if err != nil {
		root = cwd
	}
	installed, err := installer.InstallRegenHook(ctx, root, hint.JobRun.With(job.NameRegenerateOwed))
	if err != nil {
		slog.WarnContext(ctx, "server start: could not install VCS regenerate hook", slog.String("error", err.Error()))
		return
	}
	if len(installed) > 0 {
		fmt.Fprintf(os.Stderr, "magus: installed %s regenerate hook(s) [%s]; a merge's kept generated files now regenerate after it finishes\n", res.Name, strings.Join(installed, ", "))
	}
}
