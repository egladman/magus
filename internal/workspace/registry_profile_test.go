package workspace

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

func TestRegistryProfile(t *testing.T) {
	r := NewWorkspaceRegistry()
	assert.Empty(t, r.Profile(), "nothing declared is unset, which the guard reads as light")
	r.SetProfile(types.GuardProfileStrict)
	assert.Equal(t, types.GuardProfileStrict, r.Profile())
}
