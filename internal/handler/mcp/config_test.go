package mcp

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/spells"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigTool(t *testing.T) {
	cfg := config.Defaults()
	tool := &configTool{cfg: cfg}

	assert.Equal(t, hint.ToolConfig.String(), tool.Name())

	resp, err := tool.Invoke(context.Background(), spells.InvokeRequest{})
	require.NoError(t, err)
	assert.Equal(t, cfg, resp.Data, "config echoes the resolved config verbatim")
}
