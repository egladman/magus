package job

import (
	"context"
	"fmt"
	"slices"

	"github.com/egladman/magus/types"
)

// AttemptResolver resolves the recorded run behind a result's output ref. A result is
// evidence only when that ref names a real run, and the resolver is deliberately a
// function rather than a Store method: output records live in a checkout cache while
// job rows live per repository.
type AttemptResolver func(context.Context, string) (types.JobAttempt, error)

// Exit records how a worker ended one job. A nil result is an abandonment, distinct from
// a failed verification: nobody returned evidence at all. A non-nil result is filed with
// the output attempt resolved in the worker's checkout, so a verifier in another
// worktree does not need that checkout's cache to reopen the evidence.
func Exit(ctx context.Context, store *Store, id string, result *types.JobResult, resolve AttemptResolver) (types.Job, error) {
	if result == nil {
		return store.Update(ctx, id, func(row *types.Job) { row.State = types.StateNoReturn })
	}
	if resolve == nil {
		return types.Job{}, fmt.Errorf("job: no output resolver files a result's validation evidence")
	}
	attempt, gateAttempts, err := resolveResultAttempts(ctx, *result, resolve)
	if err != nil {
		return types.Job{}, err
	}
	return store.Update(ctx, id, func(row *types.Job) {
		row.Result, row.GateAttempts = result, gateAttempts
		if attempt.Found {
			row.Attempt = &attempt
		} else {
			row.Attempt = nil
		}
		row.State = types.StateExited
	})
}

// Wait verifies a returned result against the current job terms and records pass under
// the same Store update that read those terms. A rejected result is an ordinary Status,
// not an error: callers need its violations to decide what to repair. result=nil uses
// the result and attempt filed by Exit; a supplied result is resolved in this checkout.
func Wait(ctx context.Context, store *Store, id string, result *types.JobResult, resolve AttemptResolver, observe Observer) (types.JobStatus, error) {
	actor, err := store.Actor()
	if err != nil {
		return types.JobStatus{}, err
	}
	if actor.Bound() {
		return types.JobStatus{}, fmt.Errorf("job: this checkout holds the lease on %s and a holder does not verify its own work", actor.Lease)
	}
	jobs, err := store.List()
	if err != nil {
		return types.JobStatus{}, err
	}
	i := slices.IndexFunc(jobs, func(row types.Job) bool { return row.ID == id })
	if i < 0 {
		return types.JobStatus{}, fmt.Errorf("job: there is no job %q", id)
	}

	var attempt types.JobAttempt
	var gateAttempts []types.JobGateAttempt
	if result == nil {
		if jobs[i].Result == nil {
			return types.JobStatus{}, fmt.Errorf("job: %s has filed no result", id)
		}
		result = jobs[i].Result
		if jobs[i].Attempt != nil {
			attempt = *jobs[i].Attempt
		}
		gateAttempts = jobs[i].GateAttempts
	} else {
		attempt, gateAttempts, err = resolveResultAttempts(ctx, *result, resolve)
		if err != nil {
			return types.JobStatus{}, err
		}
	}

	// Observed BEFORE the update, like the attempts above and for the same reason: reading
	// a VCS under the store lock would hold every other agent's writes behind a subprocess.
	// A resolver that fails leaves the observation UNKNOWN rather than empty, and a gate
	// that needed it refuses; nothing here turns a failed look into a satisfied gate.
	var seen Observed
	if observe != nil {
		if seen, err = observe(ctx, inheritGates(jobs[i], jobs)); err != nil {
			return types.JobStatus{}, err
		}
	}

	var status types.JobStatus
	_, err = store.Update(ctx, id, func(row *types.Job) {
		status = VerifyGates(inheritGates(*row, jobs), *result, attempt, gateAttempts, jobs, seen)
		if status.Verified {
			row.State = types.StatePass
		}
	})
	if err != nil {
		return types.JobStatus{}, err
	}
	return status, nil
}

// resolveResultAttempts takes a portable snapshot for every evidence reference the
// holder supplied. It deliberately does not infer which gates exist from the result:
// verification compares these snapshots to the row's declared gates under the store
// lock, so an extra or missing reference never becomes authority.
func resolveResultAttempts(ctx context.Context, result types.JobResult, resolve AttemptResolver) (types.JobAttempt, []types.JobGateAttempt, error) {
	if resolve == nil {
		return types.JobAttempt{}, nil, fmt.Errorf("job: no output resolver files a result's evidence")
	}
	var primary types.JobAttempt
	if ref := result.Validation.OutputRef; ref != "" {
		attempt, err := resolve(ctx, ref)
		if err != nil {
			return types.JobAttempt{}, nil, err
		}
		if !attempt.Found {
			return types.JobAttempt{}, nil, fmt.Errorf("job: the result's output ref %q names no run this checkout recorded", ref)
		}
		primary = attempt
	}
	seen := map[string]bool{}
	gates := make([]types.JobGateAttempt, 0, len(result.GateEvidence))
	for _, evidence := range result.GateEvidence {
		if evidence.GateID == "" || evidence.OutputRef == "" {
			return types.JobAttempt{}, nil, fmt.Errorf("job: each gate_evidence entry requires gate_id and output_ref")
		}
		if seen[evidence.GateID] {
			return types.JobAttempt{}, nil, fmt.Errorf("job: the result carries duplicate evidence for completion gate %q", evidence.GateID)
		}
		seen[evidence.GateID] = true
		attempt, err := resolve(ctx, evidence.OutputRef)
		if err != nil {
			return types.JobAttempt{}, nil, err
		}
		if !attempt.Found {
			return types.JobAttempt{}, nil, fmt.Errorf("job: gate %q output ref %q names no run this checkout recorded", evidence.GateID, evidence.OutputRef)
		}
		gates = append(gates, types.JobGateAttempt{GateID: evidence.GateID, Attempt: attempt})
	}
	return primary, gates, nil
}

// GradeGates grades a job against its gates WITHOUT recording anything, so an orchestrator
// or a person can ask where the work stands while the holder is still working.
//
// The same VerifyGates every verdict comes from, so the answer cannot drift from the one
// `Wait` will reach: a probe with its own rules is a second contract, and the two disagree
// the first time either changes. What differs is only that nothing is written and the
// store's terminal-state rule is not applied, because a job still running is not a job
// being verified.
//
// It reads whatever the holder has filed so far, attempts included, and resolves nothing
// new; for a job still in flight that is usually nothing. That is the point: the gates that
// turn on evidence magus holds (files changed, symbols resolved) answer for a job that has
// filed no result at all.
func GradeGates(ctx context.Context, store *Store, id string, observe Observer) (types.JobStatus, error) {
	jobs, err := store.List()
	if err != nil {
		return types.JobStatus{}, err
	}
	i := slices.IndexFunc(jobs, func(row types.Job) bool { return row.ID == id })
	if i < 0 {
		return types.JobStatus{}, fmt.Errorf("job: there is no job %q", id)
	}
	row := inheritGates(jobs[i], jobs)

	result := types.JobResult{Job: row.ID}
	attempt, gateAttempts := types.JobAttempt{}, row.GateAttempts
	if row.Result != nil {
		result = *row.Result
		if row.Attempt != nil {
			attempt = *row.Attempt
		}
	}
	var seen Observed
	if observe != nil {
		if seen, err = observe(ctx, row); err != nil {
			return types.JobStatus{}, err
		}
	}
	// Graded against a row whose state is cleared, so the "already verified" rule does not
	// fire on a job that legitimately passed: this asks where the gates stand, and a job
	// that finished stands with all of them met.
	row.State = ""
	status := VerifyGates(row, result, attempt, gateAttempts, jobs, seen)

	// The GATES decide this verdict, not the whole result. VerifyGates also applies the
	// rules a filed result must satisfy (a change set that is not empty, paths inside the
	// declared boundary, descendants the store carries), and a job still in flight has filed no result
	// to hold to them: inherited, they report every unfinished job as failing for reasons
	// that have nothing to do with what was asked. The violations stay in the status, so a
	// reader still sees them; only the verdict is narrowed to the question.
	status.Verified = !slices.ContainsFunc(status.Gates, func(g types.GateStatus) bool { return !g.Verified })
	return status, nil
}
