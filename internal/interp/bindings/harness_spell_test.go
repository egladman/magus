package bindings

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/libs/testkit/register"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/gen/mocks"
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
	agents, err := agent.ShippedAgents()
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
				"tools: Read, Grep, Glob, Bash\n---\n\n" + instructions,
		}},
		{"codex", agent.HarnessAgentFile{
			Path: ".codex/agents/magus-scout.toml",
			Content: "name = \"magus-scout\"\ndescription = " + quoted + "\n" +
				"sandbox_mode = \"read-only\"\n" +
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
			Content: "---\ndescription: " + quoted + "\nmode: subagent\npermission:\n  edit: deny\n---\n\n" + instructions,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			d := loadShippedHarness(t, tc.id)
			assert.Equal(t, []agent.HarnessAgentFile{tc.want}, d.Agents)
		})
	}
}

// tieredWorkspace is a workspace that also answers the model tiers the render step asks for.
type tieredWorkspace struct {
	types.WorkspaceRepository
	set *types.ModelTierSet
}

func (w tieredWorkspace) ModelTiers() *types.ModelTierSet { return w.set }

func tiersMapping(t *testing.T, hosts map[types.HarnessKey][]string) *types.ModelTierSet {
	t.Helper()
	set, err := types.NewModelTierSet(types.ModelTierDeclaration{
		Enum: "ModelTier", Site: "magusfile.buzz:3", Cases: []string{"small", "large"}, Default: 1,
		Bounds: []types.ModelTierBounds{{}, {}},
		Skills: []types.SkillVariant{types.SkillVariantFull, types.SkillVariantShort},
		Hosts:  hosts,
	})
	require.NoError(t, err)
	return set
}

func tieredContext(t *testing.T, set *types.ModelTierSet) context.Context {
	t.Helper()
	ws := mocks.NewMockWorkspaceRepository(t)
	ws.EXPECT().All().Return(nil).Maybe()
	return types.WithWorkspace(context.Background(), tieredWorkspace{WorkspaceRepository: ws, set: set})
}

// TestHarnessSpellsOfferTheCheapestTierModel pins what the render step hands harness_agents:
// the alias the workspace's tiers map for THIS harness at rank 0, and no model key when no
// alias resolves, so a spell never invents a model.
func TestHarnessSpellsOfferTheCheapestTierModel(t *testing.T) {
	tests := []struct {
		name string
		ctx  func(t *testing.T, id string) context.Context
		want any
	}{
		{"the harness's alias for the cheapest tier", func(t *testing.T, id string) context.Context {
			return tieredContext(t, tiersMapping(t, map[types.HarnessKey][]string{{ID: id}: {"tiny", "big"}, {ID: "other"}: {"mini", "max"}}))
		}, "tiny"},
		{"no workspace", func(*testing.T, string) context.Context { return context.Background() }, nil},
		{"tiers turned off", func(t *testing.T, _ string) context.Context { return tieredContext(t, nil) }, nil},
		{"a declaration that maps another harness", func(t *testing.T, _ string) context.Context {
			return tieredContext(t, tiersMapping(t, map[types.HarnessKey][]string{{ID: "other"}: {"mini", "max"}}))
		}, nil},
		{"a declaration that maps no harness", func(t *testing.T, _ string) context.Context {
			return tieredContext(t, tiersMapping(t, nil))
		}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var offered []any
			id := register.Spell(t, "modelled-test", spells.WithInvoker(
				func(_ context.Context, req spells.InvokeRequest) (any, error) {
					switch req.Target {
					case spells.HarnessConfigContract:
						return map[string]any{"path": ""}, nil
					case spells.HarnessAgentsContract:
						offered, _ = req.Params["agents"].([]any)
					}
					return nil, nil
				}))
			_, _, ok, err := loadHarnessFromSpell(tt.ctx(t, id), id)
			require.NoError(t, err)
			require.True(t, ok)
			require.Len(t, offered, 1)
			params := offered[0].(map[string]any)
			assert.NotContains(t, params, "class")
			if tt.want == nil {
				assert.NotContains(t, params, "model")
				return
			}
			assert.Equal(t, tt.want, params["model"])
		})
	}
}

// TestHarnessSpellWithoutTheAgentsOpGetsNoAgents pins that harness_agents is optional: a spell
// that does not export it loads, ships no agent files, and was still offered the shipped
// agents in Params.
func TestHarnessSpellWithoutTheAgentsOpGetsNoAgents(t *testing.T) {
	var offered []any
	id := register.Spell(t, "agentless-test", spells.WithInvoker(
		func(_ context.Context, req spells.InvokeRequest) (any, error) {
			switch req.Target {
			case spells.HarnessConfigContract:
				return map[string]any{"path": ""}, nil
			case spells.HarnessAgentsContract:
				offered, _ = req.Params["agents"].([]any)
			}
			return nil, nil
		}))
	d := loadShippedHarness(t, id)
	assert.Empty(t, d.Agents)
	require.Len(t, offered, 1)
	assert.Equal(t, "magus-scout", offered[0].(map[string]any)["name"])
}

// TestHarnessPromptsAskBeforeAHumanOnlyRun pins that the native prompts of the hosts whose
// hooks cannot ask carry each target the workspace declares human_only, with its reason
// where the host shows one, and that a load with no workspace prompts for pushes alone.
func TestHarnessPromptsAskBeforeAHumanOnlyRun(t *testing.T) {
	for _, id := range []string{"codex", "opencode"} {
		loadShippedHarness(t, id)
	}
	ws := mocks.NewMockWorkspaceRepository(t)
	ws.EXPECT().All().Return([]*types.Project{{Path: ".", TargetPolicies: map[string]types.Target{
		"agent-bench-run": {HumanOnly: true, HumanOnlyReason: "launches paid sessions"},
		"lint":            {Slots: 2},
	}}}).Maybe()
	ctx := types.WithWorkspace(context.Background(), ws)

	codex, _, ok, err := loadHarnessFromSpell(ctx, "codex")
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, codex.Prompts, 1)
	rules := codex.Prompts[0].Content
	for _, pattern := range []string{`["magus", "run", "agent-bench-run"]`, `["./magus", "run", "agent-bench-run"]`,
		`["magus", "affected", "agent-bench-run"]`, `["./magus", "affected", "agent-bench-run"]`} {
		assert.Contains(t, rules, "pattern = "+pattern)
	}
	assert.Contains(t, rules, `justification = "magus: a person runs this target: launches paid sessions"`)
	assert.NotContains(t, rules, `"lint"`)

	opencode, _, ok, err := loadHarnessFromSpell(ctx, "opencode")
	require.NoError(t, err)
	require.True(t, ok)
	var keys []string
	for _, p := range opencode.Prompts {
		keys = append(keys, strings.Join(p.Key, "."))
	}
	assert.Contains(t, keys, "permission.bash.magus run agent-bench-run")
	assert.Contains(t, keys, "permission.bash../magus affected agent-bench-run *")

	bare, _, _, err := loadHarnessFromSpell(context.Background(), "codex")
	require.NoError(t, err)
	assert.NotContains(t, bare.Prompts[0].Content, `"run"`, "no workspace on the context, no run prompt")
}

func TestDecodeHarnessJSONReadsAListFieldForField(t *testing.T) {
	var d agent.HarnessDescriptor

	require.NoError(t, decodeHarnessJSON([]any{
		map[string]any{"path": ".cursor/agents/a.md", "content": "body\n"},
	}, &d.Agents))

	assert.Equal(t, []agent.HarnessAgentFile{{Path: ".cursor/agents/a.md", Content: "body\n"}}, d.Agents)
}

func TestDecodeHarnessJSONRefusesAMapForAListField(t *testing.T) {
	var d agent.HarnessDescriptor

	assert.Error(t, decodeHarnessJSON(map[string]any{"path": "a.md"}, &d.Agents))
	assert.Empty(t, d.Agents)
}

// TestHarnessSpellsDeclareForegroundLimits pins the one host whose shell tool backgrounds a
// command past its timeout, and whose wiring sets the hook timeout the guard reports
// against, and that every other shipped harness declares no limits.
func TestHarnessSpellsDeclareForegroundLimits(t *testing.T) {
	assert.Equal(t, agent.HarnessLimits{ForegroundDefaultMs: 120000, ForegroundMaxMs: 600000, HookTimeoutMs: 10000},
		loadShippedHarness(t, "claude-code").Limits)
	for _, id := range []string{"codex", "cursor", "opencode"} {
		assert.Zero(t, loadShippedHarness(t, id).Limits, id)
	}
}

func TestDecodeHarnessJSONReadsAMapField(t *testing.T) {
	var d agent.HarnessDescriptor

	require.NoError(t, decodeHarnessJSON(map[string]any{"foreground_default_ms": int64(1000), "foreground_max_ms": 2000.0}, &d.Limits))

	assert.Equal(t, agent.HarnessLimits{ForegroundDefaultMs: 1000, ForegroundMaxMs: 2000}, d.Limits)
}

func TestDecodeHarnessJSONRefusesAListForAMapField(t *testing.T) {
	var d agent.HarnessDescriptor

	assert.Error(t, decodeHarnessJSON([]any{}, &d.Limits))
	assert.Zero(t, d.Limits)
}

func TestHarnessSpellsShipModelRowsOnlyForClaudeCode(t *testing.T) {
	rows := loadShippedHarness(t, "claude-code").Models
	assert.Equal(t, []types.ModelPricing{
		{ID: "claude-fable-5-1", Window: 1_000_000, Input: 10, CacheWrite5m: 12.5, CacheWrite1h: 20, CacheRead: 0.25, Output: 50},
		{ID: "claude-opus-5-5", Window: 1_000_000, Input: 4, CacheWrite5m: 5, CacheWrite1h: 8, CacheRead: 0.2, Output: 20},
		{ID: "claude-sonnet-5-5", Window: 1_000_000, Input: 2, CacheWrite5m: 2.5, CacheWrite1h: 4, CacheRead: 0.1, Output: 10},
		{ID: "claude-haiku-5-5", Window: 1_000_000, Input: 0.1, CacheWrite5m: 0.125, CacheWrite1h: 0.2, CacheRead: 0.01, Output: 0.5,
			LongContext: &types.PriceStep{Above: 100_000, Multiplier: 5}},
		{ID: "claude-haiku-4-5", Window: 200_000, Input: 1, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.1, Output: 5},
	}, rows)
	for _, id := range []string{"codex", "cursor", "opencode"} {
		assert.Empty(t, loadShippedHarness(t, id).Models, id)
	}
}

func TestDecodeHarnessModelsReadsSnakeCaseRows(t *testing.T) {
	got, err := decodeHarnessModels([]any{map[string]any{
		"id": "m-1", "window": 200000.0, "input": 1.5, "cache_write_5m": 2.0, "cache_write_1h": 3.0,
		"cache_read": 0.1, "output": 7.0, "price_step": 100000.0, "step_factor": 2.0,
	}})

	require.NoError(t, err)
	assert.Equal(t, []types.ModelPricing{{ID: "m-1", Window: 200000, Input: 1.5, CacheWrite5m: 2, CacheWrite1h: 3,
		CacheRead: 0.1, Output: 7, LongContext: &types.PriceStep{Above: 100000, Multiplier: 2}}}, got)
}

func TestDecodeHarnessModelsRefusesAHalfStep(t *testing.T) {
	for name, row := range map[string]map[string]any{
		"step without factor": {"id": "m-1", "price_step": 100000.0},
		"factor without step": {"id": "m-1", "step_factor": 2.0},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decodeHarnessModels([]any{row})

			assert.EqualError(t, err, "row 1 (m-1) sets one of price_step and step_factor without the other")
		})
	}
}

func TestDecodeHarnessModelsRefusesARowWithoutAnID(t *testing.T) {
	_, err := decodeHarnessModels([]any{map[string]any{"window": 1000.0}})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "row 1 has no id")
}

func TestDecodeHarnessModelsRefusesAMap(t *testing.T) {
	_, err := decodeHarnessModels(map[string]any{"id": "m-1"})

	assert.Error(t, err)
}
