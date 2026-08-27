package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// twoCheckouts builds the layout this rule is about, without invoking git: one
// main checkout whose .git is a DIRECTORY, and one linked worktree whose .git is a
// FILE pointing into the main repository's worktrees/. Returns both roots.
//
// Hand-built rather than shelled out to `git worktree add`: the file format is the
// contract this rule reads, so writing it literally is what the test is for, and it
// keeps the test running where git is absent.
func twoCheckouts(t *testing.T) (main, worktree string) {
	t.Helper()
	root := t.TempDir()
	// EvalSymlinks on both sides of the comparison, so the fixture has to agree:
	// macOS hands out /var/folders/... temp dirs that resolve to /private/var.
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
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

	got := denySiblingCheckout("cd " + wt + " && ./magus affected ci --no-default-charms -s")

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

	assert.NotEmpty(t, denySiblingCheckout("cd "+main+" && magus run test ."))
}

// The boundary that keeps this shippable. A cd into a DIFFERENT repository is
// legitimate in a multi-repo session; denying it would export a false positive to
// every consumer of the guard to catch a mistake nobody makes. It still draws the
// `--root` advisory from evaluateBashGuard, which is the right volume for it.
func TestDenySiblingCheckoutAllowsADifferentRepository(t *testing.T) {
	main, _ := twoCheckouts(t)
	other := filepath.Join(t.TempDir(), "unrelated")
	require.NoError(t, os.MkdirAll(filepath.Join(other, ".git"), 0o755))
	t.Chdir(main)

	assert.Empty(t, denySiblingCheckout("cd "+other+" && magus run build ."))
}

// A cd WITHIN the session's own checkout is the ordinary cwd advisory's business,
// not a deny. Denying it would block the single most common shape in the corpus.
func TestDenySiblingCheckoutAllowsASubdirectoryOfThisCheckout(t *testing.T) {
	main, _ := twoCheckouts(t)
	sub := filepath.Join(main, "internal", "cache")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	t.Chdir(main)

	assert.Empty(t, denySiblingCheckout("cd "+sub+" && magus run test ."))
}

// Fail OPEN on every unknown. A guard that blocks because it could not read
// something has its priorities backwards, and these are the ways reading fails.
func TestDenySiblingCheckoutFailsOpen(t *testing.T) {
	main, wt := twoCheckouts(t)

	t.Run("session is in no checkout at all", func(t *testing.T) {
		t.Chdir(t.TempDir())
		assert.Empty(t, denySiblingCheckout("cd "+wt+" && magus run test ."))
	})

	t.Run("the line does not run magus", func(t *testing.T) {
		t.Chdir(main)
		assert.Empty(t, denySiblingCheckout("cd "+wt+" && ls -la"),
			"naming a directory is not relocating a magus command into it")
	})

	t.Run("a .git file that is not a gitdir pointer", func(t *testing.T) {
		t.Chdir(main)
		require.NoError(t, os.WriteFile(filepath.Join(wt, ".git"), []byte("garbage\n"), 0o644))
		assert.Empty(t, denySiblingCheckout("cd "+wt+" && magus run test ."))
	})
}

// git accepts a RELATIVE gitdir, and writes one for a worktree created inside the
// repository it belongs to - which is exactly this repo's `.claude/worktrees/`
// layout. Reading it as absolute resolves it against the wrong directory and the
// rule silently stops firing in the case it was written for.
func TestDenySiblingCheckoutResolvesARelativeGitdir(t *testing.T) {
	main, wt := twoCheckouts(t)
	rel, err := filepath.Rel(wt, filepath.Join(main, ".git", "worktrees", "wt-feature"))
	require.NoError(t, err)
	linkGitFile(t, wt, rel)
	t.Chdir(main)

	assert.NotEmpty(t, denySiblingCheckout("cd "+wt+" && ./magus affected ci"))
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

	assert.Empty(t, denySiblingCheckout("cd "+sub+" && magus run test ."))
}

// A path inside a QUOTED ARGUMENT is not a cd, and the line that found this was a
// test harness for the rule itself: `printf '%s' "cd <sibling> && ls" | ./magus
// session hook` mentions magus and contains the text of a cd, and the regex this
// rule inherited read that as relocating into the sibling. Anything that names a
// checkout while running magus - a note, a message, a --root - hits the same trap.
func TestDenySiblingCheckoutIgnoresAPathInsideAQuotedArgument(t *testing.T) {
	main, wt := twoCheckouts(t)
	t.Chdir(main)

	assert.Empty(t, denySiblingCheckout(`printf '%s' "cd `+wt+` && ls" | ./magus session hook`))
	assert.Empty(t, denySiblingCheckout(`./magus notes add "compare against `+wt+`"`))
}

// Shared with magusInThrowawayCopy rather than reimplemented, so the variable
// expansion that rule needed works here for free. The observed corpus chains a
// whole pipeline onto an assignment.
func TestDenySiblingCheckoutResolvesAnAssignedPath(t *testing.T) {
	main, wt := twoCheckouts(t)
	t.Chdir(main)

	assert.NotEmpty(t, denySiblingCheckout("WT="+wt+"; cd $WT && ./magus run lint ."))
}

// The ordering the wiring exists for. An ADVISE must lose: guardCdMagusRe fires on
// every one of these lines and answers "name the project instead", which is true
// and beside the point when the command is aimed at another tree.
func TestRankSiblingCheckoutOutranksAnAdvise(t *testing.T) {
	cwdAdvise := bashGuardVerdict{Context: cwdGuardContext}
	got := rankSiblingCheckout(cwdAdvise, "aimed at another checkout")

	assert.Equal(t, "aimed at another checkout", got.Deny)
	assert.Empty(t, got.Context, "a deny that still carries advisory context renders both")
}

// An existing DENY stands. Replacing it would swap a block the caller already has
// for a different one, and one is enough.
func TestRankSiblingCheckoutYieldsToAnExistingDeny(t *testing.T) {
	got := rankSiblingCheckout(bashGuardVerdict{Deny: outputPipeDeny}, "aimed at another checkout")

	assert.Equal(t, outputPipeDeny, got.Deny)
}

// The common case: nothing to add, and the pure verdict passes through untouched.
func TestRankSiblingCheckoutIsInertWithoutAReason(t *testing.T) {
	for _, v := range []bashGuardVerdict{{}, {Context: cwdGuardContext}, {Deny: outputPipeDeny}} {
		assert.Equal(t, v, rankSiblingCheckout(v, ""))
	}
}

// The pure rules must only ADVISE on this shape, or the rule above is dead weight
// and the tests around it prove nothing.
func TestCdIntoACheckoutIsOnlyAdvisoryWithoutTheSiblingRule(t *testing.T) {
	v := evaluateBashGuard("cd /Users/someone/checkouts/other && ./magus run lint .")

	assert.Empty(t, v.Deny)
	assert.NotEmpty(t, v.Context)
}
