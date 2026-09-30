package knowledge

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func guardFixture(t *testing.T) (root, cacheDir string, g *Graph) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	for rel, body := range map[string]string{
		"magusfile.buzz": "export fun lint() > void {}\n",
		"pkg/a.go":       "package pkg\nfunc Judge() {}\n",
		"docs/x.md":      "# Title\n",
		"notes.txt":      "not indexed\n",
	} {
		p := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	g = NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "symbol:go pkg/Judge().", Kind: types.KindSymbol, Label: "Judge", Source: "pkg/a.go:2"})
	g.AddNode(types.KnowledgeNode{ID: "symbol:go other/Judge().", Kind: types.KindSymbol, Label: "Judge", Source: "other/j.go:1"})
	g.AddNode(types.KnowledgeNode{ID: "file:pkg/a.go", Kind: types.KindFile})
	g.AddNode(types.KnowledgeNode{ID: "file:pkg/b_test.go", Kind: types.KindFile})
	g.AddEdge(types.KnowledgeEdge{Source: "file:pkg/a.go", Target: "symbol:go pkg/Judge().", Relation: types.RelationDefines, Provenance: "pkg/a.go"})
	g.AddEdge(types.KnowledgeEdge{Source: "file:pkg/b_test.go", Target: "symbol:go pkg/Judge().", Relation: types.RelationReferences, Provenance: "scip count=2 lines=4,9"})
	g.AddEdge(types.KnowledgeEdge{Source: "file:pkg/b_test.go", Target: "symbol:go other/Judge().", Relation: types.RelationReferences, Provenance: "scip count=1 lines=12"})
	g.AddEdge(types.KnowledgeEdge{Source: "file:pkg/a.go", Target: "symbol:go pkg/Judge().", Relation: types.RelationCalls, Provenance: "scip count=1"})
	g.AddNode(types.KnowledgeNode{ID: "docsection:docs/x.md#title", Kind: types.KindDocSection})
	g.AddNode(types.KnowledgeNode{ID: "target:.:lint", Kind: types.KindTarget})
	g.AddNode(types.KnowledgeNode{ID: "diagnostic:MGS1001", Kind: types.KindDiagnostic})
	g.AddNode(types.KnowledgeNode{ID: "spell:go", Kind: types.KindSpell})
	return root, t.TempDir(), g
}

// TestGuardIndexRoundTrip pins what the guard reads back: names for symbols, ids for the
// other kinds, nothing for a kind it never asks about, and every kind fresh right after
// the write.
func TestGuardIndexRoundTrip(t *testing.T) {
	root, cacheDir, g := guardFixture(t)
	require.NoError(t, WriteGuardIndex(cacheDir, root, g, true, GuardCheckout{}))

	x, err := ReadGuardIndex(cacheDir, root)
	require.NoError(t, err)
	assert.True(t, x.Has(GuardSymbol, "Judge"))
	assert.False(t, x.Has(GuardSymbol, "Jud"))
	assert.Equal(t, []string{"docsection:docs/x.md#title"}, x.IDs(types.KindDocSection))
	assert.Equal(t, []string{"target:.:lint"}, x.IDs(types.KindTarget))
	assert.Equal(t, []string{"diagnostic:MGS1001"}, x.IDs(types.KindDiagnostic))
	assert.Equal(t, []string{"file:pkg/a.go", "file:pkg/b_test.go"}, x.IDs(types.KindFile))
	assert.Empty(t, x.IDs(types.KindSpell))
	for _, kind := range []string{GuardSymbol, types.KindDocSection, types.KindTarget, types.KindDiagnostic, types.KindFile} {
		assert.True(t, x.Fresh(kind), kind)
	}

	// Sites are keyed by name and folded per file across every symbol of that name; a
	// definition contributes its declaring line, and a call edge contributes nothing.
	sites, err := x.RefSites("Judge")
	require.NoError(t, err)
	assert.Equal(t, []types.KnowledgeRefSite{
		{File: "pkg/a.go", Count: 1, Lines: []int{2}},
		{File: "pkg/b_test.go", Count: 3, Lines: []int{4, 9, 12}},
	}, sites)
	sites, err = x.RefSites("Jud")
	require.NoError(t, err)
	assert.Empty(t, sites)
}

// The index and its reference sites name the workspace's symbols and files, so both stay
// private to their owner.
func TestGuardIndexFilesAreOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX modes")
	}
	root, cacheDir, g := guardFixture(t)
	require.NoError(t, WriteGuardIndex(cacheDir, root, g, true, GuardCheckout{}))

	for _, p := range []string{GuardIndexPath(cacheDir), guardRefsPath(cacheDir)} {
		fi, err := os.Stat(p)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), p)
	}
}

// TestGuardIndexFreshness pins what makes a kind non-definitive: an edit to a file of its
// class, a file added beside one, and for symbols an index that was already stale when the
// graph was built. A file no kind reads changes nothing.
func TestGuardIndexFreshness(t *testing.T) {
	read := func(t *testing.T, cacheDir, root string) *GuardIndex {
		t.Helper()
		x, err := ReadGuardIndex(cacheDir, root)
		require.NoError(t, err)
		return x
	}
	future := time.Now().Add(time.Hour)

	t.Run("edited source", func(t *testing.T) {
		root, cacheDir, g := guardFixture(t)
		require.NoError(t, WriteGuardIndex(cacheDir, root, g, true, GuardCheckout{}))
		require.NoError(t, os.Chtimes(filepath.Join(root, "pkg/a.go"), future, future))
		x := read(t, cacheDir, root)
		assert.False(t, x.Fresh(GuardSymbol))
		assert.True(t, x.Fresh(types.KindDocSection), "a Go edit says nothing about docs")
		assert.True(t, x.Fresh(types.KindTarget))
		assert.False(t, x.Fresh(types.KindFile), "a file node may come from any indexed source")
		assert.False(t, x.Fresh(types.KindDiagnostic), "a diagnostic's emitters are symbols")
	})
	t.Run("added file", func(t *testing.T) {
		root, cacheDir, g := guardFixture(t)
		require.NoError(t, WriteGuardIndex(cacheDir, root, g, true, GuardCheckout{}))
		require.NoError(t, os.WriteFile(filepath.Join(root, "pkg/b.go"), []byte("package pkg\n"), 0o644))
		assert.False(t, read(t, cacheDir, root).Fresh(GuardSymbol))
	})
	t.Run("unrelated file", func(t *testing.T) {
		root, cacheDir, g := guardFixture(t)
		require.NoError(t, WriteGuardIndex(cacheDir, root, g, true, GuardCheckout{}))
		require.NoError(t, os.WriteFile(filepath.Join(root, "magus"), []byte("binary"), 0o755))
		require.NoError(t, os.Chtimes(filepath.Join(root, "notes.txt"), future, future))
		assert.True(t, read(t, cacheDir, root).Fresh(GuardSymbol))
	})
	t.Run("stale symbol index", func(t *testing.T) {
		root, cacheDir, g := guardFixture(t)
		require.NoError(t, WriteGuardIndex(cacheDir, root, g, false, GuardCheckout{}))
		x := read(t, cacheDir, root)
		assert.False(t, x.Fresh(GuardSymbol))
		assert.True(t, x.Fresh(types.KindDocSection))
	})
}

func TestGuardIndexRefusesAnotherRoot(t *testing.T) {
	root, cacheDir, g := guardFixture(t)
	_, err := ReadGuardIndex(cacheDir, root)
	require.Error(t, err, "no index yet")
	require.NoError(t, WriteGuardIndex(cacheDir, root, g, true, GuardCheckout{}))
	_, err = ReadGuardIndex(cacheDir, t.TempDir())
	assert.Error(t, err)
}

// guardRepo is guardFixture committed to a git repository on main, with a runner for more
// git commands there. Global and system config stay out, so a signing or hook setting on
// the box cannot fail a commit.
func guardRepo(t *testing.T) (root, cacheDir string, g *Graph, git func(args ...string) string) {
	t.Helper()
	root, cacheDir, g = guardFixture(t)
	git = func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_EDITOR=true")
		out, err := cmd.CombinedOutput()
		if args[0] != "rebase" {
			require.NoError(t, err, "git %v: %s", args, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("add", "-A")
	git("commit", "-q", "-m", "one")
	return root, cacheDir, g, git
}

var guardKinds = []string{GuardSymbol, types.KindDocSection, types.KindTarget, types.KindDiagnostic, types.KindFile}

// An index answers only for the revision it was built at: a rewrite that moves HEAD and
// touches no indexed file leaves every stamp holding, and still makes every kind stale.
func TestGuardIndexStaleAtAnotherRevision(t *testing.T) {
	root, cacheDir, g, git := guardRepo(t)
	at := ReadGuardCheckout(t.Context(), root)
	require.Equal(t, git("rev-parse", "HEAD"), at.Revision)
	require.NoError(t, WriteGuardIndex(cacheDir, root, g, true, at))

	x, err := ReadGuardIndex(cacheDir, root)
	require.NoError(t, err)
	assert.Empty(t, x.Stale())
	for _, kind := range guardKinds {
		assert.True(t, x.Fresh(kind), kind)
	}

	git("commit", "-q", "--amend", "-m", "one, reworded")
	x, err = ReadGuardIndex(cacheDir, root)
	require.NoError(t, err)
	assert.Equal(t, "the index was built at "+at.Revision[:12]+" and the checkout is at "+git("rev-parse", "HEAD")[:12], x.Stale())
	for _, kind := range guardKinds {
		assert.False(t, x.Fresh(kind), kind)
	}
	built, err := ReadGuardIndexCheckout(cacheDir, root)
	require.NoError(t, err)
	assert.Equal(t, at, built)
}

// An index built while a rebase is stopped describes a tree the rebase is about to
// change, and says so even though its revision and stamps match the checkout.
func TestGuardIndexStaleDuringARebase(t *testing.T) {
	root, cacheDir, g, git := guardRepo(t)
	git("checkout", "-q", "-b", "topic")
	require.NoError(t, os.WriteFile(filepath.Join(root, "pkg/a.go"), []byte("package pkg\nfunc Topic() {}\n"), 0o644))
	git("commit", "-q", "-am", "topic")
	git("checkout", "-q", "main")
	require.NoError(t, os.WriteFile(filepath.Join(root, "pkg/a.go"), []byte("package pkg\nfunc Main() {}\n"), 0o644))
	git("commit", "-q", "-am", "main")
	git("checkout", "-q", "topic")
	git("rebase", "main")

	at := ReadGuardCheckout(t.Context(), root)
	assert.Equal(t, types.OperationRebase, at.Operation)
	require.NoError(t, WriteGuardIndex(cacheDir, root, g, true, at))
	x, err := ReadGuardIndex(cacheDir, root)
	require.NoError(t, err)
	assert.Equal(t, "a rebase is in progress", x.Stale())
	for _, kind := range guardKinds {
		assert.False(t, x.Fresh(kind), kind)
	}
}

// A rebase stopped on a pick that applied cleanly has no conflict to find, and the next
// pick still moves the tree.
func TestGuardIndexStaleDuringARebaseStoppedOnACleanPick(t *testing.T) {
	root, cacheDir, g, git := guardRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "pkg/a.go"), []byte("package pkg\nfunc Two() {}\n"), 0o644))
	git("commit", "-q", "-am", "two")
	require.NoError(t, WriteGuardIndex(cacheDir, root, g, true, ReadGuardCheckout(t.Context(), root)))
	git("rebase", "--exec", "false", "HEAD~1")

	x, err := ReadGuardIndex(cacheDir, root)
	require.NoError(t, err)
	assert.Equal(t, "a rebase is in progress", x.Stale())
	for _, kind := range guardKinds {
		assert.False(t, x.Fresh(kind), kind)
	}
}

func TestGuardCheckoutStaleAt(t *testing.T) {
	const rev = "0123456789abcdef0123"
	for _, tt := range []struct {
		built, now GuardCheckout
		want       string
	}{
		{GuardCheckout{Revision: rev}, GuardCheckout{Revision: rev}, ""},
		{GuardCheckout{Revision: rev}, GuardCheckout{Revision: "0123456"}, ""},
		{GuardCheckout{}, GuardCheckout{}, ""},
		{GuardCheckout{Revision: rev}, GuardCheckout{Revision: rev, Operation: "rebase"}, "a rebase is in progress"},
		{GuardCheckout{Revision: rev, Operation: "merge"}, GuardCheckout{Revision: rev}, "the index was built during a merge"},
		{GuardCheckout{Revision: rev}, GuardCheckout{Revision: "fedcba"}, "the index was built at 0123456789ab and the checkout is at fedcba"},
		{GuardCheckout{Revision: rev}, GuardCheckout{}, "the index was built at 0123456789ab and the checkout is at no recorded revision"},
		{GuardCheckout{}, GuardCheckout{Revision: rev}, "the index was built at no recorded revision and the checkout is at 0123456789ab"},
	} {
		assert.Equal(t, tt.want, tt.built.StaleAt(tt.now), "%+v at %+v", tt.built, tt.now)
	}
}
