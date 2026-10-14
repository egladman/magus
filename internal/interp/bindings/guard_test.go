package bindings

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildGuard(t *testing.T) {
	t.Run("valid shell rule records onto the registry", func(t *testing.T) {
		reg := workspace.NewWorkspaceRegistry()
		guard := buildGuard(workspace.ContextWithRegistry(context.Background(), reg), nil, nil)

		rule := vm.NewMap()
		rule.MapSet("name", vm.StrValue("no-curl-prod"))
		rule.MapSet("decision", vm.StrValue("deny"))
		rule.MapSet("program", vm.StrValue("curl"))
		rule.MapSet("reason", vm.StrValue("do not hit prod"))
		args := vm.ListValue([]vm.Value{vm.StrValue("https://prod.example/health")})
		rule.MapSet("args", args)
		require.NoError(t, callVoidDirect(t, requireDirect(t, guard, "shell"), rule))
		got := reg.ShellRules()
		assert.Equal(t, []workspace.ShellRule{{
			Name:     "no-curl-prod",
			Decision: "deny",
			Program:  "curl",
			Args:     []string{"https://prod.example/health"},
			Reason:   "do not hit prod",
		}}, got)
	})

	t.Run("dialect is kept as written", func(t *testing.T) {
		reg := workspace.NewWorkspaceRegistry()
		guard := buildGuard(workspace.ContextWithRegistry(context.Background(), reg), nil, nil)

		rule := vm.NewMap()
		rule.MapSet("name", vm.StrValue("x"))
		rule.MapSet("decision", vm.StrValue("deny"))
		rule.MapSet("program", vm.StrValue("curl"))
		rule.MapSet("reason", vm.StrValue("r"))
		rule.MapSet("dialect", vm.StrValue(" Bash "))
		require.NoError(t, callVoidDirect(t, requireDirect(t, guard, "shell"), rule))
		got := reg.ShellRules()
		require.Len(t, got, 1)
		assert.Equal(t, "bash", got[0].Dialect)
	})

	t.Run("shell is the only rule member", func(t *testing.T) {
		_, ok := buildGuard(context.Background(), nil, nil).MapGet("bash")
		assert.False(t, ok)
	})

	t.Run("invalid dialect is rejected", func(t *testing.T) {
		guard := buildGuard(context.Background(), nil, nil)
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

	t.Run("builtins records the declared settings onto the registry", func(t *testing.T) {
		reg := workspace.NewWorkspaceRegistry()
		guard := buildGuard(workspace.ContextWithRegistry(t.Context(), reg), nil, nil)

		lines := vm.NewMap()
		lines.MapSet("decision", vm.StrValue("deny"))
		lines.MapSet("lines", vm.IntValue(120))
		settings := vm.NewMap()
		settings.MapSet("capture-filter", vm.StrValue("off"))
		settings.MapSet("read-navigation", lines)
		require.NoError(t, callVoidDirect(t, requireDirect(t, guard, "builtins"), settings))
		assert.Equal(t, map[string]builtin.Setting{
			"capture-filter":  {Decision: builtin.Off},
			"read-navigation": {Decision: builtin.Deny, Lines: 120},
		}, reg.Builtins())
	})

	t.Run("builtins records declared markers", func(t *testing.T) {
		reg := workspace.NewWorkspaceRegistry()
		guard := buildGuard(workspace.ContextWithRegistry(t.Context(), reg), nil, nil)

		compat := vm.NewMap()
		compat.MapSet("decision", vm.StrValue("advise"))
		compat.MapSet("markers", vm.ListValue([]vm.Value{vm.StrValue("legacy:"), vm.StrValue("Deprecated:")}))
		settings := vm.NewMap()
		settings.MapSet("compat-marker", compat)
		require.NoError(t, callVoidDirect(t, requireDirect(t, guard, "builtins"), settings))
		assert.Equal(t, map[string]builtin.Setting{
			"compat-marker": {Decision: builtin.Advise, Markers: []string{"legacy:", "Deprecated:"}},
		}, reg.Builtins())
	})

	t.Run("bad decision is rejected", func(t *testing.T) {
		guard := buildGuard(context.Background(), nil, nil)
		rule := vm.NewMap()
		rule.MapSet("name", vm.StrValue("x"))
		rule.MapSet("decision", vm.StrValue("warn"))
		rule.MapSet("program", vm.StrValue("curl"))
		rule.MapSet("reason", vm.StrValue("r"))
		err := callVoidDirect(t, requireDirect(t, guard, "shell"), rule)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "deny")
	})
}

// Each way of setting a built-in the guard could not honor stops the load with MGS1045,
// and records nothing.
func TestBuiltinsMisdeclarationIsCoded(t *testing.T) {
	setting := func(fields map[string]vm.Value) vm.Value {
		m := vm.NewMap()
		for k, v := range fields {
			m.MapSet(k, v)
		}
		return m
	}
	one := func(name string, v vm.Value) vm.Value { return setting(map[string]vm.Value{name: v}) }

	cases := []struct {
		name string
		arg  vm.Value
		want string
	}{
		{"not a map", vm.StrValue("deny"), "expected one map"},
		{"unknown rule names the nearest", one("whole-tre", vm.StrValue("deny")), `did you mean "whole-tree"`},
		{"unknown rule with nothing near", one("zzzzzzzzzzzz", vm.StrValue("deny")), "magus describe rules"},
		{"unknown decision", one("whole-tree", vm.StrValue("warn")), "not off, advise or deny"},
		{"neither a decision nor a setting", one("whole-tree", vm.IntValue(1)), "want"},
		{"off on the command-failed notice", one("workspace-command-failed", vm.StrValue("off")), "cannot be set; remove it from the declaration"},
		{"deny on the command-failed notice", one("workspace-command-failed", vm.StrValue("deny")), "cannot be set; remove it from the declaration"},
		{"off on the write-failed notice", one("workspace-write-failed", vm.StrValue("off")), "cannot be set; remove it from the declaration"},
		{"deny on the write-failed notice", one("workspace-write-failed", vm.StrValue("deny")), "cannot be set; remove it from the declaration"},
		{"off on the spawn-failed notice", one("workspace-spawn-failed", vm.StrValue("off")), "cannot be set; remove it from the declaration"},
		{"deny on the spawn-failed notice", one("workspace-spawn-failed", vm.StrValue("deny")), "cannot be set; remove it from the declaration"},
		{"lines on a rule that takes none", one("whole-tree", setting(map[string]vm.Value{
			"decision": vm.StrValue("deny"), "lines": vm.IntValue(10),
		})), "takes no lines parameter"},
		{"lines below 1", one("read-navigation", setting(map[string]vm.Value{
			"decision": vm.StrValue("deny"), "lines": vm.IntValue(0),
		})), `"lines" must be an int of 1 or more`},
		{"lines not an int", one("read-navigation", setting(map[string]vm.Value{
			"decision": vm.StrValue("deny"), "lines": vm.StrValue("120"),
		})), `"lines" must be an int`},
		{"a setting without a decision", one("read-navigation", setting(map[string]vm.Value{
			"lines": vm.IntValue(120),
		})), `"decision" is required`},
		{"a decision that is not a string", one("read-navigation", setting(map[string]vm.Value{
			"decision": vm.BoolValue(true),
		})), `"decision" must be`},
		{"an unknown setting field", one("read-navigation", setting(map[string]vm.Value{
			"decision": vm.StrValue("deny"), "limit": vm.IntValue(120),
		})), `unknown field "limit"`},
		{"markers on a rule that takes none", one("whole-tree", setting(map[string]vm.Value{
			"decision": vm.StrValue("deny"), "markers": vm.ListValue([]vm.Value{vm.StrValue("x")}),
		})), "takes no markers parameter; only compat-marker does"},
		{"a blank marker", one("compat-marker", setting(map[string]vm.Value{
			"decision": vm.StrValue("advise"), "markers": vm.ListValue([]vm.Value{vm.StrValue("legacy:"), vm.StrValue(" ")}),
		})), "a marker is blank"},
		{"markers that list nothing", one("compat-marker", setting(map[string]vm.Value{
			"decision": vm.StrValue("advise"), "markers": vm.ListValue(nil),
		})), `"markers" must be a list of one or more strings`},
		{"markers that are not a list", one("compat-marker", setting(map[string]vm.Value{
			"decision": vm.StrValue("advise"), "markers": vm.StrValue("legacy:"),
		})), `"markers" must be a list`},
		{"a marker that is not a string", one("compat-marker", setting(map[string]vm.Value{
			"decision": vm.StrValue("advise"), "markers": vm.ListValue([]vm.Value{vm.IntValue(1)}),
		})), `"markers"[0] must be a string`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := workspace.NewWorkspaceRegistry()
			guard := buildGuard(workspace.ContextWithRegistry(t.Context(), reg), nil, nil)
			err := callVoidDirect(t, requireDirect(t, guard, "builtins"), tc.arg)
			require.ErrorContains(t, err, string(types.GuardRuleMisdeclared))
			assert.ErrorContains(t, err, tc.want)
			assert.Nil(t, reg.Builtins())
		})
	}

	t.Run("every misdeclaration is reported at once", func(t *testing.T) {
		guard := buildGuard(workspace.ContextWithRegistry(t.Context(), workspace.NewWorkspaceRegistry()), nil, nil)
		err := callVoidDirect(t, requireDirect(t, guard, "builtins"), setting(map[string]vm.Value{
			"whole-tre":  vm.StrValue("deny"),
			"stage-all":  vm.StrValue("warn"),
			"whole-tree": vm.IntValue(1),
		}))
		require.Error(t, err)
		assert.ErrorContains(t, err, `unknown built-in rule "whole-tre"`)
		assert.ErrorContains(t, err, `decision "warn"`)
		assert.ErrorContains(t, err, `"whole-tree": want`)
	})
	t.Run("declared twice", func(t *testing.T) {
		builtins := requireDirect(t, buildGuard(workspace.ContextWithRegistry(t.Context(), workspace.NewWorkspaceRegistry()), nil, nil), "builtins")
		require.NoError(t, callVoidDirect(t, builtins, one("whole-tree", vm.StrValue("deny"))))
		err := callVoidDirect(t, builtins, one("whole-tree", vm.StrValue("deny")))
		require.ErrorContains(t, err, string(types.GuardRuleMisdeclared))
		assert.ErrorContains(t, err, "already declared")
	})
	t.Run("outside the root magusfile", func(t *testing.T) {
		ctx := interp.WithProjectPath(workspace.ContextWithRegistry(t.Context(), workspace.NewWorkspaceRegistry()), "libs/foo")
		err := callVoidDirect(t, requireDirect(t, buildGuard(ctx, nil, nil), "builtins"), one("whole-tree", vm.StrValue("deny")))
		require.ErrorContains(t, err, string(types.GuardRuleMisdeclared))
		assert.ErrorContains(t, err, "root magusfile")
	})
	t.Run("tokens on a rule that takes none", func(t *testing.T) {
		guard := buildGuard(workspace.ContextWithRegistry(t.Context(), workspace.NewWorkspaceRegistry()), nil, nil)
		err := callVoidDirect(t, requireDirect(t, guard, "builtins"), one("whole-tree", setting(map[string]vm.Value{
			"decision": vm.StrValue("advise"), "tokens": vm.IntValue(200_000),
		})))
		require.ErrorContains(t, err, string(types.GuardRuleMisdeclared))
		assert.ErrorContains(t, err, "takes no tokens parameter")
	})
	t.Run("calls below 1 is refused before resolution", func(t *testing.T) {
		guard := buildGuard(workspace.ContextWithRegistry(t.Context(), workspace.NewWorkspaceRegistry()), nil, nil)
		err := callVoidDirect(t, requireDirect(t, guard, "builtins"), one("whole-tree", setting(map[string]vm.Value{
			"decision": vm.StrValue("advise"), "calls": vm.IntValue(0),
		})))
		assert.ErrorContains(t, err, `"calls" must be an int of 1 or more`)
	})
}

func TestProfileRecordsTheLevel(t *testing.T) {
	reg := workspace.NewWorkspaceRegistry()
	guard := buildGuard(workspace.ContextWithRegistry(t.Context(), reg), nil, nil)
	require.NoError(t, callVoidDirect(t, requireDirect(t, guard, "profile"), vm.StrValue("strict")))
	assert.Equal(t, types.GuardProfileStrict, reg.Profile())
}

// A level the guard could not honor stops the load with MGS1045 and records nothing: a
// workspace that believes it chose strict and runs light is the failure this prevents.
func TestProfileMisdeclarationIsCoded(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []vm.Value
		want string
	}{
		{"no level", nil, "expected one level, off, light, standard, strict"},
		{"not a string", []vm.Value{vm.IntValue(3)}, "expected one level"},
		{"two levels", []vm.Value{vm.StrValue("light"), vm.StrValue("strict")}, "expected one level"},
		{"unknown level", []vm.Value{vm.StrValue("max")}, `unknown level "max"; give one of off, light, standard, strict`},
		{"empty level", []vm.Value{vm.StrValue("")}, `unknown level ""`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := workspace.NewWorkspaceRegistry()
			guard := buildGuard(workspace.ContextWithRegistry(t.Context(), reg), nil, nil)
			err := callVoidDirect(t, requireDirect(t, guard, "profile"), tc.args...)
			require.ErrorContains(t, err, string(types.GuardRuleMisdeclared))
			assert.ErrorContains(t, err, tc.want)
			assert.Empty(t, reg.Profile())
		})
	}
	t.Run("declared twice", func(t *testing.T) {
		profile := requireDirect(t, buildGuard(workspace.ContextWithRegistry(t.Context(), workspace.NewWorkspaceRegistry()), nil, nil), "profile")
		require.NoError(t, callVoidDirect(t, profile, vm.StrValue("strict")))
		err := callVoidDirect(t, profile, vm.StrValue("off"))
		require.ErrorContains(t, err, string(types.GuardRuleMisdeclared))
		assert.ErrorContains(t, err, "already declared")
	})
	t.Run("outside the root magusfile", func(t *testing.T) {
		reg := workspace.NewWorkspaceRegistry()
		ctx := interp.WithProjectPath(workspace.ContextWithRegistry(t.Context(), reg), "libs/foo")
		err := callVoidDirect(t, requireDirect(t, buildGuard(ctx, nil, nil), "profile"), vm.StrValue("strict"))
		require.ErrorContains(t, err, string(types.GuardRuleMisdeclared))
		assert.ErrorContains(t, err, "root magusfile")
		assert.Empty(t, reg.Profile())
	})
}
