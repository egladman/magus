package interp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReplCandidatesTypedLoader(t *testing.T) {
	names := func(context.Context, string) ([]string, []string, error) { return nil, nil, nil }
	got := ReplCandidates(names)(context.Background(), ".")()
	assert.Contains(t, got, "progress\\newRotation")
	assert.NotContains(t, got, "progress\\new_rotation")
	assert.Contains(t, got, "fmt\\sprintf")
	assert.Contains(t, got, "result\\fail")
	assert.NotContains(t, got, "result\\fail::<T>")
}
