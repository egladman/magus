package threefields

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTwoFields(t *testing.T) {
	var got Result
	assert.Equal(t, "a", got.Name)
	assert.Equal(t, 1, got.Count)
}

func TestThreeFields(t *testing.T) {
	var got Result
	assert.Equal(t, "a", got.Name) // want `got is asserted one field at a time \(Name, Count, Size\): .*this test; results are compared whole here`
	assert.Equal(t, 1, got.Count)
	assert.Equal(t, 2, got.Size)
}
