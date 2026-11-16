package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// UnmarshalText is the one door from a name to an agent role, so it is where a
// misspelling stops, and an empty name stays unset.
func TestAgentRoleUnmarshalText(t *testing.T) {
	for _, name := range []string{"", "root", "worker", "integrator"} {
		var r AgentRole
		require.NoError(t, r.UnmarshalText([]byte(name)), name)
		assert.Equal(t, AgentRole(name), r)
	}

	r := AgentRoleWorker
	require.EqualError(t, r.UnmarshalText([]byte("lead")), `unknown agent role "lead" (want one of [root worker integrator])`)
	assert.Equal(t, AgentRoleWorker, r)
}

func TestAgentRoleRendersUnsetByName(t *testing.T) {
	assert.Equal(t, "unset", AgentRole("").String())
	assert.Equal(t, "worker", AgentRoleWorker.String())
}

// A NextStep reaches the console and the MCP tools as YAML as well as JSON, with the same
// optional fields left out.
func TestNextStepYAMLOmitsEmptyFields(t *testing.T) {
	got, err := yaml.Marshal(NextStep{ID: "a", Run: "magus ls"})
	require.NoError(t, err)
	assert.Equal(t, "id: a\nrun: magus ls\n", string(got))
}
