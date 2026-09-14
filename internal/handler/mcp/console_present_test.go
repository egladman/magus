package mcp

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/spells"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConsolePresentTool(t *testing.T) {
	t.Parallel()

	tool := &consolePresentTool{host: "127.0.0.1:7391"}
	resp, err := tool.Invoke(context.Background(), spells.InvokeRequest{Params: map[string]any{
		"surface": "activity",
		"reason":  "show the completed run",
	}})
	require.NoError(t, err)
	got, ok := resp.Data.(console.Presentation)
	require.True(t, ok)
	assert.Equal(t, "http://127.0.0.1:7391/console/activity/", got.URL)
	assert.Equal(t, "show the completed run", got.Reason)
}

func TestConsolePresentToolRejectsDisabledConsole(t *testing.T) {
	t.Parallel()

	tool := &consolePresentTool{host: "127.0.0.1:7391", unavailable: "console.enabled is false"}
	_, err := tool.Invoke(context.Background(), spells.InvokeRequest{})
	require.EqualError(t, err, "mcp: console presentation is unavailable because console.enabled is false")
}
