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
// refused naming every condition that failed. `git worktree unlock` is judged the same but
// for the lock. Removal is irreversible, so a fact that cannot be read refuses too, unlike
// the sibling-checkout rule, which fails open.
//
// Split from gitGuard because it reads git and the job store, and Evaluate is a pure
// function of the command line.

// worktreeRemoveRe finds a removal or an unlock on a line that does not parse, which is
// refused as unjudged.
var worktreeRemoveRe = regexp.MustCompile(`\b` + gitOpts + `worktree\s+(?:remove|unlock)\b`)

// rankWorktreeRemove fills a silence and outranks an advisory, but never replaces a deny
// the line earned on its own. A demoted removal outranks only an advisory.
func rankWorktreeRemove(v, removal ShellVerdict) ShellVerdict {
	return stronger(v, removal)
}

// isWorktreeRemove reports a `git worktree remove`, whatever global options precede it, or
// a `git worktree unlock`, which leaves a removal nothing that would stop it.
func isWorktreeRemove(c hint.Invocation) bool {
	if c.Name != "git" {
		return false
	}
	g := parseGit(c.Args)
	return g.sub == "worktree" && len(g.rest) > 0 && (g.rest[0] == "remove" || g.rest[0] == "unlock")
}

// denyWorktreeRemove refuses each `git worktree remove` on command that cannot be proven
// safe. callDir is where the line starts, and the checkout it is in is the caller's own.
func denyWorktreeRemove(ctx context.Context, deps Dependencies, at location, callDir, command string, d Dialect) ShellVerdict {
	// Every command reaches this rule, so a line that cannot hold a removal skips the parse.
	if !mayRemoveWorktree(command) || helpOnlyLine(command, d) {
		return ShellVerdict{}
	}
	for _, path := range removeAllPaths(command) {
		if r := judgeRmTargets(ctx, deps, at, callDir, callDir, []string{path}); len(r.failed) > 0 {
			return worktreeRemoveDenial(r)
		}
	}
	calls, parsed := locateCalls(command, d, callDir, isWorktreeRemoval, false)
	if !parsed {
		if worktreeRemoveRe.MatchString(command) {
			return worktreeRemoveDenial(removal{verb: "removing", target: "the worktree", failed: []string{"the line does not parse, so which worktree it removes cannot be read"}})
		}
		return ShellVerdict{}
	}
	for _, c := range calls {
		if r := judgeWorktreeRemove(ctx, deps, at, callDir, c); len(r.failed) > 0 {
			return worktreeRemoveDenial(r)
		}
	}
	return ShellVerdict{}
}

// removal is one judged removal or unlock: what it does to which worktree, every
// condition it fails, and the command that answers them when one does.
type removal struct {
	verb, target string
	failed       []string
	lead         string
	next         types.NextStep
}

// worktreeRemoveDenial names the first failed condition in the verdict and lists every one
// under the worktree it names in the rationale.
func worktreeRemoveDenial(r removal) ShellVerdict {
	more := ""
	if len(r.failed) > 1 {
		more = fmt.Sprintf(" (%s in the full verdict)", countNoun(len(r.failed)-1, "more condition"))
	}
	v := ShellVerdict{
		Deny: fmt.Sprintf("%s %s may lose work: %s%s.", r.verb, r.target, r.failed[0], more),
		Why: fmt.Sprintf("%s%s %s is refused until magus can prove it loses nothing:\n  - %s\n"+
			"A removal passes when the worktree is a linked worktree of this repository other than your own, clean, "+
			"taken by no live job, every change its unpublished commits made reads the same in your checkout's HEAD, "+
			"and it is unlocked or locked over a worker your session spawned for a job that finished, which git removes only with -f -f. "+
			"A checkout a job's fork made and locked is magus's to remove: `%s <job>` once its job ended, `%s <job>` before. "+
			"An unlock is judged the same, but for the lock. --force changes nothing here but that lock. "+
			"`rm -r` and fs\\removeAll of a worktree, or of a directory holding one, are judged as removing it is.",
			strings.ToUpper(r.verb[:1]), r.verb[1:], r.target, strings.Join(r.failed, "\n  - "), hint.JobWait.String(), hint.JobExit.String()),
		Rule: denyRule{Name: denyRuleWorktreeRemove},
	}
	if r.next.Run == "" {
		return v
	}
	return v.withRemedy(r.lead, r.next)
}

// judgeWorktreeRemove judges one located removal or unlock, each failed condition with
// the command that inspects it.
func judgeWorktreeRemove(ctx context.Context, deps Dependencies, at location, callDir string, c locatedCall) removal {
	if isRecursiveRm(c.inv) {
		return judgeRm(ctx, deps, at, callDir, c)
	}
	s, why := locateRemoval(c)
	r := removal{verb: "removing", target: cmp.Or(s.target, "the worktree")}
	if s.unlock {
		r.verb = "unlocking"
	}
	if why != "" {
		r.failed = []string{why}
		return r
	}
	return judgeRemovalSite(ctx, deps, at, callDir, s, r)
}

// judgeRemovalSite judges the removal or unlock of the one worktree s names.
func judgeRemovalSite(ctx context.Context, deps Dependencies, at location, callDir string, s removalSite, r removal) removal {
	fail := func(why string) removal {
		r.failed = []string{why}
		return r
	}
	if callDir == "" {
		return fail("which checkout this session runs in is unknown, so it cannot be told apart from the target")
	}
	_, hereCommon, ok := gitCheckout(callDir)
	if !ok {
		return fail("this session runs in no git checkout, so the repository and its job store cannot be read")
	}
	if _, common, ok := gitCheckout(s.gitDir); !ok || common != hereCommon {
		return fail(fmt.Sprintf("git runs against another repository than this session's, whose job store magus does not read (`git -C %s rev-parse --git-common-dir`)", s.gitDir))
	}

	opts := deps.VCS
	opts.Name = "git"
	res, err := vcs.Resolve(ctx, s.gitDir, "", opts)
	if err != nil || res.VCS == nil {
		return fail("version control is disabled or unresolvable for this workspace, so nothing about the worktree can be read")
	}
	drv := res.VCS
	all, err := drv.RegisteredCheckouts(ctx, s.gitDir)
	if err != nil {
		return fail("the registered worktrees cannot be read (`git worktree list --porcelain`): " + err.Error())
	}
	i := slices.IndexFunc(all, func(co types.RegisteredCheckout) bool { return samePath(co.Root, s.target) })
	switch {
	case i < 0:
		return fail("it is not a worktree of this repository (`git worktree list`)")
	case all[i].Primary:
		return fail("it is the main worktree, which holds the repository itself (`git worktree list`)")
	}
	co := all[i]
	if within(callDir, co.Root) || within(s.gitDir, co.Root) {
		return fail("this session runs in it; remove it from another checkout")
	}

	if _, err := os.Stat(co.Root); errors.Is(err, fs.ErrNotExist) {
		r.failed = append(r.failed, "its directory is gone, so there is nothing to remove; `git worktree prune` clears the record")
	} else if dirty, err := drv.DirtyFiles(ctx, co.Root, nil); err != nil {
		r.failed = append(r.failed, fmt.Sprintf("its status cannot be read (`git -C %s status`): %s", co.Root, err.Error()))
	} else if len(dirty) > 0 {
		r.failed = append(r.failed, fmt.Sprintf("it has %d modified, staged or untracked path(s), first %s (`git -C %s status`)", len(dirty), dirty[0], co.Root))
	}

	rows, err := jobRowsAt(ctx, at)
	if err != nil {
		if co.Locked && !s.unlock {
			r.failed = append(r.failed, lockHeld(co))
		}
		r.failed = append(r.failed, fmt.Sprintf("the job store cannot be read, so who works in it is unknown (`%s`): %s", hint.LsJobs.String(), err.Error()))
		return r
	}
	var finished []types.Job
	exited, unfiled := "", ""
	for _, row := range rows {
		if row.Worktree.Standing() && samePath(row.Worktree.Root, co.Root) && !mayWorkIn(deps, rows, row) {
			r.failed = append(r.failed, fmt.Sprintf("job %s's fork made it, and only that job's holder or the session that forked it removes it (`%s`)",
				row.ID, hint.DescribeJob.With(row.ID)))
		}
		if !samePath(row.CheckoutRoot, co.Root) {
			continue
		}
		switch {
		case row.State == types.StateExited && row.Result == nil:
			// No result was filed, so there is nothing to wait for; only the tree can lose work.
			unfiled = row.ID
		case row.State.Live():
			r.failed = append(r.failed, fmt.Sprintf("job %s (%s) was taken in it (`%s`)", row.ID, row.State, hint.DescribeJob.With(row.ID)))
			if row.State == types.StateExited {
				exited = row.ID
			}
		case row.State == types.StatePass || row.State == types.StateFail:
			finished = append(finished, row)
		}
	}
	ownedLock := false
	var forked *types.Job
	switch {
	case !co.Locked || s.unlock:
	case co.LockReason == vcs.CheckoutLockReason:
		// git removes a locked worktree only with -f -f, which would also discard changes, so
		// a fork's checkout goes through magus, which removes it only once it holds no work.
		i := slices.IndexFunc(rows, func(row types.Job) bool {
			return row.Worktree.Standing() && samePath(row.Worktree.Root, co.Root) && mayWorkIn(deps, rows, row)
		})
		if i < 0 {
			r.failed = append(r.failed, lockHeld(co))
			break
		}
		forked = &rows[i]
		r.failed = append(r.failed, fmt.Sprintf("it is the checkout job %s's fork made and locked, which git removes only with -f -f; magus unlocks and removes it once the job ends", forked.ID))
	default:
		switch owner := lockOwner(deps, rows, finished); {
		case owner == "":
			r.failed = append(r.failed, lockHeld(co))
		case s.force < 2:
			r.failed = append(r.failed, fmt.Sprintf("it is locked over the worker your session spawned for job %s, which finished, and git removes a locked worktree only with -f -f", owner))
			ownedLock = true
		}
	}
	if co.Head != "" {
		r.failed = append(r.failed, unlanded(ctx, drv, s.gitDir, callDir, co.Head, res.Base)...)
	}

	switch {
	case unfiled != "" && len(r.failed) > 0:
		r.lead = fmt.Sprintf("job %s exited without filing a result, and %s still holds work.", unfiled, r.target)
		r.next = hint.NextForDenyRemedy(string(denyRuleWorktreeRemove), hint.JobExit.Argv(unfiled),
			"exit files the result the job never filed")
	case exited != "":
		r.lead = fmt.Sprintf("job %s exited and is not graded yet; wait for its grade before %s %s.", exited, r.verb, r.target)
		r.next = hint.NextForDenyRemedy(string(denyRuleWorktreeRemove), hint.JobWait.Argv(exited),
			"a job's worktree is removable once its grade is filed")
	case forked != nil && len(r.failed) == 1 && forked.State.Terminal():
		r.lead = fmt.Sprintf("job %s ended, and magus removes the checkout its fork made, keeping it if it holds work.", forked.ID)
		r.next = hint.NextForDenyRemedy(string(denyRuleWorktreeRemove), hint.JobWait.Argv(forked.ID),
			"wait unlocks and removes the checkout of every ended job once it holds no work")
	case forked != nil && len(r.failed) == 1:
		r.lead = fmt.Sprintf("job %s owns %s; ending it removes the checkout its fork made, keeping it if it holds work.", forked.ID, r.target)
		r.next = hint.NextForDenyRemedy(string(denyRuleWorktreeRemove), hint.JobExit.Argv(forked.ID),
			"exit ends the job, then unlocks and removes its checkout once it holds no work")
	case ownedLock && len(r.failed) == 1:
		r.lead = r.target + " is locked over your finished worker; git removes a locked worktree only with -f -f."
		r.next = hint.NextForDenyRemedy(string(denyRuleWorktreeRemove), []string{"git", "worktree", "remove", "-f", "-f", r.target},
			"every condition but the lock holds, and the lock is your own finished worker's")
	}
	return r
}

// mayRemoveWorktree is the cheap test that a line can hold a removal.
func mayRemoveWorktree(command string) bool {
	return strings.Contains(command, "worktree") || strings.Contains(command, "rm") || strings.Contains(command, "removeAll")
}

// isRecursiveRm reports an rm that deletes what lies under a directory.
func isRecursiveRm(c hint.Invocation) bool {
	if c.Name != "rm" {
		return false
	}
	for _, a := range c.Args {
		switch {
		case a == "--":
			return false
		case a == "--recursive":
			return true
		case strings.HasPrefix(a, "--"):
		case strings.HasPrefix(a, "-") && strings.ContainsAny(a, "rR"):
			return true
		}
	}
	return false
}

func isWorktreeRemoval(c hint.Invocation) bool {
	return isWorktreeRemove(c) || isRecursiveRm(c)
}

// buzzRemoveAllRe finds fs\removeAll of a literal path, as in `buzz -e`.
var buzzRemoveAllRe = regexp.MustCompile(`\bfs\\removeAll\s*\(\s*"([^"\\$]+)"\s*\)`)

func removeAllPaths(command string) []string {
	var paths []string
	for _, m := range buzzRemoveAllRe.FindAllStringSubmatch(command, -1) {
		paths = append(paths, m[1])
	}
	return paths
}

// judgeRm judges an rm -r by its literal operands. An unreadable one passes:
// an rm names no worktree, so refusing it would refuse every `rm -rf "$tmp"`.
func judgeRm(ctx context.Context, deps Dependencies, at location, callDir string, c locatedCall) removal {
	if c.unfollowed || c.args == nil || !c.at.known {
		return removal{}
	}
	var targets []string
	for _, w := range c.args {
		lit, ok := literalArg(w.Parts)
		if !ok || strings.HasPrefix(lit, "-") || strings.HasPrefix(lit, "~") {
			continue
		}
		targets = append(targets, lit)
	}
	return judgeRmTargets(ctx, deps, at, callDir, c.at.dir, targets)
}

// judgeRmTargets judges each registered worktree at or under a target as its
// removal is judged, and returns the first that fails. Relative targets resolve
// from dir; a glob expands to what it matches now.
func judgeRmTargets(ctx context.Context, deps Dependencies, at location, callDir, dir string, targets []string) removal {
	if dir == "" {
		return removal{}
	}
	var abs []string
	for _, t := range targets {
		if !filepath.IsAbs(t) {
			t = filepath.Join(dir, t)
		}
		t = filepath.Clean(t)
		if strings.ContainsAny(t, "*?[") {
			matches, _ := filepath.Glob(t)
			abs = append(abs, matches...)
			continue
		}
		abs = append(abs, t)
	}
	if len(abs) == 0 {
		return removal{}
	}
	regDir := ""
	for _, cand := range append([]string{dir, callDir}, abs...) {
		if _, _, ok := gitCheckout(cand); ok && cand != "" {
			regDir = cand
			break
		}
	}
	if regDir == "" {
		return removal{}
	}
	if a, err := filepath.Abs(regDir); err == nil {
		regDir = a
	}
	r := removal{verb: "removing", target: "the worktree"}
	opts := deps.VCS
	opts.Name = "git"
	res, err := vcs.Resolve(ctx, regDir, "", opts)
	if err != nil || res.VCS == nil {
		r.failed = []string{"version control is disabled or unresolvable for this workspace, so which worktrees the deletion reaches cannot be read"}
		return r
	}
	all, err := res.VCS.RegisteredCheckouts(ctx, regDir)
	if err != nil {
		r.failed = []string{"the registered worktrees cannot be read (`git worktree list --porcelain`): " + err.Error()}
		return r
	}
	for _, co := range all {
		if !slices.ContainsFunc(abs, func(t string) bool { return within(co.Root, t) }) {
			continue
		}
		site := removalSite{target: filepath.Clean(co.Root), gitDir: regDir}
		one := removal{verb: "removing", target: site.target}
		if got := judgeRemovalSite(ctx, deps, at, callDir, site, one); len(got.failed) > 0 {
			return got
		}
	}
	return removal{}
}

// ownsCheckout reports whether root is a standing checkout a fork made for a job the
// caller may work in (see [mayWorkIn]). A store that cannot be read owns nothing.
func ownsCheckout(ctx context.Context, deps Dependencies, at location, root string) bool {
	return slices.ContainsFunc(ownedCheckouts(ctx, deps, at), func(owned string) bool { return samePath(owned, root) })
}

// ownedCheckouts are the roots, cleaned, of the standing checkouts forks made for jobs the
// caller may work in (see [mayWorkIn]). A store that cannot be read owns nothing.
func ownedCheckouts(ctx context.Context, deps Dependencies, at location) []string {
	rows, err := jobRowsAt(ctx, at)
	if err != nil {
		return nil
	}
	var roots []string
	for _, row := range rows {
		if row.Worktree.Standing() && mayWorkIn(deps, rows, row) {
			roots = append(roots, filepath.Clean(row.Worktree.Root))
		}
	}
	return roots
}

// mayWorkIn reports whether the caller is row's holder or its root: the session holding row
// or a job row descends from, a caller holding no job that no session spawned, or an agent
// spawned for row whose binding the call does not carry yet.
func mayWorkIn(deps Dependencies, rows []types.Job, row types.Job) bool {
	if deps.lease == "" {
		return deps.caller.Agent == "" || spawnedAsJob(deps.facts, rows, deps.caller.Agent, row.ID)
	}
	if deps.lease == row.ID {
		return true
	}
	return slices.ContainsFunc(types.JobAncestors(rows, row.ID), func(a types.Job) bool { return a.ID == deps.lease })
}

// lockHeld is the condition a lock fails when nothing proves who holds it is done.
func lockHeld(co types.RegisteredCheckout) string {
	reason := "it is locked"
	if co.LockReason != "" {
		reason += fmt.Sprintf(" (%s)", co.LockReason)
	}
	return reason + ", so whoever locked it still holds it (`git worktree list --porcelain`)"
}

// lockOwner names the finished job whose worker locked a checkout, when the caller is the
// session that spawned it: a root caller, and a spawn record in its own session facts
// naming the job. "" when nothing proves that.
func lockOwner(deps Dependencies, rows, finished []types.Job) string {
	if !deps.caller.Identified() || deps.caller.Agent != "" {
		return ""
	}
	if deps.lease != "" {
		i := slices.IndexFunc(rows, func(row types.Job) bool { return row.ID == deps.lease })
		if i < 0 || rows[i].Parent != "" {
			return ""
		}
	}
	for _, row := range finished {
		if spawnedJob(deps.facts, rows, row.ID) {
			return row.ID
		}
	}
	return ""
}

// unlanded reports the commits head carries that no remote-tracking ref and no base
// reaches, unless every change they made reads the same in the HEAD of the checkout
// holding callDir. A base naming nothing in this clone is left out: it reaches nothing,
// so dropping it only makes the answer stricter.
func unlanded(ctx context.Context, drv types.VCSDriver, dir, callDir, head, base string) []string {
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
		return []string{fmt.Sprintf("which of its commits are published cannot be read (`%s`): %s", inspect, err.Error())}
	case len(ids) == 0:
		return nil
	}
	commits := fmt.Sprintf("%d commit(s) on it, newest %.12s, are on no remote-tracking ref or base branch", len(ids), ids[0])
	lr, ok := drv.(types.LandingReporter)
	if !ok {
		return []string{fmt.Sprintf("%s, and this version control cannot tell whether your checkout holds their changes (`%s`)", commits, inspect)}
	}
	paths, err := lr.UnlandedPaths(ctx, callDir, head, "HEAD")
	switch {
	case err != nil:
		return []string{fmt.Sprintf("%s, and whether your checkout's HEAD holds their changes cannot be read (`%s`): %s", commits, inspect, err.Error())}
	case len(paths) > 0:
		return []string{fmt.Sprintf("%s, and %d path(s) they changed read differently in your checkout's HEAD, first %s (`%s`; `git diff %.12s HEAD -- %s`)",
			commits, len(paths), paths[0], inspect, head, paths[0])}
	}
	return nil
}

// removalSite is where a located removal or unlock acts.
type removalSite struct {
	// target is the worktree it names, absolute, and gitDir the directory git runs in.
	target, gitDir string
	unlock         bool
	// force counts -f and --force, since only a second one removes a locked worktree.
	force int
}

// locateRemoval resolves the site of a located removal or unlock, or why it cannot. The
// single operand is resolved against git's -C chain.
func locateRemoval(c locatedCall) (removalSite, string) {
	switch {
	case c.unfollowed:
		return removalSite{}, "it runs inside a conditional, a function, or a loop whose words are not all literal, which the guard does not follow"
	case c.args == nil:
		return removalSite{}, "it is reached through a wrapper, whose arguments cannot be read as literal words"
	}
	at, g, ok := gitCallAt(c.args, c.at)
	if !ok || len(g.rest) != len(c.args)-g.at-1 {
		return removalSite{}, "a word on it is not literal or the directory it runs in is not knowable without running it"
	}
	s := removalSite{unlock: g.rest[0] == "unlock"}
	var operands []string
	rest := g.rest[1:]
	for i, a := range rest {
		if a == "--" {
			operands = append(operands, rest[i+1:]...)
			break
		}
		switch {
		case a == "--force":
			s.force++
		case strings.HasPrefix(a, "--"):
		case strings.HasPrefix(a, "-"):
			// Short options bundle, so -ff is two.
			if strings.Trim(a[1:], "f") == "" {
				s.force += len(a) - 1
			}
		default:
			operands = append(operands, a)
		}
	}
	if len(operands) != 1 || strings.HasPrefix(operands[0], "~") {
		return removalSite{}, "it does not name exactly one worktree as a literal path"
	}
	s.gitDir = at.dir
	if !filepath.IsAbs(s.gitDir) {
		abs, err := filepath.Abs(s.gitDir)
		if err != nil {
			return removalSite{}, "the directory it runs in cannot be resolved"
		}
		s.gitDir = abs
	}
	s.target = operands[0]
	if !filepath.IsAbs(s.target) {
		s.target = filepath.Join(s.gitDir, s.target)
	}
	s.target = filepath.Clean(s.target)
	return s, ""
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

// samePath reports whether a and b name one place. Strings are compared after symlinks
// resolve, then the files by device and inode: EvalSymlinks keeps a case variant on a
// case-insensitive filesystem and a macOS firmlink spelling (/System/Volumes/Data/Users)
// as written, and git accepts both.
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ra, rb := realPath(a), realPath(b)
	if ra == rb {
		return true
	}
	ia, err := os.Stat(ra)
	if err != nil {
		return false
	}
	ib, err := os.Stat(rb)
	return err == nil && os.SameFile(ia, ib)
}

// within reports whether p is root or lies under it, by the same two tests as samePath.
// A part of p that does not exist yet lies wherever its nearest existing ancestor does.
func within(p, root string) bool {
	rp, rr := realPath(p), realPath(root)
	if rel, err := filepath.Rel(rr, rp); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return true
	}
	ri, err := os.Stat(rr)
	if err != nil {
		return false
	}
	for d := rp; ; d = filepath.Dir(d) {
		if di, err := os.Stat(d); err == nil && os.SameFile(di, ri) {
			return true
		}
		if filepath.Dir(d) == d {
			return false
		}
	}
}
