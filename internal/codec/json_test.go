package codec

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type pair struct {
	K string `json:"k"`
	V int    `json:"v"`
}

func TestMarshalUnmarshalRoundtrip(t *testing.T) {
	t.Parallel()
	in := pair{K: "hello", V: 42}
	b, err := Marshal(in)
	require.NoError(t, err)
	var out pair
	require.NoError(t, Unmarshal(b, &out))
	assert.Equal(t, in, out)
}

func TestMarshalIndent(t *testing.T) {
	t.Parallel()
	b, err := MarshalIndent(map[string]int{"x": 1}, "", "  ")
	require.NoError(t, err)
	assert.True(t, bytes.Contains(b, []byte("\n")), "MarshalIndent output has no newlines")
}

func TestEncoderDecoder(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	require.NoError(t, enc.Encode(pair{K: "a", V: 1}))
	dec := NewDecoder(&buf)
	var got pair
	require.NoError(t, dec.Decode(&got))
	assert.Equal(t, pair{K: "a", V: 1}, got)
}
