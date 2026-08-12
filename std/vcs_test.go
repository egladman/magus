package std

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The vcs accessors had no direct unit coverage at all - they were exercised only through
// interp integration tests, which run with a workspace on the context and a real checkout
// under the process cwd. That left the branch that matters least tested and most recently
// changed: what happens when there is NO VCS to ask.
//
// These pin the contract that replaced the empty-string sentinels: outside a checkout every
// accessor RAISES rather than handing back "" - because "" is a value a branch name or a
// subject line can legitimately hold, and a caller that forgot to test for it would
// interpolate an empty commit into a version string with nothing to surface it.
//
// Note what "outside a checkout" means here, because it is not what it looks like:
// resolveVCS picks a DRIVER (git is on PATH), so it resolves even in a bare temp dir, and
// it is the git command that then fails. The v == nil branch needs git itself to be absent.
// So these run against a real driver failing on a non-repo, which is the path a user
// actually hits - building from a release tarball or a container context.

// chdirOutsideAnyRepo moves the process into a bare temp dir for the duration of the test,
// so nothing resolves a VCS by walking up from the magus checkout the tests run in.
func chdirOutsideAnyRepo(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
}

func TestVcsAccessorsRaiseWithNoVCS(t *testing.T) {
	chdirOutsideAnyRepo(t)
	ctx := context.Background()

	for name, call := range map[string]func() (string, error){
		"ref":      func() (string, error) { return VcsRef(ctx) },
		"describe": func() (string, error) { return VcsDescribe(ctx) },
	} {
		t.Run(name, func(t *testing.T) {
			v, err := call()
			require.Error(t, err, "an unavailable value must raise, not return \"\"")
			assert.Empty(t, v, "the zero value still comes back alongside the error")
			assert.Contains(t, err.Error(), "git",
				"the message must name the VCS it failed to read, not just fail")
		})
	}
}

// TestVcsCommitRaisesWithNoVCS covers the record-returning accessor. Its docs used to tell
// callers to test a FIELD (c.date == "") to discover there was no commit, which is the
// same sentinel problem one level in.
func TestVcsCommitRaisesWithNoVCS(t *testing.T) {
	chdirOutsideAnyRepo(t)

	c, err := VcsCommit(context.Background(), "")
	require.Error(t, err)
	assert.Empty(t, c.ID, "no half-populated record to sniff")
	assert.Contains(t, err.Error(), "git")
}

// TestVcsHistoryRaisesWithNoVCS: an empty history and an unreadable one are different
// answers, and only one of them means "this project has no commits".
func TestVcsHistoryRaisesWithNoVCS(t *testing.T) {
	chdirOutsideAnyRepo(t)

	got, err := VcsHistory(context.Background(), 5)
	require.Error(t, err)
	assert.Nil(t, got)
}

// TestVcsIsDirtyRaisesWhenTheProbeFails is the most important one here. is_dirty is the
// drift-gate primitive - a generate target asks it "did my output change?" - and it used to
// answer false when the git status probe FAILED. That is a gate reporting clean after a
// check that never ran, which is the one thing a gate must never do quietly.
//
// The v == nil case (no git at all) still returns false, deliberately: that is a known
// state which genuinely answers the question. Only a failed probe raises.
func TestVcsIsDirtyRaisesWhenTheProbeFails(t *testing.T) {
	chdirOutsideAnyRepo(t)

	_, err := VcsIsDirty(context.Background(), nil)
	require.Error(t, err, "a failed status probe must not read as a clean tree")
	assert.Contains(t, err.Error(), "status")
}

// TestVcsNameNeverRaises: name is the detection half of the pair and must stay answerable
// without a catch - the same split as os.env and os.lookupEnv, and what lets the accessors
// above afford to raise.
//
// It reports the resolved DRIVER, so in this bare temp dir it is still "git": the driver
// resolved, the repository is what is missing. Asking it whether a directory is a checkout
// would be a different question, and this is not it.
func TestVcsNameNeverRaises(t *testing.T) {
	chdirOutsideAnyRepo(t)

	_, err := VcsName(context.Background())
	require.NoError(t, err, "detection must never raise; that is its whole job")
}

// TestResolveVCSDoesNotCacheAnError pins that a failed vcs.Resolve (here: an
// unresolvable explicit VCS name from a misconfigured MAGUS_VCS_NAME) is not
// remembered as "no VCS" for the cwd. resolveVCS keys its cache on cwd alone, so
// before the fix the first call's error still set vcsCwdKey, and every later call
// for the same cwd short-circuited to the stale (nil, "") even after whatever
// caused the failure was gone.
func TestResolveVCSDoesNotCacheAnError(t *testing.T) {
	chdirOutsideAnyRepo(t)
	ctx := context.Background()

	t.Setenv("MAGUS_VCS_NAME", "totally-bogus-vcs-name")
	v, base := resolveVCS(ctx)
	require.Nil(t, v, "an unknown explicit VCS name must not resolve a driver")
	assert.Empty(t, base)

	// Clear the bad name; the SAME cwd must resolve normally now (git, per
	// TestVcsNameNeverRaises' note that resolution succeeds even in a bare temp
	// dir). Before the fix this returned the poisoned cached (nil, "") forever.
	t.Setenv("MAGUS_VCS_NAME", "")
	v, _ = resolveVCS(ctx)
	assert.NotNil(t, v, "a transient resolve error must not be cached as \"no VCS\" for this cwd")
}

func TestOsWhichRaisesForAMissingCommand(t *testing.T) {
	ctx := context.Background()

	t.Run("resolves a real command", func(t *testing.T) {
		got, err := OsWhich(ctx, "sh")
		require.NoError(t, err)
		assert.NotEmpty(t, got)
	})

	t.Run("raises for a missing one", func(t *testing.T) {
		// Previously "" - so a caller that skipped the equality check passed the empty
		// string straight into an exec or a path join.
		got, err := OsWhich(ctx, "definitely-no-such-cmd-zzz")
		require.Error(t, err)
		assert.Empty(t, got)
		assert.Contains(t, err.Error(), "not on PATH")
	})
}

func TestFsExistsDistinguishesAbsenceFromDenial(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	t.Run("reports a real file", func(t *testing.T) {
		p := dir + "/present.txt"
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))
		got, err := FsExists(ctx, p)
		require.NoError(t, err)
		assert.True(t, got)
	})

	t.Run("absence is false, not an error", func(t *testing.T) {
		// The distinction the sandbox change turns on: genuinely absent still answers
		// the question, so it stays a plain false. Only a DENIED path raises, because
		// then the question was never answered at all.
		got, err := FsExists(ctx, dir+"/definitely-absent.txt")
		require.NoError(t, err)
		assert.False(t, got)
	})
}

// statusPaths is the whole reason vcs.status can hand back paths rather than the
// porcelain vcs.dirty_files used to leak: each backend prefixes its status lines
// differently, and a magusfile should not have to know which one it is on.
func TestStatusPathsStripsEachBackendsPrefix(t *testing.T) {
	for name, tc := range map[string]struct {
		vcs   string
		lines []string
		want  []string
	}{
		"git porcelain has two status columns": {
			vcs:   "git",
			lines: []string{" M docs/conventions.md", "?? new.txt", "A  added.go"},
			want:  []string{"docs/conventions.md", "new.txt", "added.go"},
		},
		"a git rename names both, and the new path is the one that exists": {
			vcs:   "git",
			lines: []string{"R  old/name.md -> new/name.md"},
			want:  []string{"new/name.md"},
		},
		"hg has one status column": {
			vcs:   "hg",
			lines: []string{"M docs/conventions.md", "? new.txt"},
			want:  []string{"docs/conventions.md", "new.txt"},
		},
		"jj reports no status at all, so the line IS the path": {
			vcs:   "jj",
			lines: []string{"docs/conventions.md", "new.txt"},
			want:  []string{"docs/conventions.md", "new.txt"},
		},
		"a clean tree has nothing to strip": {vcs: "git", lines: nil, want: []string{}},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, statusPaths(tc.vcs, tc.lines))
		})
	}
}
