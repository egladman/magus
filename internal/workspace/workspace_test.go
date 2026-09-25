package workspace

import (
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/libs/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { testkit.Main(m) }

func TestWithLoadedConfig(t *testing.T) {
	opt := WithLoadedConfig(config.Config{})
	var l Load
	opt(&l)
	require.NotNil(t, l.Preloaded)
}

func TestWithWorkspaceRegistry(t *testing.T) {
	reg := NewWorkspaceRegistry()
	var l Load
	l.Registry = reg
	assert.Same(t, reg, l.Registry)
}
