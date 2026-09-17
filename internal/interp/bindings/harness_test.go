package bindings

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildHarness(t *testing.T) {
	t.Run("valid spell handle records the harness", func(t *testing.T) {
		reg := workspace.NewWorkspaceRegistry()
		harness := buildHarness(workspace.ContextWithRegistry(context.Background(), reg), nil)
		handle := vm.NewMap()
		handle.MapSet("name", vm.StrValue("cursor"))
		require.NoError(t, callVoidDirect(t, requireDirect(t, harness, "provider"), handle))
		assert.Equal(t, []string{"cursor"}, reg.Harnesses())
	})

	t.Run("duplicate provider is ignored", func(t *testing.T) {
		reg := workspace.NewWorkspaceRegistry()
		harness := buildHarness(workspace.ContextWithRegistry(context.Background(), reg), nil)
		handle := vm.NewMap()
		handle.MapSet("name", vm.StrValue("cursor"))
		require.NoError(t, callVoidDirect(t, requireDirect(t, harness, "provider"), handle))
		require.NoError(t, callVoidDirect(t, requireDirect(t, harness, "provider"), handle))
		assert.Equal(t, []string{"cursor"}, reg.Harnesses())
	})

	t.Run("multiple distinct providers are recorded in order", func(t *testing.T) {
		reg := workspace.NewWorkspaceRegistry()
		harness := buildHarness(workspace.ContextWithRegistry(context.Background(), reg), nil)
		for _, name := range []string{"cursor", "claude-code", "codex"} {
			handle := vm.NewMap()
			handle.MapSet("name", vm.StrValue(name))
			require.NoError(t, callVoidDirect(t, requireDirect(t, harness, "provider"), handle))
		}
		assert.Equal(t, []string{"cursor", "claude-code", "codex"}, reg.Harnesses())
	})
}
