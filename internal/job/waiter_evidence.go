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

	"github.com/egladman/magus/libs/safetyscissors/job"
)

// graded is the evidence a verdict grades: the result, the run behind its check, and the
// runs behind its postconditions.
type graded struct {
	result                job.Result
	attempt               job.CapturedRun
	postconditionAttempts []job.PostconditionAttempt
}

// withWaiterEvidence is ev with the runs behind the postconditions in refs swapped for runs
// resolved in THIS checkout, and the record Wait stores beside them once they verify, its
// verdicts and time left for Wait (see [gradedTake]). The holder's own attempts on the row
// stay as filed: ev's slices are cloned before they are edited.
//
// Each ref is resolved by resolve and nowhere else, and resolve must read this checkout's
// store alone: a fallback to the holder's checkout would make this the holder's evidence
// under another name. Verification then runs as always, so a run of the wrong check, older
// than the row or failed is refused there.
//
// It refuses a postcondition the job does not hold as a check, and a postcondition that already passes on
// the holder's evidence, which another run would only hide. It also refuses unless the
// files the job changed read the same here as in the holder's checkout and each run taken
// read them as they read here now (see [sameChangedPaths]): only then is a run here a run
// of the holder's tree. A postcondition records its run's attempt id where the store gave one: the
// ref typed may name a step, which the step's next run would answer for instead.
func (ev graded) withWaiterEvidence(ctx context.Context, store *Store, row job.Job, refs map[string]string, resolve AttemptResolver, seen Observed) (graded, *job.Integration, error) {
	if resolve == nil {
		return graded{}, nil, errors.New("job: no output resolver reads the runs recorded in this checkout")
	}
	if store.root == "" {
		return graded{}, nil, errors.New("job: waiter evidence names runs recorded in a checkout, and this store has none")
	}
	postconditions := row.EffectivePostconditions()
	ids := slices.Sorted(maps.Keys(refs))
	for _, postconditionID := range ids {
		i := slices.IndexFunc(postconditions, func(g job.Postcondition) bool { return g.ID == postconditionID })
		if i < 0 {
			held := make([]string, len(postconditions))
			for j, g := range postconditions {
				held[j] = g.ID
			}
			return graded{}, nil, fmt.Errorf("job: %s holds no postcondition %q to take evidence for (it holds %s)", row.ID, postconditionID, strings.Join(held, ", "))
		}
		if postconditions[i].Kind != job.PostconditionKindCheck {
			return graded{}, nil, fmt.Errorf("job: postcondition %q is a %s postcondition, and only a check postcondition is graded from a run", postconditionID, postconditions[i].Kind)
		}
		ownRef, ownAttempt := ev.postconditionRun(postconditionID)
		if verifyGate(row, postconditions[i], ownRef, ownAttempt, seen).Verified {
			return graded{}, nil, fmt.Errorf("job: postcondition %q already passes on the holder's own evidence, so there is no verdict for another run to replace", postconditionID)
		}
	}

	here, err := filepath.Abs(store.root)
	if err != nil {
		return graded{}, nil, err
	}
	out := graded{result: ev.result, attempt: ev.attempt, postconditionAttempts: slices.Clone(ev.postconditionAttempts)}
	out.result.VerificationEvidence = slices.Clone(ev.result.VerificationEvidence)
	taken := &job.Integration{Checkout: here}
	runs := make([]job.CapturedRun, 0, len(ids))
	for _, postconditionID := range ids {
		ref := strings.TrimSpace(refs[postconditionID])
		found, err := resolve(ctx, ref)
		if err != nil {
			return graded{}, nil, err
		}
		if !found.Found {
			return graded{}, nil, fmt.Errorf("job: postcondition %q output ref %q names no run this checkout recorded", postconditionID, ref)
		}
		ref = cmp.Or(found.AttemptID, ref)
		out.setPostconditionRun(postconditionID, ref, found)
		taken.Gates = append(taken.Gates, job.VerificationStatus{ID: postconditionID, OutputRef: ref})
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

// runs is every run ev holds: the check's, then each postcondition's.
func (ev graded) runs() []job.CapturedRun {
	out := append(make([]job.CapturedRun, 0, 1+len(ev.postconditionAttempts)), ev.attempt)
	for _, g := range ev.postconditionAttempts {
		out = append(out, g.Attempt)
	}
	return out
}

// postconditionRun is the ref and the run ev holds for postconditionID, zero when it holds none.
func (ev graded) postconditionRun(postconditionID string) (string, job.CapturedRun) {
	if postconditionID == job.PrimaryPostconditionID {
		return ev.result.Validation.OutputRef, ev.attempt
	}
	var (
		ref     string
		attempt job.CapturedRun
	)
	if j := slices.IndexFunc(ev.result.VerificationEvidence, func(e job.VerificationEvidence) bool { return e.GateID == postconditionID }); j >= 0 {
		ref = ev.result.VerificationEvidence[j].OutputRef
	}
	if j := slices.IndexFunc(ev.postconditionAttempts, func(g job.PostconditionAttempt) bool { return g.PostconditionID == postconditionID }); j >= 0 {
		attempt = ev.postconditionAttempts[j].Attempt
	}
	return ref, attempt
}

// setPostconditionRun makes ref and run the evidence for postconditionID, in place. ev's slices must not be
// shared with a row.
func (ev *graded) setPostconditionRun(postconditionID, ref string, run job.CapturedRun) {
	if postconditionID == job.PrimaryPostconditionID {
		ev.result.Validation.OutputRef, ev.attempt = ref, run
		return
	}
	if j := slices.IndexFunc(ev.result.VerificationEvidence, func(e job.VerificationEvidence) bool { return e.GateID == postconditionID }); j >= 0 {
		ev.result.VerificationEvidence[j].OutputRef = ref
	} else {
		ev.result.VerificationEvidence = append(ev.result.VerificationEvidence, job.VerificationEvidence{GateID: postconditionID, OutputRef: ref})
	}
	if j := slices.IndexFunc(ev.postconditionAttempts, func(g job.PostconditionAttempt) bool { return g.PostconditionID == postconditionID }); j >= 0 {
		ev.postconditionAttempts[j].Attempt = run
	} else {
		ev.postconditionAttempts = append(ev.postconditionAttempts, job.PostconditionAttempt{PostconditionID: postconditionID, Attempt: run})
	}
}

// gradedTake is taken as the row records it: each postcondition's verdict copied from status, the
// verdict as a whole status's own, stamped at.
func gradedTake(taken job.Integration, status job.Status, at int64) *job.Integration {
	taken.At, taken.Verified, taken.ObservedGeneration = at, status.Verified, status.ObservedGeneration
	taken.Gates = slices.Clone(taken.Gates)
	for i, g := range taken.Gates {
		if j := slices.IndexFunc(status.Gates, func(s job.VerificationStatus) bool { return s.ID == g.ID }); j >= 0 {
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
func sameChangedPaths(ctx context.Context, holder, here string, changed []string, runs []job.CapturedRun, current CurrentRun) error {
	if holder == "" {
		return errors.New("job: the job records no checkout for its holder, so nothing shows this one holds the work the holder changed")
	}
	if info, err := os.Stat(holder); err != nil || !info.IsDir() {
		return fmt.Errorf("job: the holder's checkout %s is not there to compare against", holder)
	}
	var differ, local []string
	for _, c := range changed {
		p := job.CleanPath(c)
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
func readAsNow(ctx context.Context, here string, paths []string, run job.CapturedRun) error {
	if why := unreadSince(ctx, here, paths, run); why != "" {
		return fmt.Errorf("job: a run here proves the holder's work only if %s", why)
	}
	return nil
}
