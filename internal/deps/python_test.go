package deps

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

func pypi(name, version string) types.KnowledgePackage {
	return types.KnowledgePackage{Manager: "python", Name: name, Version: version}
}

func TestPythonPackagesReadsUV(t *testing.T) {
	t.Parallel()
	dir := extractFixture(t, "python/uv.txtar")
	assert.Equal(t, []types.KnowledgePackage{
		pypi("pydantic-core", "2.27.1"),
		pypi("pytest", "8.3.4"),
		pypi("pyyaml", "6.0.2"),
		pypi("requests", "2.32.3"),
		pypi("ruff", "0.8.4"),
	}, PythonPackages(filepath.Join(dir, "pyproject.toml"), filepath.Join(dir, "uv.lock")),
		"names are PEP 503 normalized on both sides; the direct reference, the editable sibling and the undeclared urllib3 are skipped")
}

func TestPythonPackagesReadsPoetry(t *testing.T) {
	t.Parallel()
	dir := extractFixture(t, "python/poetry.txtar")
	assert.Equal(t, []types.KnowledgePackage{
		pypi("attrs", "24.2.0"),
		pypi("black", "24.10.0"),
		pypi("django", "5.1.4"),
		pypi("pytest", "8.3.4"),
	}, PythonPackages(filepath.Join(dir, "pyproject.toml"), filepath.Join(dir, "poetry.lock")),
		"python itself, the path dependency and the git dependency are skipped")
}

func TestPythonPackagesReadsPDM(t *testing.T) {
	t.Parallel()
	dir := extractFixture(t, "python/poetry.txtar")
	assert.Equal(t, []types.KnowledgePackage{pypi("requests", "2.32.3")},
		PythonPackages(filepath.Join(dir, "pdm/pyproject.toml"), filepath.Join(dir, "pdm/pdm.lock")))
}

func TestPythonPackagesReadsPipfileLock(t *testing.T) {
	t.Parallel()
	dir := extractFixture(t, "python/pipfile.txtar")
	assert.Equal(t, []types.KnowledgePackage{
		pypi("flask", "3.1.0"),
		pypi("pytest", "8.3.4"),
		pypi("requests", "2.32.3"),
	}, PythonPackages(filepath.Join(dir, "pyproject.toml"), filepath.Join(dir, "Pipfile.lock")))
}

func TestPythonPackagesReturnsEveryVersionAForkedLockPins(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := writeFileIn(t, dir, "pyproject.toml", "[project]\ndependencies = [\"numpy\"]\n")
	lock := writeFileIn(t, dir, "uv.lock", `version = 1
[[package]]
name = "numpy"
version = "2.2.1"
source = { registry = "https://pypi.org/simple" }

[[package]]
name = "numpy"
version = "1.26.4"
source = { registry = "https://pypi.org/simple" }
`)
	assert.Equal(t, []types.KnowledgePackage{pypi("numpy", "1.26.4"), pypi("numpy", "2.2.1")}, PythonPackages(manifest, lock))
}

func TestPythonPackagesYieldsNothingWithoutAReadableLock(t *testing.T) {
	t.Parallel()
	manifest := writeFile(t, "pyproject.toml", "[project]\ndependencies = [\"requests\"]\n")
	for name, lock := range map[string]string{
		"no lockfile":       "",
		"missing lockfile":  filepath.Join(t.TempDir(), "uv.lock"),
		"malformed uv":      writeFile(t, "uv.lock", "[[package]\nname ="),
		"malformed pipfile": writeFile(t, "Pipfile.lock", "{not json"),
		"unknown lock":      writeFile(t, "requirements.txt", "requests==2.32.3\n"),
	} {
		assert.Nil(t, PythonPackages(manifest, lock), name)
	}
	assert.Nil(t, PythonPackages(writeFile(t, "pyproject.toml", "[project\n"), ""), "malformed manifest")
}

func TestRequirementName(t *testing.T) {
	t.Parallel()
	for req, want := range map[string]string{
		"requests":                           "requests",
		"Requests[socks]>=2.31":              "requests",
		"pydantic_core ; python_version>'3'": "pydantic-core",
		"zope.interface~=6.0":                "zope-interface",
		"local-lib @ file:///tmp/local-lib":  "",
		"pkg[extra] @ git+https://h/o/r":     "",
		"":                                   "",
	} {
		assert.Equal(t, want, requirementName(req), req)
	}
}
