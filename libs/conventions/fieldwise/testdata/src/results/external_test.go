package results_test

import (
	"testing"

	"results"

	"github.com/stretchr/testify/assert"
)

func TestUnexportedFromOutside(t *testing.T) {
	in := results.MakeInner()
	assert.Equal(t, "a", in.Name)
	assert.Equal(t, 1, in.Other)
}

func TestExportedFromOutside(t *testing.T) {
	got := results.Make()
	assert.Equal(t, "a", got.Name) // want `got is asserted one field at a time \(Name, Count\): build the whole expected results.Result`
	assert.Equal(t, 1, got.Count)
}
