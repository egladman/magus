package memory

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRepoIdentityWorktree proves every worktree of one repo resolves to the same
// identity, so they share one memory directory - the reason the key is not the checkout
// path.
func TestRepoIdentityWorktree(t *testing.T) {
	main := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(main, ".git"), 0o755))

	wt := t.TempDir()
	gitfile := "gitdir: " + filepath.Join(main, ".git", "worktrees", "feature-x") + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(wt, ".git"), []byte(gitfile), 0o644))

	assert.Equal(t, main, repoIdentity(main), "a plain checkout identifies as itself")
	assert.Equal(t, main, repoIdentity(wt), "a linked worktree identifies as the main repo")
	bare := t.TempDir()
	assert.Equal(t, bare, repoIdentity(bare), "no .git: the root is the identity")
}

func TestDirIsOutsideRepoAndStable(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	root := t.TempDir()

	dir, err := Dir(root)
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(dir))
	assert.Contains(t, dir, filepath.Join(state, "magus", "memory"))
	assert.NotContains(t, dir, root, "memory must not live under the repo")
	assert.Contains(t, filepath.Base(dir), filepath.Base(root)+"-", "dir name leads with the repo basename")

	again, err := Dir(root)
	require.NoError(t, err)
	assert.Equal(t, dir, again, "the key is deterministic")
}

// testRoot isolates the store: XDG_STATE_HOME points at a temp dir, and root is an
// empty temp dir (no .git, so repoIdentity is root itself).
func testRoot(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	return t.TempDir()
}

func TestPutGetRoundTrip(t *testing.T) {
	root := testRoot(t)
	in := Record{
		Name:       "hasher-sha256-over-blake3",
		Type:       TypeDecision,
		Status:     "accepted",
		Refs:       []Ref{{Kind: RefKindNode, Target: "file:internal/hash/hasher.go"}, {Kind: RefKindOutput, Target: "ref9f3a2c1b"}},
		References: []string{"cache-key-derivation"},
		Body:       "Keep stdlib SHA256. BLAKE3 ~3.3x slower on arm64 and hashing is off the hot path.",
	}
	stored, err := Put(root, in)
	require.NoError(t, err)
	assert.NotZero(t, stored.Created)
	assert.GreaterOrEqual(t, stored.Updated, stored.Created)

	got, err := Get(root, in.Name)
	require.NoError(t, err)
	assert.Equal(t, stored, got) // whole-struct: frontmatter + body + timestamps survive the round trip
}

func TestPutPreservesCreatedOnUpdate(t *testing.T) {
	root := testRoot(t)
	rec := Record{Name: "cache-op-surface", Type: TypePointer, Refs: []Ref{{Kind: RefKindQuery, Target: "kind:op depends cache"}}}
	first, err := Put(root, rec)
	require.NoError(t, err)

	rec.Refs = []Ref{{Kind: RefKindQuery, Target: "kind:op depends hasher"}}
	second, err := Put(root, rec)
	require.NoError(t, err)
	assert.Equal(t, first.Created, second.Created, "created time is preserved across an update")
	assert.GreaterOrEqual(t, second.Updated, first.Updated)
}

func TestListIsNameOrdered(t *testing.T) {
	root := testRoot(t)
	for _, n := range []string{"zebra", "alpha", "mid"} {
		_, err := Put(root, Record{Name: n, Type: TypePointer, Refs: []Ref{{Kind: RefKindNode, Target: "project:magus"}}})
		require.NoError(t, err)
	}
	got, err := List(root)
	require.NoError(t, err)
	var names []string
	for _, r := range got {
		names = append(names, r.Name)
	}
	assert.Equal(t, []string{"alpha", "mid", "zebra"}, names)
}

func TestListEmptyStore(t *testing.T) {
	got, err := List(testRoot(t))
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestValidateRejections(t *testing.T) {
	cases := map[string]Record{
		"bad name":       {Name: "Not Kebab", Type: TypePointer, Refs: []Ref{{Kind: RefKindNode, Target: "project:magus"}}},
		"unknown type":   {Name: "x", Type: "observation", Refs: []Ref{{Kind: RefKindNode, Target: "project:magus"}}},
		"no refs":        {Name: "x", Type: TypePointer},
		"unknown kind":   {Name: "x", Type: TypePointer, Refs: []Ref{{Kind: "fact", Target: "t"}}},
		"empty target":   {Name: "x", Type: TypePointer, Refs: []Ref{{Kind: RefKindNode, Target: "  "}}},
		"pointer w/prose": {Name: "x", Type: TypePointer, Refs: []Ref{{Kind: RefKindNode, Target: "project:magus"}}, Body: "not allowed"},
	}
	root := testRoot(t)
	for name, rec := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Error(t, Validate(rec))
			_, err := Put(root, rec)
			assert.Error(t, err, "Put must reject an invalid record")
		})
	}
}

func TestDeleteAllowMissing(t *testing.T) {
	root := testRoot(t)
	assert.NoError(t, Delete(root, "ghost", true), "idempotent delete of an absent record is a no-op")
	assert.Error(t, Delete(root, "ghost", false), "strict delete of an absent record errors")

	_, err := Put(root, Record{Name: "real", Type: TypePointer, Refs: []Ref{{Kind: RefKindNode, Target: "project:magus"}}})
	require.NoError(t, err)
	require.NoError(t, Delete(root, "real", false))
	_, err = Get(root, "real")
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestCursorRoundTrip(t *testing.T) {
	root := testRoot(t)
	got, err := ReadCursor(root)
	require.NoError(t, err)
	assert.Empty(t, got, "an unwritten cursor reads empty, not an error")

	require.NoError(t, WriteCursor(root, "left off wiring the @memory shard"))
	got, err = ReadCursor(root)
	require.NoError(t, err)
	assert.Equal(t, "left off wiring the @memory shard", got)
}

