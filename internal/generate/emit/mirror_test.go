package emit

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func TestResultArgument(t *testing.T) {
	arg, ok := ResultArgument(reflect.TypeFor[types.Result[types.EditApplied]]())
	require.True(t, ok)
	assert.Equal(t, reflect.TypeFor[types.EditApplied](), arg)

	_, ok = ResultArgument(reflect.TypeFor[types.EditApplied]())
	assert.False(t, ok)
}
