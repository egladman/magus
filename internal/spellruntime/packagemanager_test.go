package spellruntime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pmDir builds a temp project dir with the named files (content is irrelevant
// for lockfiles; package.json gets real content when provided).
func pmDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}
	return dir
}

func TestResolvePackageManager_DetectionOrder(t *testing.T) {
	// All three determinants present: the explicit option beats the manifest
	// beats the lockfile beats the recorded default.
	dir := pmDir(t, map[string]string{
		"package.json": `{"packageManager": "yarn@4.1.0"}`,
		"bun.lock":     "",
	})
	assert.Equal(t, "npm", ResolvePackageManager("npm", dir, "pnpm"), "explicit option wins")
	assert.Equal(t, "yarn", ResolvePackageManager("", dir, "pnpm"), "manifest beats lockfile")

	noManifest := pmDir(t, map[string]string{"bun.lock": ""})
	assert.Equal(t, "bun", ResolvePackageManager("", noManifest, "pnpm"), "lockfile beats default")

	empty := pmDir(t, nil)
	assert.Equal(t, "pnpm", ResolvePackageManager("", empty, "pnpm"), "default when nothing detects")
}

func TestResolvePackageManager_LockfileMappings(t *testing.T) {
	cases := map[string]string{
		"pnpm-lock.yaml":      "pnpm",
		"yarn.lock":           "yarn",
		"bun.lock":            "bun",
		"bun.lockb":           "bun",
		"package-lock.json":   "npm",
		"npm-shrinkwrap.json": "npm",
	}
	for file, want := range cases {
		dir := pmDir(t, map[string]string{file: ""})
		assert.Equalf(t, want, ResolvePackageManager("", dir, "pnpm"), "lockfile %s", file)
	}
}

// Lockfile precedence is check order, first hit wins: pnpm-lock.yaml before
// yarn.lock before bun before npm.
func TestResolvePackageManager_LockfilePrecedence(t *testing.T) {
	dir := pmDir(t, map[string]string{
		"yarn.lock":         "",
		"package-lock.json": "",
	})
	assert.Equal(t, "yarn", ResolvePackageManager("", dir, "pnpm"))
}

// A packageManager field naming something outside the known set (or malformed
// JSON) falls through to the lockfile rather than being trusted or failing.
func TestResolvePackageManager_ManifestIgnoredWhenUnknown(t *testing.T) {
	dir := pmDir(t, map[string]string{
		"package.json": `{"packageManager": "corepack@0.20.0"}`,
		"yarn.lock":    "",
	})
	assert.Equal(t, "yarn", ResolvePackageManager("", dir, "pnpm"))

	malformed := pmDir(t, map[string]string{
		"package.json": `{not json`,
		"yarn.lock":    "",
	})
	assert.Equal(t, "yarn", ResolvePackageManager("", malformed, "pnpm"))
}

func TestSubstitutePackageManager(t *testing.T) {
	t.Run("same manager is untouched", func(t *testing.T) {
		bin, args := SubstitutePackageManager("pnpm", []string{"exec", "tsc"}, "pnpm")
		assert.Equal(t, "pnpm", bin)
		assert.Equal(t, []string{"exec", "tsc"}, args)
	})
	t.Run("bin swaps, exec verb kept for npm/yarn", func(t *testing.T) {
		bin, args := SubstitutePackageManager("pnpm", []string{"exec", "eslint", "."}, "yarn")
		assert.Equal(t, "yarn", bin)
		assert.Equal(t, []string{"exec", "eslint", "."}, args)
	})
	t.Run("bun rewrites exec to x", func(t *testing.T) {
		base := []string{"exec", "tsc", "--noEmit"}
		bin, args := SubstitutePackageManager("pnpm", base, "bun")
		assert.Equal(t, "bun", bin)
		assert.Equal(t, []string{"x", "tsc", "--noEmit"}, args)
		assert.Equal(t, []string{"exec", "tsc", "--noEmit"}, base, "input argv must not be mutated")
	})
	t.Run("bun leaves non-exec verbs alone", func(t *testing.T) {
		bin, args := SubstitutePackageManager("pnpm", []string{"run", "dev"}, "bun")
		assert.Equal(t, "bun", bin)
		assert.Equal(t, []string{"run", "dev"}, args)
	})
}

// KnownPackageManager is the opt-in gate on the recorded bin: python's uv, the
// scip op's sh, and friends must never be substituted even for a spell that
// declares a package-manager bin.
func TestKnownPackageManager(t *testing.T) {
	for _, pm := range PackageManagers {
		assert.Truef(t, KnownPackageManager(pm), "%s", pm)
	}
	for _, bin := range []string{"uv", "sh", "node", "cargo", ""} {
		assert.Falsef(t, KnownPackageManager(bin), "%s", bin)
	}
}
