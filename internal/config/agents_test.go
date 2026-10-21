package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func writeYAML(t *testing.T, dir, body string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, Filename)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestAgentsDecodesAndValidates(t *testing.T) {
	path := writeYAML(t, t.TempDir(), `agents:
  billing: metered
  profile: strict
  models:
    claude-next-1: {window: 2000000, input: 3, output: 15, cache_write_5m: 3.75, cache_write_1h: 6, cache_read: 0.3, price_step: 200000, step_factor: 2}
    claude-flat-1: {window: 1000000, input: 1}
`)
	cfg, err := LoadFile(path, true)
	require.NoError(t, err)
	assert.Equal(t, []types.ModelPricing{
		{ID: "claude-flat-1", Window: 1_000_000, Input: 1},
		{ID: "claude-next-1", Window: 2_000_000, Input: 3, Output: 15, CacheWrite5m: 3.75,
			CacheWrite1h: 6, CacheRead: 0.3, LongContext: &types.PriceStep{Above: 200_000, Multiplier: 2}},
	}, cfg.Agents.ModelOverrides(), "a model with no price_step has no LongContext")
	agents := cfg.Agents
	agents.Models = nil
	assert.Equal(t, Agents{Billing: types.AgentBillingMetered, Profile: types.GuardProfileStrict}, agents)
}

// Each value the guard could not honor fails the load: an agents section that reads as
// applied and is not would price or police a session by facts nobody stated.
func TestAgentsMisconfigurationIsAnError(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"unknown billing", "agents:\n  billing: oauth\n", "oauth"},
		{"unknown profile", "agents:\n  profile: max\n", "max"},
		{"unknown brief format", "agents:\n  brief_format: json\n", "json"},
		{"unknown key", "agents:\n  budget: 5\n", "budget"},
		{"unknown model key", "agents:\n  models:\n    m: {window: 1000, aliases: [x]}\n", "aliases"},
		{"window missing", "agents:\n  models:\n    m: {cache_read: 0.1}\n", "agents.models[m].window: must be >= 1 (got 0)"},
		{"window negative", "agents:\n  models:\n    m: {window: -1}\n", "agents.models[m].window: must be >= 1 (got -1)"},
		{"negative input price", "agents:\n  models:\n    m: {window: 1000, input: -1}\n", "agents.models[m].input: must be >= 0"},
		{"negative cache write price", "agents:\n  models:\n    m: {window: 1000, cache_write_1h: -1}\n", "agents.models[m].cache_write_1h: must be >= 0"},
		{"negative price", "agents:\n  models:\n    m: {window: 1000, cache_read: -0.1}\n", "agents.models[m].cache_read: must be >= 0"},
		{"step without factor", "agents:\n  models:\n    m: {window: 1000, price_step: 100}\n", "agents.models[m].step_factor: failed required_with"},
		{"factor without step", "agents:\n  models:\n    m: {window: 1000, step_factor: 2}\n", "agents.models[m].price_step: failed required_with"},
		{"factor below 1", "agents:\n  models:\n    m: {window: 1000, price_step: 100, step_factor: 0.5}\n", "agents.models[m].step_factor: must be >= 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadFile(writeYAML(t, t.TempDir(), tc.body), true)
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func TestDetectBilling(t *testing.T) {
	env := func(kv map[string]string) func(string) string {
		return func(k string) string { return kv[k] }
	}
	assert.Equal(t, types.AgentBillingMetered, DetectBilling(env(map[string]string{"ANTHROPIC_API_KEY": "sk-test"})))
	assert.Equal(t, types.AgentBillingMetered, DetectBilling(env(map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1"})))
	assert.Equal(t, types.AgentBillingMetered, DetectBilling(env(map[string]string{"CLAUDE_CODE_USE_VERTEX": "true"})))
	assert.Empty(t, DetectBilling(env(map[string]string{"CLAUDE_CODE_USE_BEDROCK": "0", "CLAUDE_CODE_USE_VERTEX": "false"})))
	assert.Empty(t, DetectBilling(env(map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "x"})), "OAuth tells nothing")
	assert.Empty(t, DetectBilling(env(nil)))
}

func TestResolveBilling(t *testing.T) {
	metered := func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "sk-test"
		}
		return ""
	}
	none := func(string) string { return "" }

	quota := Agents{Billing: types.AgentBillingQuota}
	assert.Equal(t, types.AgentBillingQuota, quota.ResolveBilling(none), "with nothing detected the declaration stands")
	assert.Equal(t, types.AgentBillingMetered, Agents{}.ResolveBilling(metered))
	assert.Equal(t, types.AgentBillingMetered, quota.ResolveBilling(metered), "the detected mode stands")
}

// The workspace's magus.yaml wins for billing, as layering always does, but a profile
// takes the higher of the two tiers: neither may lower what the other raised.
func TestLoadAgentsLayersTiers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	writeYAML(t, filepath.Join(home, "magus"), "agents:\n  billing: quota\n  profile: strict\n")
	root := t.TempDir()
	writeYAML(t, root, "agents:\n  billing: metered\n  profile: off\n")

	got, err := LoadAgents(root)
	require.NoError(t, err)
	got.Models = nil
	assert.Equal(t, Agents{Billing: types.AgentBillingMetered, Profile: types.GuardProfileStrict}, got)

	writeYAML(t, filepath.Join(home, "magus"), "agents:\n  profile: off\n")
	writeYAML(t, root, "agents:\n  profile: standard\n")
	got, err = LoadAgents(root)
	require.NoError(t, err)
	assert.Equal(t, types.GuardProfileStandard, got.Profile)

	writeYAML(t, root, "agents:\n  billing: metered\n")
	got, err = LoadAgents(root)
	require.NoError(t, err)
	assert.Equal(t, types.GuardProfileOff, got.Profile, "a tier stating no level says nothing")

	writeYAML(t, filepath.Join(home, "magus"), "cache:\n  size_mb: 1\n")
	writeYAML(t, root, "agents:\n  profile: off\n")
	got, err = LoadAgents(root)
	require.NoError(t, err)
	assert.Equal(t, types.GuardProfileOff, got.Profile)

	empty := t.TempDir()
	writeYAML(t, filepath.Join(home, "magus"), "cache:\n  size_mb: 1\n")
	got, err = LoadAgents(empty)
	require.NoError(t, err)
	assert.Empty(t, got.Profile, "no tier stating a level leaves it unset")

	writeYAML(t, filepath.Join(home, "magus"), "agents:\n  brief_format: buzz\n")
	writeYAML(t, root, "agents:\n  profile: off\n")
	got, err = LoadAgents(root)
	require.NoError(t, err)
	assert.Empty(t, got.BriefFormat, "only the workspace sets the brief format")
	writeYAML(t, root, "agents:\n  brief_format: buzz\n")
	got, err = LoadAgents(root)
	require.NoError(t, err)
	assert.Equal(t, types.BriefFormatBuzz, got.BriefFormat)

	writeYAML(t, root, "agents:\n  billing: oauth\n")
	_, err = LoadAgents(root)
	require.Error(t, err)
}
