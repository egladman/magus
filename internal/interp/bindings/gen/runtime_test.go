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
