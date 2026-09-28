package deps

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func npm(name, version string) types.KnowledgePackage {
	return types.KnowledgePackage{Manager: "npm", Name: name, Version: version}
}

func TestNodePackagesReadsPnpm(t *testing.T) {
	t.Parallel()
	dir := extractFixture(t, "npm/pnpm.txtar")

	assert.Equal(t, []types.KnowledgePackage{
		npm("@connectrpc/connect", "2.1.2"),
		npm("d3-drag", "3.0.0"),
		npm("highlight.js", "11.11.1"),
		npm("typescript", "5.8.3"),
	}, NodePackages(filepath.Join(dir, "package.json"), filepath.Join(dir, "pnpm-lock.yaml")),
		"the peer suffix is cut, the workspace: sibling and the peer dependency are skipped")

	assert.Equal(t, []types.KnowledgePackage{npm("d3-drag", "3.0.0")},
		NodePackages(filepath.Join(dir, "mono/packages/app/package.json"), filepath.Join(dir, "mono/pnpm-lock.yaml")),
		"a hoisted lock resolves through the importer for the member's directory")

	assert.Nil(t, NodePackages(filepath.Join(dir, "old/package.json"), filepath.Join(dir, "old/pnpm-lock.yaml")),
		"lockfileVersion 6 is not read")
}

func TestNodePackagesReadsPackageLock(t *testing.T) {
	t.Parallel()
	dir := extractFixture(t, "npm/package-lock.txtar")
	lock := filepath.Join(dir, "package-lock.json")

	assert.Equal(t, []types.KnowledgePackage{
		npm("@scope/util", "2.0.1"),
		npm("left-pad", "1.3.0"),
		npm("typescript", "5.8.3"),
	}, NodePackages(filepath.Join(dir, "package.json"), lock),
		"github:, file: and npm: specifiers are skipped, and so is a registry-looking range the lock resolved from git")

	assert.Equal(t, []types.KnowledgePackage{
		npm("@scope/util", "2.0.1"),
		npm("left-pad", "1.2.0"),
	}, NodePackages(filepath.Join(dir, "packages/app/package.json"), lock),
		"a member's own node_modules wins; otherwise the hoisted copy")

	shrinkwrap := filepath.Join(dir, "npm-shrinkwrap.json")
	require.NoError(t, os.Rename(lock, shrinkwrap))
	assert.Equal(t, []types.KnowledgePackage{
		npm("@scope/util", "2.0.1"),
		npm("left-pad", "1.3.0"),
		npm("typescript", "5.8.3"),
	}, NodePackages(filepath.Join(dir, "package.json"), shrinkwrap), "npm-shrinkwrap.json is the same format")
}

func TestNodePackagesReadsPackageLockV1(t *testing.T) {
	t.Parallel()
	dir := extractFixture(t, "npm/package-lock.txtar")
	assert.Equal(t, []types.KnowledgePackage{npm("left-pad", "1.3.0")},
		NodePackages(filepath.Join(dir, "v1/package.json"), filepath.Join(dir, "v1/package-lock.json")),
		"a v1 git dependency's version is its URL, never a pin")
}

func TestNodePackagesReadsYarnBerry(t *testing.T) {
	t.Parallel()
	dir := extractFixture(t, "npm/yarn.txtar")
	assert.Equal(t, []types.KnowledgePackage{
		npm("@scope/util", "2.0.1"),
		npm("left-pad", "1.3.0"),
	}, NodePackages(filepath.Join(dir, "package.json"), filepath.Join(dir, "yarn.lock")),
		"the declared range picks its descriptor out of two releases; the workspace entry is skipped")
}

func TestNodePackagesDoesNotReadYarnClassic(t *testing.T) {
	t.Parallel()
	dir := extractFixture(t, "npm/yarn.txtar")
	assert.Nil(t, NodePackages(filepath.Join(dir, "classic/package.json"), filepath.Join(dir, "classic/yarn.lock")))
}

func TestNodePackagesYieldsNothingWithoutAReadableLock(t *testing.T) {
	t.Parallel()
	manifest := writeFile(t, "package.json", `{"dependencies": {"left-pad": "^1.3.0"}}`)
	for name, lock := range map[string]string{
		"no lockfile":       "",
		"missing lockfile":  filepath.Join(t.TempDir(), "pnpm-lock.yaml"),
		"malformed pnpm":    writeFile(t, "pnpm-lock.yaml", "lockfileVersion: '9.0'\nimporters: [unclosed"),
		"malformed npm":     writeFile(t, "package-lock.json", "{not json"),
		"malformed berry":   writeFile(t, "yarn.lock", "__metadata:\n  version: 8\n\"a@npm:1\": [x"),
		"binary bun.lockb":  writeFile(t, "bun.lockb", "\x00\x01bun"),
		"berry sans header": writeFile(t, "yarn.lock", "\"left-pad@npm:^1.3.0\":\n  version: 1.3.0\n  resolution: \"left-pad@npm:1.3.0\"\n"),
	} {
		assert.Nil(t, NodePackages(manifest, lock), name)
	}
	assert.Nil(t, NodePackages(writeFile(t, "package.json", "{not json"), ""), "malformed manifest")
}

func TestIsRegistrySpec(t *testing.T) {
	t.Parallel()
	for spec, want := range map[string]bool{
		"^1.2.3":                true,
		">=1 <2":                true,
		"latest":                true,
		"catalog:":              true,
		"workspace:*":           false,
		"link:../x":             false,
		"file:../x":             false,
		"portal:../x":           false,
		"git+https://h/o/r.git": false,
		"github:o/r":            false,
		"o/r#v1":                false,
		"https://h/r.tgz":       false,
		"npm:left-pad@1.1.0":    false,
		"../local":              false,
	} {
		assert.Equal(t, want, isRegistrySpec(spec), spec)
	}
}
