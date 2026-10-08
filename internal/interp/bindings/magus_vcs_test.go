package bindings

import (
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// checkoutWorkspace is rooted at this package's own checkout, so vcs.checkpoint has a
// real repository to read.
type checkoutWorkspace struct {
	jobNamespaceWorkspace
}

func (checkoutWorkspace) VCSOptions() types.VCSOptions { return types.VCSOptions{} }

// execMagusScript loads src over ws and returns a caller for its exports. The call
// carries ws too: host members read the workspace off the call-time ctx, not the load's.
func execMagusScript(t *testing.T, ws types.WorkspaceRepository, src string) func(export string) (vm.Value, error) {
	t.Helper()
	ctx := types.WithWorkspace(t.Context(), ws)
	sess := buzz.NewSession(ctx)
	t.Cleanup(func() { _ = sess.Close() })
	RegisterModules(ctx, sess)
	RegisterMagusNamespace(ctx, sess)
	require.NoError(t, sess.Exec(ctx, src))
	return func(export string) (vm.Value, error) {
		fn, ok := sess.Exports()[export]
		require.Truef(t, ok, "export %q is missing", export)
		return sess.CallValue(ctx, fn, nil)
	}
}

func TestVCSCheckpointNamespaceMatchesTheMember(t *testing.T) {
	ws := &checkoutWorkspace{jobNamespaceWorkspace{root: "."}}
	call := execMagusScript(t, ws, `
import "magus";

export fun revision() > str !> any {
    return magus\vcs.checkpoint().revision;
}
`)
	got, err := call("revision")
	require.NoError(t, err)

	want, err := std.MagusVCSCheckpoint(types.WithWorkspace(t.Context(), ws))
	require.NoError(t, err)
	assert.NotEmpty(t, want.Revision)
	assert.Equal(t, want.Revision, got.AsString())
}
