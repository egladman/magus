package remote

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/spell"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// ListShipped names every directory of the embedded spells holding a spell.buzz, under
// the name magus/spell/<name> takes, pinned to the digest a push of it writes.
func TestListShipped(t *testing.T) {
	var dirs []string
	require.NoError(t, fs.WalkDir(spells.Shipped(), ".", func(p string, _ fs.DirEntry, err error) error {
		if dir, ok := strings.CutSuffix(p, "/spell.buzz"); ok && err == nil {
			dirs = append(dirs, dir)
		}
		return err
	}))

	shipped, err := ListShipped()
	require.NoError(t, err)
	var listed, names []string
	for _, s := range shipped {
		listed = append(listed, s.Dir)
		names = append(names, s.Name)
		_, builtIn := spell.Builtins()[s.Name]
		assert.Equal(t, builtIn, s.BuiltIn, s.Name)
		if !builtIn {
			assert.Equal(t, s.Dir, s.Name, "a source-only spell is named by its directory")
		}
		ref, _, err := Shipped(s.Name, s.Dir)
		require.NoError(t, err)
		assert.Equal(t, ref.String(), s.Reference, s.Name)
		assert.Equal(t, ref.Digest, s.Digest, s.Name)
	}
	assert.ElementsMatch(t, dirs, listed, "every shipped spell, once")
	assert.True(t, slices.IsSorted(names), "sorted by name: %v", names)
	assert.Subset(t, names, []string{"go", "endoflife-date", "harness/claude-code", "harness/codex", "harness/cursor",
		"harness/opencode", "aws/s3-cache", "github/actions", "github/review", "gitlab/ci"})
	assert.NotContains(t, names, "golang")
}

func TestShippedDirRefusesANameMagusDoesNotShip(t *testing.T) {
	dir, err := shippedDir("go")
	require.NoError(t, err)
	assert.Equal(t, "golang", dir)

	for _, name := range []string{"nope", "golang", "harness"} {
		_, err := shippedDir(name)
		require.ErrorContains(t, err, fmt.Sprintf("magus ships no spell %q", name))
	}
}

// BuildShipped writes the digest Eject stamps on a copy, and a copy of a spell that
// imports a host module offers no built-in to override.
func TestEjectShippedMatchesBuildShipped(t *testing.T) {
	_, built, err := BuildShipped("go")
	require.NoError(t, err)

	ws, dst := t.TempDir(), "spells/go"
	res, next, err := EjectShipped(ws, "go", filepath.Join(ws, dst))
	require.NoError(t, err)
	assert.Equal(t, built.Digest, res.Digest)
	assert.Equal(t, override{Import: "magus/spell/go", Path: dst}, res.Override, "a copy inside the workspace is declared by its relative path")
	assert.Contains(t, next, "spells:\n  magus/spell/go:\n    path: spells/go\n")

	res, next, err = EjectShipped("", "endoflife-date", t.TempDir())
	require.NoError(t, err)
	assert.Zero(t, res.Override)
	assert.Contains(t, next, "ships as source only")
}

// A checkout of the directory a shipped spell comes from, built with the shipped
// provenance, which carries no revision or creation time, makes the manifest a release
// publishes; BuildDir of it packs the same layer. A copy ejected outside the workspace
// is declared by its absolute path.
func TestABuildOfAShippedCheckoutMatchesBuildShipped(t *testing.T) {
	r := testkit.NewRepository(t, map[string]string{"magus.yaml": ""})
	dir := r.Path("spells", "golang")
	require.NoError(t, os.CopyFS(dir, os.DirFS(filepath.Join("..", "..", "..", "spells", "golang"))))
	r.Git("remote", "add", "origin", "https://github.com/egladman/magus.git")
	r.Commit("the go spell", nil)

	driver, err := resolveVCS(t.Context(), types.VCSOptions{}, dir)
	require.NoError(t, err)
	built, err := Build(t.Context(), dir, driver, ShippedProvenance("golang"))
	require.NoError(t, err)
	m, err := built.Manifest()
	require.NoError(t, err)
	_, shipped, err := BuildShipped("go")
	require.NoError(t, err)
	assert.Equal(t, shipped.Digest, m.Digest)

	_, res, err := BuildDir(t.Context(), types.VCSOptions{}, dir, "")
	require.NoError(t, err)
	assert.Equal(t, digest.FromBytes(built.Blobs[0].Data), res.Layer)

	dst := t.TempDir()
	pulled, _, err := EjectShipped(r.Root(), "go", dst)
	require.NoError(t, err)
	assert.Equal(t, override{Import: "magus/spell/go", Path: dst}, pulled.Override)
}

func TestPullReferenceReadsTheLockForABareImportPath(t *testing.T) {
	reg := newRegistry(t)
	first := pushTag(t, reg, spellDir(t), "v1")
	path := reg.host() + "/team/spells/x"
	root := t.TempDir()
	require.NoError(t, UpdateLock(t.Context(), root, func(Lock) (Lock, error) {
		return Relock(t.Context(), declare(path, "v1"), fixtureOptions(reg, t.TempDir()))
	}))
	writeSpellsYAML(t, root, path, "v1")

	ref, err := PullReference(root, path)
	require.NoError(t, err)
	assert.Equal(t, first, ref.Digest, "a bare import path names the locked digest")

	_, err = PullReference(root, reg.host()+"/team/spells/other")
	require.ErrorContains(t, err, "declares no remote spell by that path")

	writeSpellsYAML(t, root, path, "v2")
	_, err = PullReference(root, path)
	require.ErrorIs(t, err, types.RemoteSpellLockStale, "the lock pins another tag than the one declared")

	_, err = PullReference("", path)
	require.ErrorContains(t, err, "no workspace here")
}
