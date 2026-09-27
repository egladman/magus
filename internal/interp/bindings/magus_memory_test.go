package bindings

import (
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/libs/testkit"
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
	RegisterModuleSurface(ctx, sess)
	RegisterMagusNamespace(ctx, sess)
	require.NoError(t, sess.Exec(ctx, src))
	return func(export string) (vm.Value, error) {
		fn, ok := sess.Exports()[export]
		require.Truef(t, ok, "export %q is missing", export)
		return sess.CallValue(ctx, fn, nil)
	}
}

func TestMemoryNamespaceRoundTripsThroughBuzz(t *testing.T) {
	testkit.Isolate(t)
	ws := &jobNamespaceWorkspace{cacheDir: t.TempDir(), root: t.TempDir()}
	call := execMagusScript(t, ws, `
import "magus";

export fun roundTrip() > [{str: any}] !> any {
    _ = magus\memory\put("use-buzz", opts: {"type": "decision", "body": "compose in Buzz", "refs": ["doc: docs/doctrine.md"]});
    final stored = magus\memory\put("use-buzz", opts: {"status": "accepted"});
    final got = magus\memory\get("use-buzz");
    final listed = magus\memory\list();
    magus\memory\delete("use-buzz");
    return [stored, got, listed, magus\memory\list()];
}

export fun getAbsent() > void !> any {
    _ = magus\memory\get("absent");
}

export fun deleteAbsent() > void !> any {
    magus\memory\delete("absent");
}
`)
	got, err := call("roundTrip")
	require.NoError(t, err)
	steps := got.ListItems()
	require.Len(t, steps, 4)
	field := func(v vm.Value, key string) vm.Value {
		t.Helper()
		f, ok := v.MapGet(key)
		require.Truef(t, ok, "record has no %q", key)
		return f
	}
	assert.Equal(t, "compose in Buzz", field(steps[0], "body").AsString(), "a key opts omits is kept")
	assert.Equal(t, "accepted", field(steps[1], "status").AsString())
	assert.Len(t, field(steps[2], "records").ListItems(), 1)
	assert.Empty(t, field(steps[3], "records").ListItems())

	_, err = call("getAbsent")
	require.ErrorContains(t, err, `no entry named "absent"`)
	_, err = call("deleteAbsent")
	require.ErrorContains(t, err, `no entry named "absent"`, "a delete of a name that holds nothing raises")
}

func TestVCSCheckpointNamespaceMatchesTheMember(t *testing.T) {
	ws := &checkoutWorkspace{jobNamespaceWorkspace{root: "."}}
	call := execMagusScript(t, ws, `
import "magus";

export fun revision() > str !> any {
    return magus\vcs\checkpoint().revision;
}
`)
	got, err := call("revision")
	require.NoError(t, err)

	want, err := std.MagusVCSCheckpoint(types.WithWorkspace(t.Context(), ws))
	require.NoError(t, err)
	assert.NotEmpty(t, want.Revision)
	assert.Equal(t, want.Revision, got.AsString())
}
