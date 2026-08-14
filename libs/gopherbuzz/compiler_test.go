package buzz

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompileWith_SimpleFunction(t *testing.T) {
	prog, err := ParseEmbedded(`fun add(a: int, b: int) > int { return a + b; }`)
	require.NoError(t, err)
	chunk, err := CompileWith(prog, CompileOptions{})
	require.NoError(t, err)
	require.NotNil(t, chunk, "CompileWith returned nil chunk")
	assert.NotEmpty(t, chunk.Code, "compiled chunk has no instructions")
}

func TestCompileWith_EmptyProgram(t *testing.T) {
	prog, err := ParseEmbedded("")
	require.NoError(t, err)
	chunk, err := CompileWith(prog, CompileOptions{})
	require.NoError(t, err)
	require.NotNil(t, chunk, "CompileWith returned nil chunk for empty program")
}
