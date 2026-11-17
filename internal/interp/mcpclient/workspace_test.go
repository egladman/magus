package mcpclient

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
)

func TestOpenWorkspaceKeepsTheContextOnError(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx, err := OpenWorkspace(context.Background(), io.Discard, func(context.Context, config.Config, string) (Held, error) { return Held{}, nil })
	require.Error(t, err)
	assert.NotNil(t, ctx)
}
