package gen

import (
	"context"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
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

// TestStrUnwrapsAnEnumCase covers the second of two breaks that made an inferred
// enum case reach a host method empty.
//
// The compiler lowers both `Enum.case` and an inferred `.case` to the enum MEMBER,
// so a host method declaring an enum argument is handed an enum value, not a str.
// Str returned "" for it, and the host then reported a supplied argument as unset -
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
