package hostbuzz

import (
	"context"
	"testing"

	buzz "github.com/egladman/gopherbuzz"
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
	sess := buzz.NewSession(ctx)
	defer sess.Close()

	require.NoError(t, sess.Exec(ctx, `final f = fun() > str { return "payload"; };`))
	fn := sess.GetGlobal("f")
	require.True(t, fn.IsFun())

	cb := CallbackArg(sess, []buzz.Value{fn}, 0)
	require.NotNil(t, cb)

	ret, err := cb.Call(ctx)
	require.NoError(t, err)
	require.Len(t, ret, 1)
	assert.Equal(t, "payload", ret[0], "buzzCallback.Call must return fn's value, not its truthiness")
}
