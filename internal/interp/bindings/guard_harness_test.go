package bindings

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildGuard(t *testing.T) {
	t.Run("valid shell rule records onto the registry", func(t *testing.T) {
		reg := workspace.NewWorkspaceRegistry()
		guard := buildGuard(workspace.ContextWithRegistry(context.Background(), reg), nil)

		rule := vm.NewMap()
		rule.MapSet("name", vm.StrValue("no-curl-prod"))
		rule.MapSet("decision", vm.StrValue("deny"))
		rule.MapSet("program", vm.StrValue("curl"))
		rule.MapSet("reason", vm.StrValue("do not hit prod"))
		args := vm.ListValue([]vm.Value{vm.StrValue("https://prod.example/health")})
		rule.MapSet("args", args)
		require.NoError(t, callVoidDirect(t, requireDirect(t, guard, "shell"), rule))
		got := reg.ShellRules()
		require.Len(t, got, 1)
		assert.Equal(t, "no-curl-prod", got[0].Name)
		assert.Equal(t, "deny", got[0].Decision)
		assert.Equal(t, []string{"https://prod.example/health"}, got[0].Args)
	})

	t.Run("bash alias defaults dialect to bash", func(t *testing.T) {
		reg := workspace.NewWorkspaceRegistry()
		guard := buildGuard(workspace.ContextWithRegistry(context.Background(), reg), nil)

		rule := vm.NewMap()
		rule.MapSet("name", vm.StrValue("x"))
		rule.MapSet("decision", vm.StrValue("deny"))
		rule.MapSet("program", vm.StrValue("curl"))
		rule.MapSet("reason", vm.StrValue("r"))
		require.NoError(t, callVoidDirect(t, requireDirect(t, guard, "bash"), rule))
		got := reg.ShellRules()
		require.Len(t, got, 1)
		assert.Equal(t, "bash", got[0].Dialect)
	})

	t.Run("invalid dialect is rejected", func(t *testing.T) {
		guard := buildGuard(context.Background(), nil)
		rule := vm.NewMap()
		rule.MapSet("name", vm.StrValue("x"))
		rule.MapSet("decision", vm.StrValue("deny"))
		rule.MapSet("program", vm.StrValue("curl"))
		rule.MapSet("reason", vm.StrValue("r"))
		rule.MapSet("dialect", vm.StrValue("fish"))
		err := callVoidDirect(t, requireDirect(t, guard, "shell"), rule)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "dialect")
	})

	t.Run("bad decision is rejected", func(t *testing.T) {
		guard := buildGuard(context.Background(), nil)
		rule := vm.NewMap()
		rule.MapSet("name", vm.StrValue("x"))
		rule.MapSet("decision", vm.StrValue("warn"))
		rule.MapSet("program", vm.StrValue("curl"))
		rule.MapSet("reason", vm.StrValue("r"))
		err := callVoidDirect(t, requireDirect(t, guard, "bash"), rule)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "deny")
	})
}

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
