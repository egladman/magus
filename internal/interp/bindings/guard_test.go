package bindings

import (
	"context"
	"fmt"
	"testing"

	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/workspace"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

func TestBuildGuard(t *testing.T) {
	t.Run("valid shell rule records onto the registry", func(t *testing.T) {
		reg := workspace.NewWorkspaceRegistry()
		guard := buildGuard(workspace.ContextWithRegistry(context.Background(), reg), nil, nil)

		rule := vm.NewMap()
		rule.MapSet("name", vm.StringValue("no-curl-prod"))
		rule.MapSet("decision", vm.StringValue("deny"))
		rule.MapSet("program", vm.StringValue("curl"))
		rule.MapSet("reason", vm.StringValue("do not hit prod"))
		args := vm.ListValue([]vm.Value{vm.StringValue("https://prod.example/health")})
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
		rule.MapSet("name", vm.StringValue("x"))
		rule.MapSet("decision", vm.StringValue("deny"))
		rule.MapSet("program", vm.StringValue("curl"))
		rule.MapSet("reason", vm.StringValue("r"))
		rule.MapSet("dialect", vm.StringValue(" Bash "))
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
		rule.MapSet("name", vm.StringValue("x"))
		rule.MapSet("decision", vm.StringValue("deny"))
		rule.MapSet("program", vm.StringValue("curl"))
		rule.MapSet("reason", vm.StringValue("r"))
		rule.MapSet("dialect", vm.StringValue("fish"))
		err := callVoidDirect(t, requireDirect(t, guard, "shell"), rule)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "dialect")
	})

	t.Run("builtins records the declared settings onto the registry", func(t *testing.T) {
		reg := workspace.NewWorkspaceRegistry()
		guard := buildGuard(workspace.ContextWithRegistry(t.Context(), reg), nil, nil)

		lines := vm.NewMap()
		lines.MapSet("decision", vm.StringValue("deny"))
		lines.MapSet("lines", vm.IntValue(120))
		settings := vm.NewMap()
		settings.MapSet("capture-filter", vm.StringValue("off"))
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
		compat.MapSet("decision", vm.StringValue("advise"))
		compat.MapSet("markers", vm.ListValue([]vm.Value{vm.StringValue("legacy:"), vm.StringValue("Deprecated:")}))
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
		rule.MapSet("name", vm.StringValue("x"))
		rule.MapSet("decision", vm.StringValue("warn"))
		rule.MapSet("program", vm.StringValue("curl"))
		rule.MapSet("reason", vm.StringValue("r"))
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
		{"not a map", vm.StringValue("deny"), "expected one map"},
		{"unknown rule names the nearest", one("whole-tre", vm.StringValue("deny")), `did you mean "whole-tree"`},
		{"unknown rule with nothing near", one("zzzzzzzzzzzz", vm.StringValue("deny")), "magus describe rules"},
		{"unknown decision", one("whole-tree", vm.StringValue("warn")), `"whole-tree": unknown decision "warn", give off, advise or deny`},
		{"neither a decision nor a setting", one("whole-tree", vm.IntValue(1)), "want"},
		{"off on the command-failed notice", one("workspace-command-failed", vm.StringValue("off")), "cannot be set; remove it from the declaration"},
		{"deny on the command-failed notice", one("workspace-command-failed", vm.StringValue("deny")), "cannot be set; remove it from the declaration"},
		{"off on the write-failed notice", one("workspace-write-failed", vm.StringValue("off")), "cannot be set; remove it from the declaration"},
		{"deny on the write-failed notice", one("workspace-write-failed", vm.StringValue("deny")), "cannot be set; remove it from the declaration"},
		{"off on the spawn-failed notice", one("workspace-spawn-failed", vm.StringValue("off")), "cannot be set; remove it from the declaration"},
		{"deny on the spawn-failed notice", one("workspace-spawn-failed", vm.StringValue("deny")), "cannot be set; remove it from the declaration"},
		{"lines on a rule that takes none", one("whole-tree", setting(map[string]vm.Value{
			"decision": vm.StringValue("deny"), "lines": vm.IntValue(10),
		})), "takes no lines parameter"},
		{"lines below 1", one("read-navigation", setting(map[string]vm.Value{
			"decision": vm.StringValue("deny"), "lines": vm.IntValue(0),
		})), `"lines" must be an int of 1 or more`},
		{"lines not an int", one("read-navigation", setting(map[string]vm.Value{
			"decision": vm.StringValue("deny"), "lines": vm.StringValue("120"),
		})), `"lines" must be an int`},
		{"a setting without a decision", one("read-navigation", setting(map[string]vm.Value{
			"lines": vm.IntValue(120),
		})), `"decision" is required`},
		{"a decision that is not a string", one("read-navigation", setting(map[string]vm.Value{
			"decision": vm.BoolValue(true),
		})), `"decision" must be`},
		{"an unknown setting field", one("read-navigation", setting(map[string]vm.Value{
			"decision": vm.StringValue("deny"), "limit": vm.IntValue(120),
		})), `unknown field "limit"`},
		{"markers on a rule that takes none", one("whole-tree", setting(map[string]vm.Value{
			"decision": vm.StringValue("deny"), "markers": vm.ListValue([]vm.Value{vm.StringValue("x")}),
		})), "takes no markers parameter; only compat-marker does"},
		{"a blank marker", one("compat-marker", setting(map[string]vm.Value{
			"decision": vm.StringValue("advise"), "markers": vm.ListValue([]vm.Value{vm.StringValue("legacy:"), vm.StringValue(" ")}),
		})), "a marker is blank"},
		{"markers that list nothing", one("compat-marker", setting(map[string]vm.Value{
			"decision": vm.StringValue("advise"), "markers": vm.ListValue(nil),
		})), `"markers" must be a list of one or more strings`},
		{"markers that are not a list", one("compat-marker", setting(map[string]vm.Value{
			"decision": vm.StringValue("advise"), "markers": vm.StringValue("legacy:"),
		})), `"markers" must be a list`},
		{"a marker that is not a string", one("compat-marker", setting(map[string]vm.Value{
			"decision": vm.StringValue("advise"), "markers": vm.ListValue([]vm.Value{vm.IntValue(1)}),
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
			"whole-tre":  vm.StringValue("deny"),
			"stage-all":  vm.StringValue("warn"),
			"whole-tree": vm.IntValue(1),
		}))
		require.Error(t, err)
		assert.ErrorContains(t, err, `unknown built-in rule "whole-tre"`)
		assert.ErrorContains(t, err, `decision "warn"`)
		assert.ErrorContains(t, err, `"whole-tree": want`)
	})
	t.Run("declared twice", func(t *testing.T) {
		builtins := requireDirect(t, buildGuard(workspace.ContextWithRegistry(t.Context(), workspace.NewWorkspaceRegistry()), nil, nil), "builtins")
		require.NoError(t, callVoidDirect(t, builtins, one("whole-tree", vm.StringValue("deny"))))
		err := callVoidDirect(t, builtins, one("whole-tree", vm.StringValue("deny")))
		require.ErrorContains(t, err, string(types.GuardRuleMisdeclared))
		assert.ErrorContains(t, err, "already declared")
	})
	t.Run("outside the root magusfile", func(t *testing.T) {
		ctx := interp.WithProjectPath(workspace.ContextWithRegistry(t.Context(), workspace.NewWorkspaceRegistry()), "libs/foo")
		err := callVoidDirect(t, requireDirect(t, buildGuard(ctx, nil, nil), "builtins"), one("whole-tree", vm.StringValue("deny")))
		require.ErrorContains(t, err, string(types.GuardRuleMisdeclared))
		assert.ErrorContains(t, err, "root magusfile")
	})
	t.Run("tokens on a rule that takes none", func(t *testing.T) {
		guard := buildGuard(workspace.ContextWithRegistry(t.Context(), workspace.NewWorkspaceRegistry()), nil, nil)
		err := callVoidDirect(t, requireDirect(t, guard, "builtins"), one("whole-tree", setting(map[string]vm.Value{
			"decision": vm.StringValue("advise"), "tokens": vm.IntValue(200_000),
		})))
		require.ErrorContains(t, err, string(types.GuardRuleMisdeclared))
		assert.ErrorContains(t, err, "takes no tokens parameter")
	})
	t.Run("calls below 1 is refused before resolution", func(t *testing.T) {
		guard := buildGuard(workspace.ContextWithRegistry(t.Context(), workspace.NewWorkspaceRegistry()), nil, nil)
		err := callVoidDirect(t, requireDirect(t, guard, "builtins"), one("whole-tree", setting(map[string]vm.Value{
			"decision": vm.StringValue("advise"), "calls": vm.IntValue(0),
		})))
		assert.ErrorContains(t, err, `"calls" must be an int of 1 or more`)
	})
}

func TestProfileRecordsTheLevel(t *testing.T) {
	reg := workspace.NewWorkspaceRegistry()
	guard := buildGuard(workspace.ContextWithRegistry(t.Context(), reg), nil, nil)
	require.NoError(t, callVoidDirect(t, requireDirect(t, guard, "profile"), vm.StringValue("strict")))
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
		{"two levels", []vm.Value{vm.StringValue("light"), vm.StringValue("strict")}, "expected one level"},
		{"unknown level", []vm.Value{vm.StringValue("max")}, `unknown level "max"; give one of off, light, standard, strict`},
		{"empty level", []vm.Value{vm.StringValue("")}, `unknown level ""`},
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
		require.NoError(t, callVoidDirect(t, profile, vm.StringValue("strict")))
		err := callVoidDirect(t, profile, vm.StringValue("off"))
		require.ErrorContains(t, err, string(types.GuardRuleMisdeclared))
		assert.ErrorContains(t, err, "already declared")
	})
	t.Run("outside the root magusfile", func(t *testing.T) {
		reg := workspace.NewWorkspaceRegistry()
		ctx := interp.WithProjectPath(workspace.ContextWithRegistry(t.Context(), reg), "libs/foo")
		err := callVoidDirect(t, requireDirect(t, buildGuard(ctx, nil, nil), "profile"), vm.StringValue("strict"))
		require.ErrorContains(t, err, string(types.GuardRuleMisdeclared))
		assert.ErrorContains(t, err, "root magusfile")
		assert.Empty(t, reg.Profile())
	})
}

// loadCommandRule is loadSpawnRule for magus\guard.command: the session is closed before
// the rule runs, as it is when the guard calls it.
func loadCommandRule(t *testing.T, src string) (workspace.CommandRule, error) {
	t.Helper()
	reg := workspace.NewWorkspaceRegistry()
	t.Cleanup(func() { _ = reg.Close() })
	ctx := workspace.ContextWithRegistry(t.Context(), reg)
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	registerAllBuzz(ctx, sess, map[string]vm.Callable{}, map[string]vm.Value{}, true)
	err := sess.Exec(ctx, src)
	_ = sess.Close()
	return reg.CommandRule(), err
}

func TestCommandRuleSeesTheRequestAfterTheLoadCloses(t *testing.T) {
	rule, err := loadCommandRule(t, `
import "magus";

magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    var seen = "";
    foreach (c in req.commands) {
        seen = seen + c.program + "(" + c.args.join(",") + ")";
        if (c.repeats) { seen = seen + "@loop"; }
        seen = seen + ";";
    }
    var lease = "";
    if (req.lease != null) { lease = req.lease!.id; }
    final said = "{req.host}|{req.description}|{req.role.value}|{lease}|{req.parent}|{req.agent}|{seen}";
    if (req.command.startsWith("gh")) { return magus\guard.deny(said); }
    return magus\guard.advise(said);
});
`)
	require.NoError(t, err)
	require.NotNil(t, rule, "the root magusfile's registration reaches the registry")

	facts := hint.NewGate(t.TempDir(), "claude-code/s1")
	got, err := rule(t.Context(), types.CommandRequest{
		Host: "claude-code", Command: "gh run watch 1", Description: "watch ci", Role: types.AgentRoleRoot,
		Commands: []types.CommandInvocation{{Program: "gh", Args: []string{"run", "watch", "1"}}},
	}, facts)
	require.NoError(t, err)
	assert.Equal(t, types.GuardVerdict{Decision: types.GuardDeny, Reason: "claude-code|watch ci|root||||gh(run,watch,1);"}, got)

	got, err = rule(t.Context(), types.CommandRequest{
		Host: "codex", Command: "while true; do make; done", Role: types.AgentRoleWorker,
		Lease:    &types.Job{ID: "guard-command"},
		Parent:   "orchestrator/feat guard-command",
		Agent:    "a534fcfe",
		Commands: []types.CommandInvocation{{Program: "true", Repeats: true}, {Program: "make", Repeats: true}},
	}, facts)
	require.NoError(t, err)
	assert.Equal(t, types.GuardVerdict{
		Decision: types.GuardAdvise,
		Reason:   "codex||worker|guard-command|orchestrator/feat guard-command|a534fcfe|true()@loop;make()@loop;",
	}, got)
}

// Where the line runs, the file each program is named by, and the binary answering the
// hook all reach the rule.
func TestCommandRuleSeesWhereAndWhichBinary(t *testing.T) {
	rule, err := loadCommandRule(t, `
import "magus";

magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    final bin = magus\guard.binary();
    return magus\guard.advise("{req.dir}|{req.workspace}|{req.commands[0].path}|{bin.path != ""}|{bin.stamp}");
});
`)
	require.NoError(t, err)
	got, err := rule(t.Context(), types.CommandRequest{
		Dir: "/w/b/sub", Workspace: "/w/b",
		Commands: []types.CommandInvocation{{Program: "magus", Path: "/w/a/magus", Args: []string{"ls"}}},
	}, hint.NewGate(t.TempDir(), "claude-code/s1"))
	require.NoError(t, err)
	assert.Equal(t, types.GuardVerdict{Decision: types.GuardAdvise, Reason: "/w/b/sub|/w/b|/w/a/magus|true|" + buildStamp}, got)
}

// once and count are one store for both rules, so a key means one thing to the policy.
func TestCommandRuleSharesOnceWithTheSpawnRule(t *testing.T) {
	src := `
import "magus";

magus\guard.spawn(fun (req: SpawnRequest) > GuardVerdict {
    if (magus\guard.once("told")) { return magus\guard.advise("spawn told"); }
    return magus\guard.allow();
});
magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    if (magus\guard.once("told")) { return magus\guard.advise("command told"); }
    return magus\guard.advise("already told, {magus\guard.count("commands")}");
});
`
	reg := workspace.NewWorkspaceRegistry()
	t.Cleanup(func() { _ = reg.Close() })
	ctx := workspace.ContextWithRegistry(t.Context(), reg)
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	registerAllBuzz(ctx, sess, map[string]vm.Callable{}, map[string]vm.Value{}, true)
	require.NoError(t, sess.Exec(ctx, src))
	_ = sess.Close()

	facts := hint.NewGate(t.TempDir(), "claude-code/s1")
	spawned, err := reg.SpawnRule()(t.Context(), types.SpawnRequest{}, facts)
	require.NoError(t, err)
	assert.Equal(t, "spawn told", spawned.Reason)
	ran, err := reg.CommandRule()(t.Context(), types.CommandRequest{}, facts)
	require.NoError(t, err)
	assert.Equal(t, "already told, 1", ran.Reason)
}

// A command rule fails the way a spawn rule does: as an error the guard fails open on.
func TestCommandRuleFailuresAreErrors(t *testing.T) {
	rule, err := loadCommandRule(t, `
import "magus";

magus\guard.command(fun (req: CommandRequest) > any !> any {
    if (req.command == "boom") { throw "boom"; }
    return "not a verdict";
});
`)
	require.NoError(t, err)
	facts := hint.NewGate(t.TempDir(), "s")
	_, err = rule(t.Context(), types.CommandRequest{Command: "boom"}, facts)
	require.ErrorContains(t, err, `magus\guard.command: the rule raised`)
	_, err = rule(t.Context(), types.CommandRequest{Command: "ls"}, facts)
	require.ErrorContains(t, err, "not a GuardVerdict")
}

func TestCommandRuleMisdeclarationIsCoded(t *testing.T) {
	fn := vm.DirectValue("rule", func(context.Context, []vm.Value) (vm.Value, error) { return vm.Null, nil })
	register := func(ctx context.Context) vm.Value {
		return requireDirect(t, buildGuard(workspace.ContextWithRegistry(ctx, workspace.NewWorkspaceRegistry()), nil, nil), "command")
	}

	t.Run("not a function", func(t *testing.T) {
		err := callVoidDirect(t, register(t.Context()), vm.StringValue("rule"))
		require.ErrorContains(t, err, string(types.GuardRuleMisdeclared))
		assert.ErrorContains(t, err, "fun(req: CommandRequest) > GuardVerdict")
	})
	t.Run("registered twice", func(t *testing.T) {
		command := register(t.Context())
		require.NoError(t, callVoidDirect(t, command, fn))
		err := callVoidDirect(t, command, fn)
		require.ErrorContains(t, err, string(types.GuardRuleMisdeclared))
		assert.ErrorContains(t, err, "already registered")
	})
	t.Run("outside the root magusfile", func(t *testing.T) {
		err := callVoidDirect(t, register(interp.WithProjectPath(t.Context(), "libs/foo")), fn)
		require.ErrorContains(t, err, string(types.GuardRuleMisdeclared))
		assert.ErrorContains(t, err, "every agent shell command")
	})
}

// loadSpawnRule runs src the way a root magusfile load does and returns what it
// registered, with the session already CLOSED: the guard calls the rule after the load
// that registered it has finished, so that is the only state worth testing it in.
func loadSpawnRule(t *testing.T, src string) (workspace.SpawnRule, error) {
	t.Helper()
	reg := workspace.NewWorkspaceRegistry()
	t.Cleanup(func() { _ = reg.Close() })
	ctx := workspace.ContextWithRegistry(t.Context(), reg)
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	registerAllBuzz(ctx, sess, map[string]vm.Callable{}, map[string]vm.Value{}, true)
	err := sess.Exec(ctx, src)
	_ = sess.Close()
	return reg.SpawnRule(), err
}

const describeRule = `
import "magus";

fun describe(req: SpawnRequest) > str {
    var agent = "";
    if (req.target != null) { agent = req.target!.agent; }
    var lease = "";
    if (req.lease != null) { lease = req.lease!.id; }
    return "{req.kind.value}|{req.host}|{req.model}|{req.role.value}|{lease}|{agent}|{req.parent}";
}

magus\guard.spawn(fun (req: SpawnRequest) > GuardVerdict {
    if (req.model == "") { return magus\guard.deny(describe(req)); }
    return magus\guard.advise(describe(req));
});
`

func TestSpawnRuleSeesTheRequestAfterTheLoadCloses(t *testing.T) {
	rule, err := loadSpawnRule(t, describeRule)
	require.NoError(t, err)
	require.NotNil(t, rule, "the root magusfile's registration reaches the registry")

	facts := hint.NewGate(t.TempDir(), "claude-code/s1")
	got, err := rule(t.Context(), types.SpawnRequest{
		Kind: types.SpawnKindSpawn, Host: "claude-code", Role: types.AgentRoleRoot,
	}, facts)
	require.NoError(t, err)
	assert.Equal(t, types.GuardVerdict{Decision: types.GuardDeny, Reason: "spawn|claude-code||root|||"}, got)

	idle := int64(90_000)
	got, err = rule(t.Context(), types.SpawnRequest{
		Kind: types.SpawnKindContinue, Host: "claude-code", Model: "sonnet", Role: types.AgentRoleWorker,
		Lease:  &types.Job{ID: "guard-spawn"},
		Target: &types.SpawnTarget{Agent: "auditor", IdleMs: &idle},
		Parent: "orchestrator/brisk-heron/implement adr 0002",
	}, facts)
	require.NoError(t, err)
	assert.Equal(t, types.GuardVerdict{
		Decision: types.GuardAdvise,
		Reason:   "continue|claude-code|sonnet|worker|guard-spawn|auditor|orchestrator/brisk-heron/implement adr 0002",
	}, got)
}

// once and count are scoped to the session the gate names, which is the session the
// guard resolved for the calling agent.
func TestSpawnRuleOnceAndCountAreSessionScoped(t *testing.T) {
	rule, err := loadSpawnRule(t, `
import "magus";

magus\guard.spawn(fun (req: SpawnRequest) > GuardVerdict {
    final n = magus\guard.count("spawns");
    if (magus\guard.once("first")) { return magus\guard.advise("first of {n}"); }
    return magus\guard.advise("again {n}");
});
`)
	require.NoError(t, err)

	cache := t.TempDir()
	a := hint.NewGate(cache, "claude-code/a")
	b := hint.NewGate(cache, "claude-code/b")
	ask := func(g hint.Gate) string {
		v, err := rule(t.Context(), types.SpawnRequest{Kind: types.SpawnKindSpawn}, g)
		require.NoError(t, err)
		return v.Reason
	}
	assert.Equal(t, "first of 1", ask(a))
	assert.Equal(t, "again 2", ask(a))
	assert.Equal(t, "first of 1", ask(b), "another session has its own once and its own count")
	assert.Equal(t, "again 3", ask(a))
}

// A rule that fails is reported as an error, never as a verdict, so the guard can fail
// open on it rather than read a broken rule as a deny or an allow. The guard layer
// (internal/guard) is what turns this error into the once-per-session advisory; here we
// only need the seam to report the failure rather than mis-decode it as a verdict.
func TestSpawnRuleFailuresAreErrors(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"throws", `if (req.host == "") { throw "boom"; } return magus\guard.allow();`, "the rule raised"},
		// Declared to return `any`, not `GuardVerdict`: a rule statically typed to return
		// GuardVerdict cannot return a str at all (BZZ1005 catches it before this runs),
		// so exercising decodeGuardVerdict's runtime check needs a looser signature.
		{"returns a non-verdict value", `return "not a verdict";`, "not a GuardVerdict"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rule, err := loadSpawnRule(t, fmt.Sprintf(`
import "magus";

magus\guard.spawn(fun (req: SpawnRequest) > any !> any {
    %s
});
`, tc.body))
			require.NoError(t, err)
			_, err = rule(t.Context(), types.SpawnRequest{}, hint.NewGate(t.TempDir(), "s"))
			require.ErrorContains(t, err, tc.want)
		})
	}
}

// allow() is the pass a rule returns when it has nothing to say; verdicts are built only
// with allow()/advise(text)/deny(text), never a GuardVerdict{} literal.
func TestSpawnRuleAllowAllows(t *testing.T) {
	rule, err := loadSpawnRule(t, `
import "magus";

magus\guard.spawn(fun (req: SpawnRequest) > GuardVerdict { return magus\guard.allow(); });
`)
	require.NoError(t, err)
	got, err := rule(t.Context(), types.SpawnRequest{}, hint.NewGate(t.TempDir(), "s"))
	require.NoError(t, err)
	assert.Equal(t, types.GuardVerdict{Decision: types.GuardAllow}, got)
}

// Each way of registering a rule the guard could not honor stops the load with MGS1045.
// Called on the Go member directly, since the checker already rejects most of these
// shapes in a magusfile that annotates its rule; this is the net under the ones it lets
// through.
func TestSpawnRuleMisdeclarationIsCoded(t *testing.T) {
	fn := vm.DirectValue("rule", func(context.Context, []vm.Value) (vm.Value, error) { return vm.Null, nil })
	register := func(ctx context.Context) vm.Value {
		return requireDirect(t, buildGuard(workspace.ContextWithRegistry(ctx, workspace.NewWorkspaceRegistry()), nil, nil), "spawn")
	}

	t.Run("not a function", func(t *testing.T) {
		err := callVoidDirect(t, register(t.Context()), vm.StringValue("rule"))
		require.ErrorContains(t, err, string(types.GuardRuleMisdeclared))
		assert.ErrorContains(t, err, "expected one function")
	})
	t.Run("registered twice", func(t *testing.T) {
		spawn := register(t.Context())
		require.NoError(t, callVoidDirect(t, spawn, fn))
		err := callVoidDirect(t, spawn, fn)
		require.ErrorContains(t, err, string(types.GuardRuleMisdeclared))
		assert.ErrorContains(t, err, "already registered")
	})
	t.Run("outside the root magusfile", func(t *testing.T) {
		err := callVoidDirect(t, register(interp.WithProjectPath(t.Context(), "libs/foo")), fn)
		require.ErrorContains(t, err, string(types.GuardRuleMisdeclared))
		assert.ErrorContains(t, err, "register it in the root magusfile")
	})
	t.Run("the root project registers", func(t *testing.T) {
		require.NoError(t, callVoidDirect(t, register(interp.WithProjectPath(t.Context(), ".")), fn))
	})
}

// once and count have no session outside a rule call, so they refuse rather than count
// into a bucket nobody reads.
func TestSpawnHelpersRefuseOutsideARule(t *testing.T) {
	_, err := loadSpawnRule(t, `
import "magus";
final told = magus\guard.once("x");
`)
	require.ErrorContains(t, err, "only callable inside a magus\\guard.spawn, command or write rule")
}

// A rule reads the job rows the guard pinned and the facts magus recorded about a
// continue's target, and cannot write the store it reads.
func TestSpawnRuleReadsJobsAndTargetFacts(t *testing.T) {
	rule, err := loadSpawnRule(t, `
import "magus";

magus\guard.spawn(fun (req: SpawnRequest) > any !> any {
    final list = magus\job.list();
    var ids = "";
    foreach (row in list.jobs) { ids = ids + row.id + ";"; }
    var wrote = "refused";
    if (req.kind == magus\SpawnKind.spawn) {
        magus\job.put("smuggled", opts: {"criteria": "from a rule"});
        wrote = "written";
    }
    var target = "";
    if (req.target != null) {
        var tokens = "none";
        if (req.target!.contextTokens != null) { tokens = "{req.target!.contextTokens!}"; }
        target = "{req.target!.description}|{req.target!.model}|{tokens}";
    }
    return magus\guard.advise("{ids}|{wrote}|{target}");
});
`)
	require.NoError(t, err)
	facts := hint.NewGate(t.TempDir(), "claude-code/s1")
	rows := []types.Job{{ID: "guard-facts", State: types.StateRunning}, {ID: "guard-docs", State: types.StateDeclared}}
	ctx := job.WithSnapshot(t.Context(), job.Snapshot{Rows: rows})
	tokens := int64(19925)

	cases := []struct {
		name    string
		req     types.SpawnRequest
		want    types.GuardVerdict
		wantErr string
	}{
		{"a continue sees its target", types.SpawnRequest{Kind: types.SpawnKindContinue, Target: &types.SpawnTarget{Agent: "brisk-heron", Description: "orchestrator/integrator guard-facts", Model: "sonnet", ContextTokens: &tokens}},
			types.GuardVerdict{Decision: types.GuardAdvise, Reason: "guard-facts;guard-docs;|refused|orchestrator/integrator guard-facts|sonnet|19925"}, ""},
		{"a target the host reported no usage for", types.SpawnRequest{Kind: types.SpawnKindContinue, Target: &types.SpawnTarget{Agent: "ghost"}},
			types.GuardVerdict{Decision: types.GuardAdvise, Reason: "guard-facts;guard-docs;|refused|||none"}, ""},
		{"a write raises", types.SpawnRequest{Kind: types.SpawnKindSpawn}, types.GuardVerdict{}, "read-only"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := rule(ctx, tc.req, facts)
			if tc.wantErr != "" {
				assert.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestWriteRuleSeesTheRequestAfterTheLoadCloses(t *testing.T) {
	reg := workspace.NewWorkspaceRegistry()
	ctx := workspace.ContextWithRegistry(t.Context(), reg)
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	registerAllBuzz(ctx, sess, map[string]vm.Callable{}, map[string]vm.Value{}, true)
	require.NoError(t, sess.Exec(ctx, `
import "magus";

magus\guard.write(fun (req: WriteRequest) > GuardVerdict {
    return magus\guard.deny("{req.path}|{req.workspace}|{req.oldText}|{req.newText}|{req.content}|{req.role.value}");
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

type StopRuleBindingSuite struct {
	suite.Suite
	reg  *workspace.WorkspaceRegistry
	ctx  context.Context
	sess *buzz.Session
}

func TestStopRuleBindingSuite(t *testing.T) { suite.Run(t, new(StopRuleBindingSuite)) }

func (s *StopRuleBindingSuite) SetupTest() {
	s.reg = workspace.NewWorkspaceRegistry()
	s.ctx = workspace.ContextWithRegistry(s.T().Context(), s.reg)
	s.sess = buzz.NewSession(s.ctx, buzz.WithEmbedded())
	registerAllBuzz(s.ctx, s.sess, map[string]vm.Callable{}, map[string]vm.Value{}, true)
}

func (s *StopRuleBindingSuite) TestTheRuleSeesTheRequestAfterTheLoadCloses() {
	s.Require().NoError(s.sess.Exec(s.ctx, `
import "magus";

magus\guard.stop(fun (req: StopRequest) > GuardVerdict {
    return magus\guard.deny("{req.event}|{req.stopHookActive}|{req.transcript}|{req.workspace}|{req.role.value}");
});
`))
	_ = s.sess.Close()
	rule := s.reg.StopRule()
	s.Require().NotNil(rule)

	got, err := rule(s.T().Context(), types.StopRequest{
		Event: "Stop", StopHookActive: true, Transcript: "/t.jsonl", Workspace: "/repo", Role: types.AgentRoleRoot,
	}, hint.NewGate(s.T().TempDir(), "s"))
	s.Require().NoError(err)
	s.Equal(types.GuardVerdict{Decision: types.GuardDeny, Reason: "Stop|true|/t.jsonl|/repo|root"}, got)
}

func (s *StopRuleBindingSuite) TestRegisteringTwiceIsMisdeclared() {
	err := s.sess.Exec(s.ctx, `
import "magus";

magus\guard.stop(fun (req: StopRequest) > GuardVerdict => magus\guard.allow());
magus\guard.stop(fun (req: StopRequest) > GuardVerdict => magus\guard.allow());
`)
	s.Require().Error(err)
	s.Contains(err.Error(), "MGS1045")
}
