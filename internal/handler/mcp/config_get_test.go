package mcp

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigGetTool(t *testing.T) {
	cfg := config.Defaults()
	tool := &configGetTool{cfg: cfg}

	assert.Equal(t, "magus_config_get", tool.Name())

	resp, err := tool.Invoke(context.Background(), types.InvokeRequest{})
	require.NoError(t, err)
	assert.Equal(t, cfg, resp.Data, "config_get should echo the resolved config verbatim")
}
