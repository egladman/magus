package bindings

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const scoutDescription = "Answers standalone lookups and proves or refutes claims about a magus workspace with read-only magus queries, reporting each command and its output ref. Use for where-is, what-depends-on, is-this-generated and did-this-pass questions; not for edits or design."

func loadShippedHarness(t *testing.T, id string) agent.HarnessDescriptor {
	t.Helper()
	if _, ok := project.DefaultSpellRegistry().Lookup(id); !ok {
		_, _, err := loadBuzzSpell(context.Background(), filepath.Join("..", "..", "..", "spells", "harness", id, "spell.buzz"))
		require.NoError(t, err)
	}
	d, _, ok, err := loadHarnessFromSpell(context.Background(), id)
	require.NoError(t, err)
	require.True(t, ok, "harness spell %q is registered", id)
	return d
}

func scoutInstructions(t *testing.T) string {
	t.Helper()
	agents, err := agent.Agents()
	require.NoError(t, err)
	require.Len(t, agents, 1)
	assert.Equal(t, "magus-scout", agents[0].Name)
	return agents[0].Instructions
}

// TestHarnessSpellsRenderTheShippedAgent pins the bytes each host's spell writes for
// magus-scout, since that file is what the host reads to route to the agent.
func TestHarnessSpellsRenderTheShippedAgent(t *testing.T) {
	instructions := scoutInstructions(t)
	quoted := `"` + scoutDescription + `"`

	cases := []struct {
		id   string
		want agent.HarnessAgentFile
	}{
		{"claude-code", agent.HarnessAgentFile{
			Path: ".claude/agents/magus-scout.md",
			Content: "---\nname: \"magus-scout\"\ndescription: " + quoted + "\n" +
				"tools: Read, Grep, Glob, Bash\nmodel: haiku\n---\n\n" + instructions,
		}},
		{"codex", agent.HarnessAgentFile{
			Path: ".codex/agents/magus-scout.toml",
			Content: "name = \"magus-scout\"\ndescription = " + quoted + "\n" +
				"developer_instructions = '''\n" + instructions + "'''\n",
			Hint: "magus-scout runs on agents.default_subagent_model; set it under [agents] in ~/.codex/config.toml to a cheaper model to run lookups for less",
		}},
		{"cursor", agent.HarnessAgentFile{
			Path: ".cursor/agents/magus-scout.md",
			Content: "---\nname: \"magus-scout\"\ndescription: " + quoted + "\n" +
				"model: inherit\nreadonly: true\n---\n\n" + instructions,
		}},
		{"opencode", agent.HarnessAgentFile{
			Path:    ".opencode/agents/magus-scout.md",
			Content: "---\ndescription: " + quoted + "\nmode: subagent\n---\n\n" + instructions,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			d := loadShippedHarness(t, tc.id)
			assert.Equal(t, []agent.HarnessAgentFile{tc.want}, d.Agents)
		})
	}
}

// TestHarnessSpellWithoutTheAgentsOpGetsNoAgents pins that harness_agents is optional: a spell
// that does not export it loads, ships no agent files, and was still offered the shipped
// agents in Params.
func TestHarnessSpellWithoutTheAgentsOpGetsNoAgents(t *testing.T) {
	const id = "agentless-test"
	var offered []any
	if _, ok := project.DefaultSpellRegistry().Lookup(id); !ok {
		project.DefaultSpellRegistry().RegisterSpell(spells.NewSpell(id, spells.WithInvoker(
			func(_ context.Context, req spells.InvokeRequest) (any, error) {
				switch req.Target {
				case spells.HarnessConfigContract:
					return map[string]any{"path": ""}, nil
				case spells.HarnessAgentsContract:
					offered, _ = req.Params["agents"].([]any)
				}
				return nil, nil
			})))
	}
	d := loadShippedHarness(t, id)
	assert.Empty(t, d.Agents)
	require.Len(t, offered, 1)
	assert.Equal(t, "magus-scout", offered[0].(map[string]any)["name"])
}

// TestDecodeHarnessAgents pins that a spell's harness_agents list reaches the descriptor
// field for field.
func TestDecodeHarnessAgents(t *testing.T) {
	t.Run("path and content decode", func(t *testing.T) {
		var d agent.HarnessDescriptor
		require.NoError(t, decodeHarnessAgents([]any{
			map[string]any{"path": ".cursor/agents/a.md", "content": "body\n"},
		}, &d))
		assert.Equal(t, []agent.HarnessAgentFile{{Path: ".cursor/agents/a.md", Content: "body\n"}}, d.Agents)
	})

	t.Run("anything but a list is refused", func(t *testing.T) {
		var d agent.HarnessDescriptor
		assert.Error(t, decodeHarnessAgents(map[string]any{"path": "a.md"}, &d))
		assert.Empty(t, d.Agents)
	})
}
