package magus

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// revisionless is a driver that declines RevisionFileReader.
type revisionless struct{ types.VCSDriver }

func (revisionless) ReadFileAt(context.Context, string, string, string) (string, error) {
	return "", &types.UnsupportedError{VCS: "fake", Capability: "RevisionFileReader"}
}

// A backend that cannot read the committed revision approves nothing beyond the working
// tree: the approved copy of a file is the file itself, never an error that blocks the load.
func TestHeadPolicyReadsTheWorkingTreeWhenTheBackendCannotReadARevision(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	path := filepath.Join(root, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte("working\n"), 0o644))

	h := headPolicy{workspace: root, repoRoot: root, driver: revisionless{}}
	got, err := h.ReadFile(t.Context(), path)
	require.NoError(t, err)
	assert.Equal(t, "working\n", string(got))
}
