package bindings

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/workspace"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
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

	t.Run("provider returns the harness id", func(t *testing.T) {
		ctx := workspace.ContextWithRegistry(t.Context(), workspace.NewWorkspaceRegistry())
		sess := buzz.NewSession(ctx, buzz.WithEmbedded())
		t.Cleanup(func() { _ = sess.Close() })
		registerAllBuzz(ctx, sess, map[string]vm.Callable{}, map[string]vm.Value{}, true)
		id, err := sess.Eval(ctx, `import "magus"; return magus\harness.provider({"name": "cursor"});`)
		require.NoError(t, err)
		assert.Equal(t, "cursor", id.AsString())
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

// A spell's mgs_renderPrompts list reaches the descriptor field for field: a whole file,
// and one value inside a JSON document, its value opaque host JSON.
func TestHarnessPromptsDecodeFieldForField(t *testing.T) {
	id := harnessSource(t, "prompts-test", `export fun mgs_renderPrompts(humanOnly: {str: str}) > [Prompt] {
    return [
        Prompt{path = ".codex/rules/magus.rules", content = "prefix_rule()\n"},
        Prompt{path = "opencode.json", key = ["permission", "bash", "git push *"], value = "ask"},
    ];
}
`)
	d, _, ok, err := loadHarnessFromSpell(context.Background(), id)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, []types.Prompt{
		{Path: ".codex/rules/magus.rules", Content: "prefix_rule()\n"},
		{Path: "opencode.json", Key: []string{"permission", "bash", "git push *"}, Value: "ask"},
	}, d.Prompts)
}
