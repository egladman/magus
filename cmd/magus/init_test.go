package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	buzz "github.com/egladman/gopherbuzz"
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
		"magus.project",
		`export fun preflight`,
		`export fun test`,
	} {
		assert.Contains(t, body, want, "magusfile.buzz missing %q", want)
	}
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
			`import "magus/target"`,
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
