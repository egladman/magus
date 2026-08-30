package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/interp/bindings"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteMagusfileStub(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, writeMagusfileStub(dir))
	data, err := os.ReadFile(filepath.Join(dir, "magusfile.buzz"))
	require.NoError(t, err, "expected magusfile.buzz")
	body := string(data)
	for _, want := range []string{
		`import "magus"`,
		`magus\project`,
		`export fun preflight`,
		`export fun test`,
	} {
		assert.Contains(t, body, want, "magusfile.buzz missing %q", want)
	}
}

// TestStarterMagusfileNoRemovedAPI guards P3-23/P3-24: starterMagusfileBuzz is the
// canonical example magusfile referenced from the docs (see its doc comment in
// init.go), so a removed spelling here doesn't just fail to load - it teaches new
// users something MGS1025 will reject. RemovedAPINames comes from
// removedMagusfileAPI in internal/interp/runtime.go, the same table MGS1025 checks
// against, so this list can't drift out of sync with what actually fires the
// diagnostic. The scan covers comments too, since a real magusfile parse would
// ignore a removed name inside one (see MGS1025.md's Detection section) but the
// starter's comments are exactly what a new user reads.
func TestStarterMagusfileNoRemovedAPI(t *testing.T) {
	for _, name := range interp.RemovedAPINames() {
		removed := "magus." + name
		assert.NotContains(t, starterMagusfileBuzz, removed,
			"starter magusfile.buzz uses removed API %q (see MGS1025)", removed)
	}
}

// loadStarterEmbedded runs src through the same embedded-mode session the magusfile
// engine uses (the surface `magus buzz --embedded` drives): parse, check, and run the
// top level. It returns the diagnostic Exec raises, if any. This is the real loader,
// not a string scan - it is what surfaces a checker diagnostic like BZZ1006 that a
// parse (buzz.ParseEmbedded) and a grep both miss.
func loadStarterEmbedded(ctx context.Context, src string) error {
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer func() { _ = sess.Close() }()
	bindings.RegisterModuleSurface(ctx, sess, bindings.WithScriptOutput(io.Discard))
	bindings.RegisterMagusNamespace(ctx, sess)
	bindings.RegisterSpellSourceModules(sess)
	return sess.Exec(ctx, src)
}

// TestStarterMagusfileChecksClean is the enforcement point the string scan in
// TestStarterMagusfileNoRemovedAPI cannot be: it LOADS and CHECKS the embedded starter
// through the magusfile engine and requires zero error-level diagnostics, so the
// scaffold `magus init` writes is one that `magus run build` - the exact next command
// init suggests - can load. A BZZ1006 (a proc\exec under `> void` missing `!> any`)
// shipped once precisely because the scan never compiled the template. Unused-import
// warnings (BZZ3001) are tolerated here; the hard requirement is no error diagnostic.
func TestStarterMagusfileChecksClean(t *testing.T) {
	require.NoError(t, loadStarterEmbedded(context.Background(), starterMagusfileBuzz),
		"embedded starter magusfile must check clean under the magusfile engine")
}

// TestStarterMagusfileCheckCanFail proves the check above can actually fail: strip the
// build target's `!> any` and the same loader raises BZZ1006. A regression test that
// cannot fail is the failure mode that let the original bug through, so this guards the
// guard - and, like the test above, it also stays red until the template carries the fix.
func TestStarterMagusfileCheckCanFail(t *testing.T) {
	broken := strings.Replace(starterMagusfileBuzz, "!> any", "", 1)
	require.NotEqual(t, starterMagusfileBuzz, broken, "starter build target must declare !> any")
	err := loadStarterEmbedded(context.Background(), broken)
	require.Error(t, err, "removing !> any must raise a diagnostic")
	assert.Contains(t, err.Error(), "BZZ1006")
}

func TestInitSpellCmd(t *testing.T) {
	t.Run("scaffolds a parseable, contract-complete spell", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, initSpellCmd(context.Background(), []string{"--dir", dir, "acme"}))

		data, err := os.ReadFile(filepath.Join(dir, "acme", "spell.buzz"))
		require.NoError(t, err, "expected spells/acme/spell.buzz")
		body := string(data)

		// The generated spell must parse (embedded mode, as the engine loads it).
		_, perr := buzz.ParseEmbedded(body)
		require.NoError(t, perr, "scaffolded spell must parse")

		for _, want := range []string{
			`export fun mgs_getName() > str { return "acme"; }`,
			"mgs_listRequiredGlobs",
			"mgs_listTargets",
			`import "magus/spell"`,
			`import "magus/charm"`,
			`test "build op forks the expected command"`,
		} {
			assert.Contains(t, body, want, "scaffold missing %q", want)
		}
	})

	t.Run("rejects an invalid handle", func(t *testing.T) {
		dir := t.TempDir()
		err := initSpellCmd(context.Background(), []string{"--dir", dir, "1bad name"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a valid handle")
	})

	t.Run("refuses to overwrite without --force", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, initSpellCmd(context.Background(), []string{"--dir", dir, "acme"}))
		err := initSpellCmd(context.Background(), []string{"--dir", dir, "acme"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
		require.NoError(t, initSpellCmd(context.Background(), []string{"--dir", dir, "--force", "acme"}))
	})
}

func TestMagusfilePresent(t *testing.T) {
	for _, name := range []string{"magusfile.buzz"} {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644))
		assert.True(t, magusfilePresent(dir), "magusfilePresent should detect %s", name)
	}
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "magusfiles"), 0o755))
	assert.True(t, magusfilePresent(dir), "magusfilePresent should detect magusfiles/ directory")
	assert.False(t, magusfilePresent(t.TempDir()), "magusfilePresent should be false for an empty directory")
}

// An existing magusfile must not be clobbered by a stub write.
func TestWriteMagusfileStubSkipsExisting(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(existing, []byte("// mine\n"), 0o644))
	require.NoError(t, writeMagusfileStub(dir))
	data, _ := os.ReadFile(existing)
	assert.Equal(t, "// mine\n", string(data), "existing magusfile.buzz was modified")
}
