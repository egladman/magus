package vm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type countingObserver struct{ names []string }

func (c *countingObserver) InterceptCall(ctx context.Context, name string, args []Value, call Callable) (Value, error) {
	c.names = append(c.names, name)
	return call(ctx, args)
}

func TestCallInterceptorIsInheritedByChildVMs(t *testing.T) {
	assert.Nil(t, NewVM(t.Context()).interceptor, "a VM on a plain ctx has no observer")

	obs := &countingObserver{}
	root := NewVM(WithCallInterceptor(t.Context(), obs))
	assert.Same(t, obs, newFiberVM(root).interceptor, "a fiber VM inherits its creator's observer")
	assert.Same(t, obs, NewVM(root.ctx).interceptor, "a callback VM built on a native's ctx inherits it")

	native := DirectValue("probe.echo", func(_ context.Context, args []Value) (Value, error) { return args[0], nil })
	require.NoError(t, root.Call(native, []Value{IntValue(1)}))
	_, err := root.Invoke(ListValue([]Value{IntValue(1)}), "len", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"probe.echo"}, obs.names, "VM.Call reaches the observer; a builtin through VM.Invoke does not")
}
