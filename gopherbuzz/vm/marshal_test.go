package vm_test

import (
	"testing"

	"github.com/egladman/gopherbuzz"
	"github.com/egladman/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarshalRoundTrip(t *testing.T) {
	prog, err := buzz.ParseEmbedded(`var x: int = 42;`)
	require.NoError(t, err, "Parse")
	chunk, err := buzz.CompileWith(prog, buzz.CompileOptions{})
	require.NoError(t, err, "CompileWith")

	data, err := chunk.Marshal()
	require.NoError(t, err, "Marshal")
	require.NotEmpty(t, data, "Marshal produced empty output")

	chunk2, err := vm.UnmarshalChunk(data)
	require.NoError(t, err, "UnmarshalChunk")
	require.NotNil(t, chunk2, "UnmarshalChunk returned nil")
	assert.NotEmpty(t, chunk2.Code, "unmarshalled chunk has no instructions")
}

func TestUnmarshalChunk_InvalidData(t *testing.T) {
	_, err := vm.UnmarshalChunk([]byte("not bytecode"))
	assert.Error(t, err, "UnmarshalChunk(garbage): expected error, got nil")
}
