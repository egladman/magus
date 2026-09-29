package ffi

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetWithDoesNotMutateSource(t *testing.T) {
	original := Set{"fs": {Capabilities: Capabilities(WASM)}}
	replaced := original.With("fs", Registration{})

	assert.True(t, original["fs"].Capabilities.Has(WASM))
	assert.False(t, replaced["fs"].Capabilities.Has(WASM))
}
