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
func Wait(ctx context.Context, store *Store, id string, result *types.JobResult, resolve AttemptResolver) (types.JobStatus, error) {
	if actor := store.Actor(); actor.Bound() {
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

	var status types.JobStatus
	_, err = store.Update(ctx, id, func(row *types.Job) {
		status = VerifyGates(*row, *result, attempt, gateAttempts, jobs)
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
