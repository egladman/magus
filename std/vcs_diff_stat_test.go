//go:build !wasm

package std

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// withoutDiffStat is a driver that does not implement types.DiffStater.
type withoutDiffStat struct{ types.VCSDriver }

func (withoutDiffStat) Name() string { return "fake" }

// failingDiffStat is a DiffStater whose count fails.
type failingDiffStat struct{ withoutDiffStat }

func (failingDiffStat) DiffStat(context.Context, string, string) ([]types.FileStat, error) {
	return nil, errors.New("disk on fire")
}

func TestDiffStatRaisesForADriverThatCannotCount(t *testing.T) {
	got, err := diffStat(t.Context(), withoutDiffStat{}, "", "main")
	require.Error(t, err, "an empty list would read as a change that touched nothing")
	assert.Nil(t, got)
	var unsupported *types.VCSUnsupportedError
	require.ErrorAs(t, err, &unsupported)
	assert.Equal(t, &types.VCSUnsupportedError{VCS: "fake", Capability: types.CapDiffStater}, unsupported)
}

func TestDiffStatRaisesWhenTheBackendFails(t *testing.T) {
	got, err := diffStat(t.Context(), failingDiffStat{}, "", "main")
	require.ErrorContains(t, err, "disk on fire")
	assert.Nil(t, got)
}

func TestVcsDiffStatRaisesOutsideACheckout(t *testing.T) {
	chdirOutsideAnyRepo(t)

	got, err := VcsDiffStat(t.Context(), "", "")
	require.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "vcs.diffStat")
}

// A feature branch past main: the count starts at the merge base, takes the base from the
// resolved default when none is passed, and does not read the uncommitted edit.
func TestVcsDiffStatCountsTheBranchPastItsMergeBase(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	for _, kv := range [][2]string{
		{"GIT_AUTHOR_NAME", "t"}, {"GIT_AUTHOR_EMAIL", "t@t"},
		{"GIT_COMMITTER_NAME", "t"}, {"GIT_COMMITTER_EMAIL", "t@t"},
		{"GIT_CONFIG_GLOBAL", os.DevNull}, {"GIT_CONFIG_SYSTEM", os.DevNull},
	} {
		t.Setenv(kv[0], kv[1])
	}
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	write := func(name, body string) {
		t.Helper()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
	git("init", "-q", "-b", "main")
	write("keep.txt", "k\n")
	write("edit.txt", "1\n2\n")
	git("add", ".")
	git("commit", "-qm", "root")
	git("switch", "-qc", "feature")
	write("edit.txt", "1\n2\n3\n")
	write("docs/new.md", "n\n")
	git("add", ".")
	git("commit", "-qm", "feature")
	git("switch", "-q", "main")
	write("main-only.txt", "m\n")
	git("add", ".")
	git("commit", "-qm", "main moves on")
	git("switch", "-q", "feature")
	write("edit.txt", "uncommitted\n")

	got, err := VcsDiffStat(WithCwd(t.Context(), dir), "main", "")
	require.NoError(t, err)
	assert.Equal(t, []types.FileStat{
		{Path: "docs/new.md", Added: 1},
		{Path: "edit.txt", Added: 1},
	}, got)

	again, err := VcsDiffStat(WithCwd(t.Context(), dir), "main", dir)
	require.NoError(t, err)
	assert.Equal(t, got, again, "dir names the repository the same way the cwd does")

	_, err = VcsDiffStat(WithCwd(t.Context(), dir), "no-such-branch", "")
	assert.Error(t, err)
}
