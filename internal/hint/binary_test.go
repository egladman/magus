package hint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// restoreBinaryName puts the package back the way every other test in the tree expects to
// find it, which is the default: the assertions elsewhere are written against `magus`.
func restoreBinaryName(t *testing.T) {
	t.Helper()
	previous := BinaryName()
	t.Cleanup(func() { invokedAs.Store(previous) })
}

// TestBinaryNameFollowsHowTheProcessWasInvoked pins the rule the copy-paste failure asked
// for: a hint names the binary the reader is already running.
func TestBinaryNameFollowsHowTheProcessWasInvoked(t *testing.T) {
	restoreBinaryName(t)

	SetBinaryName("magus")
	assert.Equal(t, "magus", BinaryName(), "resolved through PATH, so PATH is what a reader would type")
	assert.Equal(t, "magus run build .", Run.With("build", "."))

	SetBinaryName("./magus")
	assert.Equal(t, "./magus", BinaryName())
	assert.Equal(t, "./magus run build .", Run.With("build", "."),
		"a worktree's own binary is what printed the hint and what has to run it")

	SetBinaryName("../magus")
	assert.Equal(t, "../magus", BinaryName(), "a relative spelling is the one the reader already used")

	SetBinaryName("/opt/homebrew/bin/magus")
	assert.Equal(t, "magus", BinaryName(),
		"an absolute path from outside the working directory is an installed magus: a full path in every hint costs more than it buys")
}

// TestBinaryNameCollapsesTheWorkspaceBinary covers the case every hook template produces:
// the template resolves the workspace's binary absolutely, and rendering that whole path
// into every verdict would cost more than it explains.
func TestBinaryNameCollapsesTheWorkspaceBinary(t *testing.T) {
	restoreBinaryName(t)
	t.Chdir(t.TempDir())

	// The directory the process REPORTS, not the one the test was handed: a temp dir is
	// reached through a symlink on macOS, and the comparison in SetBinaryName is against
	// what os.Getwd answers.
	wd, err := os.Getwd()
	require.NoError(t, err)

	SetBinaryName(filepath.Join(wd, "magus"))
	assert.Equal(t, "./magus", BinaryName())
}

// TestBinaryNameIgnoresEveryOtherProgram is what holds the rest of the tree steady. A test
// binary is magus.test and the codegen tools are magus-docs and friends; a hint they
// render is read by somebody whose own invocation is not this process's at all.
func TestBinaryNameIgnoresEveryOtherProgram(t *testing.T) {
	restoreBinaryName(t)
	SetBinaryName("magus")

	for _, argv0 := range []string{"", "   ", "/tmp/go-build/cmd/magus.test", "./magus-docs", "magus.exe"} {
		SetBinaryName(argv0)
		assert.Equal(t, "magus", BinaryName(), "argv0 %q is not a magus invocation", argv0)
	}
}
