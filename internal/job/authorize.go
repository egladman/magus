package job

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// Actor is the party a ledger write is made by: the lease it is bound to, and the
// session and host that identify it.
//
// BOUND OR UNBOUND is the whole of the vocabulary. An unbound actor is the orchestrator
// or the person at a terminal and may write anything; a bound one is a worker acting
// under one row, and the rules below are what it may do to the book from inside it.
// Binding is the checkout's lease marker or the BAGGAGE channel, the same two the guard
// reads, so a worker cannot be one party to the guard and another to the store.
type Actor struct {
	// Lease is the row this session acts under, empty when it acts under none.
	Lease string
	// Session and Host identify the actor on the rows it creates. Both are best-effort:
	// a person at a terminal has neither, and no rule keys on them.
	Session string
	Host    string
}

// ActingActor is the party this process acts as for the checkout whose cache dir is
// cacheDir: the bound lease from [ActingLease], and the identity the trace channel
// carries. A process with no lease and no trace is the unbound actor, which is what a
// person running `magus ledger register` is.
func ActingActor(cacheDir string) Actor {
	spawn := trail.SpawnFromEnv()
	return Actor{Lease: ActingLease(cacheDir), Session: spawn.TraceID, Host: spawn.Spawner}
}

// Bound reports whether this actor is a worker acting under a lease.
func (a Actor) Bound() bool { return a.Lease != "" }

// leaseActor is what a row stores about the session that created it.
func (a Actor) leaseActor() types.JobActor {
	return types.JobActor{Session: a.Session, Host: a.Host}
}

// grading is what KIND of write reaches [Store.mutate], which decides two things: whether
// the acting party's boundary applies to it, and which store-owned fields it may set. Both
// are spelled at every call site rather than defaulted, because a write that reaches the
// file ungraded, or one that carries its own registration, is the escape the rules exist
// to close and a silent default is how one gets added.
type grading int

const (
	// asDeclaration is a caller's row: the boundary applies and every store-owned field is
	// carried forward from the stored row.
	asDeclaration grading = iota
	// asExec is a worker reporting the base it landed on: graded, and the one
	// write that sets the registration fields.
	asExec
	// asObservation is the guard recording a write by somebody else: ungraded, since the
	// row it lands on is by definition not the writer's, and the one write that sets
	// Unattributed.
	asObservation
)

// graded reports whether the acting party's boundary applies to this write.
func (g grading) graded() bool { return g != asObservation }

// RefusedError is a write the store would not apply, naming the rule, the actor it
// applied to, and what the actor should do instead.
//
// A typed error rather than a string so a door can tell a refusal (the actor may not do
// this) from a failure (the file would not open): the first is an answer about the
// caller and the second is not.
type RefusedError struct {
	// Lease is the row the write targeted, and Actor the party that attempted it. The two
	// differ whenever a worker writes a child, which is why the message names both.
	Lease string
	Actor Actor
	// Rule is the sentence naming what was refused and why.
	Rule string
	// Remedy is what the actor does instead, empty when there is nothing to do: a refused
	// bind wrote nothing, so there is no unresolved risk to report.
	Remedy string
}

func (e *RefusedError) Error() string {
	msg := fmt.Sprintf("job: lease %s is bound to this session and row %s is what this targeted; %s",
		e.Actor.Lease, e.Lease, e.Rule)
	if e.Remedy == "" {
		return msg
	}
	return msg + ". " + e.Remedy
}

// refuse builds the refusal for actor's attempt to WRITE lease id.
func refuse(actor Actor, id, rule string) error {
	return &RefusedError{
		Lease: id, Actor: actor, Rule: rule,
		Remedy: "Your orchestrator writes what a worker may not; report it as an unresolved risk and stop",
	}
}

// authorizeRow grades whether actor may turn prev into next on the row id, with rows
// standing for the rest of the book (a child row's boundary is read from the parent's).
//
// THE ENFORCEMENT POINT, and it is here rather than in a shell rule because the three
// write doors (the CLI, the magus_ledger MCP tool, magus\ledger) all reach the store and
// only one of them can be graded by a command pattern. The guard's denial text sends a
// worker to the tool; this is what makes that mean "ask the orchestrator".
//
// An UNBOUND actor passes everything. A BOUND one may, on its own row, register the base
// it landed on, SHRINK its write paths (which is how the skill has it release one), and
// end itself in fail or no_return; on any other row it may only CREATE a child of itself
// inside its own boundary. Widening a lane, changing the plan's shape, and grading a row
// are the orchestrator's, which is the asymmetry the whole rule exists for: a worker that
// can widen its own row has no boundary at all.
func authorizeRow(actor Actor, id string, prev, next types.Job, exists bool, rows []types.Job) error {
	if !actor.Bound() {
		return nil
	}
	if id != actor.Lease {
		return authorizeChild(actor, id, next, exists, rows)
	}
	if !exists {
		return refuse(actor, id, "nothing declares that row, and a worker does not declare its own lease")
	}
	for _, field := range changedFields(prev, next) {
		switch field {
		case "write_paths":
			if !subset(next.WritePaths, prev.WritePaths) {
				return refuse(actor, id, "a worker may only SHRINK write_paths, which is how it releases a path, and this write widens them")
			}
		case "state":
			if next.State != types.StateFail && next.State != types.StateNoReturn {
				return refuse(actor, id, fmt.Sprintf("a worker may only end its own row in %s or %s, never %s",
					types.StateFail, types.StateNoReturn, next.State))
			}
		case "reported_base":
			// The registration, which is what a worker is asked for.
		default:
			return refuse(actor, id, "a worker may not change "+field+" on its own row")
		}
	}
	return nil
}

// authorizeChild grades a bound worker's write to a row that is not its own: a new child
// of its own lease, inside its own boundary, and nothing else.
//
// EVERY LANE THE GUARD READS IS SUBSETTED, not just the write one. A child's read paths are
// the READ boundary (cmd/magus/guard_focus.go) and its deny paths are subtracted from the
// write one, so a worker that could widen either by spawning has no boundary: it binds a
// session to the child and reads or writes what its own row denies it.
//
// The check is otherwise free: which check a child runs is how the parent partitions its
// own work, and it grades that child's report and nothing else. The ONE exception is the
// gate, which cmd/magus/guard_gate.go reads as a capability rather than as a declaration:
// a row whose check names it may run it, so a worker declaring a child with a gate check
// its own row lacks would be minting the capability it was refused.
func authorizeChild(actor Actor, id string, next types.Job, exists bool, rows []types.Job) error {
	if exists {
		return refuse(actor, id, "a worker writes no row but its own and the children it hands out")
	}
	if next.Parent != actor.Lease {
		return refuse(actor, id, fmt.Sprintf("a row a worker creates must name %s as its parent, and this one names %q", actor.Lease, next.Parent))
	}
	own := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == actor.Lease })
	if own < 0 {
		return refuse(actor, id, "its own row is not in the ledger, so there is no boundary to hand a child")
	}
	parent := rows[own]
	switch {
	case !subset(next.WritePaths, parent.WritePaths):
		return refuse(actor, id, "a child may only be handed paths its parent owns, and this one claims more")
	case !subset(readLane(next), readLane(parent)):
		return refuse(actor, id, "a child may only read what its parent reads, and this one's read paths reach further")
	case !subset(parent.DenyPaths, next.DenyPaths):
		return refuse(actor, id, "a child carries every deny_path its parent carries, and this one drops some")
	case next.State != "" && next.State != types.StateDeclared:
		return refuse(actor, id, fmt.Sprintf("a child is handed out %s or with no state at all, never %s: grading a row is the orchestrator's",
			types.StateDeclared, next.State))
	case runsTheGate(next) && !runsTheGate(parent):
		return refuse(actor, id, "only a lease whose parent runs the gate runs the gate, and this parent runs "+checkLine(parent))
	}
	return nil
}

// runsTheGate reports whether a row's declared check IS the release gate, the one check
// that carries a capability with it.
func runsTheGate(row types.Job) bool {
	return row.Check != nil && row.Check.Target == types.TargetCI
}

// checkLine names a row's check as a refusal quotes it.
func checkLine(row types.Job) string {
	if row.Check == nil {
		return "no check at all"
	}
	return row.Check.String()
}

// readLane is the declarations a row may READ: its read paths when it declares any, else
// its own write lane, which is the fallback cmd/magus/guard_focus.go makes. A parent's
// write lane rides along either way, since a child may already be handed it.
func readLane(row types.Job) []string {
	if len(row.ReadPaths) == 0 {
		return row.WritePaths
	}
	return append(slices.Clone(row.ReadPaths), row.WritePaths...)
}

// authorizeClear grades wiping the book. A worker never does: clear drops rows it did
// not write, including the one grading it.
func authorizeClear(actor Actor) error {
	if !actor.Bound() {
		return nil
	}
	return refuse(actor, actor.Lease, "clearing the ledger drops every other lease's row, so it belongs to whoever declared the plan")
}

// changedFields names the DECLARED fields that differ between two versions of a row, in
// the wire spelling a refusal quotes. Store-computed fields (the timestamps, releases,
// the registration verdict) are not here: nothing a caller sends decides them.
func changedFields(prev, next types.Job) []string {
	var out []string
	add := func(name string, differs bool) {
		if differs {
			out = append(out, name)
		}
	}
	add("parent", prev.Parent != next.Parent)
	add("goal", prev.Goal != next.Goal)
	add("checkpoint", prev.Checkpoint != next.Checkpoint)
	add("write_paths", !slices.Equal(prev.WritePaths, next.WritePaths))
	add("deny_paths", !slices.Equal(prev.DenyPaths, next.DenyPaths))
	add("read_paths", !slices.Equal(prev.ReadPaths, next.ReadPaths))
	add("depends_on", !slices.Equal(prev.DependsOn, next.DependsOn))
	add("model", prev.Model != next.Model)
	add("validation", prev.Validation != next.Validation)
	add("state", prev.State != next.State)
	add("read_only", prev.ReadOnly != next.ReadOnly)
	add("reported_base", prev.ReportedBase != next.ReportedBase)
	return out
}

// subset reports whether every declaration in inner is one outer also holds, compared as
// the strings they were declared as.
//
// String equality rather than path containment, because this grades a DECLARATION against
// a declaration: a worker that may rewrite its declarations into any covered form can also
// rewrite a glob into a wider one that still looks contained. The exact-string rule costs a
// worker one round trip and cannot be argued with.
//
// Both sides are trimmed here because [Store.Update] writes a types.Job the caller
// built, which no door has trimmed.
func subset(inner, outer []string) bool {
	for _, p := range inner {
		if !slices.ContainsFunc(outer, func(o string) bool { return strings.TrimSpace(o) == strings.TrimSpace(p) }) {
			return false
		}
	}
	return true
}
