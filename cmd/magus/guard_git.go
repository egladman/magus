package main

import (
	"slices"
	"strings"
)

// The git rules, which are the guard's largest single policy and the one with the
// most to lose: these are the calls that destroy uncommitted work, and a concurrent
// agent's along with it. Split out of guard_shell.go so the VCS reasoning can be
// read without the raw-tool and output-plumbing rules interleaved through it.

// guardDependencyMutations are the argv prefixes that RE-RESOLVE dependencies and
// rewrite the lockfile, keyed by program. They are what types.CharmRelock exists
// for: `rw` grants rewriting DERIVED output, which is reproducible from a clean
// checkout, while these read a registry and yield different bytes on different
// days - which is why relock is not folded into rw.
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
// string - a sentinel indistinguishable from a typo'd entry; here it is the empty prefix
// {{}}, which is what it means.
var guardDependencyMutations = map[string][][]string{
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
func isDependencyMutation(c guardCommand) bool {
	for _, want := range guardDependencyMutations[c.Name] {
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
func gitGuard(cmds []guardCommand) (bashGuardVerdict, bool) {
	for _, c := range cmds {
		if c.Name != "git" || len(c.Args) == 0 {
			continue
		}
		sub, rest := c.Args[0], c.Args[1:]
		switch sub {
		case "stash":
			// Reading a stash is safe. RESTORING one is not, which this rule used to
			// assume it was: the stash stack is per-REPOSITORY, shared by every linked
			// worktree, so `git stash pop` with no ref applies whatever is at stash@{0} -
			// routinely a stranger's work from another worktree - into your tree, and
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
			// bootstrap deadlock - shelve the one hunk an old binary rejects, build,
			// restore - which this rule was denying, putting that answer out of reach.
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
				return bashGuardVerdict{
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
			if isWholeTreePathspec(rest) {
				return denyWholeTree("git checkout ."), true
			}
		case "restore":
			if isWholeTreePathspec(rest) {
				return denyWholeTree("git restore ."), true
			}
		case "clean":
			if isDeletingClean(rest) {
				return denyWholeTree("git clean"), true
			}
		case "add":
			// In the DENY pass, not beside the advisory below it: a stage-everything
			// form reached second - `git restore -- x && git add -A` - lost its deny to
			// whichever advisory the first command earned, which is the ordering the
			// two-pass split exists to prevent.
			if slices.ContainsFunc(rest, isStageAllOperand) {
				return bashGuardVerdict{Deny: denyStageAll, Rule: denyRule{Name: denyRuleStageAll}}, true
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
			return bashGuardVerdict{Context: pushGuardContext}, true
		case "add":
			// The stage-everything forms already denied in the first pass.
			return bashGuardVerdict{Context: vcsGuardContext, Kind: advisoryStageClassify}, true
		case "commit":
			return bashGuardVerdict{Context: vcsGuardContext, Kind: advisoryStageClassify}, true
		case "checkout":
			// A revert needs the `--` separator; without it the operand is a
			// branch, which is not this rule's business.
			if slices.Contains(rest, "--") {
				return bashGuardVerdict{Context: revertGuardContext}, true
			}
		case "restore":
			// `git restore` targets worktree files by definition.
			return bashGuardVerdict{Context: revertGuardContext}, true
		case "describe":
			// --tags and --always are the build-stamp spelling (this repository's own
			// go_build target uses both): the caller wants a version string to embed,
			// not the identity of a tree it is handing to someone. A checkpoint does not
			// replace that, so the advisory would be noise on every build.
			if slices.ContainsFunc(rest, func(a string) bool { return a == "--tags" || a == "--always" }) {
				continue
			}
			return bashGuardVerdict{Context: checkpointGuardContext}, true
		case "stash":
			if len(rest) > 0 && rest[0] == "create" {
				return bashGuardVerdict{Context: checkpointGuardContext}, true
			}
		case "rev-parse":
			if isTreeIdentityQuery(rest) {
				return bashGuardVerdict{Context: checkpointGuardContext}, true
			}
		}
	}
	return bashGuardVerdict{}, false
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
// is confined to the case where there is no AST to consult - and there, an
// over-eager deny really is the safe direction, because these rules guard work
// that cannot be recovered.
func gitGuardFallback(command string) (bashGuardVerdict, bool) {
	switch {
	case guardStashRe.MatchString(command) && !guardStashSafeRe.MatchString(command):
		return denyWholeTree("git stash"), true
	case guardResetRe.MatchString(command):
		return denyWholeTree("git reset --hard"), true
	case guardCheckoutRe.MatchString(command):
		return denyWholeTree("git checkout ."), true
	case guardRestoreRe.MatchString(command):
		return denyWholeTree("git restore ."), true
	case guardCleanRe.MatchString(command):
		return denyWholeTree("git clean"), true
	// Above the push ADVISORY, which used to answer first: `git add -A && git push` on an
	// unparsable line got a reminder instead of the deny, in the one place the file's own
	// invariant says an over-eager deny is the safe direction.
	case guardStageAllRe.MatchString(command):
		return bashGuardVerdict{Deny: denyStageAll, Rule: denyRule{Name: denyRuleStageAll}}, true
	case guardPushRe.MatchString(command):
		return bashGuardVerdict{Context: pushGuardContext}, true
	case guardStageRe.MatchString(command):
		return bashGuardVerdict{Context: vcsGuardContext, Kind: advisoryStageClassify}, true
	case guardScopedRevertRe.MatchString(command):
		return bashGuardVerdict{Context: revertGuardContext}, true
	}
	return bashGuardVerdict{}, false
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
// denied `git clean --dry-run` (the d in "dry") and `git clean --exclude=x` (the x) -
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
