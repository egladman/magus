package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/testkit"
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
// rather than a real magus binary (a fixture script proves the mechanism, namely
// argv, stdio, and exit code, identically well, without the cost or risk of
// shelling out to an actual magus build from inside a test).
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
	cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%s", bootstrapExecIntoHelperTargetVar, fixture))
	out, runErr := cmd.CombinedOutput()

	var exitErr *exec.ExitError
	require.True(t, errors.As(runErr, &exitErr), "expected the helper process to exit non-zero via the fixture; output:\n%s", out)
	assert.Equal(t, 7, exitErr.ExitCode())
	assert.Contains(t, string(out), "fixture ran: hello world")
}

// BootstrapExecRefusalSuite pins which ./magus a checkout's own build may
// hand over to.
type BootstrapExecRefusalSuite struct {
	suite.Suite
	repo *testkit.Repo
	self string
}

func TestBootstrapExecRefusalSuite(t *testing.T) {
	suite.Run(t, new(BootstrapExecRefusalSuite))
}

func (s *BootstrapExecRefusalSuite) SetupTest() {
	s.repo = testkit.NewRepository(s.T(), map[string]string{"magus.yaml": "{}\n"})
	s.self = filepath.Join(s.repo.Root(), "magus")
}

func (s *BootstrapExecRefusalSuite) TestALinkedCheckoutTakesOver() {
	linked := s.repo.AddWorktree("job").Root()
	s.Empty(bootstrapExecRefusal(filepath.Join(linked, "magus"), s.self))
}

func (s *BootstrapExecRefusalSuite) TestAPlantedWorkspaceInTheCheckoutIsRefused() {
	planted := filepath.Join(s.repo.Root(), "planted")
	s.Require().NoError(os.MkdirAll(planted, 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(planted, "magus.yaml"), []byte("{}\n"), 0o644))
	target := writeBootstrapFixtureWorkspace(s.T(), planted)
	s.Contains(bootstrapExecRefusal(target, s.self), "is not a checkout of the session's repository")
}

func (s *BootstrapExecRefusalSuite) TestAnotherRepositoryIsRefused() {
	clone := testkit.NewRepository(s.T(), map[string]string{"magus.yaml": "{}\n"})
	s.Contains(bootstrapExecRefusal(filepath.Join(clone.Root(), "magus"), s.self), "is not a checkout of the session's repository")
}

func (s *BootstrapExecRefusalSuite) TestAnInstalledBinaryDefersAnywhere() {
	clone := testkit.NewRepository(s.T(), map[string]string{"magus.yaml": "{}\n"})
	s.Empty(bootstrapExecRefusal(filepath.Join(clone.Root(), "magus"), filepath.Join(s.T().TempDir(), "magus")))
}
