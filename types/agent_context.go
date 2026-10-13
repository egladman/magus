package types

import (
	"fmt"
	"slices"
)

// AgentThread is which thread of an agent session made a call: the main thread a person
// talks to, or a subagent it spawned.
type AgentThread string

const (
	// AgentThreadMain is the session's main thread.
	AgentThreadMain AgentThread = "main"
	// AgentThreadSub is a subagent.
	AgentThreadSub AgentThread = "sub"
	// AgentThreadUnknown is a call whose host reports nothing that tells the two apart.
	AgentThreadUnknown AgentThread = "unknown"
)

// Values lists the threads a caller may name, excluding the zero value.
func (t AgentThread) Values() []string {
	return []string{string(AgentThreadMain), string(AgentThreadSub), string(AgentThreadUnknown)}
}

// Valid reports whether t is a declared thread or unset.
func (t AgentThread) Valid() bool {
	return t == "" || slices.Contains(t.Values(), string(t))
}

// String renders t for a message, with unset read as unknown.
func (t AgentThread) String() string {
	if t == "" {
		return string(AgentThreadUnknown)
	}
	return string(t)
}

// MarshalText writes the name as given.
func (t AgentThread) MarshalText() ([]byte, error) { return []byte(t), nil }

// UnmarshalText sets t from a name, refusing one outside Values.
func (t *AgentThread) UnmarshalText(text []byte) error {
	v := AgentThread(text)
	if !v.Valid() {
		return fmt.Errorf("unknown agent thread %q (want one of %v)", text, v.Values())
	}
	*t = v
	return nil
}

// AgentBilling is how the account an agent runs under pays for tokens.
type AgentBilling string

const (
	// AgentBillingMetered pays per token: an API key, Bedrock, Vertex, or a usage-billed
	// seat.
	AgentBillingMetered AgentBilling = "metered"
	// AgentBillingQuota is a subscription whose usage counts against time windows that
	// reset, with no per-token charge.
	AgentBillingQuota AgentBilling = "quota"
)

// Values lists the billing modes a caller may name, excluding the zero value, which
// means unknown.
func (b AgentBilling) Values() []string {
	return []string{string(AgentBillingMetered), string(AgentBillingQuota)}
}

// Valid reports whether b is a declared billing mode or unset.
func (b AgentBilling) Valid() bool {
	return b == "" || slices.Contains(b.Values(), string(b))
}

// String renders b for a message, or "unset" when empty.
func (b AgentBilling) String() string {
	if b == "" {
		return "unset"
	}
	return string(b)
}

// MarshalText writes the name as given.
func (b AgentBilling) MarshalText() ([]byte, error) { return []byte(b), nil }

// UnmarshalText sets b from a name, refusing one outside Values.
func (b *AgentBilling) UnmarshalText(text []byte) error {
	v := AgentBilling(text)
	if !v.Valid() {
		return fmt.Errorf("unknown agent billing %q (want one of %v)", text, v.Values())
	}
	*b = v
	return nil
}

// GuardProfile is how hard the guard's cost rules press on an agent session. Each level
// is a set of built-in rule settings; a rule the workspace names in magus\guard.builtins
// overrides its level's setting.
type GuardProfile string

// The profiles, least to most refusing. Unset reads as GuardProfileLight.
const (
	GuardProfileOff      GuardProfile = "off"
	GuardProfileLight    GuardProfile = "light"
	GuardProfileStandard GuardProfile = "standard"
	GuardProfileStrict   GuardProfile = "strict"
)

// Values lists the profiles in rank order, excluding the zero value.
func (p GuardProfile) Values() []string {
	return []string{string(GuardProfileOff), string(GuardProfileLight), string(GuardProfileStandard), string(GuardProfileStrict)}
}

// Valid reports whether p is a declared profile or unset.
func (p GuardProfile) Valid() bool {
	return p == "" || slices.Contains(p.Values(), string(p))
}

// Level is p with unset read as GuardProfileLight, the level that applies when nothing
// declares one.
func (p GuardProfile) Level() GuardProfile {
	if p == "" {
		return GuardProfileLight
	}
	return p
}

// Rank orders p by how much it refuses, unset ranking as light; -1 for an unknown name.
func (p GuardProfile) Rank() int {
	return slices.Index(p.Values(), string(p.Level()))
}

// String renders p as its level.
func (p GuardProfile) String() string { return string(p.Level()) }

// MarshalText writes the name as given.
func (p GuardProfile) MarshalText() ([]byte, error) { return []byte(p), nil }

// UnmarshalText sets p from a name, refusing one outside Values.
func (p *GuardProfile) UnmarshalText(text []byte) error {
	v := GuardProfile(text)
	if !v.Valid() {
		return fmt.Errorf("unknown guard profile %q (want one of %v)", text, v.Values())
	}
	*p = v
	return nil
}

// StricterProfile returns the highest-ranked of profiles. An unset or unknown one says
// nothing and is skipped, and light is the answer when none is left; pass p.Level() for a
// side whose unset means light rather than "no opinion".
func StricterProfile(profiles ...GuardProfile) GuardProfile {
	best := GuardProfileLight
	rank := -1
	for _, p := range profiles {
		if p == "" {
			continue
		}
		if r := p.Rank(); r > rank {
			best, rank = p, r
		}
	}
	return best
}

// AgentContext is the calling thread's context window as its host last reported it.
type AgentContext struct {
	// Tokens is the prompt the thread's last model call sent: input plus cache-read plus
	// cache-write tokens. 0 when no usage was reported.
	Tokens int64
	// Window is the model's context window in tokens: the one the host reported, else
	// the model table's, else 0.
	Window int64
	// Model is the model id the usage was reported for, raw; empty when unknown.
	Model string
	// Source names where Tokens came from, such as "transcript" or "precompact"; empty
	// when nothing reported it.
	Source string
}

// ModelFacts is one model's row in the table magus prices agent context against. Prices
// are USD per million tokens.
type ModelFacts struct {
	// ID is the model id the row matches, and every id that extends it with a date,
	// a provider prefix or a context suffix.
	ID string
	// Window is the context window in tokens.
	Window int64
	// Input, CacheWrite5m, CacheWrite1h, CacheRead and Output are the standard prices.
	// A field left 0 is unknown, never free.
	Input        float64
	CacheWrite5m float64
	CacheWrite1h float64
	CacheRead    float64
	Output       float64
	// PriceStep is the prompt size in tokens above which every price is multiplied by
	// StepFactor, cache reads and writes counting toward it; 0 when the model has no
	// such step.
	PriceStep  int64
	StepFactor float64
}

// Factor is the price multiplier a call sending prompt tokens pays: StepFactor above
// PriceStep, else 1.
func (f ModelFacts) Factor(prompt int64) float64 {
	if f.PriceStep > 0 && prompt > f.PriceStep && f.StepFactor > 0 {
		return f.StepFactor
	}
	return 1
}
