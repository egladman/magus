package bindings

import (
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/workspace"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteRuleSeesTheRequestAfterTheLoadCloses(t *testing.T) {
	reg := workspace.NewWorkspaceRegistry()
	ctx := workspace.ContextWithRegistry(t.Context(), reg)
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	registerAllBuzz(ctx, sess, map[string]vm.Callable{}, map[string]vm.Value{}, true)
	require.NoError(t, sess.Exec(ctx, `
import "magus";

magus\guard.write(fun (req: WriteRequest) > GuardVerdict {
    return magus\guard.deny("{req.path}|{req.workspace}|{req.oldText}|{req.newText}|{req.content}|{req.role}");
});
`))
	_ = sess.Close()
	rule := reg.WriteRule()
	require.NotNil(t, rule)

	got, err := rule(t.Context(), types.WriteRequest{
		Path: "/repo/CHANGELOG.md", Workspace: "/repo", OldText: "a", NewText: "b", Role: types.AgentRoleWorker,
	}, hint.NewGate(t.TempDir(), "s"))
	require.NoError(t, err)
	assert.Equal(t, types.GuardVerdict{Decision: types.GuardDeny, Reason: "/repo/CHANGELOG.md|/repo|a|b||worker"}, got)
}
