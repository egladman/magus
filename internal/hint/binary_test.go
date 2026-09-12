package hint

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// keepBinaryName puts the package back the way every other test in the tree expects to
// find it. The STORED value, not BinaryName(): reading through the fallback and storing
// the answer destroys the unset state for the rest of the process, and the fallback is
// itself a branch a later test may want.
func keepBinaryName(t *testing.T) {
	t.Helper()
	previous := invokedAs.Load()
	t.Cleanup(func() {
		if previous == nil {
			invokedAs = atomic.Value{}
			return
		}
		invokedAs.Store(previous)
	})
}

// A hint names the binary the reader is already running, which is what the copy-paste
// failure asked for.
func TestBinaryNameFollowsHowTheProcessWasInvoked(t *testing.T) {
	keepBinaryName(t)

	ResolveBinaryNameFrom("magus")
	assert.Equal(t, "magus", BinaryName(), "resolved through PATH, so PATH is what a reader would type")
	assert.Equal(t, "magus run build .", Run.With("build", "."))

	ResolveBinaryNameFrom("./magus")
	assert.Equal(t, "./magus", BinaryName())
	assert.Equal(t, "./magus run build .", Run.With("build", "."),
		"a worktree's own binary is what printed the hint and what has to run it")

	ResolveBinaryNameFrom("../magus")
	assert.Equal(t, "../magus", BinaryName(), "a relative spelling is the one the reader already used")

	ResolveBinaryNameFrom("/opt/homebrew/bin/magus")
	assert.Equal(t, "magus", BinaryName(),
		"an absolute path from outside the working directory is an installed magus: a full path in every hint costs more than it buys")
}

// Every hook template resolves the workspace's binary absolutely, and rendering that
// whole path into every verdict would cost more than it explains.
func TestBinaryNameCollapsesTheWorkspaceBinary(t *testing.T) {
	keepBinaryName(t)
	t.Chdir(t.TempDir())

	// The directory the process REPORTS, not the one the test was handed: a temp dir is
	// reached through a symlink on macOS, and the comparison is against os.Getwd.
	wd, err := os.Getwd()
	require.NoError(t, err)

	ResolveBinaryNameFrom(filepath.Join(wd, "magus"))
	assert.Equal(t, "./magus", BinaryName())
}

// What holds the rest of the tree steady: a test binary is magus.test and the codegen
// tools are magus-docs and friends, and a hint they render is read by somebody whose own
// invocation is not this process's at all.
func TestBinaryNameIgnoresEveryOtherProgram(t *testing.T) {
	keepBinaryName(t)
	ResolveBinaryNameFrom("magus")

	for _, argv0 := range []string{"", "   ", "/tmp/go-build/cmd/magus.test", "./magus-docs", "magus.exe"} {
		ResolveBinaryNameFrom(argv0)
		assert.Equal(t, "magus", BinaryName(), "argv0 %q is not a magus invocation", argv0)
	}
}

// The MCP surface renders the PATH spelling. The daemon resolved its own argv0 once at
// startup, and a client rooted elsewhere cannot run the `./magus` that answered it.
func TestOnPathRespellsForAReaderElsewhere(t *testing.T) {
	keepBinaryName(t)
	ResolveBinaryNameFrom("./magus")

	served := OnPath([]Next{breadcrumb("query-explain", Explain, "why", "spell:go")})
	require.Len(t, served, 1)
	assert.Equal(t, "magus explain spell:go", served[0].Run)
	assert.Equal(t, []string{"magus", "explain", "spell:go"}, served[0].Argv)

	ResolveBinaryNameFrom("magus")
	assert.Equal(t, "magus explain spell:go", OnPath([]Next{breadcrumb("query-explain", Explain, "why", "spell:go")})[0].Run)
}
