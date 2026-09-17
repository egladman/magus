package bindings

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/agent"
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

// TestDecodeHarnessPrompts pins that a spell's harness_prompts list reaches the descriptor
// field for field, in the shape a Buzz spell returns it: lists of any, maps of any.
func TestDecodeHarnessPrompts(t *testing.T) {
	t.Run("a whole file and a JSON key both decode", func(t *testing.T) {
		var d agent.HarnessDescriptor
		require.NoError(t, decodeHarnessPrompts([]any{
			map[string]any{"path": ".codex/rules/magus.rules", "content": "prefix_rule()\n"},
			map[string]any{"path": "opencode.json", "key": []any{"permission", "bash", "git push *"}, "value": "ask"},
		}, &d))
		assert.Equal(t, []agent.HarnessPrompt{
			{Path: ".codex/rules/magus.rules", Content: "prefix_rule()\n"},
			{Path: "opencode.json", Key: []string{"permission", "bash", "git push *"}, Value: "ask"},
		}, d.Prompts)
	})

	t.Run("anything but a list is refused", func(t *testing.T) {
		var d agent.HarnessDescriptor
		assert.Error(t, decodeHarnessPrompts(map[string]any{"path": "opencode.json"}, &d))
		assert.Empty(t, d.Prompts)
	})

	t.Run("a field of the wrong type is refused", func(t *testing.T) {
		var d agent.HarnessDescriptor
		assert.Error(t, decodeHarnessPrompts([]any{map[string]any{"path": "x", "key": "not a list"}}, &d))
	})
}
