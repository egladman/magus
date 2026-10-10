package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHarnessShippedAgentsAreCompleteAndHostNeutral(t *testing.T) {
	agents, err := Agents()
	require.NoError(t, err)
	require.NotEmpty(t, agents)

	for _, a := range agents {
		t.Run(a.Name, func(t *testing.T) {
			assert.NotEmpty(t, a.Description)
			assert.NotEmpty(t, a.Instructions)
			assert.True(t, strings.HasSuffix(a.Instructions, "\n"))
			assert.NotEmpty(t, a.Class)
			for _, model := range []string{"claude", "haiku", "sonnet", "opus", "gpt", "codex"} {
				assert.NotContains(t, strings.ToLower(a.Instructions+a.Description), model, "an agent names no model")
			}
		})
	}
}

func TestHarnessScoutIsReadOnlyAndEconomy(t *testing.T) {
	agents, err := Agents()
	require.NoError(t, err)
	require.Len(t, agents, 1)
	scout := agents[0]
	assert.Equal(t, "magus-scout", scout.Name)
	assert.True(t, scout.ReadOnly)
	assert.Equal(t, AgentClassEconomy, scout.Class)
	for _, command := range []string{"magus query", "magus refs", "magus describe", "magus affected --explain", "magus query output"} {
		assert.Contains(t, scout.Instructions, command)
	}
}

func TestHarnessAgentParamsCarryEveryField(t *testing.T) {
	params, err := AgentParams()
	require.NoError(t, err)
	require.Len(t, params, 1)
	got, ok := params[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "magus-scout", got["name"])
	assert.Equal(t, true, got["read_only"])
	assert.Equal(t, "economy", got["class"])
	assert.Contains(t, got, "description")
	assert.Contains(t, got, "instructions")
}
