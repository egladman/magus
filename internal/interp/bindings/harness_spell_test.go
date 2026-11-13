package bindings

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/gen/typestest"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/testkit/register"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	typesagent "github.com/egladman/magus/types/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
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
	if !strings.Contains(body, "fun mgs_renderPrompts") {
		body += "\nexport fun mgs_renderPrompts(humanOnly: {str: str}) > [Prompt] { return []; }\n"
	}
	return register.Spell(t, name, spells.WithFuncSource(fmt.Sprintf("import \"magus/spell\";\n"+
		"export fun mgs_getHarness() > Harness { return Harness{config = HarnessConfig{}, guard = \"\"}; }\n%s", body)))
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
			Content: fmt.Sprintf("---\nname: \"magus-scout\"\ndescription: %s\ndisallowedTools: Write, Edit, MultiEdit, NotebookEdit, mcp__ccd_host__sync_with_base_branch, mcp__ccd_host__clean_up_worktrees, mcp__ccd_host__discard_kept_worktree\n---\n\n%s", quoted, instructions),
		}},
		{"codex", types.AgentFile{
			Path:    ".codex/agents/magus-scout.toml",
			Content: fmt.Sprintf("name = \"magus-scout\"\ndescription = %s\ndeveloper_instructions = '''\n%s'''\n", quoted, instructions),
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

type hookEntry struct {
	Matcher string `json:"matcher"`
	Hooks   []struct {
		Command string `json:"command"`
	} `json:"hooks"`
}

type hookSettings struct {
	Hooks map[string][]hookEntry `json:"hooks"`
}

// renderHarnessHookConfig uses the production plan and installer over the descriptor
// compiled from the canonical Buzz spell. Its empty temp root makes the resulting file
// independent of whichever host wiring happens to be installed in this checkout.
func renderHarnessHookConfig(t *testing.T, id string) (string, agent.HarnessDescriptor) {
	t.Helper()
	d := loadShippedHarness(t, id)
	previous := agent.RegisterHarnessSpellLoader(func(_ context.Context, requested string) (agent.HarnessDescriptor, string, bool, error) {
		return d, "spell:" + id, requested == id, nil
	})
	t.Cleanup(func() { agent.RegisterHarnessSpellLoader(previous) })

	root := t.TempDir()
	ctx := agent.ContextWithWiredHarnesses(context.Background(), []string{id})
	plan, err := agent.PlanHarness(ctx, root, id)
	require.NoError(t, err)
	_, err = agent.ApplyHarnessInstallation(ctx, root, plan, false)
	require.NoError(t, err)
	require.NotEmpty(t, d.Config.Path, "%s has no hook config", id)
	return filepath.Join(root, d.Config.Path), d
}

func configJobs(t *testing.T, path string, declared []types.HookCommand) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "read %s", path)
	var cfg hookSettings
	require.NoError(t, json.Unmarshal(raw, &cfg), "parse %s", path)
	require.NotEmpty(t, cfg.Hooks, "%s wires no events at all", path)

	found := map[string]bool{}
	for _, entries := range cfg.Hooks {
		for _, entry := range entries {
			for _, h := range entry.Hooks {
				for _, hook := range declared {
					name := strings.TrimSuffix(filepath.Base(hook.Script), ".buzz")
					if name == "" || !strings.Contains(h.Command, name+".buzz") {
						continue
					}
					if strings.Contains(entry.Matcher, "mcp__") {
						name += " on mcp__magus__"
					}
					found[name] = true
				}
			}
		}
	}
	require.NotEmpty(t, found, "%s recognizes no declared hook", path)
	return found
}

var hookConfigExemptions = map[string]map[string]string{
	"codex": {
		"magus-duration": "no codex hook schema under testdata/hosts/codex carries a tool duration, and the " +
			"recorder writes nothing for an event without one",
		"magus-session": "SessionStart hands a codex hook no env file and reads back only additionalContext " +
			"(testdata/hosts/codex/session-start.command.{input,output}.schema.json), so no hook can put the " +
			"checkout root on PATH for later shell commands",
		"magus-observe": "Codex has no read tool: a file is read through a shell command, which the command " +
			"rules already judge, so no PreToolUse matcher exists for the read record to ride",
	},
}

func TestShippedHookConfigsWireTheSameJobs(t *testing.T) {
	wired := map[string]map[string]bool{}
	paths := map[string]string{}
	for _, id := range []string{"claude-code", "codex"} {
		path, d := renderHarnessHookConfig(t, id)
		paths[id] = path
		wired[id] = configJobs(t, path, d.Hooks)
	}

	for host, templates := range wired {
		for other, otherTemplates := range wired {
			if other == host {
				continue
			}
			for name := range otherTemplates {
				if templates[name] {
					continue
				}
				why, exempt := hookConfigExemptions[host][name]
				assert.True(t, exempt,
					"%s runs %s and %s does not.\n"+
						"Wire it there too, or record why that host does without it in hookConfigExemptions.\n"+
						"A host doing less than another is a decision; a host doing less than another with\n"+
						"nothing saying so is the gap this gate exists to refuse.",
					paths[other], name, paths[host])
				if exempt {
					t.Logf("%s does not run %s: %s", host, name, why)
				}
			}
		}
	}

	for host, exemptions := range hookConfigExemptions {
		for name := range exemptions {
			assert.False(t, wired[host][name],
				"hookConfigExemptions says %s does not run %s, but %s invokes it. Drop the exemption.",
				host, name, paths[host])
		}
	}

	// Every input reaches one glue through one entry: both spells wire every tool call
	// to magus-command.buzz under the catch-all matcher, so no second entry judges it.
	for host, path := range paths {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		var cfg hookSettings
		require.NoError(t, json.Unmarshal(raw, &cfg), "parse %s", path)
		var judging []string
		for _, entry := range cfg.Hooks["PreToolUse"] {
			for _, h := range entry.Hooks {
				assert.NotContains(t, h.Command, "magus-path.buzz",
					"%s: a write reaches the catch-all entry whole; a path entry judges it twice", host)
				if strings.Contains(h.Command, "magus-command.buzz") {
					judging = append(judging, entry.Matcher)
				}
			}
		}
		assert.Equal(t, []string{"*"}, judging, "%s: one PreToolUse entry runs the command glue, on every tool", host)
	}
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
	set *typesagent.ModelTierSet
}

func (w tieredWorkspace) ModelTiers(context.Context) *typesagent.ModelTierSet { return w.set }

func tiersMapping(t *testing.T, hosts map[string][]string) *typesagent.ModelTierSet {
	t.Helper()
	set, err := typesagent.NewModelTierSet(typesagent.ModelTierDeclaration{
		Enum: "ModelTier", Site: "magusfile.buzz:3", Cases: []string{"small", "large"}, Default: 1,
		Bounds: []typesagent.ModelTierBounds{{}, {}},
		Skills: []typesagent.SkillVariant{typesagent.SkillVariantFull, typesagent.SkillVariantShort},
		Hosts:  hosts,
	})
	require.NoError(t, err)
	return set
}

func tieredContext(t *testing.T, set *typesagent.ModelTierSet) context.Context {
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

// A spell that exports the config and the required prompts function, and nothing else,
// loads as a harness whose host lacks every other capability: no agents, no MCP, no
// tools, no prompt files, no models.
func TestHarnessSpellWithOnlyItsConfigLacksEveryOtherCapability(t *testing.T) {
	d := loadShippedHarness(t, harnessSource(t, "bare-test", ""))
	assert.Empty(t, d.Agents)
	assert.Empty(t, d.Prompts)
	assert.Empty(t, d.Models)
	assert.Nil(t, d.MCP)
	assert.Nil(t, d.Tools)
	assert.Zero(t, d.Limits)
}

// A harness that omits mgs_renderPrompts is refused. The empty list is a declared answer
// for a host whose hook prompts; an omission used to decode as that same list.
func TestAHarnessThatOmitsRenderPromptsIsRefused(t *testing.T) {
	id := register.Spell(t, "no-prompts-test", spells.WithFuncSource(`import "magus/spell";
export fun mgs_getHarness() > Harness { return Harness{config = HarnessConfig{}, guard = ""}; }`))
	_, _, ok, err := loadHarnessFromSpell(context.Background(), id)
	require.Error(t, err)
	assert.False(t, ok)
	assert.Contains(t, err.Error(), "mgs_renderPrompts")
}

// A spell that does not export mgs_getHarness is not a harness, and the loader says
// so with a miss rather than an error.
func TestASpellWithoutTheConfigIsNoHarness(t *testing.T) {
	id := register.Spell(t, "not-a-harness-test", spells.WithFuncSource(`export fun mgs_getName() > str { return "x"; }`))
	_, _, ok, err := loadHarnessFromSpell(context.Background(), id)
	require.NoError(t, err)
	assert.False(t, ok)
}

// An answer of the wrong shape is refused strictly, naming the function.
func TestHarnessFunctionsDecodeStrictly(t *testing.T) {
	id := register.Spell(t, "malformed-test", spells.WithFuncSource(`export fun mgs_getHarness() > any { return {"path": 3}; }`))
	_, _, _, err := loadHarnessFromSpell(context.Background(), id)
	require.ErrorIs(t, err, errMalformedAnswer)
	assert.Contains(t, err.Error(), "mgs_getHarness")
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

	for _, id := range []string{"cursor", "claude-code", "claude-code-mod"} {
		loadShippedHarness(t, id)
		d, _, ok, err := loadHarnessFromSpell(ctx, id)
		require.NoError(t, err)
		require.True(t, ok)
		assert.Empty(t, d.Prompts, id)
	}
}

// TestHarnessSpellsDeclareForegroundLimits pins the one host whose shell tool backgrounds a
// command past its timeout, and whose wiring sets the hook timeout the guard reports
// against, and the reply channels each host declares.
func TestHarnessSpellsDeclareForegroundLimits(t *testing.T) {
	assert.Equal(t, types.Limits{ForegroundDefaultMs: 120000, ForegroundMaxMs: 600000, HookTimeoutMs: 10000, ResumesOnBackgroundFrom: "2.1.299", ObservesSkillLoads: true,
		PayloadFields: []string{"prompt_id", "effort", "scratchpad_dir"}, Channels: []string{"display", "capture", "notice"}},
		loadShippedHarness(t, "claude-code").Limits)
	assert.Equal(t, types.Limits{HookTimeoutMs: 10000, ObservesSkillLoads: true,
		PayloadFields: []string{"cursor_version", "conversation_id", "generation_id", "workspace_roots"}, Channels: []string{"capture", "notice"}},
		loadShippedHarness(t, "cursor").Limits)
	assert.Equal(t, types.Limits{PayloadFields: []string{"turn_id"}, Channels: []string{"display", "capture", "notice"}}, loadShippedHarness(t, "codex").Limits)
	assert.Equal(t, types.Limits{Channels: []string{"display"}}, loadShippedHarness(t, "opencode").Limits)
}

// TestNoTwoShippedHarnessesClaimOnePayloadField holds the payload fingerprints
// to their meaning: a field two hosts send names neither.
func TestNoTwoShippedHarnessesClaimOnePayloadField(t *testing.T) {
	owner := map[string]string{}
	for _, id := range shippedHarnessIDs(t) {
		for _, f := range loadShippedHarness(t, id).Limits.PayloadFields {
			assert.Empty(t, owner[f], "%s and %s both claim payload field %s", owner[f], id, f)
			owner[f] = id
		}
	}
}

const channelsHook = "export fun mgs_listHooks() > [HookCommand] { return [HookCommand{event = \"PreToolUse\", script = \"magus-command\"}]; }\n"

// A harness that wires a hook and declares no channels is refused: nothing would say who
// reads its replies.
func TestAHookedHarnessWithNoChannelsIsRefused(t *testing.T) {
	id := harnessSource(t, "no-channels-test", channelsHook)
	_, _, ok, err := loadHarnessFromSpell(context.Background(), id)
	require.Error(t, err)
	assert.False(t, ok)
	assert.Contains(t, err.Error(), "declares no channels")
}

func TestAHarnessWithAnUnknownChannelIsRefused(t *testing.T) {
	id := harnessSource(t, "odd-channel-test", channelsHook+
		"export fun mgs_getLimits() > Limits { return Limits{channels = [\"capture\", \"pager\"]}; }\n")
	_, _, _, err := loadHarnessFromSpell(context.Background(), id)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown channel "pager"`)
}

func TestHarnessSpellsShipModelRowsOnlyForClaudeCode(t *testing.T) {
	rows := loadShippedHarness(t, "claude-code").Models
	assert.Equal(t, []types.ModelPricing{
		{ID: "claude-fable-5-1", Window: 1_000_000, Input: 10, CacheWrite5m: 12.5, CacheWrite1h: 20, CacheRead: 0.25, Output: 50},
		{ID: "claude-opus-5-5", Tier: "design", Alias: "opus", Window: 1_000_000, Input: 4, CacheWrite5m: 5, CacheWrite1h: 8, CacheRead: 0.2, Output: 20},
		{ID: "claude-fable-5-2", Tier: "frontier", Alias: "fable", Window: 1_000_000, Input: 10, CacheWrite5m: 12.5, CacheWrite1h: 20, CacheRead: 0.25, Output: 50},
		{ID: "claude-sonnet-5-5", Tier: "bounded", Alias: "sonnet", Window: 1_000_000, Input: 2, CacheWrite5m: 2.5, CacheWrite1h: 4, CacheRead: 0.1, Output: 10},
		{ID: "claude-haiku-5-5", Tier: "routine", Alias: "haiku", Window: 1_000_000, Input: 0.1, CacheWrite5m: 0.125, CacheWrite1h: 0.2, CacheRead: 0.01, Output: 0.5,
			LongContext: &types.PriceStep{Above: 100_000, Multiplier: 5}},
		{ID: "claude-haiku-4-5", Window: 200_000, Input: 1, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.1, Output: 5},
	}, rows)
	for _, id := range []string{"codex", "cursor", "opencode"} {
		assert.Empty(t, loadShippedHarness(t, id).Models, id)
	}
}

func TestValidateModelRowsReadsPricesAndTiers(t *testing.T) {
	got, tiers, err := validateModelRows([]types.HarnessModel{{
		ID: "m-1", Tier: "bounded", Alias: "mid", Window: 200000,
		Price: types.Price{Input: 1.5, CacheWrite5m: 2, CacheWrite1h: 3, CacheRead: 0.1, Output: 7},
		Step:  types.PriceStep{Above: 100000, Multiplier: 2},
	}})

	require.NoError(t, err)
	assert.Equal(t, []types.ModelPricing{{ID: "m-1", Tier: "bounded", Alias: "mid", Window: 200000, Input: 1.5, CacheWrite5m: 2, CacheWrite1h: 3,
		CacheRead: 0.1, Output: 7, LongContext: &types.PriceStep{Above: 100000, Multiplier: 2}}}, got)
	assert.Equal(t, map[string][]string{"bounded": {"m-1"}}, tiers)

	_, tiers, err = validateModelRows([]types.HarnessModel{{ID: "m-1"}})
	require.NoError(t, err)
	assert.Nil(t, tiers, "no row names a tier")
}

func TestValidateModelRowsRefusesWhatNamesNoOneModel(t *testing.T) {
	tests := map[string]struct {
		rows []types.HarnessModel
		want string
	}{
		"step without multiplier": {[]types.HarnessModel{{ID: "m-1", Step: types.PriceStep{Above: 100000}}},
			"row 1 (m-1) sets one of the price step's above and multiplier without the other"},
		"multiplier without step": {[]types.HarnessModel{{ID: "m-1", Step: types.PriceStep{Multiplier: 2}}},
			"row 1 (m-1) sets one of the price step's above and multiplier without the other"},
		"alias without a tier": {[]types.HarnessModel{{ID: "m-1", Alias: "mid"}},
			"row 1 (m-1) sets alias mid without a tier, so the alias names no tier"},
		"one alias on two rows": {[]types.HarnessModel{{ID: "m-1", Tier: "bounded", Alias: "mid"}, {ID: "m-2", Tier: "routine", Alias: "mid"}},
			"rows m-1 and m-2 both declare alias mid"},
		"a row without an id": {[]types.HarnessModel{{Window: 1000}}, "row 1 has no id"},
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
	rows, _, err := callHarness[[]types.HarnessModel](context.Background(), drv, "mgs_listModels")
	require.NoError(t, err)
	_, tierRows, err := validateModelRows(rows)
	require.NoError(t, err)
	tiers, err := pickTierModels(tierRows, func(string) (string, bool) { return "", false })
	require.NoError(t, err, "each tier has one row, so nothing has to map one")
	assert.Equal(t, map[string]string{
		"routine": "claude-haiku-5-5", "bounded": "claude-sonnet-5-5", "design": "claude-opus-5-5", "frontier": "claude-fable-5-2",
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

const (
	harnessSpellDir = "../../../spells/harness"
	glueDir         = "../../../docs/guides/integrations/agents"
	bindingsGuide   = glueDir + "/README.md"
)

// shippedHarnessIDs reads the harnesses off the spell directory, so a new
// harness owes its bindings without anyone listing it here.
func shippedHarnessIDs(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(harnessSpellDir)
	if err != nil {
		t.Fatalf("read %s: %v", harnessSpellDir, err)
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	if len(ids) == 0 {
		t.Fatalf("%s holds no harness spell", harnessSpellDir)
	}
	return ids
}

func readGlue(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(glueDir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(body)
}

// The OpenCode adapter is installed from Go/Wasm assets, not a guide template.
func readOpenCodeRuntime(t *testing.T) string {
	t.Helper()
	var source strings.Builder
	for _, path := range []string{"opencode/plugin_js.go", "opencode/plugin.go", "opencode/runtime.go", "opencode/plugin.buzz"} {
		body, err := os.ReadFile(path)
		require.NoError(t, err)
		source.Write(body)
	}
	return source.String()
}

// HookBindingCompletenessSuite holds every shipped harness to an answer at
// every hook point. It extends TestShippedHookConfigsWireTheSameJobs from two
// configs to every harness, keyed by point. A host whose glue is one script
// or a plugin is held to it too.
type HookBindingCompletenessSuite struct {
	suite.Suite
	tables map[string][]types.HookBinding
	loaded map[string]agent.HarnessDescriptor
}

func TestHookBindingCompletenessSuite(t *testing.T) {
	suite.Run(t, new(HookBindingCompletenessSuite))
}

func (s *HookBindingCompletenessSuite) SetupSuite() {
	s.tables = map[string][]types.HookBinding{}
	s.loaded = map[string]agent.HarnessDescriptor{}
	for _, id := range shippedHarnessIDs(s.T()) {
		d := loadShippedHarness(s.T(), id)
		s.loaded[id] = d
		table, err := agent.HookBindings(d)
		s.Require().NoError(err, "%s leaves a hook point unexplained", id)
		s.tables[id] = table
	}
}

func (s *HookBindingCompletenessSuite) TestEveryHarnessAnswersEveryPoint() {
	for id, table := range s.tables {
		var points []types.HookPoint
		for _, b := range table {
			s.True(b.Status == types.Bound || b.Status == types.Unsupported, "%s %s: %q", id, b.Point, b.Status)
			points = append(points, b.Point)
		}
		s.Equal(types.HookPoints(), points, id)
	}
}

func (s *HookBindingCompletenessSuite) TestAStaleUnsupportedRowFails() {
	d := s.loaded["claude-code"]
	d.UnsupportedHooks = append(slices.Clone(d.UnsupportedHooks), types.HookBinding{
		Point: types.HookPointShell, Status: types.Unsupported, Reason: "not verified: anything"})
	_, err := agent.HookBindings(d)
	s.ErrorContains(err, "claude-code: mgs_listUnsupportedHooks says shell is unsupported, but magus-command binds it")
}

func (s *HookBindingCompletenessSuite) TestARemovedRowFails() {
	d := s.loaded["codex"]
	d.UnsupportedHooks = nil
	_, err := agent.HookBindings(d)
	s.ErrorContains(err, "codex: no hook binds attach")
}

// Attach stays unsupported until an SVG renders inline (decision 135).
func (s *HookBindingCompletenessSuite) TestAttachIsUnsupportedEverywhere() {
	for id, table := range s.tables {
		attach := table[slices.IndexFunc(table, func(b types.HookBinding) bool { return b.Point == types.HookPointAttach })]
		s.Equal(types.Unsupported, attach.Status, id)
	}
}

// The plugin is not a rendered config, so nothing else holds it to the
// hooks the opencode spell lists.
func (s *HookBindingCompletenessSuite) TestTheOpenCodePluginRunsEveryHookItsSpellLists() {
	plugin := readOpenCodeRuntime(s.T())
	for _, h := range s.loaded["opencode"].Hooks {
		s.Contains(plugin, fmt.Sprintf("%q", h.Event), "the plugin handles no %s", h.Event)
		if h.Script != "opencode-plugin" {
			s.Contains(plugin, h.Script+".buzz", "the plugin never runs %s", h.Script)
		}
	}
}

func (s *HookBindingCompletenessSuite) TestTheCursorHookAnswersEveryEventItsSpellLists() {
	glue := readGlue(s.T(), "cursor-hook.buzz")
	for _, h := range s.loaded["cursor"].Hooks {
		if h.Script == "cursor-hook" {
			s.Contains(glue, fmt.Sprintf("eventName == %q", h.Event), "cursor-hook.buzz has no %s arm", h.Event)
		}
	}
}

// guideRow reads one matrix row of the guide: the point, then a cell per
// harness in the header's order.
var guideRow = regexp.MustCompile("^\\| `([a-z-]+)` +\\|(.*)\\|$")

func (s *HookBindingCompletenessSuite) TestTheGuideShowsTheTable() {
	guide := readGuide(s.T())
	lines := strings.Split(guide, "\n")
	header := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "| hook point ") })
	s.Require().NotEqual(-1, header, "%s has no matrix; regenerate it", bindingsGuide)
	var columns []string
	for _, cell := range strings.Split(strings.Trim(lines[header], "|"), "|")[1:] {
		columns = append(columns, strings.TrimSpace(cell))
	}
	s.ElementsMatch(shippedHarnessIDs(s.T()), columns)

	seen := 0
	for _, line := range lines[header+2:] {
		m := guideRow.FindStringSubmatch(line)
		if m == nil {
			break
		}
		seen++
		cells := strings.Split(m[2], "|")
		s.Require().Len(cells, len(columns), line)
		for i, id := range columns {
			want := s.cell(id, types.HookPoint(m[1]))
			s.Equal(want, strings.TrimSpace(cells[i]), "%s %s; regenerate %s", id, m[1], bindingsGuide)
		}
	}
	s.Equal(len(types.HookPoints()), seen, "one row per hook point")
	// The page links each URL in a reason; the spell states it bare.
	bareGuide := strings.NewReplacer("<https://", "https://", ">", "").Replace(guide)
	for id, table := range s.tables {
		for _, b := range table {
			if b.Status == types.Unsupported {
				s.Contains(bareGuide, b.Reason, "%s %s's reason", id, b.Point)
			}
		}
	}
}

func (s *HookBindingCompletenessSuite) cell(id string, point types.HookPoint) string {
	for _, b := range s.tables[id] {
		if b.Point != point {
			continue
		}
		if b.Status == types.Unsupported {
			return "unsupported"
		}
		return fmt.Sprintf("`%s`", b.Adapter)
	}
	return ""
}

func readGuide(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(bindingsGuide)
	if err != nil {
		t.Fatalf("read %s: %v", bindingsGuide, err)
	}
	return string(body)
}

// AdapterConformanceSuite ties each Bound adapter to the cases that run it.
// guard_templates.txtar and opencode-plugin.test.ts execute the verdict each
// host gets for one recorded event. This suite holds each Bound judging point
// to a declaration those gates read, and pins each host's fail stance.
type AdapterConformanceSuite struct {
	suite.Suite
	loaded map[string]agent.HarnessDescriptor
}

func TestAdapterConformanceSuite(t *testing.T) {
	suite.Run(t, new(AdapterConformanceSuite))
}

func (s *AdapterConformanceSuite) SetupSuite() {
	s.loaded = map[string]agent.HarnessDescriptor{}
	for _, id := range shippedHarnessIDs(s.T()) {
		s.loaded[id] = loadShippedHarness(s.T(), id)
	}
}

func (s *AdapterConformanceSuite) TestEveryBoundAdapterIsAShippedGlue() {
	for id, d := range s.loaded {
		table, err := agent.HookBindings(d)
		s.Require().NoError(err, id)
		for _, b := range table {
			if b.Status != types.Bound {
				continue
			}
			for _, script := range strings.Split(b.Adapter, ", ") {
				if script == "opencode-plugin" {
					s.FileExists("opencode/plugin_js.go", "%s %s", id, b.Point)
				} else {
					s.FileExists(filepath.Join(glueDir, script+".buzz"), "%s %s", id, b.Point)
				}
			}
		}
	}
}

// judgedInput is the guard input a judging point hands magus, as the
// magus-guard-coverage markers name it.
var judgedInput = map[types.HookPoint]string{
	types.HookPointShell: "command",
	types.HookPointEdit:  "path",
	types.HookPointMCP:   "mcp",
}

var coverageLine = regexp.MustCompile(`magus-guard-coverage: schema=\d+ host=(\S+) input=(\S+) deny=(\S+)`)

func (s *AdapterConformanceSuite) TestEveryBoundJudgingPointDeclaresItsVerdictTransport() {
	declared := map[string]bool{}
	entries, err := os.ReadDir(glueDir)
	s.Require().NoError(err)
	for _, e := range entries {
		if ext := filepath.Ext(e.Name()); ext != ".buzz" && ext != ".ts" {
			continue
		}
		for _, m := range coverageLine.FindAllStringSubmatch(readGlue(s.T(), e.Name()), -1) {
			for _, host := range strings.Split(m[1], ",") {
				declared[fmt.Sprintf("%s/%s", host, m[2])] = m[3] != "none"
			}
		}
	}
	canonical, err := os.ReadFile(filepath.Join("..", "..", "..", "spells", "harness", "opencode", "spell.buzz"))
	s.Require().NoError(err)
	for _, m := range coverageLine.FindAllStringSubmatch(string(canonical), -1) {
		declared[fmt.Sprintf("%s/%s", m[1], m[2])] = m[3] != "none"
	}
	for id, d := range s.loaded {
		table, err := agent.HookBindings(d)
		s.Require().NoError(err, id)
		for _, b := range table {
			input, judging := judgedInput[b.Point]
			if !judging || b.Status != types.Bound {
				continue
			}
			s.True(declared[fmt.Sprintf("%s/%s", id, input)],
				"%s binds %s, but no glue declares how %s carries a deny on the %s input", id, b.Point, id, input)
		}
	}
}

// failClosed says which hosts' hook config blocks a gated call when the hook
// itself cannot run. Only Cursor's can say so (failClosed on an entry).
var failClosed = map[string]bool{"cursor": true}

func (s *AdapterConformanceSuite) TestEachHostsFailStanceIsTheOneDecided() {
	for id, d := range s.loaded {
		for _, group := range d.ManagedEntries {
			for _, entry := range group.Entries {
				if closed, _ := entry["failClosed"].(bool); closed {
					s.True(failClosed[id], "%s %v fails closed, which failClosed does not record", id, group.Path)
				}
			}
		}
	}
	for _, event := range []string{"preToolUse", "beforeShellExecution", "beforeMCPExecution"} {
		s.True(entryFailsClosed(s.loaded["cursor"], event), "cursor's %s entry must fail closed", event)
	}
	s.Contains(readOpenCodeRuntime(s.T()), `; refused.`,
		"the plugin refuses a call no magus judged, and says so")
}

func entryFailsClosed(d agent.HarnessDescriptor, event string) bool {
	for _, group := range d.ManagedEntries {
		if group.Path[len(group.Path)-1] != event {
			continue
		}
		for _, entry := range group.Entries {
			if closed, _ := entry["failClosed"].(bool); closed {
				return true
			}
		}
	}
	return false
}

func TestValidateModelRowsKeepsEveryRowOfATier(t *testing.T) {
	_, tiers, err := validateModelRows([]types.HarnessModel{
		{ID: "m-1", Tier: "design", Alias: "big"}, {ID: "m-2", Tier: "design", Alias: "bigger"}, {ID: "m-3", Tier: "routine"},
	})

	require.NoError(t, err)
	assert.Equal(t, map[string][]string{"design": {"m-1", "m-2"}, "routine": {"m-3"}}, tiers)
}

func TestPickTierModelsTakesTheDeclaredRowOfASeveralRowTier(t *testing.T) {
	got, err := pickTierModels(map[string][]string{"design": {"m-1", "m-2"}, "routine": {"m-3"}},
		func(tier string) (string, bool) { return "m-2", tier == "design" })

	require.NoError(t, err)
	assert.Equal(t, map[string]string{"design": "m-2", "routine": "m-3"}, got)
}

func TestPickTierModelsRefusesASeveralRowTierTheDeclarationLeavesUnmapped(t *testing.T) {
	_, err := pickTierModels(map[string][]string{"design": {"m-1", "m-2"}},
		func(string) (string, bool) { return "", false })

	require.Error(t, err)
	assert.Contains(t, err.Error(), "rows m-1, m-2 all declare tier design")
}

func TestPickTierModelsRefusesADeclaredIdThatIsNoRowOfTheTier(t *testing.T) {
	_, err := pickTierModels(map[string][]string{"design": {"m-1", "m-2"}},
		func(string) (string, bool) { return "other", true })

	require.Error(t, err)
	assert.Contains(t, err.Error(), "rows m-1, m-2 all declare tier design")
}

func TestPickTierModelsPicksNothingForAHarnessWithNoTiers(t *testing.T) {
	got, err := pickTierModels(nil, func(string) (string, bool) { return "", false })

	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestPickTierModelsRunsTheOneRowOfATierWithNoDeclaration(t *testing.T) {
	got, err := pickTierModels(map[string][]string{"design": {"m-1"}}, func(string) (string, bool) { return "", false })

	require.NoError(t, err)
	assert.Equal(t, map[string]string{"design": "m-1"}, got)
}
