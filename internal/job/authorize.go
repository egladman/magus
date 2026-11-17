package job

import (
	"context"
	"os"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
)

// EnvStampedLease marks a process whose BAGGAGE lease was stamped by the transport that
// started it on behalf of a caller, rather than claimed by the process's own spawner: a
// shared server forks a script for a remote caller with the caller's lease, or with none.
//
// It only ever DOWNGRADES: under it a missing lease is [job.Actor.Unstamped] rather than
// unbound, and a checkout record naming a different lease makes the actor unstamped rather
// than letting either win. A process that sets it on itself can lose rights, never gain one.
const EnvStampedLease = "MAGUS_STAMPED_LEASE"

// ActingActor is the party this process acts as for the checkout loc places: the lease
// [ActingLease] resolves from the checkout's record and this process's BAGGAGE claim. A
// process with no lease is the unbound actor, unless [EnvStampedLease] says a transport
// stamped that absence.
func ActingActor(loc Location) job.Actor {
	claim := trail.LeaseFromEnv()
	lease, _ := ActingLease(loc, claim)
	if os.Getenv(EnvStampedLease) == "" {
		return job.Actor{Lease: lease}
	}
	if claim == "" || lease != claim {
		return job.Actor{Unstamped: true}
	}
	return job.Actor{Lease: claim}
}

// RunLease is the lease [ActingLease] resolves for work running on ctx. The claim is the
// lease of the process that asked for the work, which ctx carries whenever an entry point
// stamped it: the CLI stamps its BAGGAGE, and the daemon stamps a client's lease, or the
// client's lack of one, so a client run never reads the daemon's own environment. Only a
// ctx no entry point stamped, such as an embedding Go program's, falls back to BAGGAGE.
func RunLease(ctx context.Context, loc Location) (string, job.LeaseSource) {
	claim, ok := job.LookupLease(ctx)
	if !ok {
		claim = trail.LeaseFromEnv()
	}
	return ActingLease(loc, claim)
}

// As returns a Store over s's file that writes as a, for a door serving a caller other
// than this process: the shared server's job tool acts as whoever called it. The view
// takes its own mutex; the file lock still serializes it against s and every other writer.
func (s *Store) As(a job.Actor) *Store {
	v := s.view()
	v.actor = &a
	return v
}

// unstampedRemedy is what a refused [job.Actor.Unstamped] caller is told to do, the
// module store's refusals included (see [diagnose]).
const unstampedRemedy = "send the lease you act under with the call (a baggage header carrying " + trail.BaggageLease +
	"), or make the write from your own checkout, where magus reads it for you"

// refuse builds the refusal for actor's attempt to WRITE lease id.
func refuse(actor job.Actor, id, rule string) error {
	if actor.Unstamped {
		return &job.RefusedError{Lease: id, Actor: actor, Rule: rule, Remedy: unstampedRemedy}
	}
	return &job.RefusedError{
		Lease: id, Actor: actor, Rule: rule,
		Remedy: "ask your orchestrator to make this write",
	}
}

// authorizeRow is [job.AuthorizeRow] with magus's rules added: a proved claim graded by
// [authorizeClaim], and the gate a child may run (see [authorizeGate]).
func authorizeRow(actor job.Actor, id string, kind job.WriteKind, prev, next job.Job, exists bool, rows []job.Job) error {
	if kind == job.AsClaim {
		return authorizeClaim(actor, id, prev, next, exists, rows)
	}
	if err := job.AuthorizeRow(actor, id, kind, prev, next, exists, rows); err != nil {
		return diagnose(err)
	}
	return authorizeGate(actor, id, next, exists, rows)
}

// authorizeGate refuses a bound worker a new child whose check is the release gate its own
// row lacks. cmd/magus/guard_gate.go reads the gate as a capability rather than as a
// declaration: a row whose check names it may run it, so such a child would be minting
// the capability its parent was refused.
func authorizeGate(actor job.Actor, id string, next job.Job, exists bool, rows []job.Job) error {
	if !actor.Bound() || actor.Unstamped || exists || id == actor.Lease || next.Parent != actor.Lease {
		return nil
	}
	own := slices.IndexFunc(rows, func(r job.Job) bool { return r.ID == actor.Lease })
	if own < 0 {
		return nil
	}
	if parent := rows[own]; runsTheGate(next) && !runsTheGate(parent) {
		return refuse(actor, id, "only a lease whose parent runs the gate runs the gate, and this parent runs "+checkLine(parent))
	}
	return nil
}

// runsTheGate reports whether a row's declared check IS the release gate, the one check
// that carries a capability with it.
func runsTheGate(row job.Job) bool {
	return slices.ContainsFunc(row.EffectiveGoals(), func(gate job.Goal) bool {
		return gate.Check.Target == types.TargetCI
	})
}

// checkLine names a row's check as a refusal quotes it.
func checkLine(row job.Job) string {
	gates := row.EffectiveGoals()
	if len(gates) == 0 {
		return "no check at all"
	}
	lines := make([]string, 0, len(gates))
	for _, gate := range gates {
		lines = append(lines, CheckCommand(gate.Check))
	}
	return strings.Join(lines, ", ")
}
