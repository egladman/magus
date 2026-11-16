package job

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// graded is the evidence a verdict grades: the result, the run behind its check, and the
// runs behind its goals.
type graded struct {
	result       types.JobResult
	attempt      types.JobAttempt
	gateAttempts []types.JobGateAttempt
}

// withWaiterEvidence is ev with the runs behind the goals in refs swapped for runs
// resolved in THIS checkout, and the record Wait stores beside them once they verify, its
// verdicts and time left for Wait (see [gradedTake]). The holder's own attempts on the row
// stay as filed: ev's slices are cloned before they are edited.
//
// Each ref is resolved by resolve and nowhere else, and resolve must read this checkout's
// store alone: a fallback to the holder's checkout would make this the holder's evidence
// under another name. Verification then runs as always, so a run of the wrong check, older
// than the row or failed is refused there.
//
// It refuses a goal the job does not hold as a check, and a goal that already passes on
// the holder's evidence, which another run would only hide. It also refuses unless the
// files the job changed read the same here as in the holder's checkout and each run taken
// read them as they read here now (see [sameChangedPaths]): only then is a run here a run
// of the holder's tree. A goal records its run's attempt id where the store gave one: the
// ref typed may name a step, which the step's next run would answer for instead.
func (ev graded) withWaiterEvidence(ctx context.Context, store *Store, row types.Job, refs map[string]string, resolve AttemptResolver, seen Observed) (graded, *types.JobIntegration, error) {
	if resolve == nil {
		return graded{}, nil, errors.New("job: no output resolver reads the runs recorded in this checkout")
	}
	if store.root == "" {
		return graded{}, nil, errors.New("job: waiter evidence names runs recorded in a checkout, and this store has none")
	}
	goals := row.EffectiveGoals()
	ids := slices.Sorted(maps.Keys(refs))
	for _, goalID := range ids {
		i := slices.IndexFunc(goals, func(g types.Goal) bool { return g.ID == goalID })
		if i < 0 {
			held := make([]string, len(goals))
			for j, g := range goals {
				held[j] = g.ID
			}
			return graded{}, nil, fmt.Errorf("job: %s holds no goal %q to take evidence for (it holds %s)", row.ID, goalID, strings.Join(held, ", "))
		}
		if goals[i].Kind != types.GoalKindCheck {
			return graded{}, nil, fmt.Errorf("job: goal %q is a %s goal, and only a check goal is graded from a run", goalID, goals[i].Kind)
		}
		ownRef, ownAttempt := ev.goalRun(goalID)
		if verifyGate(row, goals[i], ownRef, ownAttempt, seen).Verified {
			return graded{}, nil, fmt.Errorf("job: goal %q already passes on the holder's own evidence, so there is no verdict for another run to replace", goalID)
		}
	}

	here, err := filepath.Abs(store.root)
	if err != nil {
		return graded{}, nil, err
	}
	out := graded{result: ev.result, attempt: ev.attempt, gateAttempts: slices.Clone(ev.gateAttempts)}
	out.result.VerificationEvidence = slices.Clone(ev.result.VerificationEvidence)
	taken := &types.JobIntegration{Checkout: here}
	runs := make([]types.JobAttempt, 0, len(ids))
	for _, goalID := range ids {
		ref := strings.TrimSpace(refs[goalID])
		found, err := resolve(ctx, ref)
		if err != nil {
			return graded{}, nil, err
		}
		if !found.Found {
			return graded{}, nil, fmt.Errorf("job: goal %q output ref %q names no run this checkout recorded", goalID, ref)
		}
		ref = cmp.Or(found.AttemptID, ref)
		out.setGoalRun(goalID, ref, found)
		taken.Gates = append(taken.Gates, types.VerificationStatus{ID: goalID, OutputRef: ref})
		runs = append(runs, found)
	}

	if !seen.ChangedKnown {
		return graded{}, nil, errors.New("job: the job's diff could not be read, so nothing names the files a run here has to share with the holder's checkout")
	}
	changed := slices.Compact(slices.Sorted(slices.Values(append(slices.Clone(seen.Changed), ev.result.ChangedPaths...))))
	if err := sameChangedPaths(ctx, row.CheckoutRoot, here, changed, runs, seen.CurrentRun); err != nil {
		return graded{}, nil, err
	}
	return out, taken, nil
}

// runs is every run ev holds: the check's, then each goal's.
func (ev graded) runs() []types.JobAttempt {
	out := append(make([]types.JobAttempt, 0, 1+len(ev.gateAttempts)), ev.attempt)
	for _, g := range ev.gateAttempts {
		out = append(out, g.Attempt)
	}
	return out
}

// goalRun is the ref and the run ev holds for goalID, zero when it holds none.
func (ev graded) goalRun(goalID string) (string, types.JobAttempt) {
	if goalID == types.PrimaryGoalID {
		return ev.result.Validation.OutputRef, ev.attempt
	}
	var (
		ref     string
		attempt types.JobAttempt
	)
	if j := slices.IndexFunc(ev.result.VerificationEvidence, func(e types.VerificationEvidence) bool { return e.GateID == goalID }); j >= 0 {
		ref = ev.result.VerificationEvidence[j].OutputRef
	}
	if j := slices.IndexFunc(ev.gateAttempts, func(g types.JobGateAttempt) bool { return g.GateID == goalID }); j >= 0 {
		attempt = ev.gateAttempts[j].Attempt
	}
	return ref, attempt
}

// setGoalRun makes ref and run the evidence for goalID, in place. ev's slices must not be
// shared with a row.
func (ev *graded) setGoalRun(goalID, ref string, run types.JobAttempt) {
	if goalID == types.PrimaryGoalID {
		ev.result.Validation.OutputRef, ev.attempt = ref, run
		return
	}
	if j := slices.IndexFunc(ev.result.VerificationEvidence, func(e types.VerificationEvidence) bool { return e.GateID == goalID }); j >= 0 {
		ev.result.VerificationEvidence[j].OutputRef = ref
	} else {
		ev.result.VerificationEvidence = append(ev.result.VerificationEvidence, types.VerificationEvidence{GateID: goalID, OutputRef: ref})
	}
	if j := slices.IndexFunc(ev.gateAttempts, func(g types.JobGateAttempt) bool { return g.GateID == goalID }); j >= 0 {
		ev.gateAttempts[j].Attempt = run
	} else {
		ev.gateAttempts = append(ev.gateAttempts, types.JobGateAttempt{GateID: goalID, Attempt: run})
	}
}

// gradedTake is taken as the row records it: each goal's verdict copied from status, the
// verdict as a whole status's own, stamped at.
func gradedTake(taken types.JobIntegration, status types.JobStatus, at int64) *types.JobIntegration {
	taken.At, taken.Verified, taken.ObservedGeneration = at, status.Verified, status.ObservedGeneration
	taken.Gates = slices.Clone(taken.Gates)
	for i, g := range taken.Gates {
		if j := slices.IndexFunc(status.Gates, func(s types.VerificationStatus) bool { return s.ID == g.ID }); j >= 0 {
			taken.Gates[i].Verified = status.Gates[j].Verified
			taken.Gates[i].Violations = slices.Clone(status.Gates[j].Violations)
		}
	}
	return &taken
}

// sameChangedPaths refuses unless every changed path reads the same under the holder's
// checkout and this one, a path absent from both counting as the same, and each of runs
// read every changed path as it reads here now. The bytes are compared now, so that second
// half is what ties them to the runs: a run made before the job's changes arrived here, or
// while they arrived, graded other files.
//
// A run is tied to the tree by the best witness its record carries:
//   - A run of a clean tree at a recorded revision read exactly that revision, so each path
//     must read here now as it does there (see [readsAsRevision]). Only a directory, such as
//     a submodule, falls to the next witness, since a revision holds none as one file.
//   - Any other run, of a dirty tree or with no revision recorded, is held to modification
//     times against its START, TimestampMs less DurationMs (see [modifiedSince]). Times are
//     the weaker witness: a filesystem with coarse timestamps can date a change made in the
//     run's first second or two before it, and a copy that keeps its times shows only
//     through the directory it lands in.
func sameChangedPaths(ctx context.Context, holder, here string, changed []string, runs []types.JobAttempt, current CurrentRun) error {
	if holder == "" {
		return errors.New("job: the job records no checkout for its holder, so nothing shows this one holds the work the holder changed")
	}
	if info, err := os.Stat(holder); err != nil || !info.IsDir() {
		return fmt.Errorf("job: the holder's checkout %s is not there to compare against", holder)
	}
	var differ, local []string
	for _, c := range changed {
		p := cleanPath(c)
		rel := filepath.FromSlash(p)
		if !filepath.IsLocal(rel) || holder != here && !sameContent(ctx, filepath.Join(holder, rel), filepath.Join(here, rel)) {
			differ = append(differ, p)
			continue
		}
		local = append(local, p)
	}
	if len(differ) > 0 {
		return fmt.Errorf("job: a run here proves the holder's work only if the changed paths read the same in both checkouts,"+
			" and %d differ between %s and %s: %s", len(differ), holder, here, listPaths(differ))
	}
	for _, run := range runs {
		if current.holds(ctx, here, run) {
			continue
		}
		if err := readAsNow(ctx, here, local, run); err != nil {
			return err
		}
	}
	return nil
}

// readAsNow refuses unless run read each of paths, local slash paths under here, as they
// read now; see [sameChangedPaths] for the witnesses.
func readAsNow(ctx context.Context, here string, paths []string, run types.JobAttempt) error {
	if why := unreadSince(ctx, here, paths, run); why != "" {
		return fmt.Errorf("job: a run here proves the holder's work only if %s", why)
	}
	return nil
}
