package guard

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// The worktree-remove rule: `git worktree remove` passes when the guard can prove the
// worktree holds nothing that exists nowhere else and nobody is working in it, and is
// refused naming every condition that failed. Removal is irreversible, so a fact that
// cannot be read refuses too, unlike the sibling-checkout rule, which fails open.
//
// Split from gitGuard because it reads git and the job store, and Evaluate is a pure
// function of the command line.

// worktreeRemoveRe finds a removal on a line that does not parse, which is refused as
// unjudged.
var worktreeRemoveRe = regexp.MustCompile(`\b` + gitOpts + `worktree\s+remove\b`)

// rankWorktreeRemove fills a silence and outranks an advisory, but never replaces a deny
// the line earned on its own.
func rankWorktreeRemove(v, removal ShellVerdict) ShellVerdict {
	if removal.Deny == "" || v.Deny != "" {
		return v
	}
	return removal
}

// isWorktreeRemove reports a `git worktree remove`, whatever global options precede it.
func isWorktreeRemove(c hint.Invocation) bool {
	if c.Name != "git" {
		return false
	}
	g := parseGit(c.Args)
	return g.sub == "worktree" && len(g.rest) > 0 && g.rest[0] == "remove"
}

// denyWorktreeRemove refuses each `git worktree remove` on command that cannot be proven
// safe. callDir is where the line starts, and the checkout it is in is the caller's own.
func denyWorktreeRemove(ctx context.Context, deps Dependencies, at location, callDir, command string, d Dialect) ShellVerdict {
	// Every command reaches this rule, so a line that cannot hold a removal skips the parse.
	if !strings.Contains(command, "worktree") || helpOnlyLine(command, d) {
		return ShellVerdict{}
	}
	calls, parsed := locateCalls(command, d, callDir, isWorktreeRemove, false)
	if !parsed {
		if worktreeRemoveRe.MatchString(command) {
			return worktreeRemoveDenial("the worktree", []string{"the line does not parse, so which worktree it removes cannot be read"})
		}
		return ShellVerdict{}
	}
	for _, c := range calls {
		target, failed := judgeWorktreeRemove(ctx, deps, at, callDir, c)
		if len(failed) > 0 {
			return worktreeRemoveDenial(target, failed)
		}
	}
	return ShellVerdict{}
}

// worktreeRemoveDenial lists every failed condition under the worktree it names.
func worktreeRemoveDenial(target string, failed []string) ShellVerdict {
	return ShellVerdict{
		Deny: "Removing " + target + " is refused until magus can prove it loses nothing:\n  - " +
			strings.Join(failed, "\n  - ") + "\n" +
			"A removal passes when the worktree is a linked worktree of this repository other than your own, clean, " +
			"unlocked, taken by no live job, and every commit on it is published or filed by a finished job. " +
			"--force changes nothing here.",
		Rule: denyRule{Name: denyRuleWorktreeRemove},
	}
}

// judgeWorktreeRemove returns the worktree one located removal names and every
// condition it fails, each with the command that inspects it.
func judgeWorktreeRemove(ctx context.Context, deps Dependencies, at location, callDir string, c locatedCall) (string, []string) {
	target, gitDir, why := removalSite(c)
	if why != "" {
		return cmp.Or(target, "the worktree"), []string{why}
	}
	if callDir == "" {
		return target, []string{"which checkout this session runs in is unknown, so it cannot be told apart from the target"}
	}
	_, hereCommon, ok := gitCheckout(callDir)
	if !ok {
		return target, []string{"this session runs in no git checkout, so the repository and its job store cannot be read"}
	}
	if _, common, ok := gitCheckout(gitDir); !ok || common != hereCommon {
		return target, []string{"git runs against another repository than this session's, whose job store magus does not read (`git -C " + gitDir + " rev-parse --git-common-dir`)"}
	}

	opts := deps.VCS
	opts.Name = "git"
	res, err := vcs.Resolve(ctx, gitDir, "", opts)
	if err != nil || res.VCS == nil {
		return target, []string{"version control is disabled or unresolvable for this workspace, so nothing about the worktree can be read"}
	}
	drv := res.VCS
	all, err := drv.RegisteredCheckouts(ctx, gitDir)
	if err != nil {
		return target, []string{"the registered worktrees cannot be read (`git worktree list --porcelain`): " + err.Error()}
	}
	i := slices.IndexFunc(all, func(co types.RegisteredCheckout) bool { return samePath(co.Root, target) })
	switch {
	case i < 0:
		return target, []string{"it is not a worktree of this repository (`git worktree list`)"}
	case all[i].Primary:
		return target, []string{"it is the main worktree, which holds the repository itself (`git worktree list`)"}
	}
	co := all[i]
	if within(callDir, co.Root) || within(gitDir, co.Root) {
		return target, []string{"this session runs in it; remove it from another checkout"}
	}

	var failed []string
	if _, err := os.Stat(co.Root); errors.Is(err, fs.ErrNotExist) {
		failed = append(failed, "its directory is gone, so there is nothing to remove; `git worktree prune` clears the record")
	} else if dirty, err := drv.DirtyFiles(ctx, co.Root, nil); err != nil {
		failed = append(failed, "its status cannot be read (`git -C "+co.Root+" status`): "+err.Error())
	} else if len(dirty) > 0 {
		failed = append(failed, fmt.Sprintf("it has %d modified, staged or untracked path(s), first %s (`git -C %s status`)", len(dirty), dirty[0], co.Root))
	}
	if co.Locked {
		reason := "it is locked"
		if co.LockReason != "" {
			reason += " (" + co.LockReason + ")"
		}
		failed = append(failed, reason+", so whoever locked it still holds it (`git worktree list --porcelain`)")
	}

	rows, err := jobRowsAt(ctx, at)
	if err != nil {
		return target, append(failed, "the job store cannot be read, so who works in it is unknown (`"+hint.LsJobs.String()+"`): "+err.Error())
	}
	filed := false
	for _, row := range rows {
		if !samePath(row.CheckoutRoot, co.Root) {
			continue
		}
		switch {
		case row.State.Live():
			failed = append(failed, fmt.Sprintf("job %s (%s) was taken in it (`%s`)", row.ID, row.State, hint.DescribeJob.With(row.ID)))
		case row.State.Terminal() && row.State != types.StateNoReturn:
			filed = true
		}
	}
	if !filed && co.Head != "" {
		failed = append(failed, unpublished(ctx, drv, gitDir, co.Head, res.Base)...)
	}
	return target, failed
}

// unpublished reports the commits head carries that no remote-tracking ref and no base
// reaches. A base naming nothing in this clone is left out: it reaches nothing, so
// dropping it only makes the answer stricter.
func unpublished(ctx context.Context, drv types.VCSDriver, dir, head, base string) []string {
	var bases []string
	if base != "" {
		if _, err := drv.FindCommit(ctx, dir, base); err == nil {
			bases = append(bases, base)
		}
	}
	inspect := strings.Join(append([]string{"git log", head, "--not --remotes"}, bases...), " ")
	ids, err := drv.UnpublishedRevisions(ctx, dir, head, bases...)
	switch {
	case err != nil:
		return []string{"which of its commits are published cannot be read (`" + inspect + "`): " + err.Error()}
	case len(ids) > 0:
		return []string{fmt.Sprintf("%d commit(s) on it, newest %.12s, are on no remote-tracking ref or base branch and no finished job filed them (`%s`)", len(ids), ids[0], inspect)}
	}
	return nil
}

// removalSite resolves the worktree a located removal names, absolute, and the directory
// git runs in, or why it cannot. The single operand is resolved against git's -C chain.
func removalSite(c locatedCall) (target, gitDir, why string) {
	switch {
	case c.unfollowed:
		return "", "", "it runs inside a conditional, a function, or a loop whose words are not all literal, which the guard does not follow"
	case c.args == nil:
		return "", "", "it is reached through a wrapper, whose arguments cannot be read as literal words"
	}
	at, g, ok := gitCallAt(c.args, c.at)
	if !ok || len(g.rest) != len(c.args)-g.at-1 {
		return "", "", "a word on it is not literal or the directory it runs in is not knowable without running it"
	}
	var operands []string
	for i, a := range g.rest[1:] {
		if a == "--" {
			operands = append(operands, g.rest[i+2:]...)
			break
		}
		if !strings.HasPrefix(a, "-") {
			operands = append(operands, a)
		}
	}
	if len(operands) != 1 || strings.HasPrefix(operands[0], "~") {
		return "", "", "it does not name exactly one worktree as a literal path"
	}
	gitDir = at.dir
	if !filepath.IsAbs(gitDir) {
		abs, err := filepath.Abs(gitDir)
		if err != nil {
			return "", "", "the directory it runs in cannot be resolved"
		}
		gitDir = abs
	}
	target = operands[0]
	if !filepath.IsAbs(target) {
		target = filepath.Join(gitDir, target)
	}
	return filepath.Clean(target), gitDir, ""
}

// jobRowsAt is leaseRows for a rule that must refuse without a store: no cache directory
// is no job store to read, not an empty one.
func jobRowsAt(ctx context.Context, at location) ([]types.Job, error) {
	if at.cacheDir == "" {
		return nil, errors.New("no cache directory resolved for this workspace")
	}
	return leaseRows(ctx, at)
}

// realPath resolves symlinks through the nearest existing ancestor, so a directory that
// is gone still compares equal to its registration.
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p
	}
	return filepath.Join(realPath(parent), filepath.Base(p))
}

func samePath(a, b string) bool {
	return a != "" && b != "" && realPath(a) == realPath(b)
}

// within reports whether p is root or lies under it.
func within(p, root string) bool {
	rel, err := filepath.Rel(realPath(root), realPath(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
