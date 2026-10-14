package config

import (
	"cmp"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// Agents is the agents section of magus.yaml: facts about the account agents run under,
// which only the person or the account owner knows, and a person's guard level.
type Agents struct {
	// Billing is how the account pays for tokens: metered or quota. Unset is unknown. A
	// workspace's magus.yaml wins over the user tier, so a company repository can state
	// metered for everyone who works in it.
	Billing types.AgentBilling `json:"billing" yaml:"billing" validate:"omitempty,oneof=metered quota"`
	// Profile is a person's guard level. It can only raise the level the workspace's
	// magus\guard.profile declares, never lower it, and of the tiers that set it the
	// highest applies.
	Profile types.GuardProfile `json:"profile" yaml:"profile" validate:"omitempty,oneof=off light standard strict"`
	// Models adds or overrides rows of the model table the workspace's harnesses publish,
	// keyed by model id, for a model released after the harness spell or a price that
	// changed.
	Models map[string]AgentModel `json:"models" yaml:"models" validate:"dive"`
}

// AgentModel is one agents.models entry. Prices are USD per million tokens.
type AgentModel struct {
	// Window is the model's context window in tokens.
	Window int64 `json:"window" yaml:"window" validate:"gte=1"`
	// Input and Output are the prices of an input and an output token.
	Input  float64 `json:"input" yaml:"input" validate:"gte=0"`
	Output float64 `json:"output" yaml:"output" validate:"gte=0"`
	// CacheWrite5m and CacheWrite1h are the prices of a cache write with the five-minute
	// and the one-hour lifetime.
	CacheWrite5m float64 `json:"cache_write_5m" yaml:"cache_write_5m" validate:"gte=0"`
	CacheWrite1h float64 `json:"cache_write_1h" yaml:"cache_write_1h" validate:"gte=0"`
	// CacheRead is the price of a cache read.
	CacheRead float64 `json:"cache_read" yaml:"cache_read" validate:"gte=0"`
	// PriceStep is the prompt size in tokens above which every price is multiplied by
	// StepFactor; 0 for none.
	PriceStep int64 `json:"price_step" yaml:"price_step" validate:"gte=0"`
	// StepFactor is the multiplier above PriceStep, required with it.
	StepFactor float64 `json:"step_factor" yaml:"step_factor" validate:"required_with=PriceStep,omitempty,gte=1"`
}

// ModelOverrides returns Models as the rows model.Table.Merge applies, sorted by id.
func (a Agents) ModelOverrides() []types.ModelFacts {
	out := make([]types.ModelFacts, 0, len(a.Models))
	for _, id := range slices.Sorted(maps.Keys(a.Models)) {
		m := a.Models[id]
		out = append(out, types.ModelFacts{
			ID: id, Window: m.Window, Input: m.Input, Output: m.Output, CacheWrite5m: m.CacheWrite5m,
			CacheWrite1h: m.CacheWrite1h, CacheRead: m.CacheRead, PriceStep: m.PriceStep, StepFactor: m.StepFactor,
		})
	}
	return out
}

// meteredEnv are the variables whose presence in an agent host's environment means it
// bills per token. OAuth sign-in is absent on purpose: a usage-billed Enterprise seat and
// a Max plan both sign in that way.
var meteredEnv = []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX"}

// DetectBilling reads the billing mode from an agent host's environment, as a hook
// process inherits it: metered when any of ANTHROPIC_API_KEY, CLAUDE_CODE_USE_BEDROCK or
// CLAUDE_CODE_USE_VERTEX is set to something other than "", "0" or "false"; unset
// otherwise, since nothing in the environment tells quota from an OAuth metered seat.
func DetectBilling(getenv func(string) string) types.AgentBilling {
	for _, name := range meteredEnv {
		switch v := strings.TrimSpace(getenv(name)); strings.ToLower(v) {
		case "", "0", "false":
		default:
			return types.AgentBillingMetered
		}
	}
	return ""
}

// ResolveBilling is the billing mode in effect: what the environment shows, else what
// agents.billing declares. A declaration the environment contradicts yields the detected
// mode.
func (a Agents) ResolveBilling(getenv func(string) string) types.AgentBilling {
	return cmp.Or(DetectBilling(getenv), a.Billing)
}

// LoadAgents reads the agents section the guard applies in the workspace at root: the
// user tier's and the workspace magus.yaml's, layered as Load layers them except for
// Profile, where the higher of the two applies. The cwd tier is not read: a guard hook
// judges for the workspace, wherever the host's shell stands. An error is a magus.yaml that
// does not decode or validate.
func LoadAgents(root string) (Agents, error) {
	user := Defaults()
	if dir, err := UserConfigDir(); err == nil {
		loaded, err := loadDirInto(user, filepath.Join(dir, "magus"))
		if err != nil {
			return Agents{}, err
		}
		user = loaded
	}
	merged := user
	workspace := Defaults()
	if root != "" {
		loaded, err := loadDirInto(user, root)
		if err != nil {
			return Agents{}, err
		}
		merged = loaded
		if workspace, err = loadDirInto(Defaults(), root); err != nil {
			return Agents{}, err
		}
	}
	if err := Validate(merged); err != nil {
		return Agents{}, err
	}
	agents := merged.Agents
	agents.Profile = workspace.Agents.Profile
	if p := user.Agents.Profile; p != "" {
		agents.Profile = p.Stricter(agents.Profile)
	}
	return agents, nil
}
