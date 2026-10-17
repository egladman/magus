package ffi

import (
	"context"
	"errors"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuzzCallbackReturnsValue(t *testing.T) {
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()

	require.NoError(t, sess.Exec(ctx, `final f = fun() > str { return "payload"; };`))
	fn := sess.GetGlobal("f")
	require.True(t, fn.IsFun())

	cb := CallbackArg(sess, []vm.Value{fn}, 0)
	require.NotNil(t, cb)
	ret, err := cb.Call(ctx)
	require.NoError(t, err)
	require.Len(t, ret, 1)
	assert.Equal(t, "payload", ret[0])
}

// os\retry classifies what its Buzz callback raised. A host error raised inside the callback
// keeps its chain across the VM, so a non-retryable one ends the loop at the first call. A
// string the script throws carries no type, so it is retryable and runs every attempt.
func TestBuzzCallbackErrorsClassifyForRetry(t *testing.T) {
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()

	refused := types.NonRetryable(errors.New("hook is not a command"), types.RetryAfterConfig, "edit the hook")
	calls := 0
	sess.SetGlobal("refuse", vm.DirectValue("refuse", func(context.Context, []vm.Value) (vm.Value, error) {
		calls++
		return vm.Null, Error(refused)
	}))
	sess.SetGlobal("tick", vm.DirectValue("tick", func(context.Context, []vm.Value) (vm.Value, error) {
		calls++
		return vm.Null, nil
	}))
	require.NoError(t, sess.Exec(ctx, `
final viaHost = fun() > void !> any { refuse(); };
final thrown = fun() > void !> any { tick(); throw "flaky"; };
`))
	callback := func(name string) std.Callback { return CallbackArg(sess, []vm.Value{sess.GetGlobal(name)}, 0) }
	fast := map[string]any{"backoff_ms": 0}

	_, err := std.OsRetry(ctx, 3, callback("viaHost"), fast)
	var typed *types.NonRetryableError
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, types.RetryAfterConfig, types.ClassifyRetry(err).Change)
	assert.Equal(t, 1, calls)

	calls = 0
	_, err = std.OsRetry(ctx, 3, callback("thrown"), fast)
	require.ErrorContains(t, err, "flaky")
	assert.True(t, types.ClassifyRetry(err).Retryable)
	assert.Equal(t, 3, calls)
}

func TestOptStrValCrossesNilAsNull(t *testing.T) {
	assert.True(t, OptStrVal(nil).IsNull())
	empty := ""
	got := OptStrVal(&empty)
	require.True(t, got.IsStr(), "an empty string is a value, not an absence")
	assert.Equal(t, "", got.AsString())
}

func TestAnyValNamedBuzzObject(t *testing.T) {
	t.Parallel()
	got := AnyVal(types.BuzzObject{"major": 1, "original": "v1.2.3"})
	require.False(t, got.IsNull())
	require.True(t, got.IsMap())
	major, ok := got.MapGet("major")
	require.True(t, ok)
	assert.Equal(t, int64(1), major.AsInt())
	original, ok := got.MapGet("original")
	require.True(t, ok)
	assert.Equal(t, "v1.2.3", original.AsString())
}

func TestAnyMapValNestsBuzzObject(t *testing.T) {
	t.Parallel()
	got := AnyMapVal(types.BuzzObject{
		"name":    "v0.3.0",
		"version": types.BuzzObject{"major": 0, "minor": 3},
	})
	require.True(t, got.IsMap())
	inner, ok := got.MapGet("version")
	require.True(t, ok)
	require.True(t, inner.IsMap())
	minor, ok := inner.MapGet("minor")
	require.True(t, ok)
	assert.Equal(t, int64(3), minor.AsInt())
}

// An object a script builds for an Object argument arrives as its field map, like a map
// literal; before, AnyMap read it as absent and the host saw nil.
func TestAnyMapReadsAnObjectInstance(t *testing.T) {
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()

	require.NoError(t, sess.Exec(ctx, `
		object Retry { attempts: int = 1, note: str = "" }
		final built = Retry{ attempts = 3 };
		final literal = {"attempts": 2};
	`))
	assert.Equal(t, map[string]any{"attempts": int64(3), "note": ""}, AnyMap([]vm.Value{sess.GetGlobal("built")}, 0))
	assert.Equal(t, map[string]any{"attempts": int64(2)}, AnyMap([]vm.Value{sess.GetGlobal("literal")}, 0))
	assert.Nil(t, AnyMap([]vm.Value{vm.StrValue("x")}, 0))
	assert.Nil(t, AnyMap(nil, 0))
}

func TestAnyMapKeepsEntriesUnderNonStrKeys(t *testing.T) {
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()

	require.NoError(t, sess.Exec(ctx, `
		final ints = {1: "a", 2: "b"};
		final nested = {"k": {2: "z"}};
	`))
	assert.Equal(t, map[string]any{"1": "a", "2": "b"}, AnyMap([]vm.Value{sess.GetGlobal("ints")}, 0))
	assert.Equal(t, map[string]any{"k": map[string]any{"2": "z"}}, AnyMap([]vm.Value{sess.GetGlobal("nested")}, 0))
}

// TestStrUnwrapsAnEnumCase covers the second of two breaks that made an inferred
// enum case reach a host method empty.
//
// The compiler lowers both `Enum.case` and an inferred `.case` to the enum MEMBER,
// so a host method declaring an enum argument is handed an enum value, not a str.
// Str returned "" for it, and the host then reported a supplied argument as unset:
// exactly the failure vm.Value.EnumValue's doc calls "the one failure mode a typed
// enum was adopted to prevent". It had been fixed on the decode path and missed here.
func TestStrUnwrapsAnEnumCase(t *testing.T) {
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()

	require.NoError(t, sess.Exec(ctx, `
		enum<str> SignAlgorithm { Ed25519 = "ed25519" }
		final picked = SignAlgorithm.Ed25519;
	`))
	picked := sess.GetGlobal("picked")
	require.False(t, picked.IsStr(), "an enum case is not a str; that is the whole trap")

	assert.Equal(t, "ed25519", Str([]vm.Value{picked}, 0),
		"a str-backed enum case must cross as its VALUE")

	// The ordinary cases still behave.
	assert.Equal(t, "plain", Str([]vm.Value{vm.StrValue("plain")}, 0))
	assert.Equal(t, "", Str([]vm.Value{vm.IntValue(7)}, 0), "a non-str, non-enum is still empty")
	assert.Equal(t, "", Str(nil, 0), "a missing arg is still empty")
}

// TestStrUnwrapsOnlyStrBackedEnums: an int-backed enum has no string to give, so it
// must read as empty rather than as some rendering of the number.
func TestStrUnwrapsOnlyStrBackedEnums(t *testing.T) {
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()

	require.NoError(t, sess.Exec(ctx, `
		enum Level { low, high }
		final picked = Level.high;
	`))
	assert.Equal(t, "", Str([]vm.Value{sess.GetGlobal("picked")}, 0))
}

// BenchmarkStr guards the hot path. Str runs for EVERY string argument of every
// host call, so the enum unwrap added to it is the one change in that area that
// could cost something measurable.
//
// The str case is what to watch: it is the overwhelmingly common one, and it now
// runs a tag check before the IsStr it always ran. The enum case is rarer and pays
// one extra branch plus the unwrap by definition.
func BenchmarkStr(b *testing.B) {
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()
	if err := sess.Exec(ctx, `
		enum<str> Alg { Ed25519 = "ed25519" }
		final picked = Alg.Ed25519;
	`); err != nil {
		b.Fatal(err)
	}

	cases := []struct {
		name string
		args []vm.Value
	}{
		{"str", []vm.Value{vm.StrValue("ed25519")}},
		{"enum", []vm.Value{sess.GetGlobal("picked")}},
		{"absent", nil},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sink = Str(c.args, 0)
			}
		})
	}
}

// sink defeats dead-store elimination of the benchmarked call.
var sink string

// A defined type over a basic kind matches no case in AnyVal's type switch, because a
// type switch matches on identity. Before this was handled, every such field crossed as
// NULL: `doctor().checks[0].status` read null rather than "ok", and a magusfile told to
// branch on status was branching on nothing.
func TestAnyValNamedStringType(t *testing.T) {
	got, ok := AnyVal(map[string]any{"status": types.CheckOK}).MapGet("status")
	require.True(t, ok, "status key missing")
	require.True(t, got.IsStr(), "a named string crossed as %s, not a string", got.Kind())
	assert.Equal(t, "ok", got.AsString())
}

// The same hole, one type over: TargetRun.state is types.TargetRunState.
func TestAnyValNamedStringOnTargetRun(t *testing.T) {
	got, ok := AnyVal(map[string]any{"state": types.TargetRunPassed}).MapGet("state")
	require.True(t, ok)
	require.True(t, got.IsStr(), "a named string crossed as %s", got.Kind())
	assert.Equal(t, "passed", got.AsString())
}

// Reflection covers every basic kind, not just string, so the next defined type does
// not reintroduce the same silent null.
func TestAnyValNamedBasicKinds(t *testing.T) {
	type namedInt int
	type namedBool bool
	assert.Equal(t, int64(7), AnyVal(namedInt(7)).AsInt())
	assert.True(t, AnyVal(namedBool(true)).AsBool())

	// A shape the boundary cannot represent still yields null rather than guessing.
	assert.True(t, AnyVal(struct{ X int }{1}).IsNull())
}
