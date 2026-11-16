package verdict

import "github.com/egladman/magus/libs/apiversion"

// APIVersion is the version of the Judgment envelope, carried as api_version.
const APIVersion = apiversion.APIVersion("magus.guard/v1alpha1")

// Judgment is the neutral result of judging one call (a shell command, a write, a spawn,
// a tool call): exactly one decision, carrying the field that decision needs. This
// envelope is the stable contract a host's hook config shapes or parses; host-specific
// response dialects live in the documentation, never in code.
type Judgment struct {
	APIVersion apiversion.APIVersion `json:"api_version"`
	// Decision is pass, advise, deny or ask. It is wider than a rule's Decision: a
	// judgment can pass without any rule allowing, and can ask a person.
	Decision string `json:"decision"`
	// Reason is a deny's block reason, written for the model.
	Reason string `json:"reason,omitempty"`
	// Context is an advise's context, injected alongside the allowed call.
	Context string `json:"context,omitempty"`
	// Rule names the catalogued rule or advisory that produced this judgment, and is set
	// on every deny, advise and ask. Hosts render Reason and Context; Rule is what a
	// person looks up, reports as a false positive, or greps a trail for. Empty on a pass.
	Rule string `json:"rule,omitempty"`
	// Lease is the job this judgment was graded under, empty when the call named none.
	// It tells a typo'd id, a finished job and no lease apart.
	Lease string `json:"lease,omitempty"`
	// LeaseFrom is which source answered Lease, such as flag, agent or env.
	LeaseFrom string `json:"lease_from,omitempty"`
	// NextSteps is a deny's remedy, served only when it passes the guard for the acting
	// lease, and pre-authorized for the calls after it. Reason renders it too.
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

// NextStep is one remedy on a deny: a complete command to run, a stable id to count it
// by, and one sentence saying what it answers. It mirrors magus's types.NextStep field
// for field, since this module may not import magus; one converts to the other.
type NextStep struct {
	ID string `json:"id" yaml:"id"`
	// Run is the command for a person to paste, with real ids filled in.
	Run string `json:"run" yaml:"run"`
	// Argv is Run as an argument vector, unquoted, for a caller to exec.
	Argv []string `json:"argv,omitempty" yaml:"argv,omitempty"`
	Why  string   `json:"why,omitempty" yaml:"why,omitempty"`
	// Reads marks a script that declares it changes nothing. It clears only that one
	// invocation, never any other command.
	Reads bool `json:"reads" yaml:"reads,omitempty"`
}

// DeniedError is a call refused because the judge denied it. Judgment is the whole
// deny, so a caller reads its Rule and NextSteps.
type DeniedError struct {
	Judgment Judgment
}

// Error is the deny's Reason, which already names what to run instead.
func (e *DeniedError) Error() string { return e.Judgment.Reason }
