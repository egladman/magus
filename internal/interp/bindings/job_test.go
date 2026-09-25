package bindings

import (
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jobNamespaceWorkspace is the smallest workspace the job namespace needs: job rows
// are repository-scoped, while the cache directory is only retained for the legacy
// adoption path and resolving a returned attempt.
type jobNamespaceWorkspace struct {
	types.WorkspaceRepository
	cacheDir string
	root     string
}

func (w *jobNamespaceWorkspace) CacheDir() string { return w.cacheDir }
func (w *jobNamespaceWorkspace) Root() string     { return w.root }

// TestJobExitWithoutResultThroughBuzzScript exercises the actual parser -> VM ->
// direct-binding path. Buzz materializes an omitted optional map as {}, so testing the
// Go callable directly would miss the only call shape that matters to a script author.
func TestJobExitWithoutResultThroughBuzzScript(t *testing.T) {
	testkit.Isolate(t)
	workspace := &jobNamespaceWorkspace{cacheDir: t.TempDir(), root: t.TempDir()}
	ctx := types.WithWorkspace(t.Context(), workspace)
	sess := buzz.NewSession(ctx)
	t.Cleanup(func() { _ = sess.Close() })
	RegisterModuleSurface(ctx, sess)
	RegisterMagusNamespace(ctx, sess)

	require.NoError(t, sess.Exec(ctx, `
import "magus";

export fun abandon() > str !> any {
    magus\job\put("optional-exit", opts: {"criteria": "exercise omitted result"});
    final row = magus\job\exit("optional-exit");
    return row.state;
}

export fun waitOnNoReturn() > void !> any {
    magus\job\wait("optional-exit");
}
`))
	fn, ok := sess.Exports()["abandon"]
	require.True(t, ok, "exported Buzz entrypoint is missing")

	got, err := sess.CallValue(ctx, fn, nil)
	require.NoError(t, err)
	assert.Equal(t, string(types.StateNoReturn), got.AsString())

	wait, ok := sess.Exports()["waitOnNoReturn"]
	require.True(t, ok, "exported Buzz wait entrypoint is missing")
	_, err = sess.CallValue(ctx, wait, nil)
	require.ErrorContains(t, err, "has filed no result")
}
