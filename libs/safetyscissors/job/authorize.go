package job

import (
	"fmt"
	"slices"
	"strings"
)

// Actor is the party a job-store write is made by: the lease it is bound to.
//
// BOUND OR UNBOUND is the whole of the vocabulary. An unbound actor may write anything; a
// bound one is a worker acting under one row, and the rules below are what it may do to
// the book from inside it. Unbound says nothing about who is writing: an orchestrator and
// an agent nobody bound are both unbound. An [Actor.Unstamped] caller counts as bound, to
// no row.
//
// Who the writer is beyond its lease is not the actor's: a row's registered_by is stamped
// from the write's context through [WithOrigin], where each door put its entry point,
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

// Bound reports whether this actor is held to a worker's boundary: a worker acting under a
// lease, or an [Actor.Unstamped] caller.
func (a Actor) Bound() bool { return a.Lease != "" || a.Unstamped }

// Verifies reports whether this actor may verify row id: an unbound actor verifies any
// row, a bound one only a row below its own lease in rows' parent chain. Its own row, a
// sibling's and an ancestor's stay refused, since each would be work grading itself or
// its peers.
func (a Actor) Verifies(rows []Job, id string) bool {
	if !a.Bound() {
		return true
	}
	return slices.ContainsFunc(Ancestors(rows, id), func(r Job) bool { return r.ID == a.Lease })
}

// WriteKind is what KIND of write reaches [Store.Mutate], which decides two things: whether
// the acting party's boundary applies to it, and which store-owned fields it may set. Both
// are spelled at every call site rather than defaulted, because a write that reaches the
// file ungraded, or one that carries its own registration, is the escape the rules exist
// to close and a silent default is how one gets added.
type WriteKind int

const (
	// AsDeclaration is a caller's row: the boundary applies and every store-owned field is
	// carried forward from the stored row.
	AsDeclaration WriteKind = iota
	// AsExec is a worker reporting the base it landed on: graded, and the one
	// write that sets the registration fields.
	AsExec
	// AsVerdict is a waiter recording the pass it graded from the row's evidence:
	// graded, and the one write through which a bound holder moves a row to pass.
	AsVerdict
	// AsObservation is a guard recording a write by somebody else: ungraded, since the
	// row it lands on is by definition not the writer's, and the one write that sets
	// Unattributed.
	AsObservation
	// AsNarrowing trades a whole-file hold for claims.
	// Ungraded: the claims sit inside what the entry covered.
	// It stamps its own release.
	AsNarrowing
	// AsClaim adds proved write paths: graded by the store's [StageAuthorize] hook, and
	// the one write that appends to Claims. With no hook every claim is refused.
	AsClaim
	// AsAdvance is the one write after a fork that sets Authority. A bound caller may
	// make it only on a row it lands (see [AuthorizeRow]).
	AsAdvance
)

// Graded reports whether the acting party's boundary applies to this write.
func (g WriteKind) Graded() bool { return g != AsObservation && g != AsNarrowing }

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
	msg := fmt.Sprintf("job: lease %s is bound to this session and row %s is what this targeted: %s",
		e.Actor.Lease, e.Lease, e.Rule)
	switch {
	case e.Actor.Unstamped && e.Lease == "":
		msg = "job: this call arrived with no lease stamped on it: " + e.Rule
	case e.Actor.Unstamped:
		msg = fmt.Sprintf("job: this call arrived with no lease stamped on it and row %s is what it targeted: %s",
			e.Lease, e.Rule)
	}
	if e.Remedy == "" {
		return msg
	}
	return fmt.Sprintf("%s, %s", msg, e.Remedy)
}

// unstampedRemedy is what a refused [Actor.Unstamped] caller is told to do. A caller
// whose transport names how to send a lease says so through [WithErrorWrap].
const unstampedRemedy = "send the lease you act under with the call, or make the write from your own checkout, where the store reads it for you"

// refuse builds the refusal for actor's attempt to WRITE lease id under rule.
func refuse(actor Actor, id, rule string) error {
	if actor.Unstamped {
		return &RefusedError{Lease: id, Actor: actor, Rule: rule, Remedy: unstampedRemedy}
	}
	return &RefusedError{Lease: id, Actor: actor, Rule: rule, Remedy: "ask your orchestrator to make this write"}
}

// landsRow reports whether actor holds an integration row that lands row id:
// its own, or a branch its Integrates names.
func landsRow(actor Actor, id string, rows []Job) bool {
	if actor.Unstamped {
		return false
	}
	i := slices.IndexFunc(rows, func(r Job) bool { return r.ID == actor.Lease })
	if i < 0 || !rows[i].State.Live() || len(rows[i].Integrates) == 0 {
		return false
	}
	return id == actor.Lease || slices.Contains(rows[i].Integrates, id)
}

// AuthorizeRow grades whether actor may turn prev into next on the row id, with rows
// standing for the rest of the book (a child row's boundary is read from the parent's).
//
// THE ENFORCEMENT POINT, and it is here rather than in a shell rule because every write
// door reaches the store and only one of them can be graded by a command pattern.
//
// An UNBOUND actor passes everything. A BOUND one may, on its own row, register the base
// it landed on (which starts a declared row running, or one its ended session held), SHRINK
// its write paths (which is how it releases one), and end itself in fail, no_return, or
// exited. On any other row it may only CREATE a child of itself inside its own boundary,
// or, as an [AsVerdict] write, pass a row below it. Widening a boundary, changing the
// plan's shape, and grading its own row are the orchestrator's, which is the asymmetry the
// whole rule exists for: a worker that can widen its own row has no boundary at all.
//
// An [AsClaim] write is the store's claim rule's to grade, so this refuses it: a claim
// graded by nothing would be a widening anybody can make.
//
// equal compares the goals' checks, as [ChangedFields] does; an error from it refuses
// the write.
func AuthorizeRow(actor Actor, id string, kind WriteKind, prev, next Job, exists bool, rows []Job, equal CheckEqual) error {
	if kind == AsClaim {
		return refuse(actor, id, "no claim rule grades a claim on this store, so none is accepted")
	}
	if kind == AsAdvance {
		if !actor.Bound() || landsRow(actor, id, rows) {
			return nil
		}
		return refuse(actor, id, "only the root advances a row's authority, or the integrator that lands it, and a worker that moved its own would approve its own work")
	}
	if !actor.Bound() {
		return nil
	}
	if actor.Unstamped {
		return refuse(actor, id, "a caller the transport cannot name may be any row's holder or none, so it writes no row")
	}
	if kind == AsVerdict {
		return authorizeVerdict(actor, id, prev, next, rows, equal)
	}
	if id != actor.Lease {
		return authorizeChild(actor, id, next, exists, rows)
	}
	if !exists {
		return refuse(actor, id, "nothing declares that row, and a worker does not declare its own lease")
	}
	changed, err := ChangedFields(prev, next, equal)
	if err != nil {
		return err
	}
	for _, field := range changed {
		switch field {
		case "paths.write":
			if !Subset(next.Paths.Write, prev.Paths.Write) {
				return refuse(actor, id, "a worker may only SHRINK paths.write, which is how it releases a path, and this write widens them")
			}
		case "state":
			// Taking a declared job is what starts it, so the exec carries that move.
			if kind == AsExec && next.State == StateRunning && (prev.State == StateDeclared || prev.EndedWithSession()) {
				continue
			}
			// StatePass stays refused here on purpose: pass is the claim that the work
			// met its criteria, and that judgment belongs to whoever is waiting on the
			// row, never to the holder announcing it is done.
			if next.State != StateFail && next.State != StateNoReturn && next.State != StateExited {
				return refuse(actor, id, fmt.Sprintf("a worker may only end its own row in %s, %s, or %s, never %s",
					StateFail, StateNoReturn, StateExited, next.State))
			}
		case "reported_base":
			// The registration, which is what a worker is asked for.
		default:
			return refuse(actor, id, fmt.Sprintf("a worker may not change %s on its own row", field))
		}
	}
	return nil
}

// authorizeVerdict grades a bound holder's verdict: on a row below its own lease (see
// [Actor.Verifies]), the move to pass and no other change.
func authorizeVerdict(actor Actor, id string, prev, next Job, rows []Job, equal CheckEqual) error {
	if !actor.Verifies(rows, id) {
		return refuse(actor, id, "a holder grades only the rows below its own lease, never its own, a sibling's or an ancestor's")
	}
	changed, err := ChangedFields(prev, next, equal)
	if err != nil {
		return err
	}
	for _, field := range changed {
		if field != "state" || next.State != StatePass {
			return refuse(actor, id, fmt.Sprintf("a verdict moves a row to %s and changes nothing else, and this one changes %s",
				StatePass, field))
		}
	}
	return nil
}

// authorizeChild grades a bound worker's write to a row that is not its own: a new child
// of its own lease, inside its own boundary, and nothing else.
//
// EVERY BOUNDARY IS SUBSETTED, not just the write one. A child's read paths are the READ
// boundary and its deny paths are subtracted from the write one, so a worker that could
// widen either by spawning has no boundary: it binds a session to the child and reads or
// writes what its own row denies it.
//
// The check is otherwise free: which check a child runs is how the parent partitions its
// own work. A caller whose checks carry a capability adds that rule through the store's
// [StageAuthorize] hook.
func authorizeChild(actor Actor, id string, next Job, exists bool, rows []Job) error {
	if exists {
		return refuse(actor, id, "a worker writes no row but its own and the children it hands out")
	}
	if next.Parent != actor.Lease {
		return refuse(actor, id, fmt.Sprintf("a row a worker creates must name %s as its parent, and this one names %q", actor.Lease, next.Parent))
	}
	own := slices.IndexFunc(rows, func(r Job) bool { return r.ID == actor.Lease })
	if own < 0 {
		return refuse(actor, id, "its own row is not in the job store, so there is no boundary to hand a child")
	}
	parent := rows[own]
	switch {
	case !Subset(next.Paths.Write, parent.Paths.Write):
		return refuse(actor, id, "a child may only be handed paths its parent owns, and this one claims more")
	case !Subset(ReadBoundary(next), ReadBoundary(parent)):
		return refuse(actor, id, "a child may only read what its parent reads, and this one's read paths reach further")
	case !listedIn(next.Breaks, parent.Breaks):
		return refuse(actor, id, "a child may only break what its parent may break, and this one's breaks reach further")
	case !Subset(parent.Paths.Deny, next.Paths.Deny):
		return refuse(actor, id, "a child carries every deny_path its parent carries, and this one drops some")
	case next.State != "" && next.State != StateDeclared:
		return refuse(actor, id, fmt.Sprintf("a child is handed out %s or with no state at all, never %s: grading a row is the orchestrator's",
			StateDeclared, next.State))
	}
	return nil
}

// ReadBoundary is the declarations a row may read: its write paths plus its read paths.
// A guard's focus-read deny and the store's subset check both use it.
func ReadBoundary(row Job) []string {
	if len(row.Paths.Read) == 0 {
		return row.Paths.Write
	}
	return append(slices.Clone(row.Paths.Read), row.Paths.Write...)
}

// authorizeClear grades wiping the book. A worker never does: clear drops rows it did
// not write, including the one grading it.
func authorizeClear(actor Actor) error {
	if !actor.Bound() {
		return nil
	}
	return refuse(actor, actor.Lease, "clearing the job store drops every other lease's row, so it belongs to whoever declared the plan")
}

// ChangedFields names the DECLARED fields that differ between two versions of a row, in
// the wire spelling a refusal quotes. Store-computed fields (the timestamps, releases,
// the registration verdict) are not here: nothing a caller sends decides them.
//
// equal compares two goals' checks, which only the caller can read (see [WithCheckSpec]);
// an error from it is a [*CheckRefusedError], and a nil equal asked to compare two checks
// an [*UnconfiguredError].
func ChangedFields(prev, next Job, equal CheckEqual) ([]string, error) {
	goals, err := goalsEqual(next.ID, prev.Goals, next.Goals, equal)
	if err != nil {
		return nil, err
	}
	var out []string
	add := func(name string, differs bool) {
		if differs {
			out = append(out, name)
		}
	}
	add("parent", prev.Parent != next.Parent)
	out = append(out, changedAgentFields(prev.Agent, next.Agent)...)
	add("checkpoint", prev.Checkpoint != next.Checkpoint)
	add("paths.write", !slices.Equal(prev.Paths.Write, next.Paths.Write))
	add("paths.deny", !slices.Equal(prev.Paths.Deny, next.Paths.Deny))
	add("paths.read", !slices.Equal(prev.Paths.Read, next.Paths.Read))
	add("depends_on", !slices.Equal(prev.DependsOn, next.DependsOn))
	add("integrates", !slices.Equal(prev.Integrates, next.Integrates))
	add("paths.soft", !slices.Equal(prev.Paths.Soft, next.Paths.Soft))
	add("breaks", !slices.Equal(prev.Breaks, next.Breaks))
	add("follows", prev.Follows != next.Follows)
	add("brief_format", prev.BriefFormat != next.BriefFormat)
	add("delivery", prev.Delivery != next.Delivery)
	add("parent_head", prev.ParentHead != next.ParentHead)
	add("validation", prev.Validation != next.Validation)
	add("goals", !goals)
	add("deviations", !slices.Equal(prev.Deviations, next.Deviations))
	add("state", prev.State != next.State)
	add("read_only", prev.ReadOnly != next.ReadOnly)
	add("max_attempts", prev.MaxAttempts != next.MaxAttempts)
	add("max_claims", prev.MaxClaims != next.MaxClaims)
	add("weight", prev.Weight != next.Weight)
	add("reported_base", prev.ReportedBase != next.ReportedBase)
	// Stamped by the store, yet a worker's put can move it through a timeout, and a holder
	// that extends its own bound has none.
	add("deadline", prev.Deadline != next.Deadline)
	return out, nil
}

// SpecChanges names the spec fields that differ between prev and next, in the wire
// spelling: [ChangedFields] less the ones a declaration never writes.
func SpecChanges(prev, next Job, equal CheckEqual) ([]string, error) {
	changed, err := ChangedFields(prev, next, equal)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(changed, func(f string) bool {
		return f == "state" || f == "reported_base" || f == "deadline"
	}), nil
}

// changedAgentFields names what differs between two agent sections: agent
// when only one exists, else each member that differs.
func changedAgentFields(prev, next *Agent) []string {
	switch {
	case prev == nil && next == nil:
		return nil
	case prev == nil || next == nil:
		return []string{"agent"}
	}
	var out []string
	if prev.Model != next.Model {
		out = append(out, "agent.model")
	}
	if prev.Instructions != next.Instructions {
		out = append(out, "agent.instructions")
	}
	if prev.Worker != next.Worker {
		out = append(out, "agent.worker")
	}
	return out
}

// goalsEqual is deliberately explicit instead of reflect.DeepEqual: this path grades every
// bound worker mutation, and the model is small, typed, and stable. Every field counts: a
// holder that could rewrite a goal's kind, expectation or subject could grade itself done.
// The checks go through equal, the caller's reading of them; id names the row in its
// refusal.
func goalsEqual(id string, a, b []Goal, equal CheckEqual) (bool, error) {
	if len(a) != len(b) {
		return false, nil
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Description != b[i].Description ||
			a[i].Kind != b[i].Kind || a[i].Expect != b[i].Expect ||
			!slices.Equal(a[i].Paths, b[i].Paths) || !slices.Equal(a[i].Symbols, b[i].Symbols) ||
			!slices.Equal(a[i].DependsOn, b[i].DependsOn) {
			return false, nil
		}
		same, err := checksEqual(id, a[i], b[i], equal)
		if err != nil || !same {
			return false, err
		}
	}
	return true, nil
}

// checksEqual compares a's and b's checks through equal. Two goals that declare none are
// equal without it.
func checksEqual(id string, a, b Goal, equal CheckEqual) (bool, error) {
	switch has := HasCheck(a.Check); {
	case has != HasCheck(b.Check):
		return false, nil
	case !has:
		return true, nil
	case equal == nil:
		return false, &UnconfiguredError{Op: "ChangedFields", Option: "WithCheckSpec"}
	}
	same, err := equal(a.Check, b.Check)
	if err != nil {
		return false, &CheckRefusedError{Job: id, Field: goalCheckField(b.ID), Err: err}
	}
	return same, nil
}

// listedIn reports whether every entry of inner is one outer also lists, as exact strings.
// Breaks entries are free text, so unlike [Subset] a `#` in one claims nothing.
func listedIn(inner, outer []string) bool {
	return !slices.ContainsFunc(inner, func(p string) bool {
		return !slices.ContainsFunc(outer, func(o string) bool { return strings.TrimSpace(o) == strings.TrimSpace(p) })
	})
}

// Subset reports whether every declaration in inner is within some entry of outer by
// [PathWithin], so a holder may narrow a glob to the files it matches or a file to
// one declaration of it. A glob rewritten into a wider one is not within the narrower
// (`internal/**` is not within `internal/job/*.go`).
func Subset(inner, outer []string) bool {
	for _, p := range inner {
		p := strings.TrimSpace(p)
		if !slices.ContainsFunc(outer, func(o string) bool { return PathWithin(p, strings.TrimSpace(o)) }) {
			return false
		}
	}
	return true
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

// forkAuthority is the Authority a fork by actor records (ADR 0045). The
// transport named actor, so no declared parent or checkpoint decides it. A
// worker's child takes its parent's, and an unbound fork the store's head.
func forkAuthority(actor Actor, rows []Job, head func() string) string {
	if !actor.Bound() {
		return head()
	}
	if i := slices.IndexFunc(rows, func(r Job) bool { return r.ID == actor.Lease }); i >= 0 {
		return rows[i].Authority
	}
	return ""
}
