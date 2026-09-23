package types

import (
	"slices"
	"strings"
)

// SpawnKind is which agent event a magus\guard.spawn rule is judging.
//
// UnmarshalText is the only way from a name to a kind, and it refuses an unknown one.
type SpawnKind string

const (
	// SpawnKindSpawn is a new subagent being started.
	SpawnKindSpawn SpawnKind = "spawn"
	// SpawnKindContinue is a message to a subagent that already exists. A host with no
	// such tool never sends one.
	SpawnKindContinue SpawnKind = "continue"
)

// SpawnRole is where the caller of a spawn stands, computed from the job store rather than
// declared by anyone.
type SpawnRole string

const (
	// SpawnRoleRoot is a session no lease binds: the orchestrator, or a person.
	SpawnRoleRoot SpawnRole = "root"
	// SpawnRoleWorker is a session a lease binds in this checkout.
	SpawnRoleWorker SpawnRole = "worker"
)

// SpawnDecision is what a magus\guard.spawn rule answers. The zero value allows, so an
// empty SpawnVerdict{} is the pass a rule returns when it has nothing to say.
type SpawnDecision string

const (
	// SpawnAllow adds nothing to the built-in verdict.
	SpawnAllow SpawnDecision = "allow"
	// SpawnAdvise lets the call through with context for the agent.
	SpawnAdvise SpawnDecision = "advise"
	// SpawnDeny blocks the call.
	SpawnDeny SpawnDecision = "deny"
)

// rank orders decisions by strictness, so merging two verdicts keeps the stricter. An
// undeclared decision ranks as deny: a verdict nobody can read must not pass as allow.
func (d SpawnDecision) rank() int {
	switch d {
	case "", SpawnAllow:
		return 0
	case SpawnAdvise:
		return 1
	}
	return 2
}

// SpawnRequest is what a magus\guard.spawn rule is handed: one agent spawn or
// continuation, normalized from whichever host sent it.
//
// Every field is a fact the host reported or magus recorded. A host that does not report
// a field leaves it empty; nothing here is inferred from a host's name or defaults.
type SpawnRequest struct {
	Kind SpawnKind
	// Host is the agent host the hook wiring named itself as.
	Host string
	// Session is the host's session id for the CALLER, empty when it reported none.
	Session string
	// Model is the model string the caller named, raw. Empty means it named none and the
	// host picks; magus maps no model to a tier.
	Model string
	// AgentType is the subagent type the caller asked for.
	AgentType string
	// Description is the short task label the caller wrote for the spawn.
	Description string
	// Name is the host's separate addressable name for the new agent, when it has one.
	Name string
	// Prompt is the text handed over: the brief for a spawn, the message for a continue.
	Prompt string
	// Background is true only when the caller asked for it explicitly. A host whose
	// default is to run agents in the background still reports false when unasked.
	Background bool
	// Isolated is true when the caller asked for the agent to run outside this checkout,
	// such as in its own worktree.
	Isolated bool
	// Parent is the description the CALLING agent was itself spawned with, empty when the
	// caller is a root session or its spawn was never recorded.
	Parent string
	// Role is worker when a lease binds the calling session in this checkout.
	Role SpawnRole
	// Lease is the job row a worker acts under, nil for root. A bound id the job store
	// does not carry comes back with only its ID set.
	Lease *Job
	// Target is the agent a continue addresses, nil on a spawn.
	Target *SpawnTarget
}

// SpawnTarget is the existing agent a continue is addressed to.
type SpawnTarget struct {
	// Agent is the name or id the caller addressed.
	Agent string
	// IdleMs is how long ago magus last saw this agent spawned, continued or finish its
	// spawn call, in milliseconds. Nil when magus has no record of it.
	IdleMs *int64
	// Description is the title the agent was spawned with, empty when magus never saw
	// its spawn finish.
	Description string
	// Model is the model its spawn named, raw, empty when the spawn named none.
	Model string
	// ContextTokens is the agent's last observed context size: input plus cache-read
	// plus cache-write tokens from the latest usage its host reported for it. Nil when
	// the host reported none.
	ContextTokens *int64
}

// SpawnVerdict is what a magus\guard.spawn rule returns. The zero value allows.
type SpawnVerdict struct {
	Decision SpawnDecision
	// Reason is shown to the agent: the refusal for a deny, the context for an advise.
	Reason string
}

// StricterSpawnVerdict merges two verdicts on one call, keeping the stricter decision:
// deny over advise over allow. When both carry the same decision their reasons are both
// kept, once each, so two rules that agree on a deny still explain themselves.
func StricterSpawnVerdict(a, b SpawnVerdict) SpawnVerdict {
	switch {
	case a.Decision.rank() > b.Decision.rank():
		return a
	case b.Decision.rank() > a.Decision.rank():
		return b
	}
	var reasons []string
	for _, r := range []string{a.Reason, b.Reason} {
		if r = strings.TrimSpace(r); r != "" && !slices.Contains(reasons, r) {
			reasons = append(reasons, r)
		}
	}
	return SpawnVerdict{Decision: a.Decision, Reason: strings.Join(reasons, "\n\n")}
}
