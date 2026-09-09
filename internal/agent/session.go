package agent

// The session-load contract: the vocabulary a per-host extraction recipe emits
// and `magus session load` reads, in the one package that both the CLI and the
// repo-root parity tests can import.
//
// Same reasoning as the guard contract beside it. package main cannot be
// imported, so a check living outside it would restate the vocabulary, and a
// restated contract is the copy that goes stale.
//
// The split from the guard is real rather than cosmetic. A guard verdict travels
// live and is shaped by what a host's hook surface can CARRY; a session event is
// read afterwards out of a host's own log and is shaped by what that log
// RECORDS. A host can be fully guarded and observe almost nothing, or the other
// way round, so the two contracts move independently.

// SessionSchemaVersion is the version of the event line every recipe emits.
// Bump it only when an existing field changes MEANING; adding a kind or a
// dimension is not a bump. A bump invalidates every installed recipe at once,
// because a reader's copy is theirs from the moment they download it.
const SessionSchemaVersion = 1

// SessionCoverageMarker introduces a recipe's machine-readable statement of what
// its host can supply, and is what a reader greps for in their own copy.
const SessionCoverageMarker = "magus-session-coverage:"

// sessionDimensions is what a recipe declares a stance on: whether the host's
// store carries shell commands, a command's exit status, skill loads, the output
// of magus's own hooks, subagent spawns, and a session id to join them by.
//
// A new entry here is a promise that every recipe answers for it. The parity gate
// is what collects on that promise, and the honest answer for a host whose store
// has nothing to say is "none" rather than silence: an undeclared dimension is
// one nobody asked about, which reads in a report as a zero it never measured.
var sessionDimensions = []string{"commands", "exit", "skills", "hook-output", "spawn", "session-id"}

// sessionStances are the answers a recipe may give: the host records it, or it
// does not.
var sessionStances = []string{"yes", "none"}

// sessionKinds is every event kind a line may carry. shell.command is the one
// that gets re-judged offline against the current rules; magus.call is a direct
// tool call to magus, which never appears as a shell command and so would
// otherwise be invisible to the audit.
var sessionKinds = []string{
	"shell.command", "file.read", "file.write",
	"skill.load", "hook.output", "spawn", "magus.call",
}

// SessionDimensions returns every dimension a recipe declares coverage for.
func SessionDimensions() []string { return append([]string(nil), sessionDimensions...) }

// SessionStances returns every stance a coverage declaration may take.
func SessionStances() []string { return append([]string(nil), sessionStances...) }

// SessionKinds returns every event kind a session line may carry.
func SessionKinds() []string { return append([]string(nil), sessionKinds...) }
