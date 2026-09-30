package job

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// Actor is the party a job-store write is made by: the lease it is bound to.
//
// BOUND OR UNBOUND is the whole of the vocabulary. An unbound actor may write anything; a
// bound one is a worker acting under one row, and the rules below are what it may do to
// the book from inside it. Unbound says nothing about who is writing: an orchestrator and
// an agent nobody bound are both unbound. A process binds through the checkout's record or
// its BAGGAGE claim. An [Actor.Unstamped] caller counts as bound, to no row. The guard
// also reads the calling session's and subagent's record, which no process can see, and
// refuses a bound caller's rebinding writes before they run
// (denyLeaseScopedRebind), so for a hook-identified worker that rule is the enforcement
// and this store is the backstop for everyone else.
//
// Who the writer is beyond its lease is not the actor's: a row's registered_by is stamped
// from the write's context (trail.StampOrigin), where each door put its entry point,
// credential and host.
type Actor struct {
	// Lease is the row this session acts under, empty when it acts under none.
	Lease string
	// Unstamped is a caller the transport it arrived on could not name: a shared server
	// serves callers other than itself, and one that sent no lease may be any of them. It
	// is held to a worker's boundary with no row of its own: it may read, and every graded
	// write it makes is refused. The server's own identity is never lent to it.
	Unstamped bool
}

// EnvStampedLease marks a process whose BAGGAGE lease was stamped by the transport that
// started it on behalf of a caller, rather than claimed by the process's own spawner: a
// shared server forks a script for a remote caller with the caller's lease, or with none.
//
// It only ever DOWNGRADES: under it a missing lease is [Actor.Unstamped] rather than
// unbound, and a checkout record naming a different lease makes the actor unstamped rather
// than letting either win. A process that sets it on itself can lose rights, never gain one.
const EnvStampedLease = "MAGUS_STAMPED_LEASE"

// ActingActor is the party this process acts as for the checkout whose cache dir is
// cacheDir: the lease [ActingLease] resolves from the checkout's record and this
// process's BAGGAGE claim. A process with no lease is the unbound actor, unless
// [EnvStampedLease] says a transport stamped that absence.
func ActingActor(cacheDir string) Actor {
	claim := trail.LeaseFromEnv()
	lease, _ := ActingLease(cacheDir, claim)
	if os.Getenv(EnvStampedLease) == "" {
		return Actor{Lease: lease}
	}
	if claim == "" || lease != claim {
		return Actor{Unstamped: true}
	}
	return Actor{Lease: claim}
}

// Bound reports whether this actor is held to a worker's boundary: a worker acting under a
// lease, or an [Actor.Unstamped] caller.
func (a Actor) Bound() bool { return a.Lease != "" || a.Unstamped }

// As returns a Store over s's file that writes as a, for a door serving a caller other
// than this process: the shared server's job tool acts as whoever called it. The view
// takes its own mutex; the file lock still serializes it against s and every other writer.
func (s *Store) As(a Actor) *Store {
	return &Store{
		path: s.path, err: s.err, root: s.root, actor: &a, cacheDir: s.cacheDir,
		clock: s.clock, staleAfter: s.staleAfter, notices: s.notices, outputs: s.outputs, landed: s.landed,
	}
}

// RefuseUnstampedVerify is the refusal for an [Actor.Unstamped] caller verifying row id,
// which [Actor.Verifies] answers only as a bool.
func RefuseUnstampedVerify(id string) error {
	return refuse(Actor{Unstamped: true}, id, "verifying a row grades its holder's work, and a caller magus cannot name may be that holder")
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
	switch {
	case e.Actor.Unstamped && e.Lease == "":
		msg = "job: this call arrived with no lease stamped on it; " + e.Rule
	case e.Actor.Unstamped:
		msg = fmt.Sprintf("job: this call arrived with no lease stamped on it and row %s is what it targeted; %s",
			e.Lease, e.Rule)
	}
	if e.Remedy == "" {
		return msg
	}
	return msg + ". " + e.Remedy
}

// refuse builds the refusal for actor's attempt to WRITE lease id.
func refuse(actor Actor, id, rule string) error {
	if actor.Unstamped {
		return &RefusedError{
			Lease: id, Actor: actor, Rule: rule,
			Remedy: "Send the lease you act under with the call (a baggage header carrying " + trail.BaggageLease +
				"), or make the write from your own checkout, where magus reads it for you",
		}
	}
	return &RefusedError{
		Lease: id, Actor: actor, Rule: rule,
		Remedy: "Your orchestrator writes what a worker may not; report it as an unresolved risk and stop",
	}
}

// authorizeRow grades whether actor may turn prev into next on the row id, with rows
// standing for the rest of the book (a child row's boundary is read from the parent's).
//
// THE ENFORCEMENT POINT, and it is here rather than in a shell rule because the three
// write doors (the CLI, the client MCP tool, magus\job) all reach the store and
// only one of them can be graded by a command pattern. The guard's denial text sends a
// worker to the tool; this is what makes that mean "ask the orchestrator".
//
// An UNBOUND actor passes everything. A BOUND one may, on its own row, register the base
// it landed on, SHRINK its write paths (which is how the skill has it release one), and
// end itself in fail, no_return, or exited; on any other row it may only CREATE a child of itself
// inside its own boundary. Widening a boundary, changing the plan's shape, and grading a row
// are the orchestrator's, which is the asymmetry the whole rule exists for: a worker that
// can widen its own row has no boundary at all.
func authorizeRow(actor Actor, id string, prev, next types.Job, exists bool, rows []types.Job) error {
	if !actor.Bound() {
		return nil
	}
	if actor.Unstamped {
		return refuse(actor, id, "a caller magus cannot name may be any row's holder or none, so it writes no row")
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
			// StatePass stays refused here on purpose: pass is the claim that the work
			// met its criteria, and that judgment belongs to whoever is waiting on the
			// row, never to the holder announcing it is done.
			if next.State != types.StateFail && next.State != types.StateNoReturn && next.State != types.StateExited {
				return refuse(actor, id, fmt.Sprintf("a worker may only end its own row in %s, %s, or %s, never %s",
					types.StateFail, types.StateNoReturn, types.StateExited, next.State))
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
// EVERY BOUNDARY THE GUARD READS IS SUBSETTED, not just the write one. A child's read paths are
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
		return refuse(actor, id, "its own row is not in the job store, so there is no boundary to hand a child")
	}
	parent := rows[own]
	switch {
	case !subset(next.WritePaths, parent.WritePaths):
		return refuse(actor, id, "a child may only be handed paths its parent owns, and this one claims more")
	case !subset(ReadBoundary(next), ReadBoundary(parent)):
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
	return slices.ContainsFunc(row.EffectiveGoals(), func(gate types.CompletionGate) bool {
		return gate.Check.Target == types.TargetCI
	})
}

// gateCommand renders the command whose output will SATISFY a check, which is not
// types.LeaseCheck.String(): that renders the DECLARATION. A check naming no charm means
// the charmless run, so in a workspace setting default_charms the declaration's bare
// `magus run generate .` produces a `generate:rw` descriptor that the same check then
// refuses (see bindsTo). Quoting a command guaranteed to fail its own gate is the failure
// this avoids. Built through hint.Run so a subcommand rename is one edit; types renders
// its own form because it imports no CLI surface.
func gateCommand(c types.LeaseCheck) string {
	project := c.Project
	if project == "" {
		project = "."
	}
	args := []string{c.Target, project}
	if !c.NamesCharm() {
		args = append(args, "--no-default-charms")
	}
	if len(c.Args) > 0 {
		args = append(args, append([]string{"--"}, c.Args...)...)
	}
	return hint.Run.With(args...)
}

// checkLine names a row's check as a refusal quotes it.
func checkLine(row types.Job) string {
	gates := row.EffectiveGoals()
	if len(gates) == 0 {
		return "no check at all"
	}
	lines := make([]string, 0, len(gates))
	for _, gate := range gates {
		lines = append(lines, gateCommand(gate.Check))
	}
	return strings.Join(lines, ", ")
}

// ReadBoundary is the declarations a row may read: its write paths plus its read paths.
// The guard's focus-read deny and the store's subset check both use it.
func ReadBoundary(row types.Job) []string {
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
	return refuse(actor, actor.Lease, "clearing the job store drops every other lease's row, so it belongs to whoever declared the plan")
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
	add("criteria", prev.Criteria != next.Criteria)
	add("checkpoint", prev.Checkpoint != next.Checkpoint)
	add("write_paths", !slices.Equal(prev.WritePaths, next.WritePaths))
	add("deny_paths", !slices.Equal(prev.DenyPaths, next.DenyPaths))
	add("read_paths", !slices.Equal(prev.ReadPaths, next.ReadPaths))
	add("depends_on", !slices.Equal(prev.DependsOn, next.DependsOn))
	add("model", prev.Model != next.Model)
	add("validation", prev.Validation != next.Validation)
	add("goals", !goalsEqual(prev.Goals, next.Goals))
	add("state", prev.State != next.State)
	add("read_only", prev.ReadOnly != next.ReadOnly)
	add("reported_base", prev.ReportedBase != next.ReportedBase)
	// Stamped by the store, yet a worker's put can move it through a timeout, and a holder
	// that extends its own bound has none.
	add("deadline", prev.Deadline != next.Deadline)
	return out
}

// goalsEqual is deliberately explicit instead of reflect.DeepEqual: this path grades every
// bound worker mutation, and the model is small, typed, and stable. Every field counts: a
// holder that could rewrite a goal's kind, expectation or subject could grade itself done.
func goalsEqual(a, b []types.CompletionGate) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Description != b[i].Description ||
			a[i].Kind != b[i].Kind || a[i].Expect != b[i].Expect ||
			!slices.Equal(a[i].Paths, b[i].Paths) || !slices.Equal(a[i].Symbols, b[i].Symbols) ||
			a[i].Check.Target != b[i].Check.Target || a[i].Check.Project != b[i].Check.Project ||
			!slices.Equal(a[i].Check.Args, b[i].Check.Args) || !slices.Equal(a[i].DependsOn, b[i].DependsOn) {
			return false
		}
	}
	return true
}

// subset reports whether every declaration in inner is one outer also holds, compared as
// exact strings (a worker rewriting a glob into a wider one must not look contained), with
// one narrowing allowed one way only: a claim on one declaration of a file (`F#d`) is
// inside outer's whole-file entry for that file (`F`), never the reverse.
func subset(inner, outer []string) bool {
	for _, p := range inner {
		p := strings.TrimSpace(p)
		if slices.ContainsFunc(outer, func(o string) bool { return strings.TrimSpace(o) == p }) {
			continue
		}
		if !narrowsWholeFile(p, outer) {
			return false
		}
	}
	return true
}

// narrowsWholeFile reports whether p claims one declaration of a file that some entry in
// outer holds whole, the one case [subset] admits beyond exact string equality.
func narrowsWholeFile(p string, outer []string) bool {
	file, decl := types.SplitClaim(p)
	if decl == "" {
		return false
	}
	return slices.ContainsFunc(outer, func(o string) bool {
		of, od := types.SplitClaim(strings.TrimSpace(o))
		return od == "" && of == file
	})
}

// authorizeDelete refuses a bound holder deleting a row.
//
// Its OWN row included, and that is the difference from ending one: a holder may end its
// job (fail, no_return, exited) because that is the report it owes, and the row it leaves
// is what the orchestrator reads. Deleting it instead removes the evidence that the work
// was ever handed out, which is the one outcome no holder should be able to produce.
func authorizeDelete(actor Actor, id string) error {
	if !actor.Bound() {
		return nil
	}
	return refuse(actor, id, "deleting a row removes the record that the work was handed out, so it belongs to whoever declared the plan;"+
		" a holder ENDS its job instead")
}
