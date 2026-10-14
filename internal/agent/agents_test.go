package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHarnessShippedAgentsAreCompleteAndHostNeutral(t *testing.T) {
	agents, err := ShippedAgents()
	require.NoError(t, err)
	require.NotEmpty(t, agents)

	for _, a := range agents {
		t.Run(a.Name, func(t *testing.T) {
			assert.NotEmpty(t, a.Description)
			assert.NotEmpty(t, a.Instructions)
			assert.True(t, strings.HasSuffix(a.Instructions, "\n"))
			for _, model := range []string{"claude", "haiku", "sonnet", "opus", "gpt", "codex"} {
				assert.NotContains(t, strings.ToLower(a.Instructions+a.Description), model, "an agent names no model")
			}
		})
	}
}

func TestHarnessScoutIsReadOnly(t *testing.T) {
	agents, err := ShippedAgents()
	require.NoError(t, err)
	require.Len(t, agents, 1)
	scout := agents[0]
	for _, command := range []string{"magus query", "magus refs", "magus describe", "magus affected --explain", "magus query output"} {
		assert.Contains(t, scout.Instructions, command)
	}
	// The body is prose checked above; the rest of the record is compared whole.
	scout.Instructions = ""
	assert.Equal(t, Agent{Name: "magus-scout", Description: agentDefs[0].Description, ReadOnly: true}, scout)
}

func TestHarnessAgentParamsCarryEveryField(t *testing.T) {
	agents, err := ShippedAgents()
	require.NoError(t, err)
	require.Len(t, agents, 1)

	params, err := ShippedAgentParams("small")
	require.NoError(t, err)
	require.Len(t, params, 1)
	assert.Equal(t, map[string]any{
		"name":         "magus-scout",
		"description":  agents[0].Description,
		"instructions": agents[0].Instructions,
		"read_only":    true,
		"model":        "small",
	}, params[0])
}

// With no model mapped for the host the key is absent rather than empty, so a spell tests
// for it and renders no model line instead of one that names nothing.
func TestHarnessAgentParamsOmitTheModelWhenNoneIsMapped(t *testing.T) {
	params, err := ShippedAgentParams("")
	require.NoError(t, err)
	require.Len(t, params, 1)
	assert.NotContains(t, params[0], "model")
	assert.NotContains(t, params[0], "class")
}
