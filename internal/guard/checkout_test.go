package guard

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/libs/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testSiblingCheckoutDeny(command string) string {
	return denySiblingCheckout(".", command, DialectBash, nil)
}

// twoCheckouts builds the layout this rule is about, without invoking git: one
// main checkout whose .git is a DIRECTORY, and one linked worktree whose .git is a
// FILE pointing into the main repository's worktrees/. Returns both roots.
//
// Hand-built rather than shelled out to `git worktree add`: the file format is the
// contract this rule reads, so writing it literally is what the test is for, and it
// keeps the test running where git is absent.
func twoCheckouts(t *testing.T) (main, worktree string) {
	t.Helper()
	root := testkit.NewWorkspace(t).Root()
	main = filepath.Join(root, "repo")
	worktree = filepath.Join(root, "wt-feature")
	require.NoError(t, os.MkdirAll(filepath.Join(main, ".git", "worktrees", "wt-feature"), 0o755))
	require.NoError(t, os.MkdirAll(worktree, 0o755))
	linkGitFile(t, worktree, filepath.Join(main, ".git", "worktrees", "wt-feature"))
	return main, worktree
}

func linkGitFile(t *testing.T, dir, gitdir string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644))
}

// The observed command, reduced: an agent whose session is rooted in one checkout
// reaches into another and runs ITS binary. CLAUDE.md forbids this in capitals and
// nothing enforced it.
func TestDenySiblingCheckoutBlocksAnotherCheckoutOfThisRepository(t *testing.T) {
	main, wt := twoCheckouts(t)
	t.Chdir(main)

	got := testSiblingCheckoutDeny("cd " + wt + " && ./magus affected ci --no-default-charms -s")

	require.NotEmpty(t, got, "a sibling checkout is the case this rule exists for")
	assert.Contains(t, got, wt, "the deny has to name the tree the command points at")
	assert.Contains(t, got, main, "and the one it should have run in; neither is obvious alone")
}

// Symmetric: the mistake is running a checkout that is not the session's, not
// running a worktree specifically. A session in the worktree reaching back into
// main is the same error with the arrows reversed.
func TestDenySiblingCheckoutIsDirectionAgnostic(t *testing.T) {
	main, wt := twoCheckouts(t)
	t.Chdir(wt)

	assert.NotEmpty(t, testSiblingCheckoutDeny("cd "+main+" && magus run test ."))
	// Another checkout is protected rather than routed, so a help request is refused too.
	assert.NotEmpty(t, testSiblingCheckoutDeny("cd "+main+" && magus run --help"))
}

// The observed failure: the host started the hook in the main checkout while the
// session worked in a linked worktree, and every `cd <that worktree> && ./magus` was
// refused as a sibling. The session's checkout is where the command runs, so the
// rule judges from callDir, and a relative cd resolves from there as the shell would.
func TestDenySiblingCheckoutJudgesFromTheCallDirectory(t *testing.T) {
	main, wt := twoCheckouts(t)
	t.Chdir(main)

	assert.Empty(t, denySiblingCheckout(wt, "cd "+wt+" && ./magus run lint .", DialectBash, nil),
		"a cd into the session's own worktree is not a relocation")
	assert.Empty(t, denySiblingCheckout(wt, "cd internal && ./magus run lint .", DialectBash, nil),
		"a relative cd stays inside the session's checkout")
	assert.NotEmpty(t, denySiblingCheckout(wt, "cd "+main+" && ./magus run lint .", DialectBash, nil),
		"the process's own checkout is a sibling of the session's")
	assert.NotEmpty(t, denySiblingCheckout(main, "cd ../wt-feature && ./magus run lint .", DialectBash, nil),
		"a relative cd that leaves the session's checkout is still refused")
}

// A checkout a fork made for the caller's job, or a job it forked, is the caller's own to
// run magus in; any other sibling stays refused.
func TestDenySiblingCheckoutPassesAJobsOwnCheckout(t *testing.T) {
	main, wt := twoCheckouts(t)
	t.Chdir(main)
	ownsWT := func(root string) bool { return samePath(root, wt) }
	ownsNothing := func(string) bool { return false }

	assert.Empty(t, denySiblingCheckout(main, "cd "+wt+" && ./magus run lint .", DialectBash, ownsWT),
		"the job's own checkout is not a sibling to its holder or its root")
	assert.NotEmpty(t, denySiblingCheckout(main, "cd "+wt+" && ./magus run lint .", DialectBash, ownsNothing),
		"a checkout no job of the caller's owns is refused")
}

// A `~` or $HOME target was joined under the call directory as a literal directory, which
// no checkout holds, so the deny was skipped.
func TestDenySiblingCheckoutPlacesAHomeTarget(t *testing.T) {
	main, wt := twoCheckouts(t)
	t.Setenv("HOME", filepath.Dir(main))
	for _, line := range []string{
		"cd ~/repo && ./magus run lint .",
		"cd $HOME/repo && ./magus run lint .",
		`cd "${HOME}/repo" && ./magus run lint .`,
		"cd ~ && cd repo && ./magus run lint .",
	} {
		assert.NotEmpty(t, denySiblingCheckout(wt, line, DialectBash, nil), line)
	}
}

// The boundary that keeps this shippable. A cd into a DIFFERENT repository is
// legitimate in a multi-repo session; denying it would export a false positive to
// every consumer of the guard to catch a mistake nobody makes. It still draws the
// `--root` advisory from Evaluate, which is the right volume for it.
func TestDenySiblingCheckoutAllowsADifferentRepository(t *testing.T) {
	main, _ := twoCheckouts(t)
	other := filepath.Join(t.TempDir(), "unrelated")
	require.NoError(t, os.MkdirAll(filepath.Join(other, ".git"), 0o755))
	t.Chdir(main)

	assert.Empty(t, testSiblingCheckoutDeny("cd "+other+" && magus run build ."))
}

// A cd WITHIN the session's own checkout is not a sibling-checkout finding.
// The general cd deny still refuses it; this rule only upgrades the reason when
// the target is another checkout of THIS repository.
func TestDenySiblingCheckoutAllowsASubdirectoryOfThisCheckout(t *testing.T) {
	main, _ := twoCheckouts(t)
	sub := filepath.Join(main, "internal", "cache")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	t.Chdir(main)

	assert.Empty(t, testSiblingCheckoutDeny("cd "+sub+" && magus run test ."))
}

// Fail OPEN on every unknown. A guard that blocks because it could not read
// something has its priorities backwards, and these are the ways reading fails.
func TestDenySiblingCheckoutFailsOpen(t *testing.T) {
	main, wt := twoCheckouts(t)

	t.Run("session is in no checkout at all", func(t *testing.T) {
		t.Chdir(t.TempDir())
		assert.Empty(t, testSiblingCheckoutDeny("cd "+wt+" && magus run test ."))
	})

	t.Run("the line does not run magus", func(t *testing.T) {
		t.Chdir(main)
		assert.Empty(t, testSiblingCheckoutDeny("cd "+wt+" && ls -la"),
			"naming a directory is not relocating a magus command into it")
	})

	t.Run("a .git file that is not a gitdir pointer", func(t *testing.T) {
		t.Chdir(main)
		require.NoError(t, os.WriteFile(filepath.Join(wt, ".git"), []byte("garbage\n"), 0o644))
		assert.Empty(t, testSiblingCheckoutDeny("cd "+wt+" && magus run test ."))
	})
}

// git accepts a RELATIVE gitdir, and writes one for a worktree created inside the
// repository it belongs to, which is exactly this repo's `.claude/worktrees/`
// layout. Reading it as absolute resolves it against the wrong directory and the
// rule silently stops firing in the case it was written for.
func TestDenySiblingCheckoutResolvesARelativeGitdir(t *testing.T) {
	main, wt := twoCheckouts(t)
	rel, err := filepath.Rel(wt, filepath.Join(main, ".git", "worktrees", "wt-feature"))
	require.NoError(t, err)
	linkGitFile(t, wt, rel)
	t.Chdir(main)

	assert.NotEmpty(t, testSiblingCheckoutDeny("cd "+wt+" && ./magus affected ci"))
}

// A submodule is its own repository, so its common dir is its own git dir with no
// worktrees segment to trim. It must compare UNEQUAL to its parent, or every
// submodule in every consumer's tree becomes an unrunnable directory.
func TestDenySiblingCheckoutAllowsASubmodule(t *testing.T) {
	main, _ := twoCheckouts(t)
	sub := filepath.Join(main, "vendor", "dep")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	gitdir := filepath.Join(main, ".git", "modules", "dep")
	require.NoError(t, os.MkdirAll(gitdir, 0o755))
	linkGitFile(t, sub, gitdir)
	t.Chdir(main)

	assert.Empty(t, testSiblingCheckoutDeny("cd "+sub+" && magus run test ."))
}

// A path inside a QUOTED ARGUMENT is not a cd, and the line that found this was a
// test harness for the rule itself: `printf '%s' "cd <sibling> && ls" | ./magus
// shell` mentions magus and contains the text of a cd, and the regex this
// rule inherited read that as relocating into the sibling. Anything that names a
// checkout while running magus (a note, a message, a --root) hits the same trap.
func TestDenySiblingCheckoutIgnoresAPathInsideAQuotedArgument(t *testing.T) {
	main, wt := twoCheckouts(t)
	t.Chdir(main)

	assert.Empty(t, testSiblingCheckoutDeny(`printf '%s' "cd `+wt+` && ls" | ./magus shell`))
	assert.Empty(t, testSiblingCheckoutDeny(`./magus notes add "compare against `+wt+`"`))
}

// Shared with magusInThrowawayCopy rather than reimplemented, so the variable
// expansion that rule needed works here for free. The observed sample chains a
// whole pipeline onto an assignment.
func TestDenySiblingCheckoutResolvesAnAssignedPath(t *testing.T) {
	main, wt := twoCheckouts(t)
	t.Chdir(main)

	assert.NotEmpty(t, testSiblingCheckoutDeny("WT="+wt+"; cd $WT && ./magus run lint ."))
}

// A sibling reason replaces a silence or an advisory: the advice is about a tree the
// command was never going to judge.
func TestRankSiblingCheckoutFillsASilence(t *testing.T) {
	for _, v := range []ShellVerdict{{}, {Context: "advice"}} {
		got := rankSiblingCheckout(v, "aimed at another checkout")

		assert.Equal(t, "aimed at another checkout", got.Deny)
		assert.Equal(t, denyRuleSiblingCheckout, got.Rule.Name)
		assert.Empty(t, got.Context, "a deny that still carries advisory context renders both")
	}
}

// An existing DENY stands. Replacing it would swap a block the caller already has for
// a different one, and one is enough.
func TestRankSiblingCheckoutYieldsToAnExistingDeny(t *testing.T) {
	pipe := pipeDeny("ls", "grep")
	got := rankSiblingCheckout(ShellVerdict{Deny: pipe}, "aimed at another checkout")

	assert.Equal(t, pipe, got.Deny)
}

// The common case: nothing to add, and the pure verdict passes through untouched.
func TestRankSiblingCheckoutIsInertWithoutAReason(t *testing.T) {
	for _, v := range []ShellVerdict{{}, {Context: "advice"}, {Deny: pipeDeny("ls", "grep")}} {
		assert.Equal(t, v, rankSiblingCheckout(v, ""))
	}
}

// Without the sibling rule, a cd ahead of magus passes: it is the same command as the
// project operand or `--root`. Only another checkout of THIS repository, which the
// sibling rule resolves on disk, or a throwaway copy is refused.
func TestCdAheadOfMagusPassesWithoutTheSiblingRule(t *testing.T) {
	for _, cmd := range []string{
		"cd /Users/someone/checkouts/other && ./magus run lint .",
		"cd libs/gopherbuzz && magus run test .",
		"cd libs/diagnostics && magus run test",
		"cd .github/actions/advice && ../../../magus buzz -t merge-queue.buzz",
		"cd /repo && git status && ./magus run lint .",
		"(cd libs/foo && magus run test .)",
	} {
		assert.Empty(t, Evaluate(strict(testDependencies()), cmd).Deny, cmd)
	}
	// The relocations that change which tree magus judges stay refused.
	assert.Equal(t, denyRuleThrowawayCopy,
		Evaluate(strict(testDependencies()), "cd /tmp/copy && magus run lint .").Rule.Name)
}

func TestCheckoutRulesAdviseWhenSetToAdvise(t *testing.T) {
	requireAdvisedOnce(t, Evaluate(advising(testDependencies()), "cd /tmp/copy && magus run lint ."), denyRuleThrowawayCopy)

	main, wt := twoCheckouts(t)
	t.Chdir(main)
	command := "cd " + wt + " && ./magus affected ci --no-default-charms -s"
	sibling := advising(Dependencies{}).rankGraded(ShellVerdict{}, denyRuleSiblingCheckout, testSiblingCheckoutDeny(command), rankSiblingCheckout)
	requireAdvisedOnce(t, sibling, denyRuleSiblingCheckout)
}

// The workspace's foreign-binary rule refuses this checkout's binary rooted at another,
// so the denial and its remedy name the target's own binary or nothing.
func TestSiblingCheckoutNamesTheTargetsOwnBinary(t *testing.T) {
	main, wt := twoCheckouts(t)
	require.NoError(t, os.WriteFile(filepath.Join(main, "magus"), []byte("#!/bin/sh\n"), 0o755))
	t.Chdir(main)
	line := "cd ../" + filepath.Base(wt) + " && ./magus describe job harness/worker"

	denial := testSiblingCheckoutDeny(line)
	assert.Contains(t, denial, "`"+filepath.Join(wt, "magus")+" --root "+wt+" <args>`")
	assert.NotContains(t, denial, filepath.Join(main, "magus")+" --root")

	lead, next := siblingCheckoutRemedy(main, line, DialectBash)
	assert.Empty(t, lead, "with no binary in the target there is no command to name")
	assert.Empty(t, next)

	require.NoError(t, os.WriteFile(filepath.Join(wt, "magus"), []byte("#!/bin/sh\n"), 0o755))
	_, next = siblingCheckoutRemedy(main, line, DialectBash)
	require.Len(t, next, 1)
	assert.Equal(t, filepath.Join(wt, "magus")+" --root "+wt+" describe job harness/worker", next[0].Run)
}
