package types

// GuardJudgment is the neutral result of judging one tool call (a shell command, a
// write, a spawn or an MCP call): exactly one decision, carrying the field that
// decision needs. This envelope is the stable contract an agent host's hook config
// shapes with -o template (or parses from -o json); the host-specific response
// dialects live in the documentation, never in code.
type GuardJudgment struct {
	SchemaVersion int    `json:"schema_version"`
	Decision      string `json:"decision"`          // one of agent.GuardDecisions
	Reason        string `json:"reason,omitempty"`  // deny: the block reason, written for the model
	Context       string `json:"context,omitempty"` // advise: context to inject alongside the allowed call
	// Rule names the catalogued rule or advisory that produced this judgment, and is set on
	// every deny, advise and ask (a workspace rule's carries its workspace prefix). Host
	// adapters still render Reason and Context; this is what a PERSON looks up, reports
	// as a false positive, or greps a trail for, and the text arm prints it beside the
	// decision for exactly that reason. Empty on a pass.
	Rule string `json:"rule,omitempty"`
	// Lease is the row this judgment was graded under, empty when the call named none.
	//
	// A session bound to a typo'd id, a session bound to a finished row, and a session
	// nobody leased are otherwise indistinguishable, and two of the three run unguarded.
	// An added optional field is not a schema bump: a glue that does not read it is
	// unaffected, which is the rule agent.GuardSchemaVersion states.
	Lease string `json:"lease,omitempty"`
	// LeaseFrom is which source answered Lease; see LeaseSource.
	LeaseFrom LeaseSource `json:"lease_from,omitempty"`
	// Next is a deny's remedy, served only when it passes the guard for the acting
	// lease, and pre-authorized for the calls after it. Reason renders it too.
	Next []NextStep `json:"next,omitempty"`
}
