package deps

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

func crate(name, version string) types.KnowledgePackage {
	return types.KnowledgePackage{Manager: "cargo", Name: name, Version: version}
}

func TestCargoPackagesReadsEveryTable(t *testing.T) {
	t.Parallel()
	dir := extractFixture(t, "cargo/cargo.txtar")
	serde := crate("serde", "1.0.217")
	serde.Replaced = true
	assert.Equal(t, []types.KnowledgePackage{
		crate("cc", "1.2.5"),
		crate("rand", "0.8.5"),
		crate("rand", "0.9.0"),
		crate("regex", "1.11.1"),
		serde,
		crate("tempfile", "3.14.0"),
		crate("winapi", "0.3.9"),
	}, CargoPackages(filepath.Join(dir, "Cargo.toml"), filepath.Join(dir, "Cargo.lock")),
		"the rename reports rand, both majors emit, the patched crate is Replaced, and the path and git dependencies are skipped")
}

func TestCargoPackagesReadsAVirtualWorkspaceRoot(t *testing.T) {
	t.Parallel()
	dir := extractFixture(t, "cargo/cargo.txtar")
	assert.Equal(t, []types.KnowledgePackage{
		crate("anyhow", "1.0.95"),
		crate("tokio", "1.42.0"),
	}, CargoPackages(filepath.Join(dir, "ws/Cargo.toml"), filepath.Join(dir, "ws/Cargo.lock")))
}

func TestCargoPackagesDropsACratePatchedToAPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := writeFileIn(t, dir, "Cargo.toml", `[package]
name = "x"
version = "0.1.0"

[dependencies]
serde = "1"

[patch.crates-io]
serde = { path = "../serde" }
`)
	lock := writeFileIn(t, dir, "Cargo.lock", "version = 4\n\n[[package]]\nname = \"serde\"\nversion = \"1.0.217\"\n")
	assert.Nil(t, CargoPackages(manifest, lock), "a local patch has no pin, as a local go.mod replace has none")
}

func TestCargoPackagesYieldsNothingWithoutAReadableLock(t *testing.T) {
	t.Parallel()
	manifest := writeFile(t, "Cargo.toml", "[dependencies]\nserde = \"1\"\n")
	for name, lock := range map[string]string{
		"no lockfile":      "",
		"missing lockfile": filepath.Join(t.TempDir(), "Cargo.lock"),
		"malformed lock":   writeFile(t, "Cargo.lock", "[[package]\nname ="),
	} {
		assert.Nil(t, CargoPackages(manifest, lock), name)
	}
	assert.Nil(t, CargoPackages(writeFile(t, "Cargo.toml", "[dependencies\n"), ""), "malformed manifest")
}
