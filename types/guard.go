package types

import (
	"fmt"
	"slices"
	"strings"
)

// AgentRole is where the calling agent stands, computed from the job store rather than
// declared by anyone.
type AgentRole string

const (
	// AgentRoleRoot is a session no lease binds: the orchestrator, or a person.
	AgentRoleRoot AgentRole = "root"
	// AgentRoleWorker is a session a lease binds in this checkout.
	AgentRoleWorker AgentRole = "worker"
)

// Values lists the roles a caller may name, excluding the zero value.
func (r AgentRole) Values() []string {
	return []string{string(AgentRoleRoot), string(AgentRoleWorker)}
}

// Valid reports whether r is a declared role or unset.
func (r AgentRole) Valid() bool {
	switch r {
	case "", AgentRoleRoot, AgentRoleWorker:
		return true
	}
	return false
}

// String renders r for an error message: the value, or "unset" when empty.
func (r AgentRole) String() string {
	if r == "" {
		return "unset"
	}
	return string(r)
}

// MarshalText writes the name as given.
func (r AgentRole) MarshalText() ([]byte, error) { return []byte(r), nil }

// UnmarshalText sets r from a name, refusing one outside Values.
func (r *AgentRole) UnmarshalText(text []byte) error {
	v := AgentRole(text)
	if !v.Valid() {
		return fmt.Errorf("unknown agent role %q (want one of %v)", text, v.Values())
	}
	*r = v
	return nil
}

// GuardDecision is what a workspace guard rule (magus\guard.spawn, magus\guard.command)
// answers. The zero value allows, so an empty GuardVerdict{} is the pass a rule returns
// when it has nothing to say.
type GuardDecision string

const (
	// GuardAllow adds nothing to the built-in verdict.
	GuardAllow GuardDecision = "allow"
	// GuardAdvise lets the call through with context for the agent.
	GuardAdvise GuardDecision = "advise"
	// GuardDeny blocks the call.
	GuardDeny GuardDecision = "deny"
)

// Values lists the decisions a rule may return, excluding the zero value.
func (d GuardDecision) Values() []string {
	return []string{string(GuardAllow), string(GuardAdvise), string(GuardDeny)}
}

// Valid reports whether d is a declared decision or unset.
func (d GuardDecision) Valid() bool {
	switch d {
	case "", GuardAllow, GuardAdvise, GuardDeny:
		return true
	}
	return false
}

// String renders d for an error message: the value, or "unset" when empty.
func (d GuardDecision) String() string {
	if d == "" {
		return "unset"
	}
	return string(d)
}

// MarshalText writes the name as given.
func (d GuardDecision) MarshalText() ([]byte, error) { return []byte(d), nil }

// UnmarshalText sets d from a name, refusing one outside Values.
func (d *GuardDecision) UnmarshalText(text []byte) error {
	v := GuardDecision(text)
	if !v.Valid() {
		return fmt.Errorf("unknown guard decision %q (want one of %v)", text, v.Values())
	}
	*d = v
	return nil
}

// rank orders decisions by strictness, so merging two verdicts keeps the stricter. An
// undeclared decision ranks as deny: a verdict nobody can read must not pass as allow.
func (d GuardDecision) rank() int {
	switch d {
	case "", GuardAllow:
		return 0
	case GuardAdvise:
		return 1
	}
	return 2
}

// GuardVerdict is what a workspace guard rule returns. The zero value allows.
type GuardVerdict struct {
	Decision GuardDecision
	// Reason is shown to the agent: the refusal for a deny, the context for an advise.
	Reason string
}

// StricterGuardVerdict merges two verdicts on one call, keeping the stricter decision:
// deny over advise over allow. When both carry the same decision their reasons are both
// kept, once each, so two rules that agree on a deny still explain themselves.
func StricterGuardVerdict(a, b GuardVerdict) GuardVerdict {
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
	return GuardVerdict{Decision: a.Decision, Reason: strings.Join(reasons, "\n\n")}
}

// CommandRequest is what a magus\guard.command rule is handed: one shell command an agent
// is about to run, with the facts magus parsed from it and recorded about its caller.
//
// Every field is a fact the host reported or magus computed. A host that does not report
// a field leaves it empty; nothing here is inferred from a host's name.
type CommandRequest struct {
	// Host is the agent host the hook wiring named itself as.
	Host string
	// Session is the host's session id for the caller, empty when it reported none.
	Session string
	// Command is the shell line as the host sent it.
	Command string
	// Description is the label the caller wrote for the command, empty when its host
	// has no such field or the caller left it blank.
	Description string
	// Commands are the programs the line runs, resolved through wrappers (env, timeout,
	// sh -c, ...) the way the built-in rules see them. Empty when the line does not parse,
	// which is also a line the shell would refuse to run.
	Commands []CommandInvocation
	// Parent is the description the calling subagent was itself spawned with, empty for a
	// root session or one whose spawn magus never saw finish.
	Parent string
	// Role is worker when a lease binds the calling session in this checkout.
	Role AgentRole
	// Lease is the job row a worker acts under, nil for root. A bound id the job store
	// does not carry comes back with only its ID set.
	Lease *Job
	// Checkout is the state of the checkout a push leaves from, read only for a line that
	// pushes, since reading it costs processes. Nil otherwise, and when the checkout's
	// version control cannot report it: a rule reads nil as unknown.
	Checkout *CheckoutState
	// Dir is the directory the line runs in, as the host reported it; empty when it
	// reported none.
	Dir string
	// Workspace is the root of the workspace holding Dir, empty when none does.
	Workspace string
}

// GuardBinary is the magus binary answering a guard hook, as magus\guard.binary() reports
// it to a rule.
type GuardBinary struct {
	// Path is the running executable, absolute with symlinks resolved; empty when the
	// operating system cannot say.
	Path string
	// Stamp is the text the build passed through the linker (`-X
	// github.com/egladman/magus/internal/interp/bindings.buildStamp=<text>`), empty for a
	// build that passed none. magus never reads it: its format is whatever the build that
	// wrote it and the rule that reads it agree on.
	Stamp string
}

// WriteRequest is what a magus\guard.write rule is handed: one file an agent is about to
// write through its host's edit tools, with the text the host said it writes.
type WriteRequest struct {
	// Host, Session, Parent, Role and Lease are the caller's, as on CommandRequest.
	Host    string
	Session string
	Parent  string
	Role    AgentRole
	Lease   *Job
	// Path is the file as the host named it.
	Path string
	// Workspace is the root of the workspace holding Path, empty when none does.
	Workspace string
	// Content is the whole new content of a write that replaces the file, empty for an
	// edit.
	Content string
	// OldText and NewText are an edit's replaced text and its replacement, empty for a
	// write that replaces the file. A host edit shape magus does not read leaves all
	// three empty, which a rule reads as "unknown", never as "empty file".
	OldText string
	NewText string
}

// CommandInvocation is one program a shell line runs.
type CommandInvocation struct {
	// Program is the program's base name as the line spells it.
	Program string
	// Path is the program's file when the line names it by a path (`./magus`,
	// `/opt/bin/magus`): absolute against the request's Dir and cleaned, symlinks left
	// as spelled. Empty for a program found on PATH, and whenever the file is not known
	// before the line runs: a word holding a variable or substitution, a relative path
	// after a `cd` on the same line, or no Dir to resolve it against.
	Path string
	// Args are its arguments, each rendered to the literal text the shell would pass; a
	// word whose value is known only at run time (a variable, a substitution) renders empty.
	Args []string
	// Repeats is true when the program runs inside a while or until loop on the line, which
	// runs it again until a condition changes. A for loop walks a fixed list and does not
	// count.
	Repeats bool
}
