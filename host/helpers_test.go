package host

import (
	"context"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuzzCallbackReturnsValue guards the buzzCallback boundary: Call must hand
// back the callback's marshalled return value, not a pre-reduced bool. os.retry
// relies on this to return fn's result on success; before the fix it always saw
// a bool. Predicate truthiness is derived downstream, so this does not regress
// fs.walk / charm.*_func.
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
	assert.Equal(t, "payload", ret[0], "buzzCallback.Call must return fn's value, not its truthiness")
}

// TestAnyVal_NamedBuzzObject guards a failure mode that is silent rather than
// loud. A Go type switch matches on type IDENTITY, so `case map[string]any` does
// NOT match types.BuzzObject even though that is its underlying type - such a
// value falls through to AnyVal's trailing vm.Null.
//
// That is not hypothetical. When BuzzObject was introduced as a named type, every
// NESTED boundary value broke at once: Tag.BuzzObject() nests
// Version.BuzzObject(), whose Go type is BuzzObject, so a magusfile read
// `tag.version` as null and every field off it as null too. Nothing failed - the
// values were simply absent, and the docs site only fell over further downstream.
func TestAnyVal_NamedBuzzObject(t *testing.T) {
	t.Parallel()

	nested := types.BuzzObject{"major": 1, "original": "v1.2.3"}
	got := AnyVal(nested)

	require.False(t, got.IsNull(), "a named BuzzObject must not marshal to null")
	require.True(t, got.IsMap(), "a BuzzObject must marshal to a Buzz map")
	major, ok := got.MapGet("major")
	require.True(t, ok, "major must be present")
	assert.Equal(t, int64(1), major.AsInt())
	original, ok := got.MapGet("original")
	require.True(t, ok, "original must be present")
	assert.Equal(t, "v1.2.3", original.AsString())
}

// TestAnyMapVal_NestsBuzzObject is the end-to-end shape the bug actually took: an
// outer BuzzObject carrying an inner one, which is what every mirrored type with a
// struct field produces.
func TestAnyMapVal_NestsBuzzObject(t *testing.T) {
	t.Parallel()

	outer := types.BuzzObject{
		"name":    "v0.3.0",
		"version": types.BuzzObject{"major": 0, "minor": 3},
	}
	got := AnyMapVal(outer)

	require.True(t, got.IsMap())
	inner, ok := got.MapGet("version")
	require.True(t, ok, "version must be present")
	require.True(t, inner.IsMap(), "the nested BuzzObject must survive as a map, not null")
	minor, ok := inner.MapGet("minor")
	require.True(t, ok, "minor must be present")
	assert.Equal(t, int64(3), minor.AsInt())
}
