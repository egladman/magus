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
	// It is the one spelling of unknown: magus writes it rather than leave a thread unset.
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

// ThreadSource is what an AgentThread rests on.
type ThreadSource string

const (
	// ThreadReported is a thread the host reported: a call naming a subagent, or one
	// naming none from a host that has named subagents before.
	ThreadReported ThreadSource = "reported"
	// ThreadInferred is a thread magus concluded with no report to rest on, such as an
	// unleased session of a host that never names subagents, or an unknown thread. A rule
	// reading one says a refusal as advice.
	ThreadInferred ThreadSource = "inferred"
)

// Values lists the sources, excluding the zero value.
func (s ThreadSource) Values() []string {
	return []string{string(ThreadReported), string(ThreadInferred)}
}

// Valid reports whether s is a declared source or unset.
func (s ThreadSource) Valid() bool {
	return s == "" || slices.Contains(s.Values(), string(s))
}

// MarshalText writes the name as given.
func (s ThreadSource) MarshalText() ([]byte, error) { return []byte(s), nil }

// UnmarshalText sets s from a name, refusing one outside Values.
func (s *ThreadSource) UnmarshalText(text []byte) error {
	v := ThreadSource(text)
	if !v.Valid() {
		return fmt.Errorf("unknown thread source %q (want one of %v)", text, v.Values())
	}
	*s = v
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

// DelegableClass is the kind of work a subagent could have done in its caller's place.
// Empty is a call no subagent would make for it.
type DelegableClass string

const (
	// DelegableSearch is a search: a recursive grep, rg, find, or a host's search tool.
	DelegableSearch DelegableClass = "search"
	// DelegableRead is a whole read of a source file or of a run's full log.
	DelegableRead DelegableClass = "read"
	// DelegableDiff is a diff or a patch read whole.
	DelegableDiff DelegableClass = "diff"
	// DelegableRun is a target run.
	DelegableRun DelegableClass = "run"
	// DelegableFetch is a fetch of a URL.
	DelegableFetch DelegableClass = "fetch"
	// DelegableEdit is a write or edit of a file inside a checkout of the repository.
	DelegableEdit DelegableClass = "edit"
)

// Orchestration is how surely a session hands its work to workers. Empty means magus
// recorded no evidence that it does.
type Orchestration string

const (
	// OrchestrationInferred rests on evidence short of proof: a spawn bound to no job row,
	// a loaded multi-agent skill, or proof on a thread the host did not report. A rule
	// reading it only advises.
	OrchestrationInferred Orchestration = "inferred"
	// OrchestrationProven is a main thread the host reported that forked a job row the
	// store then showed live, or spawned a worker bound to one. It lasts the session.
	OrchestrationProven Orchestration = "proven"
)

// Values lists the levels in rank order, excluding the zero value.
func (o Orchestration) Values() []string {
	return []string{string(OrchestrationInferred), string(OrchestrationProven)}
}

// Valid reports whether o is a declared level or unset.
func (o Orchestration) Valid() bool {
	return o == "" || slices.Contains(o.Values(), string(o))
}

// MarshalText writes the name as given.
func (o Orchestration) MarshalText() ([]byte, error) { return []byte(o), nil }

// UnmarshalText sets o from a name, refusing one outside Values.
func (o *Orchestration) UnmarshalText(text []byte) error {
	v := Orchestration(text)
	if !v.Valid() {
		return fmt.Errorf("unknown orchestration %q (want one of %v)", text, v.Values())
	}
	*o = v
	return nil
}

// AgentFacts is what magus records about a caller's cost beside one of its calls. Every
// field is the caller's, never a child it spawns, and the JSON names are the trail's.
type AgentFacts struct {
	// Thread is which thread of its session the caller is, and ThreadFrom what that
	// rests on.
	Thread     AgentThread  `json:"thread,omitempty"`
	ThreadFrom ThreadSource `json:"thread_from,omitempty"`
	// ContextTokens and ContextModel are the caller's last reported context size and the
	// model it was reported for; 0 and empty when the host reported none.
	ContextTokens int64  `json:"context_tokens,omitzero"`
	ContextModel  string `json:"context_model,omitempty"`
	// Billing is the billing mode in effect for the call, empty when unknown.
	Billing AgentBilling `json:"billing,omitempty"`
	// DelegableClass is the work a subagent could have done in the caller's place.
	DelegableClass DelegableClass `json:"delegable_class,omitempty"`
	// Orchestration is how surely the caller's session hands its work to workers, empty
	// with no evidence.
	Orchestration Orchestration `json:"orchestration,omitempty"`
	// ResultChars is the size of the call's result in characters, nil when it was not
	// observed, which is every record made before the call ran.
	ResultChars *int `json:"result_chars,omitempty"`
	// Shadow is what each guard profile would have decided for the call, whichever one is
	// in force, so a level can be judged from the calls it would have changed.
	Shadow map[GuardProfile]GuardDecision `json:"shadow,omitempty"`
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

// Effective is the level p applies: p, or GuardProfileLight when unset.
func (p GuardProfile) Effective() GuardProfile {
	if p == "" {
		return GuardProfileLight
	}
	return p
}

// Rank orders p's effective level by how much it refuses; -1 for an unknown name.
func (p GuardProfile) Rank() int {
	return slices.Index(p.Values(), string(p.Effective()))
}

// Stricter is p raised by o: o when its level refuses more than p's effective one, else
// p's effective level. An unset o raises nothing, since a tier that declares no level has
// no say, and an unknown o ranks below off.
func (p GuardProfile) Stricter(o GuardProfile) GuardProfile {
	if o != "" && o.Rank() > p.Rank() {
		return o
	}
	return p.Effective()
}

// String renders p as written, empty when unset; Effective names the level that applies.
func (p GuardProfile) String() string { return string(p) }

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

// ContextSource names where a ContextWindow's size came from.
type ContextSource string

// ContextFromTranscript is a size read from the usage a host wrote into a transcript.
const ContextFromTranscript ContextSource = "transcript"

// ContextWindow is a thread's context window as its host last reported it.
type ContextWindow struct {
	// Tokens is the prompt the thread's last model call sent: input plus cache-read plus
	// cache-write tokens. 0 when no usage was reported.
	Tokens int64
	// Window is the model's context window in tokens: the one the host reported, else
	// the model table's, else 0.
	Window int64
	// Model is the model id the usage was reported for, raw; empty when unknown.
	Model string
	// Source is where Tokens came from, empty when nothing reported it.
	Source ContextSource
}

// USDPerMTok is a token price in US dollars per million tokens.
type USDPerMTok float64

// PriceStep is a long-context surcharge: every price is multiplied by Multiplier once a
// call's prompt passes Above tokens, cache reads and writes counting toward it.
type PriceStep struct {
	Above      int64
	Multiplier float64
}

// ModelPricing is one model's row in the table magus prices agent context against.
type ModelPricing struct {
	// ID is the model id the row matches, exactly: a provider prefix, a date or a context
	// suffix makes another id, which needs a row of its own.
	ID string
	// Tier is the model-tier case this exact id runs, "" for none.
	Tier string
	// Alias is the short name the host takes for this model, matched exactly like ID and
	// never as a prefix, "" for none.
	Alias string
	// Window is the context window in tokens.
	Window int64
	// Input, CacheWrite5m, CacheWrite1h, CacheRead and Output are the standard prices.
	// A field left 0 is unknown, never free.
	Input        USDPerMTok
	CacheWrite5m USDPerMTok
	CacheWrite1h USDPerMTok
	CacheRead    USDPerMTok
	Output       USDPerMTok
	// LongContext is the model's long-context surcharge, nil when it has none.
	LongContext *PriceStep
}

// PriceMultiplier is the multiplier a call sending promptTokens pays on every price:
// LongContext.Multiplier above LongContext.Above, else 1.
func (p ModelPricing) PriceMultiplier(promptTokens int64) float64 {
	if s := p.LongContext; s != nil && s.Above > 0 && promptTokens > s.Above && s.Multiplier > 0 {
		return s.Multiplier
	}
	return 1
}
