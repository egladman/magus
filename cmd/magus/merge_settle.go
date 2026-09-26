package main

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// vcsResolveHook is `magus vcs resolve --hook <name>`, the settle hooks' half of the
// merge driver: once git has the whole tree it regenerates what the operation changed
// and stages the result. Which hooks fire when, and what each sees, is vcs.SettleHooks.
//
// Everything is bounded by what the operation changed. Its paths are classified against
// the workspace's declarations, and only the projects that own a changed source or
// output run their generate target, plus the exact targets the merge driver recorded
// for the generated files it kept a side of. An operation that touches no generator's
// input runs nothing and prints nothing.
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
		return settleFailed(hook, vcs.HookOperation{}, nil, err)
	}
	op, ok, err := vcs.GitHookOperation(ctx, wsRoot, vcs.HookEvent{Hook: hook, Args: args, IndexFile: hookIndexFile()})
	if err != nil {
		return settleFailed(hook, op, nil, err)
	}
	if !ok || len(op.Changed) == 0 {
		return nil
	}
	if op.CommitPending {
		// The pre-merge-commit stop below leads to a `git commit` whose pre-commit hook
		// lands here again with the very tree that was just settled.
		if settled, err := vcs.HookTreeSettled(ctx, wsRoot, op); err != nil {
			return settleFailed(hook, op, nil, err)
		} else if settled {
			return nil
		}
	}

	m, err := loadMagus(withoutMergeDriverRefresh(ctx), root)
	if err != nil {
		return settleFailed(hook, op, nil, fmt.Errorf("the workspace did not load, so nothing regenerated: %w", err))
	}
	files, err := m.ClassifyFiles(ctx, op.Changed)
	if err != nil {
		return settleFailed(hook, op, nil, err)
	}
	owed, err := vcs.OwedRegenerations(ctx, m.Root())
	if err != nil {
		return settleFailed(hook, op, nil, err)
	}
	s := settlement{op: op, kind: op.Kind}
	s.ran = settleInvocations(generatorProjects(files), owed, buildDefinesTarget(ctx, m))
	if len(s.ran) == 0 {
		return nil
	}
	for _, inv := range s.ran {
		if err := runTarget(ctx, root, rc, inv); err != nil {
			s.recordOwed(ctx, m, files)
			return settleFailed(hook, op, s.ran, fmt.Errorf("%s: %w", hint.Run.With(inv...), err))
		}
	}
	if s.staged, err = stageRegenerated(ctx, m, op, s.ran); err != nil {
		s.recordOwed(ctx, m, files)
		return settleFailed(hook, op, s.ran, err)
	}
	if err := vcs.DropOwedRegenerations(ctx, m.Root(), owed); err != nil {
		return settleFailed(hook, op, s.ran, err)
	}
	if op.CommitPending {
		if err := vcs.HookRecordSettledTree(ctx, m.Root(), op); err != nil {
			return settleFailed(hook, op, s.ran, err)
		}
	}
	fmt.Fprintln(os.Stderr, s.notice(hook, foldCommand(ctx, m.Root(), resolvedDriver(ctx, root, m))))
	if hook == vcs.HookPreMergeCommit && len(s.staged) > 0 {
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
	ran := settleInvocations(nil, owed, buildDefinesTarget(ctx, m))
	if len(ran) == 0 {
		fmt.Println("vcs resolve: nothing to resolve; no conflicted paths and no regeneration owed")
		return nil
	}
	plan := resolutionPlan{rebuild: map[string][]string{}}
	for _, inv := range ran {
		plan.rebuild[strings.TrimSuffix(inv[0], ":rw")] = inv[1:]
	}
	if err := runRebuildTargets(ctx, root, rc, plan.rebuild); err != nil {
		return fmt.Errorf("vcs resolve: regenerate: %w", err)
	}
	settled, err := settledPaths(ctx, m, driver, plan)
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

// generatorProjects are the projects whose declared source or output the operation
// changed, sorted. An output that moved counts too: it was regenerated against a base
// this operation replaced, or the merge driver kept one side of it.
func generatorProjects(files []types.FileEntry) []string {
	set := types.SourceProjects(files)
	for _, f := range files {
		if f.Role == "output" {
			for _, p := range f.OutputOf {
				set[p] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(set))
}

// settleInvocations groups the runs into `<target>:rw <projects...>` invocations,
// deepest projects first: the generate target of every project in projects that defines
// one, plus each owed record's own target. An ancestor's generators read what its nested
// projects generate (the root's knowledge graph indexes docs/ pages), and docs is not
// declared a dependency of the root, so one run across both could build the root first
// and index stale pages.
func settleInvocations(projects []string, owed []vcs.OwedRegeneration, defines func(path, target string) bool) [][]string {
	type group struct {
		depth  int
		target string
	}
	groups := map[group]map[string]bool{}
	add := func(project, target string) {
		g := group{projectDepth(project), target}
		if groups[g] == nil {
			groups[g] = map[string]bool{}
		}
		groups[g][project] = true
	}
	for _, p := range projects {
		if defines(p, "generate") {
			add(p, "generate")
		}
	}
	for _, o := range owed {
		if defines(o.Project, o.Target) {
			add(o.Project, o.Target)
		}
	}
	order := slices.SortedFunc(maps.Keys(groups), func(a, b group) int {
		return cmp.Or(cmp.Compare(b.depth, a.depth), cmp.Compare(a.target, b.target))
	})
	out := make([][]string, 0, len(order))
	for _, g := range order {
		out = append(out, append([]string{g.target + ":rw"}, slices.Sorted(maps.Keys(groups[g]))...))
	}
	return out
}

func projectDepth(path string) int {
	if path == "." || path == "" {
		return 0
	}
	return strings.Count(path, "/") + 1
}

// stageRegenerated stages every declared output the invocations rewrote and returns
// them. Limited to the outputs of the projects that ran, so a file already modified
// elsewhere is not swept into the merge.
func stageRegenerated(ctx context.Context, m *magus.Magus, op vcs.HookOperation, ran [][]string) ([]string, error) {
	rebuilt := map[string]bool{}
	for _, inv := range ran {
		for _, p := range inv[1:] {
			rebuilt[p] = true
		}
	}
	dirty, err := vcs.HookDirtyFiles(ctx, m.Root(), op)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, path := range dirty {
		producer := m.FindOutputProducer(filepath.Join(m.Root(), filepath.FromSlash(path)))
		if producer != nil && rebuilt[projectKey(producer)] {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return nil, nil
	}
	return paths, vcs.HookStage(ctx, m.Root(), op, paths)
}

// settlement is what one hook run did, for the line it prints.
type settlement struct {
	op     vcs.HookOperation
	kind   string
	ran    [][]string
	staged []string
}

// recordOwed leaves the runs that did not settle owed in the git dir, each with the
// changed outputs of its projects, so `magus doctor` reports them until a run does.
func (s settlement) recordOwed(ctx context.Context, m *magus.Magus, files []types.FileEntry) {
	for _, inv := range s.ran {
		target := strings.TrimSuffix(inv[0], ":rw")
		for _, project := range inv[1:] {
			var paths []string
			for _, f := range files {
				if f.Role == "output" && slices.Contains(f.OutputOf, project) {
					paths = append(paths, f.Path)
				}
			}
			_, _ = vcs.RecordOwedRegeneration(ctx, m.Root(), vcs.OwedRegeneration{Project: project, Target: target, Paths: paths})
		}
	}
}

// notice is the one line a settled hook prints: what changed, what ran, and what the
// output's fate is.
func (s settlement) notice(hook, fold string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "magus: this %s changed generator inputs; ran %s", s.kind, joinRuns(s.ran))
	switch {
	case len(s.staged) == 0:
		b.WriteString("; the generated output was already current")
	case hook == vcs.HookPreMergeCommit:
		fmt.Fprintf(&b, "; staged %d regenerated file(s) into the merge, so the commit is left to you: `git commit` concludes it", len(s.staged))
	case s.op.CommitPending:
		fmt.Fprintf(&b, "; staged %d regenerated file(s) into this commit", len(s.staged))
	case fold != "":
		fmt.Fprintf(&b, "; staged %d regenerated file(s); fold them into HEAD with `%s`", len(s.staged), fold)
	default:
		fmt.Fprintf(&b, "; staged %d regenerated file(s); HEAD may already be pushed, so commit them as a new commit", len(s.staged))
	}
	return b.String()
}

// settleFailed prints what did not happen and the command that does it, then fails
// the hook. A commit git has not made yet is stopped by that failure; one it has made
// carries stale output, and the line says so.
func settleFailed(hook string, op vcs.HookOperation, ran [][]string, err error) error {
	regenerate := hint.VCSResolve.With("--hook", hook)
	if len(ran) > 0 {
		regenerate = joinRuns(ran)
	}
	fmt.Fprintf(os.Stderr, "magus: could not regenerate after this %s: %v\n", cmp.Or(op.Kind, "operation"), err)
	switch {
	case op.CommitPending:
		fmt.Fprintf(os.Stderr, "magus: the commit is stopped; regenerate with `%s`, stage the result, and commit again (`git commit --no-verify` commits without it, and the output stays stale)\n", regenerate)
	case op.Kind != "":
		fmt.Fprintf(os.Stderr, "magus: HEAD carries stale generated output; regenerate with `%s`, then `git commit --amend --no-edit`\n", regenerate)
	}
	return errSilent{exitCode: 1}
}

func joinRuns(ran [][]string) string {
	runs := make([]string, len(ran))
	for i, inv := range ran {
		runs[i] = hint.Run.With(inv...)
	}
	return strings.Join(runs, " && ")
}

// resolvedDriver is the workspace's VCS driver, or nil when none resolves; foldCommand
// then has nothing to say, which is the safe answer.
func resolvedDriver(ctx context.Context, root string, m *magus.Magus) types.VCSDriver {
	res, err := resolveVCS(ctx, root, m)
	if err != nil {
		return nil
	}
	return res.VCS
}

// foldCommand is how to fold staged output into HEAD: an amend when HEAD is an unpushed
// git commit, "" when it may already be published or the backend cannot say.
func foldCommand(ctx context.Context, root string, driver types.VCSDriver) string {
	if driver == nil || driver.Name() != "git" {
		return ""
	}
	head, err := driver.Metadata(ctx, root)
	if err != nil {
		return ""
	}
	if pushed, known, err := driver.CommitPushed(ctx, root, head.ID); err != nil || !known || pushed {
		return ""
	}
	return "git commit --amend --no-edit"
}
