package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// fakeCache lays out one file per size, in bytes, across a few subdirectories.
func fakeCache(t *testing.T, sizes ...int) string {
	t.Helper()
	dir := t.TempDir()
	for i, n := range sizes {
		sub := filepath.Join(dir, strconv.Itoa(i%4))
		require.NoError(t, os.MkdirAll(sub, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(sub, "f"+strconv.Itoa(i)), make([]byte, n), 0o644))
	}
	return dir
}

func buildCache(dir string) []goCache {
	return []goCache{{label: "go build cache", dir: dir, fix: goBuildCacheFix}}
}

func TestGoCacheBudgetWithinBudget(t *testing.T) {
	got := goCacheBudget(context.Background(), buildCache(fakeCache(t, 1<<20, 1<<20)), 4, 1000)
	assert.Equal(t, types.CheckOK, got.Status)
	assert.Empty(t, got.Details)
}

func TestGoCacheBudgetOverBudgetNamesTheFixAndKeepsTheFiles(t *testing.T) {
	dir := fakeCache(t, 2<<20, 2<<20, 2<<20)
	got := goCacheBudget(context.Background(), buildCache(dir), 3, 1000)
	assert.Equal(t, types.CheckAdvice, got.Status)
	require.Len(t, got.Details, 1)
	assert.Contains(t, got.Details[0], "past the 3 MB budget")
	assert.Contains(t, got.Details[0], "reclaim\\trimGoCache()", "the fix trims by entry age; it never empties the cache")
	assert.Contains(t, got.Details[0], dir)

	var files int
	require.NoError(t, filepath.WalkDir(dir, func(_ string, d os.DirEntry, _ error) error {
		if !d.IsDir() {
			files++
		}
		return nil
	}))
	assert.Equal(t, 3, files, "doctor deletes nothing")
}

func TestGoCacheBudgetStopsAtTheEntryCap(t *testing.T) {
	dir := fakeCache(t, 10, 10, 10, 10, 10, 10)
	got := goCacheBudget(context.Background(), []goCache{{label: "golangci-lint cache", dir: dir, fix: lintCacheFix}}, 1, 3)
	assert.Equal(t, types.CheckAdvice, got.Status)
	require.Len(t, got.Details, 1)
	assert.Contains(t, got.Details[0], "stopped after 3 entries")
	assert.Contains(t, got.Details[0], "unmeasured")
}

func TestGoCacheBudgetMissingDirIsEmpty(t *testing.T) {
	got := goCacheBudget(context.Background(), buildCache(filepath.Join(t.TempDir(), "absent")), 1, 10)
	assert.Equal(t, types.CheckOK, got.Status)
}

func TestCheckGoCacheBudgetZeroTurnsItOff(t *testing.T) {
	r := &runner{ctx: context.Background()}
	got := r.checkGoCacheBudget()
	assert.Equal(t, types.CheckOK, got.Status)
	assert.Contains(t, got.Message, "go.cache_budget_mb is 0")
}

func TestLintCacheUnscoped(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GOLANGCI_LINT_CACHE", filepath.Join(root, ".magus", "golangci-lint"))
	assert.Equal(t, "", lintCacheUnscoped(root))

	t.Setenv("GOLANGCI_LINT_CACHE", "/shared/lint")
	want := fmt.Sprintf("[MGS2014] the golangci-lint cache is outside the checkout %s (GOLANGCI_LINT_CACHE=/shared/lint), "+
		"so results from other checkouts can answer for this one's; `magus run golangci-lint <project>` keeps it in %s",
		root, filepath.Join(root, ".magus", "golangci-lint"))
	assert.Equal(t, want, lintCacheUnscoped(root))

	got := (&runner{ctx: context.Background(), root: root}).checkGoCacheBudget()
	assert.Equal(t, types.Check{Name: "go-cache-budget", Status: types.CheckAdvice, Message: want, Details: []string{want}}, got)
}

func TestLintCacheDirHonorsItsEnv(t *testing.T) {
	t.Setenv("GOLANGCI_LINT_CACHE", "/elsewhere/lint")
	got, err := lintCacheDir()
	require.NoError(t, err)
	assert.Equal(t, "/elsewhere/lint", got)
}
