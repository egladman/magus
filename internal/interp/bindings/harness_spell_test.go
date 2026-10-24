package bindings

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/gen/typestest"
	"github.com/egladman/magus/libs/testkit/register"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
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

// harnessSource registers a harness spell under name whose Buzz source is the harness
// contract functions in body, beside the config every harness exports.
func harnessSource(t *testing.T, name, body string) string {
	t.Helper()
	return register.Spell(t, name, spells.WithFuncSource(fmt.Sprintf("import \"magus/spell\";\n"+
		"export fun mgs_getHarnessConfig() > HarnessConfig { return HarnessConfig{}; }\n%s", body)))
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
		want types.AgentFile
	}{
		{"claude-code", types.AgentFile{
			Path:    ".claude/agents/magus-scout.md",
			Content: fmt.Sprintf("---\nname: \"magus-scout\"\ndescription: %s\ntools: Read, Grep, Glob, Bash\n---\n\n%s", quoted, instructions),
		}},
		{"codex", types.AgentFile{
			Path:    ".codex/agents/magus-scout.toml",
			Content: fmt.Sprintf("name = \"magus-scout\"\ndescription = %s\nsandbox_mode = \"read-only\"\ndeveloper_instructions = '''\n%s'''\n", quoted, instructions),
			Hint:    "magus-scout runs on agents.default_subagent_model; set it under [agents] in ~/.codex/config.toml to a cheaper model to run lookups for less",
		}},
		{"cursor", types.AgentFile{
			Path:    ".cursor/agents/magus-scout.md",
			Content: fmt.Sprintf("---\nname: \"magus-scout\"\ndescription: %s\nmodel: inherit\nreadonly: true\n---\n\n%s", quoted, instructions),
		}},
		{"opencode", types.AgentFile{
			Path:    ".opencode/agents/magus-scout.md",
			Content: fmt.Sprintf("---\ndescription: %s\nmode: subagent\npermission:\n  edit: deny\n---\n\n%s", quoted, instructions),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			d := loadShippedHarness(t, tc.id)
			assert.Equal(t, []types.AgentFile{tc.want}, d.Agents)
		})
	}
}

// Every shipped harness declares a stdio MCP server running the checkout's own binary.
func TestHarnessSpellsDeclareAStdioMCPServer(t *testing.T) {
	for _, id := range []string{"claude-code", "codex", "cursor", "opencode"} {
		d := loadShippedHarness(t, id)
		require.NotNil(t, d.MCP, id)
		got := *d.MCP
		// The setup prose is each host's own; the server it describes is the same.
		got.Hint, got.Docs, got.Register = "", "", nil
		assert.Equal(t, types.McpServer{Transport: types.McpStdio, Command: []string{"./magus", "mcp"}}, got, id)
	}
	assert.Nil(t, loadShippedHarness(t, "claude-code-mod").MCP, "the mod declares no server")
}

// Every hook a shipped harness lists is rendered into its entries, the Buzz check among
// them, so verify's drift check passes.
func TestHarnessSpellsRenderEveryHookTheyList(t *testing.T) {
	for _, id := range []string{"claude-code", "codex", "cursor"} {
		d := loadShippedHarness(t, id)
		assert.Equal(t, agent.HarnessVerified, agentHookCheck(d).Status, "%s: %s", id, agentHookCheck(d).Reason)
		assert.True(t, strings.Contains(joinedScripts(d.Hooks), "magus-buzz-check"), id)
	}
	opencode := loadShippedHarness(t, "opencode")
	assert.Equal(t, "skipped: opencode declares no config path", agentHookCheck(opencode).Reason)
}

// agentHookCheck runs verify's hook check through VerifyHarness's own entry point: a
// descriptor wired as the only harness, against an empty workspace.
func agentHookCheck(d agent.HarnessDescriptor) agent.HarnessCheck {
	prev := agent.RegisterHarnessSpellLoader(func(_ context.Context, id string) (agent.HarnessDescriptor, string, bool, error) {
		return d, "spell:" + id, id == d.ID, nil
	})
	defer agent.RegisterHarnessSpellLoader(prev)
	result, err := agent.VerifyHarness(context.Background(), "/nonexistent-root", d.ID)
	if err != nil {
		return agent.HarnessCheck{Status: agent.HarnessInvalid, Reason: err.Error()}
	}
	return result.Hooks
}

func joinedScripts(hooks []types.HookCommand) string {
	scripts := make([]string, len(hooks))
	for i, h := range hooks {
		scripts[i] = h.Script
	}
	return strings.Join(scripts, " ")
}

// tieredWorkspace is a workspace that also answers the model tiers the render step asks for.
type tieredWorkspace struct {
	types.WorkspaceRepository
	set *types.ModelTierSet
}

func (w tieredWorkspace) ModelTiers(context.Context) *types.ModelTierSet { return w.set }

func tiersMapping(t *testing.T, hosts map[string][]string) *types.ModelTierSet {
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
	ws := typestest.NewMockWorkspaceRepository(t)
	ws.EXPECT().All().Return(nil).Maybe()
	return types.WithWorkspace(context.Background(), tieredWorkspace{WorkspaceRepository: ws, set: set})
}

// modelEcho renders each shipped agent as a file holding the model it was handed.
const modelEcho = `export fun mgs_renderAgents(agents: [ShippedAgent]) > [AgentFile] {
    return agents.map(fun (index: int, a: ShippedAgent) > AgentFile => AgentFile{path = "{a.name}.md", content = "model={a.model}"});
}
`

// TestHarnessSpellsOfferTheCheapestTierModel pins what the render step hands
// mgs_renderAgents: the alias the workspace's tiers map for THIS harness at rank 0, and no
// model when no alias resolves, so a spell never invents a model.
func TestHarnessSpellsOfferTheCheapestTierModel(t *testing.T) {
	tests := []struct {
		name string
		ctx  func(t *testing.T, id string) context.Context
		want string
	}{
		{"the harness's alias for the cheapest tier", func(t *testing.T, id string) context.Context {
			return tieredContext(t, tiersMapping(t, map[string][]string{id: {"tiny", "big"}, "other": {"mini", "max"}}))
		}, "tiny"},
		{"no workspace", func(*testing.T, string) context.Context { return context.Background() }, ""},
		{"tiers turned off", func(t *testing.T, _ string) context.Context { return tieredContext(t, nil) }, ""},
		{"a declaration that maps another harness", func(t *testing.T, _ string) context.Context {
			return tieredContext(t, tiersMapping(t, map[string][]string{"other": {"mini", "max"}}))
		}, ""},
		{"a declaration that maps no harness", func(t *testing.T, _ string) context.Context {
			return tieredContext(t, tiersMapping(t, nil))
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := harnessSource(t, "modelled-test", modelEcho)
			d, _, ok, err := loadHarnessFromSpell(tt.ctx(t, id), id)
			require.NoError(t, err)
			require.True(t, ok)
			assert.Equal(t, []types.AgentFile{{Path: "magus-scout.md", Content: "model=" + tt.want}}, d.Agents)
		})
	}
}

// A spell that exports only the config loads as a harness whose host lacks every other
// capability: no agents, no MCP, no tools, no prompts, no models.
func TestHarnessSpellWithOnlyItsConfigLacksEveryOtherCapability(t *testing.T) {
	d := loadShippedHarness(t, harnessSource(t, "bare-test", ""))
	assert.Empty(t, d.Agents)
	assert.Empty(t, d.Prompts)
	assert.Empty(t, d.Models)
	assert.Nil(t, d.MCP)
	assert.Nil(t, d.Tools)
	assert.Zero(t, d.Limits)
}

// A spell that does not export mgs_getHarnessConfig is not a harness, and the loader says
// so with a miss rather than an error.
func TestASpellWithoutTheConfigIsNoHarness(t *testing.T) {
	id := register.Spell(t, "not-a-harness-test", spells.WithFuncSource(`export fun mgs_getName() > str { return "x"; }`))
	_, _, ok, err := loadHarnessFromSpell(context.Background(), id)
	require.NoError(t, err)
	assert.False(t, ok)
}

// An answer of the wrong shape is refused strictly, naming the function.
func TestHarnessFunctionsDecodeStrictly(t *testing.T) {
	id := register.Spell(t, "malformed-test", spells.WithFuncSource(`export fun mgs_getHarnessConfig() > any { return {"path": 3}; }`))
	_, _, _, err := loadHarnessFromSpell(context.Background(), id)
	require.ErrorIs(t, err, errMalformedAnswer)
	assert.Contains(t, err.Error(), "mgs_getHarnessConfig")
}

// TestHarnessPromptsAskBeforeAHumanOnlyRun pins that the native prompts of the hosts whose
// hooks cannot ask carry each target the workspace declares human_only, with its reason
// where the host shows one, and that a load with no workspace prompts for pushes alone.
func TestHarnessPromptsAskBeforeAHumanOnlyRun(t *testing.T) {
	for _, id := range []string{"codex", "opencode"} {
		loadShippedHarness(t, id)
	}
	ws := typestest.NewMockWorkspaceRepository(t)
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

// TestHarnessSpellsDeclareForegroundLimits pins the one host whose shell tool backgrounds a
// command past its timeout, and whose wiring sets the hook timeout the guard reports
// against, and that every other shipped harness declares no limits.
func TestHarnessSpellsDeclareForegroundLimits(t *testing.T) {
	assert.Equal(t, types.Limits{ForegroundDefaultMs: 120000, ForegroundMaxMs: 600000, HookTimeoutMs: 10000},
		loadShippedHarness(t, "claude-code").Limits)
	for _, id := range []string{"codex", "cursor", "opencode"} {
		assert.Zero(t, loadShippedHarness(t, id).Limits, id)
	}
}

func TestHarnessSpellsShipModelRowsOnlyForClaudeCode(t *testing.T) {
	rows := loadShippedHarness(t, "claude-code").Models
	assert.Equal(t, []types.ModelPricing{
		{ID: "claude-fable-5-1", Window: 1_000_000, Input: 10, CacheWrite5m: 12.5, CacheWrite1h: 20, CacheRead: 0.25, Output: 50},
		{ID: "claude-opus-5-5", Tier: "principal", Alias: "opus", Window: 1_000_000, Input: 4, CacheWrite5m: 5, CacheWrite1h: 8, CacheRead: 0.2, Output: 20},
		{ID: "claude-sonnet-5-5", Tier: "standard", Alias: "sonnet", Window: 1_000_000, Input: 2, CacheWrite5m: 2.5, CacheWrite1h: 4, CacheRead: 0.1, Output: 10},
		{ID: "claude-haiku-5-5", Tier: "economy", Alias: "haiku", Window: 1_000_000, Input: 0.1, CacheWrite5m: 0.125, CacheWrite1h: 0.2, CacheRead: 0.01, Output: 0.5,
			LongContext: &types.PriceStep{Above: 100_000, Multiplier: 5}},
		{ID: "claude-haiku-4-5", Window: 200_000, Input: 1, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.1, Output: 5},
	}, rows)
	for _, id := range []string{"codex", "cursor", "opencode"} {
		assert.Empty(t, loadShippedHarness(t, id).Models, id)
	}
}

func TestValidateModelRowsReadsPricesAndTiers(t *testing.T) {
	got, tiers, err := validateModelRows([]types.ModelRow{{
		ID: "m-1", Tier: "standard", Alias: "mid", Window: 200000,
		Price: types.Price{Input: 1.5, CacheWrite5m: 2, CacheWrite1h: 3, CacheRead: 0.1, Output: 7},
		Step:  types.PriceStep{Above: 100000, Multiplier: 2},
	}})

	require.NoError(t, err)
	assert.Equal(t, []types.ModelPricing{{ID: "m-1", Tier: "standard", Alias: "mid", Window: 200000, Input: 1.5, CacheWrite5m: 2, CacheWrite1h: 3,
		CacheRead: 0.1, Output: 7, LongContext: &types.PriceStep{Above: 100000, Multiplier: 2}}}, got)
	assert.Equal(t, map[string]string{"standard": "m-1"}, tiers)

	_, tiers, err = validateModelRows([]types.ModelRow{{ID: "m-1"}})
	require.NoError(t, err)
	assert.Nil(t, tiers, "no row names a tier")
}

func TestValidateModelRowsRefusesWhatNamesNoOneModel(t *testing.T) {
	tests := map[string]struct {
		rows []types.ModelRow
		want string
	}{
		"step without multiplier": {[]types.ModelRow{{ID: "m-1", Step: types.PriceStep{Above: 100000}}},
			"row 1 (m-1) sets one of the price step's above and multiplier without the other"},
		"multiplier without step": {[]types.ModelRow{{ID: "m-1", Step: types.PriceStep{Multiplier: 2}}},
			"row 1 (m-1) sets one of the price step's above and multiplier without the other"},
		"alias without a tier": {[]types.ModelRow{{ID: "m-1", Alias: "mid"}},
			"row 1 (m-1) sets alias mid without a tier, so the alias names no tier"},
		"one alias on two rows": {[]types.ModelRow{{ID: "m-1", Tier: "standard", Alias: "mid"}, {ID: "m-2", Tier: "economy", Alias: "mid"}},
			"rows m-1 and m-2 both declare alias mid"},
		"one tier on two rows": {[]types.ModelRow{{ID: "m-1", Tier: "economy"}, {ID: "m-2", Tier: "economy"}},
			"rows m-1 and m-2 both declare tier economy"},
		"a row without an id": {[]types.ModelRow{{Window: 1000}}, "row 1 has no id"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, err := validateModelRows(tt.rows)
			assert.EqualError(t, err, tt.want)
		})
	}
}

// The Claude Code rows name the exact model id each shipped tier runs on, and each is a row
// of its own table, so a reported model resolves to a tier by equality and never by a name.
func TestClaudeCodeRowsNameAModelForEveryShippedTier(t *testing.T) {
	d := loadShippedHarness(t, "claude-code")
	drv, ok := project.DefaultSpellRegistry().Lookup("claude-code")
	require.True(t, ok)
	rows, _, err := callHarness[[]types.ModelRow](context.Background(), drv, "mgs_listModels")
	require.NoError(t, err)
	_, tiers, err := validateModelRows(rows)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"economy": "claude-haiku-5-5", "standard": "claude-sonnet-5-5", "principal": "claude-opus-5-5",
	}, tiers)
	ids := map[string]bool{}
	for _, row := range d.Models {
		ids[row.ID] = true
	}
	for tier, id := range tiers {
		assert.True(t, ids[id], "tier %s runs %s, which is a row", tier, id)
	}
}

// A declaration that does not map the harness falls to the harness's own rows, and rows
// that declare tiers without the cheapest one are an error naming the row to add, never a
// render with no model.
func TestCheapestTierModelReadsTheHarnessRows(t *testing.T) {
	ctx := tieredContext(t, tiersMapping(t, map[string][]string{"other": {"mini", "max"}}))

	model, err := cheapestTierModel(ctx, "rowed", map[string]string{"small": "m-small-1", "large": "m-large-1"})
	require.NoError(t, err)
	assert.Equal(t, "m-small-1", model, "the exact id the harness row names for the cheapest tier")

	model, err = cheapestTierModel(ctx, "rowless", nil)
	require.NoError(t, err)
	assert.Empty(t, model, "a harness that declares no tier takes no per-agent model")

	_, err = cheapestTierModel(ctx, "rowed", map[string]string{"large": "m-large-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `add tier = "small"`)
	assert.Contains(t, err.Error(), "magusfile.buzz:3")

	mapped := tieredContext(t, tiersMapping(t, map[string][]string{"rowed": {"tiny", "big"}}))
	model, err = cheapestTierModel(mapped, "rowed", map[string]string{"small": "m-small-1"})
	require.NoError(t, err)
	assert.Equal(t, "tiny", model, "the workspace's own mapping wins over the harness rows")
}
