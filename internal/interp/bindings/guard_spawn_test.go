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

// loadSpawnRule runs src the way a root magusfile load does and returns what it
// registered, with the session already CLOSED: the guard calls the rule after the load
// that registered it has finished, so that is the only state worth testing it in.
func loadSpawnRule(t *testing.T, src string) (workspace.SpawnRule, error) {
	t.Helper()
	reg := workspace.NewWorkspaceRegistry()
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
    return "{req.kind}|{req.host}|{req.model}|{req.role}|{lease}|{agent}|{req.parent}";
}

magus\guard.spawn(fun (req: SpawnRequest) > SpawnVerdict {
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
		Kind: types.SpawnKindSpawn, Host: "claude-code", Role: types.SpawnRoleRoot,
	}, facts)
	require.NoError(t, err)
	assert.Equal(t, types.SpawnVerdict{Decision: types.SpawnDeny, Reason: "spawn|claude-code||root|||"}, got)

	idle := int64(90_000)
	got, err = rule(t.Context(), types.SpawnRequest{
		Kind: types.SpawnKindContinue, Host: "claude-code", Model: "sonnet", Role: types.SpawnRoleWorker,
		Lease:  &types.Job{ID: "guard-spawn"},
		Target: &types.SpawnTarget{Agent: "auditor", IdleMs: &idle},
		Parent: "orchestrator/brisk-heron/implement adr 0002",
	}, facts)
	require.NoError(t, err)
	assert.Equal(t, types.SpawnVerdict{
		Decision: types.SpawnAdvise,
		Reason:   "continue|claude-code|sonnet|worker|guard-spawn|auditor|orchestrator/brisk-heron/implement adr 0002",
	}, got)
}

// once and count are scoped to the session the gate names, which is the session the
// guard resolved for the calling agent.
func TestSpawnRuleOnceAndCountAreSessionScoped(t *testing.T) {
	rule, err := loadSpawnRule(t, `
import "magus";

magus\guard.spawn(fun (req: SpawnRequest) > SpawnVerdict {
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
		// Declared to return `any`, not `SpawnVerdict`: a rule statically typed to return
		// SpawnVerdict cannot return a str at all (BZZ1005 catches it before this runs),
		// so exercising decodeSpawnVerdict's runtime check needs a looser signature.
		{"returns a non-verdict value", `return "not a verdict";`, "not a SpawnVerdict"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rule, err := loadSpawnRule(t, `
import "magus";

magus\guard.spawn(fun (req: SpawnRequest) > any !> any {
    `+tc.body+`
});
`)
			require.NoError(t, err)
			_, err = rule(t.Context(), types.SpawnRequest{}, hint.NewGate(t.TempDir(), "s"))
			require.ErrorContains(t, err, tc.want)
		})
	}
}

// allow() is the pass a rule returns when it has nothing to say; verdicts are built only
// with allow()/advise(text)/deny(text), never a SpawnVerdict{} literal.
func TestSpawnRuleAllowAllows(t *testing.T) {
	rule, err := loadSpawnRule(t, `
import "magus";

magus\guard.spawn(fun (req: SpawnRequest) > SpawnVerdict { return magus\guard.allow(); });
`)
	require.NoError(t, err)
	got, err := rule(t.Context(), types.SpawnRequest{}, hint.NewGate(t.TempDir(), "s"))
	require.NoError(t, err)
	assert.Equal(t, types.SpawnVerdict{Decision: types.SpawnAllow}, got)
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
		err := callVoidDirect(t, register(t.Context()), vm.StrValue("rule"))
		require.ErrorContains(t, err, string(types.GuardSpawnMisdeclared))
		assert.ErrorContains(t, err, "expected one function")
	})
	t.Run("registered twice", func(t *testing.T) {
		spawn := register(t.Context())
		require.NoError(t, callVoidDirect(t, spawn, fn))
		err := callVoidDirect(t, spawn, fn)
		require.ErrorContains(t, err, string(types.GuardSpawnMisdeclared))
		assert.ErrorContains(t, err, "already registered")
	})
	t.Run("outside the root magusfile", func(t *testing.T) {
		err := callVoidDirect(t, register(interp.WithProjectPath(t.Context(), "libs/foo")), fn)
		require.ErrorContains(t, err, string(types.GuardSpawnMisdeclared))
		assert.ErrorContains(t, err, "register it in the root magusfile")
	})
	t.Run("the root project registers", func(t *testing.T) {
		assert.NoError(t, callVoidDirect(t, register(interp.WithProjectPath(t.Context(), ".")), fn))
	})
}

// once and count have no session outside a rule call, so they refuse rather than count
// into a bucket nobody reads.
func TestSpawnHelpersRefuseOutsideARule(t *testing.T) {
	_, err := loadSpawnRule(t, `
import "magus";
final told = magus\guard.once("x");
`)
	require.ErrorContains(t, err, "only callable inside a magus\\guard.spawn rule")
}
