package bindings

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/workspace"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loadCommandRule is loadSpawnRule for magus\guard.command: the session is closed before
// the rule runs, as it is when the guard calls it.
func loadCommandRule(t *testing.T, src string) (workspace.CommandRule, error) {
	t.Helper()
	reg := workspace.NewWorkspaceRegistry()
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
    final said = "{req.host}|{req.description}|{req.role}|{lease}|{req.parent}|{seen}";
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
	assert.Equal(t, types.GuardVerdict{Decision: types.GuardDeny, Reason: "claude-code|watch ci|root|||gh(run,watch,1);"}, got)

	got, err = rule(t.Context(), types.CommandRequest{
		Host: "codex", Command: "while true; do make; done", Role: types.AgentRoleWorker,
		Lease:    &types.Job{ID: "guard-command"},
		Parent:   "orchestrator/feat guard-command",
		Commands: []types.CommandInvocation{{Program: "true", Repeats: true}, {Program: "make", Repeats: true}},
	}, facts)
	require.NoError(t, err)
	assert.Equal(t, types.GuardVerdict{
		Decision: types.GuardAdvise,
		Reason:   "codex||worker|guard-command|orchestrator/feat guard-command|true()@loop;make()@loop;",
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
		err := callVoidDirect(t, register(t.Context()), vm.StrValue("rule"))
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
