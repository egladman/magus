package sandbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildPolicy_NonNil(t *testing.T) {
	p := BuildPolicy("", nil, nil, nil, nil)
	assert.NotNil(t, p, "BuildPolicy should not return nil")
}

func TestBuildPolicy_WithWorkspace(t *testing.T) {
	dir := t.TempDir()
	p := BuildPolicy(dir, nil, nil, nil, nil)
	require.NotNil(t, p, "BuildPolicy should not return nil")
	// Workspace is always readable and writable.
	assert.NoError(t, p.CheckRead(dir), "CheckRead workspace")
	assert.NoError(t, p.CheckWrite(dir), "CheckWrite workspace")
}
