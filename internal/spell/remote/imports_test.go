package remote

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/oci"
	"github.com/egladman/magus/types"
)

// pushTag publishes dir to the fixture's team/spells/x under tag and returns the
// manifest digest the tag now names.
func pushTag(t *testing.T, reg *registry, dir, tag string) digest.Digest {
	t.Helper()
	content, err := Build(t.Context(), dir, allTracked{}, Provenance{Title: "x"})
	require.NoError(t, err)
	d, err := (&oci.Client{HTTP: reg.srv.Client()}).Push(t.Context(), oci.Reference{Registry: reg.host(), Repository: "team/spells/x", Tag: tag}, content)
	require.NoError(t, err)
	return d
}

func fixtureOptions(reg *registry, cacheRoot string) LoadOptions {
	return LoadOptions{
		CacheRoot: cacheRoot,
		Client: func(context.Context, string) (*oci.Client, error) {
			return &oci.Client{HTTP: reg.srv.Client()}, nil
		},
	}
}

func declare(path, tag string) config.SpellsConfig {
	return config.SpellsConfig{Imports: map[string]config.SpellImport{path: {Tag: tag}}}
}

// The update charm's path end to end: Relock resolves the tag once, the lock records
// it, and a frozen load serves those bytes laid out by import path, even after the tag
// moves and with the registry gone.
func TestRelockThenLoadFrozen(t *testing.T) {
	reg := newRegistry(t)
	src := spellDir(t)
	first := pushTag(t, reg, src, "v1")
	path := reg.host() + "/team/spells/x"
	cfg := declare(path, "v1")
	root, cache := t.TempDir(), t.TempDir()
	opts := fixtureOptions(reg, cache)

	require.NoError(t, UpdateLock(t.Context(), root, func(Lock) (Lock, error) { return Relock(t.Context(), cfg, opts) }))
	lock, err := ReadLock(root)
	require.NoError(t, err)
	assert.Equal(t, Lock{Version: lockVersion, Spells: map[string]LockEntry{path: {Tag: "v1", Digest: first}}}, lock)

	// The publisher moves v1. Nothing but another Relock may notice.
	moved := spellDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(moved, entryFile), []byte("export fun mgs_getName() > str { return \"y\"; }\n"), 0o644))
	require.NotEqual(t, first, pushTag(t, reg, moved, "v1"))
	reg.srv.Close()

	im, err := LoadImports(t.Context(), root, cfg, opts)
	require.NoError(t, err)
	require.NoError(t, im.Err())
	dir, err := im.Dir(path)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(im.View(), filepath.FromSlash(path)), dir, "the view is laid out by import path")
	want, err := dirFiles(src)
	require.NoError(t, err)
	got, err := dirFiles(dir)
	require.NoError(t, err)
	assert.Equal(t, want, got, "the locked bytes, not what the tag names now")
}

// A stale pin is held against its path, not the load: the target that repairs the lock
// has to load first, so only an import of that spell fails.
func TestLoadImportsHoldsAStaleLockAgainstThePath(t *testing.T) {
	t.Parallel()
	const lint = "ghcr.io/team/spells/lint"
	root := t.TempDir()
	cfg := declare(lint, "1.4")

	im, err := LoadImports(t.Context(), root, cfg, LoadOptions{CacheRoot: t.TempDir()})
	require.NoError(t, err)
	_, err = im.Dir(lint)
	require.ErrorIs(t, err, types.RemoteSpellLockStale)
	require.ErrorContains(t, err, "pins no digest")
	require.ErrorIs(t, im.Err(), types.RemoteSpellLockStale)

	lock := Lock{Version: lockVersion, Spells: map[string]LockEntry{
		lint: {Tag: "1.3", Digest: digest.FromBytes([]byte("m"))},
	}}
	raw, err := lock.Marshal()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, LockFile), raw, 0o644))
	im, err = LoadImports(t.Context(), root, cfg, LoadOptions{CacheRoot: t.TempDir()})
	require.NoError(t, err)
	_, err = im.Dir(lint)
	require.ErrorIs(t, err, types.RemoteSpellLockStale)
	require.ErrorContains(t, err, `magus.yaml tracks tag "1.4" but magus.lock was written for "1.3"`)
	assert.Empty(t, im.View(), "nothing was served")
}

// Offline with nothing cached, the locked digest cannot be served: MGS1042's sibling
// failure is held against the path the same way.
func TestLoadImportsHoldsAnUnservedPinAgainstThePath(t *testing.T) {
	t.Setenv("MAGUS_OFFLINE", "1")
	const lint = "ghcr.io/team/spells/lint"
	root := t.TempDir()
	lock := Lock{Version: lockVersion, Spells: map[string]LockEntry{lint: {Tag: "1.4", Digest: digest.FromBytes([]byte("m"))}}}
	raw, err := lock.Marshal()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, LockFile), raw, 0o644))

	im, err := LoadImports(t.Context(), root, declare(lint, "1.4"), LoadOptions{CacheRoot: t.TempDir()})
	require.NoError(t, err)
	_, err = im.Dir(lint)
	require.ErrorContains(t, err, "is not cached and MAGUS_OFFLINE is set")
}

func TestLoadImportsOverrides(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	goDir := filepath.Join(root, "spells", "go")
	require.NoError(t, os.MkdirAll(goDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(goDir, entryFile), []byte("export fun mgs_getName() > str { return \"go\"; }\n"), 0o644))
	embedded := func(name string) bool { return name == "go" }
	cfg := config.SpellsConfig{Imports: map[string]config.SpellImport{
		"magus/spell/go":           {Path: "spells/go"},
		"ghcr.io/team/spells/lint": {Path: "spells/go"},
	}}

	im, err := LoadImports(t.Context(), root, cfg, LoadOptions{Embedded: embedded})
	require.NoError(t, err)
	dir, ok := im.Override("magus/spell/go")
	assert.True(t, ok)
	assert.Equal(t, goDir, dir)
	dir, err = im.Dir("ghcr.io/team/spells/lint")
	require.NoError(t, err)
	assert.Equal(t, goDir, dir, "a remote spell replaced by a workspace copy never reaches a registry")
	assert.Empty(t, im.View(), "nothing remote is declared")

	for name, cfg := range map[string]config.SpellsConfig{
		"no spell there":  {Imports: map[string]config.SpellImport{"magus/spell/go": {Path: "vendor/go"}}},
		"no such builtin": {Imports: map[string]config.SpellImport{"magus/spell/golang": {Path: "spells/go"}}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := LoadImports(t.Context(), root, cfg, LoadOptions{Embedded: embedded})
			require.ErrorIs(t, err, types.SpellOverrideInvalid)
		})
	}
}

// A workspace directory at a remote path is never read, so it is dead unless an
// override claims it.
func TestLoadImportsRefusesAnUndeclaredShadow(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "ghcr.io", "team", "spells", "lint"), 0o755))
	_, err := LoadImports(t.Context(), root, declare("ghcr.io/team/spells/lint", "1.4"), LoadOptions{CacheRoot: t.TempDir()})
	require.ErrorIs(t, err, types.SpellShadowed)
}

func TestImportsDirRefusesAnUndeclaredPath(t *testing.T) {
	t.Parallel()
	var none *Imports
	_, err := none.Dir("ghcr.io/team/spells/lint")
	require.ErrorIs(t, err, types.RemoteSpellUndeclared)
	require.ErrorContains(t, err, "spells: {ghcr.io/team/spells/lint: {tag: <tag>}}")
	assert.Empty(t, none.View())
	_, ok := none.Override("magus/spell/go")
	assert.False(t, ok)
}

func TestUnlocked(t *testing.T) {
	t.Parallel()
	d := digest.FromBytes([]byte("m"))
	lock := Lock{Version: lockVersion, Spells: map[string]LockEntry{
		"ghcr.io/team/spells/lint": {Tag: "1", Digest: d},
		"ghcr.io/team/spells/fmt":  {Tag: "1", Digest: d},
		"ghcr.io/team/spells/old":  {Tag: "1", Digest: d},
	}}
	cfg := config.SpellsConfig{Imports: map[string]config.SpellImport{
		"ghcr.io/team/spells/lint": {Tag: "1"},
		"ghcr.io/team/spells/fmt":  {Path: "vendor/fmt"},
	}}
	assert.Equal(t, []string{"ghcr.io/team/spells/fmt", "ghcr.io/team/spells/old"}, Unlocked(cfg, lock))
}
