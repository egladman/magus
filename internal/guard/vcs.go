package guard

import (
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
)

// The git rules, which are the guard's largest single policy and the one with the
// most to lose: these are the calls that destroy uncommitted work, and a concurrent
// agent's along with it. Split out of internal/guard/shell.go so the VCS reasoning can be
// read without the raw-tool and output-plumbing rules interleaved through it.

// dependencyMutations are the argv prefixes that RE-RESOLVE dependencies and
// rewrite the lockfile, keyed by program. They are what types.CharmUpdate exists
// for: `rw` grants rewriting DERIVED output, which is reproducible from a clean
// checkout, while these read a registry and yield different bytes on different
// days, which is why update is not folded into rw.
//
// A hand-kept list rather than a catalog lookup, unlike the raw-tool rule: the
// spell catalog says which op renders a command, never whether that command's
// write is reproducible, and only the second question picks the charm.
//
// Deliberately narrow, and the exclusions are the load-bearing part. A bare `npm
// install`, `npm ci` and `pnpm install --frozen-lockfile` APPLY a lockfile rather
// than re-resolve one, so they are rw work at most and firing on them would put an
// advisory on the most routine command in a JS repo. `go mod edit` writes go.mod
// without consulting a registry, for the same reason. `mise install` installs
// TOOLS, whose versions are pinned in config rather than resolved into a lockfile.
//
// Each prefix is spelled as its argv words. Written as a space-joined string it needed
// re-splitting on every call, and the bare-program case had to be encoded as an empty
// string: a sentinel indistinguishable from a typo'd entry; here it is the empty prefix
// {{}}, which is what it means.
var dependencyMutations = map[string][][]string{
	"go":          {{"get"}, {"mod", "tidy"}},
	"npm":         {{"update"}, {"up"}},
	"pnpm":        {{"add"}, {"update"}, {"up"}},
	"yarn":        {{"add"}, {"upgrade"}, {"up"}},
	"bun":         {{"add"}, {"update"}},
	"cargo":       {{"update"}, {"add"}},
	"uv":          {{"lock"}, {"add"}},
	"poetry":      {{"lock"}, {"update"}, {"add"}},
	"pip-compile": {{}},
}

// isDependencyMutation reports whether one resolved command re-resolves dependency
// state. The empty prefix matches the program on its own.
func isDependencyMutation(c hint.Invocation) bool {
	for _, want := range dependencyMutations[c.Name] {
		if len(want) == 0 {
			return true
		}
		if len(c.Args) >= len(want) && slices.Equal(c.Args[:len(want)], want) {
			return true
		}
	}
	return false
}

// gitGuard classifies git invocations from PARSED commands, returning the first
// verdict any of them earns.
//
// Parsed rather than pattern-matched because the unanchored form denied `git
// stash` written as PROSE - a commit message, or the magus-vcs-hygiene skill,
// whose whole subject is those commands. A false positive here was once defended
// as the safe direction; with an AST it is not a trade at all, since a quoted
// word structurally cannot be a command, and `cd /repo && git stash` still
// matches however it is reached.
func gitGuard(cmds []hint.Invocation) (BashVerdict, bool) {
	for _, c := range cmds {
		if c.Name != "git" || len(c.Args) == 0 {
			continue
		}
		sub, rest := c.Args[0], c.Args[1:]
		switch sub {
		case "stash":
			// Reading a stash is safe. RESTORING one is not, which this rule used to
			// assume it was: the stash stack is per-REPOSITORY, shared by every linked
			// worktree, so `git stash pop` with no ref applies whatever is at stash@{0}
			// (routinely a stranger's work from another worktree) into your tree, and
			// drops the entry if it applies cleanly. Naming the entry after reading
			// `git stash list` is the deliberate form and stays allowed.
			// `create` writes a stash COMMIT OBJECT and returns its name, touching
			// neither the working tree nor the stash stack, so denyWholeTree was a
			// false positive on it. It falls through to the checkpoint advisory
			// below, which is what it was reaching for.
			if len(rest) > 0 && slices.Contains([]string{"list", "show", "create"}, rest[0]) {
				continue
			}
			if len(rest) > 1 && slices.Contains([]string{"pop", "apply", "drop", "branch"}, rest[0]) {
				continue // an explicit stash@{N}: the caller chose which entry
			}
			// `git stash push -- <paths>` shelves only what it names, so the whole-tree
			// reason does not apply: nothing outside those paths moves, and a concurrent
			// agent's untracked work is untouched. It is also how a workspace escapes a
			// bootstrap deadlock (shelve the one hunk an old binary rejects, build,
			// restore), which this rule was denying, putting that answer out of reach.
			// A bare `git stash push` names nothing and stashes everything, so it stays
			// denied.
			if len(rest) > 1 && rest[0] == "push" && slices.Contains(rest, "--") {
				continue
			}
			if len(rest) > 0 && slices.Contains([]string{"pop", "apply", "drop"}, rest[0]) {
				return denySharedStash(rest[0]), true
			}
			return denyWholeTree("git stash"), true
		case "worktree":
			if len(rest) > 0 && rest[0] == "remove" {
				return BashVerdict{
					Deny: "Check it is clean first with `git -C <path> status`, then remove the worktree from a session that owns it.\n" +
						"git worktree remove deletes that worktree's uncommitted and untracked work, which in a repo running several worktrees is routinely another session's and is in no commit to recover from.",
					Rule: denyRule{Name: denyRuleWorktreeRemove},
				}, true
			}
		case "reset":
			if slices.Contains(rest, "--hard") {
				return denyWholeTree("git reset --hard"), true
			}
		case "checkout":
			// Whole-tree first: it is the broader reason, and one rule owning `-- .`
			// keeps the message the same whichever revision precedes the pathspec.
			if isWholeTreePathspec(rest) {
				return denyWholeTree("git checkout ."), true
			}
			if side := mergeSideRef(rest); side != "" {
				return BashVerdict{
					Deny: denyMergeSideCheckout(side),
					Rule: denyRule{Name: denyRuleMergeSideCheckout, Arg: side},
				}, true
			}
		case "restore":
			if isWholeTreePathspec(rest) {
				return denyWholeTree("git restore ."), true
			}
			if side := mergeSideRef(rest); side != "" {
				return BashVerdict{
					Deny: denyMergeSideCheckout(side),
					Rule: denyRule{Name: denyRuleMergeSideCheckout, Arg: side},
				}, true
			}
		case "clean":
			if isDeletingClean(rest) {
				return denyWholeTree("git clean"), true
			}
		case "add":
			// In the DENY pass, not beside the advisory below it: a stage-everything
			// form reached second (`git restore -- x && git add -A`) lost its deny to
			// whichever advisory the first command earned, which is the ordering the
			// two-pass split exists to prevent.
			if slices.ContainsFunc(rest, isStageAllOperand) {
				return BashVerdict{Deny: denyStageAll, Rule: denyRule{Name: denyRuleStageAll}}, true
			}
		}
	}

	// Advisories, in a second pass so a deny anywhere in a compound command wins
	// over an advisory earlier in it.
	for _, c := range cmds {
		if c.Name != "git" || len(c.Args) == 0 {
			continue
		}
		sub, rest := c.Args[0], c.Args[1:]
		switch sub {
		case "push":
			return BashVerdict{Context: pushGuardContext}, true
		case "add":
			// The stage-everything forms already denied in the first pass.
			return BashVerdict{Context: vcsGuardContext, Kind: advisoryStageClassify}, true
		case "commit":
			return BashVerdict{Context: vcsGuardContext, Kind: advisoryStageClassify}, true
		case "checkout":
			// A revert needs the `--` separator; without it the operand is a
			// branch, which is not this rule's business.
			if slices.Contains(rest, "--") {
				return BashVerdict{Context: revertGuardContext}, true
			}
		case "restore":
			// `git restore` targets worktree files by definition.
			return BashVerdict{Context: revertGuardContext}, true
		case "describe":
			// --tags and --always are the build-stamp spelling (this repository's own
			// go_build target uses both): the caller wants a version string to embed,
			// not the identity of a tree it is handing to someone. A checkpoint does not
			// replace that, so the advisory would be noise on every build.
			if slices.ContainsFunc(rest, func(a string) bool { return a == "--tags" || a == "--always" }) {
				continue
			}
			return BashVerdict{Context: checkpointGuardContext}, true
		case "stash":
			if len(rest) > 0 && rest[0] == "create" {
				return BashVerdict{Context: checkpointGuardContext}, true
			}
		case "rev-parse":
			if isTreeIdentityQuery(rest) {
				return BashVerdict{Context: checkpointGuardContext}, true
			}
		}
	}
	return BashVerdict{}, false
}

// isTreeIdentityQuery reports whether a `git rev-parse` invocation is asking WHICH
// REVISION this is, rather than one of the many repository-layout questions the
// same subcommand answers (`--show-toplevel`, `--git-dir`, `--is-inside-work-tree`).
//
// Two conditions, and both are needed. A HEAD-ish operand excludes the layout
// queries, which take no revision at all. `--abbrev-ref` is then excluded
// explicitly: it takes HEAD and answers with the BRANCH NAME, which a checkpoint
// does not replace.
//
// HEAD-ish is HEAD itself plus the forms that navigate from it (HEAD~2, HEAD^,
// HEAD@{1}), and NOT every word starting with those four letters: a branch called
// HEADLESS_BRANCH is an ordinary revision nobody is asking the identity of.
func isTreeIdentityQuery(args []string) bool {
	if slices.Contains(args, "--abbrev-ref") {
		return false
	}
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if a == "@" || a == "HEAD" || strings.HasPrefix(a, "HEAD~") ||
			strings.HasPrefix(a, "HEAD^") || strings.HasPrefix(a, "HEAD@{") {
			return true
		}
	}
	return false
}

// gitGuardFallback applies the legacy regexes, and runs ONLY when the line does
// not parse. Its false positives on prose are the reason gitGuard exists, so it
// is confined to the case where there is no AST to consult, and there, an
// over-eager deny really is the safe direction, because these rules guard work
// that cannot be recovered.
func gitGuardFallback(command string) (BashVerdict, bool) {
	switch {
	case stashRe.MatchString(command) && !stashSafeRe.MatchString(command):
		return denyWholeTree("git stash"), true
	case resetRe.MatchString(command):
		return denyWholeTree("git reset --hard"), true
	case checkoutRe.MatchString(command):
		return denyWholeTree("git checkout ."), true
	case restoreRe.MatchString(command):
		return denyWholeTree("git restore ."), true
	case cleanRe.MatchString(command):
		return denyWholeTree("git clean"), true
	// Above the push ADVISORY, which used to answer first: `git add -A && git push` on an
	// unparsable line got a reminder instead of the deny, in the one place the file's own
	// invariant says an over-eager deny is the safe direction.
	case stageAllRe.MatchString(command):
		return BashVerdict{Deny: denyStageAll, Rule: denyRule{Name: denyRuleStageAll}}, true
	case pushRe.MatchString(command):
		return BashVerdict{Context: pushGuardContext}, true
	case stageRe.MatchString(command):
		return BashVerdict{Context: vcsGuardContext, Kind: advisoryStageClassify}, true
	case scopedRevertRe.MatchString(command):
		return BashVerdict{Context: revertGuardContext}, true
	}
	return BashVerdict{}, false
}

// isWholeTreePathspec reports the `.` pathspec forms, with or without the `--`
// separator, and nothing narrower.
//
// Not every operand is a pathspec, which is what the earlier "first non-flag word"
// reading missed: `git checkout HEAD -- .` names a tree-ish first and `git restore
// --source HEAD .` passes one as a flag value, so both compared a REVISION against "."
// and fell through to the advisory. Everything after `--` is a pathspec by definition;
// without the separator a bare `.` is one wherever it sits, since no revision is spelled
// that way.
// mergeSideRefs are the pseudo-refs that name ONE SIDE of an operation in progress.
// MERGE_HEAD is the incoming side, ORIG_HEAD the position before it started, and
// CHERRY_PICK_HEAD and REVERT_HEAD are the same shape for their own operations.
var mergeSideRefs = []string{"MERGE_HEAD", "ORIG_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD"}

// mergeSideRef returns the side-naming ref a checkout or restore is pulling a path out of,
// or "" when it names none.
//
// It requires a pathspec, because that is what separates the two commands. `git checkout
// MERGE_HEAD` moves HEAD and touches no file; `git checkout MERGE_HEAD -- <path>`
// OVERWRITES that path with one side's copy, discarding both the other side's changes and
// any merge already computed for it.
func mergeSideRef(args []string) string {
	if !slices.Contains(args, "--") {
		return ""
	}
	for _, a := range args {
		if a == "--" {
			return ""
		}
		if ref, ok := strings.CutPrefix(a, "--source="); ok {
			if slices.Contains(mergeSideRefs, ref) {
				return ref
			}
			continue
		}
		if slices.Contains(mergeSideRefs, a) {
			return a
		}
	}
	return ""
}

// denyMergeSideCheckout explains why restoring a path from one side of a merge is refused.
func denyMergeSideCheckout(ref string) string {
	return "Restoring a path from " + ref + " overwrites it with ONE side, discarding the " +
		"other side's changes and any merge already computed for that file.\n" +
		"It reads like \"undo my edit to this file\" and is not: during a merge the working-tree " +
		"copy IS the merge, and this replaces it wholesale.\n" +
		"Measured here: `git checkout MERGE_HEAD -- magusfile.buzz` during a conflict resolution " +
		"silently dropped the branch's own half of a merged feature. Nothing failed, the gate " +
		"stayed green, and the feature could not fire until someone read the code days later.\n" +
		"What to do instead: for a generated file, `" + hint.VCSResolve.String() + "` settles every " +
		"conflicted one by regenerating. To take one side deliberately, say which - `git checkout " +
		"--ours` or `--theirs` -- <path>`. To keep the merged result, it is already in the file; " +
		"copy it aside before doing anything else."
}

func isWholeTreePathspec(args []string) bool {
	if i := slices.Index(args, "--"); i >= 0 {
		args = args[i+1:]
	}
	return slices.Contains(args, ".")
}

// isStageAllOperand reports the stage-everything spellings of `git add`.
func isStageAllOperand(a string) bool {
	return a == "-A" || a == "--all" || a == "-u" || a == "--update" || a == "."
}

// isDeletingClean reports whether a `git clean` would actually delete.
//
// Read as short-flag CLUSTERS rather than as any word containing one of fdxX, which
// denied `git clean --dry-run` (the d in "dry") and `git clean --exclude=x` (the x):
// two invocations that remove nothing. A dry run anywhere wins: -n and --dry-run only
// list what would go.
func isDeletingClean(args []string) bool {
	deletes := false
	for _, a := range args {
		switch {
		case a == "--dry-run":
			return false
		case strings.HasPrefix(a, "--"):
			deletes = deletes || a == "--force"
		case strings.HasPrefix(a, "-"):
			cluster := a[1:]
			if strings.Contains(cluster, "n") {
				return false
			}
			deletes = deletes || strings.ContainsAny(cluster, "fdxX")
		}
	}
	return deletes
}

// nonGitVCSGuard is gitGuard for every other backend magus drives: Mercurial and
// Sapling, which are one dialect, and Jujutsu, which is its own.
//
// These were unmatched, and the guard doc justified that by saying magus "also drives
// Mercurial and Jujutsu, where recoverability differs: jj snapshots the working copy and
// keeps an operation log, so its nearest equivalents are undoable". That reasoning is
// TRUE OF JJ AND ONLY JJ, and it was generalized to Mercurial without argument. hg has no
// operation log: `hg rollback` only ever undid the last transaction and is gone, `hg
// revert` writes .orig backups for modified TRACKED files alone (and --no-backup drops
// even those), and `hg purge` deletes untracked files with no backup at all: the same
// blast radius as `git clean -f`, which denies. So an hg or sl user had none of the
// protection a git user has, for operations that are just as irrecoverable.
//
// jj was exempt too, on the same sentence's reasoning that its operation log makes these
// undoable. That exemption is gone. Undoable-IN-PRINCIPLE is a weaker guarantee than this
// bar: it needs someone to know `jj undo` exists and to reach for it before later
// operations bury the entry. And recoverability was never the whole test: the worktree
// rule denies because it destroys ANOTHER session's work, which `jj abandon` on a shared
// working copy does just as thoroughly.
//
// SCOPED forms stay allowed, matching the git rules: `hg revert <paths>` names what it
// touches, and only the whole-tree flags below discard a tree the caller did not enumerate.
func nonGitVCSGuard(cmds []hint.Invocation) (BashVerdict, bool) {
	for _, c := range cmds {
		if len(c.Args) == 0 {
			continue
		}
		sub, rest := c.Args[0], c.Args[1:]
		if c.Name == "jj" {
			if v, matched := jjRule(c.Name, sub, rest); matched {
				return v, true
			}
			continue
		}
		if c.Name != "hg" && c.Name != "sl" {
			continue
		}
		switch sub {
		// Deletes UNTRACKED files outright. No backup, no undo, and untracked is exactly
		// where a concurrent agent's unfinished work lives.
		case "purge", "clean":
			return denyWholeTree(c.Name + " " + sub), true
		case "revert":
			if hasAnyFlag(rest, "--all", "-a") {
				return denyWholeTree(c.Name + " revert --all"), true
			}
		// `-C`/`--clean` discards uncommitted changes on the way to another revision,
		// with no .orig backup. sl spells the verb goto; hg accepts update, up and co.
		case "update", "up", "goto", "co":
			if hasAnyFlag(rest, "--clean", "-C") {
				return denyWholeTree(c.Name + " " + sub + " --clean"), true
			}
		}
	}
	return BashVerdict{}, false
}

// jjRule is the Jujutsu arm. Its verbs share no spelling with the others, so it reads as
// its own switch rather than another arm of one that would then be a lookup table.
func jjRule(prog, sub string, rest []string) (BashVerdict, bool) {
	switch sub {
	// Abandons the changes in a revision, defaulting to the working copy.
	case "abandon":
		return denyWholeTree(prog + " abandon"), true
	// With paths it is the scoped restore the git rules also allow; with none it discards
	// every change in the working copy.
	case "restore":
		if !hasPositional(rest) {
			return denyWholeTree(prog + " restore"), true
		}
	case "workspace":
		if len(rest) > 0 && rest[0] == "forget" {
			return BashVerdict{
				Deny: "Check it is clean first, then forget the workspace from a session that owns it.\n" +
					"jj workspace forget drops that workspace's working copy, which in a repo running several is routinely another session's.",
				Rule: denyRule{Name: denyRuleWorktreeRemove},
			}, true
		}
	}
	return BashVerdict{}, false
}

// hasPositional reports whether args carries a non-flag argument, which for a restore is
// the difference between naming what to touch and taking the whole tree.
func hasPositional(args []string) bool {
	for _, a := range args {
		if a != "--" && !strings.HasPrefix(a, "-") {
			return true
		}
	}
	return false
}

// hasAnyFlag reports whether args carries any of the given flags as its own token, so a
// PATH that merely contains one does not match.
func hasAnyFlag(args []string, flags ...string) bool {
	for _, a := range args {
		if slices.Contains(flags, a) {
			return true
		}
	}
	return false
}
