package magus

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// The committed load is skipped only when no pending path can change what the root load
// registers, so each shape that can is pinned here.
func TestPendingTouchesLoad(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("import \"magus\";\n"), 0o644))
	sources := []interp.SourceFile{
		{Path: filepath.Join(root, "magusfile.buzz")},
		{Path: filepath.Join(root, "tools", "policy", "guard.buzz")},
	}
	sep := string(filepath.Separator)
	cases := []struct {
		name    string
		pending []string
		want    bool
	}{
		{"a file the load read", []string{filepath.Join(root, "tools", "policy", "guard.buzz")}, true},
		{"the root magusfile", []string{filepath.Join(root, "magusfile.buzz")}, true},
		{"the config that shapes the load", []string{filepath.Join(root, "magus.yaml")}, true},
		{"a new magusfiles source", []string{filepath.Join(root, "magusfiles", "a.buzz")}, true},
		{"an untracked directory holding a source", []string{filepath.Join(root, "tools") + sep}, true},
		{"an untracked magusfiles directory", []string{filepath.Join(root, "magusfiles") + sep}, true},
		{"a spell no load read", []string{filepath.Join(root, "spells", "go", "spell.buzz")}, false},
		{"an untracked directory elsewhere", []string{filepath.Join(root, "scratch") + sep}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, pendingTouchesLoad(root, newPendingSet(tc.pending), sources))
		})
	}
}

// A second Pending of an unchanged scope trusts the recorded answer, which is
// how a hook avoids git status. The recorded file is rewritten with a sentinel
// path under the same fingerprint; only a call that did not ask git can return
// it. An edit, and a commit of that edit, both change the fingerprint, so the
// sentinel is dropped and git answers again.
func TestPendingReusesAStatusWhenNothingChanged(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MAGUS_CACHE_DIR", "")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoErrorf(t, err, "git %v: %s", args, out)
	}
	if out, err := exec.Command("git", "-C", root, "init", "-b", "main").CombinedOutput(); err != nil {
		t.Skipf("git init: %v\n%s", err, out)
	}
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "test")
	file := filepath.Join(root, "policy.buzz")
	require.NoError(t, os.WriteFile(file, []byte("fun judge() > void {}\n"), 0o644))
	git("add", "policy.buzz")
	git("commit", "-q", "-m", "init")

	res, err := vcs.Resolve(t.Context(), root, "", types.VCSOptions{})
	require.NoError(t, err)
	require.NotNil(t, res.VCS)
	h := headPolicy{
		workspace: root,
		driver:    res.VCS,
		repoRoot: func(ctx context.Context) (string, error) {
			return res.VCS.Root(ctx, root)
		},
		batch: &objectBatchState{},
	}
	scope := []string{file}

	got, err := h.Pending(t.Context(), scope)
	require.NoError(t, err)
	assert.Empty(t, got)

	cache, ok := pendingCacheFile(root, scope)
	require.True(t, ok)
	body, err := os.ReadFile(cache)
	require.NoError(t, err)
	fp, _, ok := strings.Cut(string(body), "\n")
	require.True(t, ok)
	sentinel := filepath.Join(root, "sentinel.buzz")
	require.NoError(t, os.WriteFile(cache, []byte(fp+"\n"+sentinel+"\n"), 0o644))

	got, err = h.Pending(t.Context(), scope)
	require.NoError(t, err)
	assert.Equal(t, []string{sentinel}, got, "an unchanged tree must reuse the recorded pending list")

	require.NoError(t, os.WriteFile(file, []byte("fun judge() > void { changed; }\n"), 0o644))
	got, err = h.Pending(t.Context(), scope)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, canonicalPath(file), canonicalPath(got[0]), "an edit must ask git again")

	git("add", "policy.buzz")
	git("commit", "-q", "-m", "edit")
	got, err = h.Pending(t.Context(), scope)
	require.NoError(t, err)
	assert.Empty(t, got, "a commit of that edit is clean, which HEAD has to say because the file bytes did not change again")
}

// gitPolicy is a committed repository at root holding files, and the approval
// authority over it.
func gitPolicy(t *testing.T, root string, files map[string]string) (headPolicy, func(...string)) {
	t.Helper()
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		require.NoErrorf(t, err, "git %v: %s", args, out)
	}
	if out, err := exec.Command("git", "-C", root, "init", "-b", "main").CombinedOutput(); err != nil {
		t.Skipf("git init: %v\n%s", err, out)
	}
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "test")
	for name, body := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(body), 0o644))
		git("add", name)
	}
	git("commit", "-q", "-m", "init")
	res, err := vcs.Resolve(t.Context(), root, "", types.VCSOptions{})
	require.NoError(t, err)
	require.NotNil(t, res.VCS)
	return headPolicy{
		workspace: root,
		driver:    res.VCS,
		repoRoot:  func(ctx context.Context) (string, error) { return res.VCS.Root(ctx, root) },
		batch:     &objectBatchState{},
	}, git
}

// racingDriver runs before and after around each status, standing in for a
// writer that races the hook.
type racingDriver struct {
	types.VCSDriver
	before, after func()
}

func (d racingDriver) DirtyFiles(ctx context.Context, dir string, paths []string) ([]string, error) {
	d.before()
	out, err := d.VCSDriver.DirtyFiles(ctx, dir, paths)
	d.after()
	return out, err
}

func (d racingDriver) CheckoutID(dir string) (string, bool) {
	return d.VCSDriver.(vcs.CheckoutIDReader).CheckoutID(dir)
}

// A status answer is recorded only for the state it saw. Otherwise a file
// swapped to its committed bytes while status runs, or a checkout moved after
// it, files a clean answer under the key of a loosened tree, and the next hook
// reads that tree as approved.
func TestPendingRecordsOnlyTheStateStatusSaw(t *testing.T) {
	t.Setenv("MAGUS_CACHE_DIR", "")
	loosen := func(t *testing.T, file string) {
		require.NoError(t, os.WriteFile(file, []byte("loosened\n"), 0o644))
	}
	cases := []struct {
		name string
		// setup leaves the file loosened; before and after race the status.
		setup         func(t *testing.T, file string, git func(...string))
		before, after func(t *testing.T, file string, git func(...string))
		// restore puts the loosened bytes back once the racing call returns.
		restore bool
	}{
		{
			name:  "a file swapped to its committed bytes while status runs",
			setup: func(t *testing.T, file string, _ func(...string)) { loosen(t, file) },
			before: func(t *testing.T, file string, _ func(...string)) {
				require.NoError(t, os.WriteFile(file, []byte("strict\n"), 0o644))
			},
			after:   func(*testing.T, string, func(...string)) {},
			restore: true,
		},
		{
			name: "the checkout moved after status",
			setup: func(t *testing.T, file string, git func(...string)) {
				loosen(t, file)
				git("commit", "-q", "-am", "loosen")
			},
			before: func(*testing.T, string, func(...string)) {},
			after:  func(_ *testing.T, _ string, git func(...string)) { git("reset", "-q", "--soft", "HEAD~1") },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			h, git := gitPolicy(t, root, map[string]string{"policy.buzz": "strict\n"})
			file := filepath.Join(root, "policy.buzz")
			tc.setup(t, file, git)
			scope := []string{file}
			racing := h
			racing.driver = racingDriver{
				VCSDriver: h.driver,
				before:    func() { tc.before(t, file, git) },
				after:     func() { tc.after(t, file, git) },
			}
			_, err := racing.Pending(t.Context(), scope)
			require.NoError(t, err)
			if tc.restore {
				loosen(t, file)
			}

			got, err := h.Pending(t.Context(), scope)
			require.NoError(t, err)
			require.Len(t, got, 1, "the loosened file differs from HEAD")
			assert.Equal(t, canonicalPath(file), canonicalPath(got[0]))
		})
	}
}

// Every scope keeps its own record, so the scopes a hook asks about in turn
// do not evict each other, and the record lives in the cache dir magus.yaml
// names.
func TestPendingRecordsEachScopeInTheConfiguredCacheDir(t *testing.T) {
	t.Setenv("MAGUS_CACHE_DIR", "")
	root := t.TempDir()
	h, _ := gitPolicy(t, root, map[string]string{
		"magus.yaml": "cache:\n  dir: elsewhere\n",
		"a.buzz":     "a\n",
		"b.buzz":     "b\n",
	})
	scopeA := []string{filepath.Join(root, "a.buzz")}
	scopeB := []string{filepath.Join(root, "b.buzz")}
	for _, scope := range [][]string{scopeA, scopeB} {
		_, err := h.Pending(t.Context(), scope)
		require.NoError(t, err)
	}
	a, ok := pendingCacheFile(root, scopeA)
	require.True(t, ok)
	b, ok := pendingCacheFile(root, scopeB)
	require.True(t, ok)
	assert.NotEqual(t, a, b)
	for _, p := range []string{a, b} {
		assert.FileExists(t, p)
		assert.Equal(t, filepath.Join(root, "elsewhere", "policy-pending"), filepath.Dir(p))
	}
	assert.NoDirExists(t, filepath.Join(root, ".magus"))
}

// A workspace nested in a repository has no marker of its own, and its
// checkout id is read from the repository above it.
func TestCheckoutIDReadsTheRepositoryAboveANestedWorkspace(t *testing.T) {
	root := t.TempDir()
	h, _ := gitPolicy(t, root, map[string]string{"sub/magusfile.buzz": "x\n"})
	top, ok := checkoutID(h.driver, root)
	require.True(t, ok)
	nested, ok := checkoutID(h.driver, filepath.Join(root, "sub"))
	require.True(t, ok)
	assert.Equal(t, top, nested)
}

// Each hashed record is framed with its kind, so a file whose bytes spell out
// the missing marker does not hash like an absent file.
func TestHashPendingTellsContentFromAMissingFile(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "x.buzz")
	absent, ok := hashPending(root, []string{p})
	require.True(t, ok)
	require.NoError(t, os.WriteFile(p, []byte("\x07\x00\x00\x00missing"), 0o644))
	present, ok := hashPending(root, []string{p})
	require.True(t, ok)
	assert.NotEqual(t, absent, present)
}

// unbatchedDriver is a driver with no object batch that counts the reads it
// serves, as jj is.
type unbatchedDriver struct {
	types.VCSDriver
	reads *[]string
}

func (d unbatchedDriver) ReadFileAt(ctx context.Context, root, rev, rel string) (string, error) {
	*d.reads = append(*d.reads, rel)
	return d.VCSDriver.ReadFileAt(ctx, root, rev, rel)
}

// Without a batch every approved read is a process, so a file the whole
// tree's pending answer does not cover is read from disk. A batched authority
// reads every path through the batch, since its pending answer may be scoped.
func TestApprovedReaderReadsCleanFilesFromDiskWithoutABatch(t *testing.T) {
	root := t.TempDir()
	h, _ := gitPolicy(t, root, map[string]string{"clean.buzz": "clean\n", "edited.buzz": "committed\n"})
	clean := filepath.Join(root, "clean.buzz")
	edited := filepath.Join(root, "edited.buzz")
	require.NoError(t, os.WriteFile(edited, []byte("loosened\n"), 0o644))

	var reads []string
	unbatched := h
	unbatched.driver = unbatchedDriver{VCSDriver: h.driver, reads: &reads}
	pending, err := unbatched.Pending(t.Context(), nil)
	require.NoError(t, err)
	read := approvedReader(t.Context(), unbatched, pending)
	got, err := read(clean)
	require.NoError(t, err)
	assert.Equal(t, "clean\n", string(got))
	got, err = read(edited)
	require.NoError(t, err)
	assert.Equal(t, "committed\n", string(got))
	assert.Equal(t, []string{"edited.buzz"}, reads, "only the pending file costs a process")

	assert.True(t, batched(h))
	defer closeApproved(h)
	got, err = approvedReader(t.Context(), h, nil)(edited)
	require.NoError(t, err)
	assert.Equal(t, "committed\n", string(got), "a batched authority does not trust disk")
}

func TestPendingSetCoversFilesUnderAnUntrackedDirectory(t *testing.T) {
	root := t.TempDir()
	set := newPendingSet([]string{filepath.Join(root, "new") + string(filepath.Separator), filepath.Join(root, "a.buzz")})
	assert.True(t, set.covers(filepath.Join(root, "a.buzz")))
	assert.True(t, set.covers(filepath.Join(root, "new", "deep", "b.buzz")))
	assert.False(t, set.covers(filepath.Join(root, "newer.buzz")), "a sibling sharing the prefix is not under it")
	assert.False(t, set.covers(filepath.Join(root, "b.buzz")))
}
