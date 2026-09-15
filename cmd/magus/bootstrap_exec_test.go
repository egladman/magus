package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bootstrapFixtureBinaryName returns the fixture binary name resolveBootstrapExecTarget
// looks for on this platform.
func bootstrapFixtureBinaryName() string {
	if runtime.GOOS == "windows" {
		return "magus.exe"
	}
	return "magus"
}

// writeBootstrapFixtureWorkspace creates dir/magusfile.buzz and an executable
// dir/magus (or dir/magus.exe), returning the resolved path
// resolveBootstrapExecTarget should report for it.
func writeBootstrapFixtureWorkspace(t *testing.T, dir string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magusfile.buzz"), []byte("// fixture\n"), 0o644))
	bin := filepath.Join(dir, bootstrapFixtureBinaryName())
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	resolved, err := filepath.EvalSymlinks(bin)
	require.NoError(t, err)
	return resolved
}

// Test 1: a workspace with an executable ./magus hands off.
func TestResolveBootstrapExecTargetFindsWorkspaceBinary(t *testing.T) {
	dir := t.TempDir()
	want := writeBootstrapFixtureWorkspace(t, dir)

	sub := filepath.Join(dir, "a", "b")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	target, ok := resolveBootstrapExecTarget(sub, filepath.Join(dir, "not-the-workspace-binary"))
	require.True(t, ok)
	assert.Equal(t, want, target)
}

// Test 2: the same binary does NOT hand off to itself.
func TestResolveBootstrapExecTargetSkipsSelf(t *testing.T) {
	dir := t.TempDir()
	self := writeBootstrapFixtureWorkspace(t, dir)

	_, ok := resolveBootstrapExecTarget(dir, self)
	assert.False(t, ok, "must not exec into the binary already running")
}

// Test 5: no magusfile.buzz anywhere above means no substitution.
func TestResolveBootstrapExecTargetNoMagusfileAnywhereAbove(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "a", "b", "c")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	_, ok := resolveBootstrapExecTarget(sub, "/nonexistent-self")
	assert.False(t, ok)
}

// Test 3: the sentinel prevents a second hop.
func TestBootstrapExecDecisionSentinelPreventsSecondHop(t *testing.T) {
	dir := t.TempDir()
	writeBootstrapFixtureWorkspace(t, dir)
	t.Setenv(bootstrapExecSentinelVar, "1")

	_, ok := bootstrapExecDecision([]string{"magus", "--root", dir, "run", "build"}, filepath.Join(dir, "not-it"))
	assert.False(t, ok, "the sentinel must block a second hop")
}

// Test 6: the opt-out disables the whole mechanism.
func TestBootstrapExecDecisionOptOut(t *testing.T) {
	dir := t.TempDir()
	writeBootstrapFixtureWorkspace(t, dir)
	t.Setenv(bootstrapExecOptOutVar, "1")
	t.Chdir(dir)

	_, ok := bootstrapExecDecision([]string{"magus", "run", "build"}, filepath.Join(dir, "not-it"))
	assert.False(t, ok, "MAGUS_NO_BOOTSTRAP_EXEC must disable the whole mechanism")
}

// Test 4: --root picks the named tree rather than $PWD.
func TestBootstrapExecDecisionHonorsRootOverCwd(t *testing.T) {
	namedRoot := t.TempDir()
	want := writeBootstrapFixtureWorkspace(t, namedRoot)

	decoy := t.TempDir() // no magusfile.buzz here; proves --root, not $PWD, wins
	t.Chdir(decoy)

	target, ok := bootstrapExecDecision([]string{"magus", "--root", namedRoot, "run", "build"}, filepath.Join(namedRoot, "not-it"))
	require.True(t, ok)
	assert.Equal(t, want, target)
}

// bootstrapExecIntoHelperTargetVar carries the fixture path into the subprocess
// TestBootstrapExecIntoReplacesProcess spawns; its presence is what tells
// TestBootstrapExecIntoHelperProcess it is running AS that subprocess rather than as
// an ordinary part of the package's test run.
const bootstrapExecIntoHelperTargetVar = "BOOTSTRAP_EXEC_INTO_HELPER_TARGET"

// TestBootstrapExecIntoHelperProcess is not a real test: it is the subprocess body
// TestBootstrapExecIntoReplacesProcess re-invokes this test binary as (the standard
// os/exec-style helper-process pattern), so that the actual syscall.Exec call runs in
// a throwaway process rather than the one running `go test`. Skips as a no-op measure
// when run as an ordinary part of `go test ./cmd/magus/...`.
func TestBootstrapExecIntoHelperProcess(t *testing.T) {
	target := os.Getenv(bootstrapExecIntoHelperTargetVar)
	if target == "" {
		t.Skip("not invoked as the bootstrapExecInto helper process")
	}
	bootstrapExecInto(target, []string{"self-name", "hello", "world"}, os.Environ())
	t.Fatal("bootstrapExecInto returned without replacing this process")
}

// Exercises the real syscall.Exec call end to end, against a small fixture script
// rather than a real magus binary (a fixture script proves the mechanism - argv,
// stdio, exit code - identically well, without the cost or risk of shelling out to an
// actual magus build from inside a test).
func TestBootstrapExecIntoReplacesProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture is a POSIX shell script; the windows exec path is a separate implementation, reviewed rather than run here")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fixture.sh")
	require.NoError(t, os.WriteFile(fixture, []byte("#!/bin/sh\necho fixture ran: \"$@\"\nexit 7\n"), 0o755))

	exe, err := os.Executable()
	require.NoError(t, err)

	cmd := exec.Command(exe, "-test.run", "^TestBootstrapExecIntoHelperProcess$")
	cmd.Env = append(os.Environ(), bootstrapExecIntoHelperTargetVar+"="+fixture)
	out, runErr := cmd.CombinedOutput()

	exitErr, ok := runErr.(*exec.ExitError)
	require.True(t, ok, "expected the helper process to exit non-zero via the fixture; output:\n%s", out)
	assert.Equal(t, 7, exitErr.ExitCode())
	assert.Contains(t, string(out), "fixture ran: hello world")
}
