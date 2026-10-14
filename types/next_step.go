package types

// A breadcrumb belongs on the RESULT, not in the prose under it. Measured over 21
// days of this repo's agent sessions: the one breadcrumb magus already printed (the
// `magus query output <ref>` line after a failing run) was followed 21% of the time,
// the same rate as an unhinted next step, while re-running the same failing target
// won 84%. Text at the bottom of output does not convert.
//
// So a next is a FIELD: a harness can present it as an affordance, and because every
// entry carries a stable id, uptake per breadcrumb is a query rather than a guess.
// One that nobody takes gets deleted from the number instead of reworded.
//
// It stays a suggestion. magus informs and never decides, so a reader (or an agent)
// may ignore every one of them, and nothing here enforces an order.

// NextStep is one breadcrumb on a result: a complete command to run, a stable id to
// count it by, and one sentence saying what it answers.
//
// Run always has the real ids or refs filled in, never a placeholder, because a
// command that still needs editing is prose. Why is one sentence: a caller that
// repeats a result within a session shows Run every time and Why once.
//
// Argv is the same command as an argument vector, unquoted: Run is for a person to
// paste and Argv for a caller to exec, so the two differ wherever an argument needs
// shell quotes.
//
// Reads marks a workspace script that declares it changes nothing. A guard serves it
// to a reviewer for a single `buzz` invocation only, so it cannot clear any other
// command.
type NextStep struct {
	ID    string   `json:"id"              yaml:"id"`
	Run   string   `json:"run"             yaml:"run"`
	Argv  []string `json:"argv,omitempty"  yaml:"argv,omitempty"`
	Why   string   `json:"why,omitempty"   yaml:"why,omitempty"`
	Reads bool     `json:"reads,omitempty" yaml:"reads,omitempty"`
}
