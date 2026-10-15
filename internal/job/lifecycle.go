package job

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// AttemptResolver resolves the recorded run behind a result's output ref. A result is
// evidence only when that ref names a real run, and the resolver is deliberately a
// function rather than a Store method: output records live in a checkout cache while
// job rows live per repository.
type AttemptResolver func(context.Context, string) (types.JobAttempt, error)

// endedReason says which end a write recording no_return on the live row prev is, so a
// row its holder returned with nothing to grade never reads as one nobody returned. byHolder
// is a write under the row's lease or from the checkout its holder took it in.
func endedReason(prev types.Job, byHolder bool) string {
	ungradable := prev.Check == nil && len(prev.Goals) == 0
	switch {
	case prev.ReadOnly && ungradable && prev.Result != nil:
		return "returned ungraded: its holder filed a result, and a read-only row declaring no check or goal has nothing magus can grade"
	case prev.Result != nil:
		return "ended unverified: its holder filed a result and nobody ran `" + hint.JobWait.With(prev.ID) + "` on it"
	case prev.Registered == 0:
		return "ended with no result filed, before any holder ran `" + hint.JobExec.With(prev.ID) + "` on it"
	case prev.ReadOnly && ungradable:
		return "ended ungraded: a read-only row declaring no check or goal has nothing magus can grade, so what it found went only to its caller"
	case byHolder:
		return "abandoned by its holder, which filed no result"
	default:
		return "ended by someone other than its holder, which filed no result, so nothing was graded"
	}
}

// Exit records how a worker ended one job. A nil result is an abandonment, distinct from
// a failed verification: nobody returned evidence at all. A non-nil result is filed with
// the output attempt resolved in the worker's checkout, so a verifier in another
// worktree does not need that checkout's cache to reopen the evidence.
func Exit(ctx context.Context, store *Store, id string, result *types.JobResult, resolve AttemptResolver) (types.Job, error) {
	// Update declares a row it does not find, so an exit naming a mistyped or never-forked
	// id would mint a job only to end it. Nobody declared it, so there is nothing to end.
	rows, err := store.List()
	if err != nil {
		return types.Job{}, err
	}
	i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id })
	if i < 0 {
		return types.Job{}, fmt.Errorf("%w: %q, nothing declared it, so there is nothing to end", ErrUnknownJob, id)
	}
	// An ended row is not ended again: a second end would rewrite how the first one ended.
	if ended := rows[i]; ended.State.Terminal() {
		reason := ""
		if ended.EndReason != "" {
			reason = " (" + ended.EndReason + ")"
		}
		return types.Job{}, fmt.Errorf("job: %s already ended %s %s ago%s, so there is nothing to end, `%s` lists it",
			id, ended.State, updatedAgo(ended), reason, hint.LsJobs.With("--all"))
	}
	if result == nil {
		return store.Update(ctx, id, func(row *types.Job) { row.State = types.StateNoReturn })
	}
	if resolve == nil {
		return types.Job{}, errors.New("job: no output resolver files a result's validation evidence")
	}
	resolve, where := store.acrossCheckouts(rows, id, resolve)
	attempt, gateAttempts, err := resolveResultAttempts(ctx, *result, resolve, where)
	if err != nil {
		return types.Job{}, err
	}
	return store.Update(ctx, id, func(row *types.Job) {
		// Stamped with the generation the write keeps: an exit declares nothing new.
		generation := max(row.Generation, 1)
		filed := *result
		filed.ObservedGeneration = generation
		row.Result = &filed
		row.GateAttempts = gateAttempts
		for i := range row.GateAttempts {
			row.GateAttempts[i].Attempt.ObservedGeneration = generation
		}
		if attempt.Found {
			attempt.ObservedGeneration = generation
			row.Attempt = &attempt
		} else {
			row.Attempt = nil
		}
		row.State = types.StateExited
	})
}

// Wait verifies a returned result against the current job terms and records pass under
// the same Store update that read those terms. A rejected result is an ordinary Status,
// not an error: callers need its violations to decide what to repair, and the row records
// them as its Rejection in the same write, its state left where it was. result=nil uses the
// result and attempts filed by Exit at the row's generation (see [evidenceAt]), and evidence
// filed against an earlier spec never passes; a supplied result is resolved in this checkout.
// A bound store verifies only a row below its own lease (see [Actor.Verifies]).
//
// evidence names, per check goal, a run recorded in the waiter's checkout to grade in place
// of the holder's: the key is the goal id, or [types.PrimaryGoalID] for the job's own
// check, and the value the run's output ref, which resolve must look up in this checkout
// alone. Nil or empty grades the filed evidence. See [graded.withWaiterEvidence] for what
// must hold first.
//
// observe reads store's footprint cache and fills it, unless store is a dry run's.
func Wait(ctx context.Context, store *Store, id string, result *types.JobResult, resolve AttemptResolver, observe Observer, evidence map[string]string) (types.JobStatus, error) {
	if len(evidence) > 0 && result != nil {
		return types.JobStatus{}, errors.New("job: waiter evidence starts from the result filed with the job, and a supplied result is not that")
	}
	ctx = withFootprintCache(ctx, store.footprintCache())
	jobs, err := store.List()
	if err != nil {
		return types.JobStatus{}, err
	}
	actor := store.Actor()
	if !actor.Verifies(jobs, id) {
		return types.JobStatus{}, fmt.Errorf("job: this checkout holds the lease on %s and a holder does not verify its own work", actor.Lease)
	}
	i := slices.IndexFunc(jobs, func(row types.Job) bool { return row.ID == id })
	if i < 0 {
		return types.JobStatus{}, fmt.Errorf("job: there is no job %q", id)
	}

	var ev graded
	var prior int
	if result == nil {
		if jobs[i].Result == nil {
			return types.JobStatus{}, fmt.Errorf("job: %s has filed no result", id)
		}
		ev, prior = evidenceAt(jobs[i])
	} else {
		resolve, where := store.acrossCheckouts(jobs, id, resolve)
		ev.result = *result
		ev.attempt, ev.gateAttempts, err = resolveResultAttempts(ctx, *result, resolve, where)
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
		if seen, err = observe(ctx, inheritGoals(jobs[i], jobs)); err != nil {
			return types.JobStatus{}, err
		}
	}
	seen.Stale = staleRuns(ctx, workTree(store.root, jobs[i]), seen, ev.result, ev.runs()...)

	var taken *types.JobIntegration
	if len(evidence) > 0 {
		if ev, taken, err = ev.withWaiterEvidence(ctx, store, inheritGoals(jobs[i], jobs), evidence, resolve, seen); err != nil {
			return types.JobStatus{}, err
		}
	}

	var status types.JobStatus
	_, err = store.mutate(ctx, id, asVerdict, func(row *types.Job, exists bool, now int64) error {
		if !exists {
			return fmt.Errorf("job: there is no job %q", id)
		}
		held := inheritGoals(*row, jobs)
		status = VerifyGates(held, ev.result, ev.attempt, ev.gateAttempts, jobs, seen)
		// A verdict declares nothing, so the row keeps this generation through the write.
		generation := max(row.Generation, 1)
		status.ObservedGeneration, status.PriorGeneration = generation, prior
		// Evidence taken against an earlier spec never passes: what the spec now asks was
		// never run.
		if prior != 0 {
			status.Verified = false
			status.Violations = append(status.Violations, fmt.Sprintf("the evidence is from generation %d and the spec is now generation %d:"+
				" run %s again and file its result with `%s`", prior, generation, checkLine(held), hint.JobExit.With(id, "--stdin")))
		}
		if !status.Verified {
			row.Rejection = &types.JobRejection{
				Unmet:              unmetGates(status.Gates),
				Violations:         slices.Clone(status.Violations[:min(len(status.Violations), maxFolded)]),
				At:                 now,
				ObservedGeneration: generation,
			}
			return nil
		}
		row.State = types.StatePass
		row.Rejection = nil
		if taken != nil {
			row.WaiterEvidence = gradedTake(*taken, status, now)
		}
		return nil
	})
	if err != nil {
		return types.JobStatus{}, err
	}
	return status, nil
}

// Verifies reports whether this actor may verify row id: an unbound actor verifies any
// row, a bound one only a row below its own lease in rows' parent chain. Its own row, a
// sibling's and an ancestor's stay refused, since each would be work grading itself or
// its peers.
func (a Actor) Verifies(rows []types.Job, id string) bool {
	if !a.Bound() {
		return true
	}
	return slices.ContainsFunc(types.JobAncestors(rows, id), func(r types.Job) bool { return r.ID == a.Lease })
}

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
	out.result.GateEvidence = slices.Clone(ev.result.GateEvidence)
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
		taken.Gates = append(taken.Gates, types.GateStatus{ID: goalID, OutputRef: ref})
		runs = append(runs, found)
	}

	if !seen.ChangedKnown {
		return graded{}, nil, errors.New("job: the job's diff could not be read, so nothing names the files a run here has to share with the holder's checkout")
	}
	changed := slices.Compact(slices.Sorted(slices.Values(append(slices.Clone(seen.Changed), ev.result.ChangedPaths...))))
	if err := sameChangedPaths(ctx, row.CheckoutRoot, here, changed, runs); err != nil {
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
	if j := slices.IndexFunc(ev.result.GateEvidence, func(e types.GateEvidence) bool { return e.GateID == goalID }); j >= 0 {
		ref = ev.result.GateEvidence[j].OutputRef
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
	if j := slices.IndexFunc(ev.result.GateEvidence, func(e types.GateEvidence) bool { return e.GateID == goalID }); j >= 0 {
		ev.result.GateEvidence[j].OutputRef = ref
	} else {
		ev.result.GateEvidence = append(ev.result.GateEvidence, types.GateEvidence{GateID: goalID, OutputRef: ref})
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
		if j := slices.IndexFunc(status.Gates, func(s types.GateStatus) bool { return s.ID == g.ID }); j >= 0 {
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
func sameChangedPaths(ctx context.Context, holder, here string, changed []string, runs []types.JobAttempt) error {
	if holder == "" {
		return errors.New("job: the job records no checkout for its holder, so nothing shows this one holds the work the holder changed")
	}
	if info, err := os.Stat(holder); err != nil || !info.IsDir() {
		return fmt.Errorf("job: the holder's checkout %s is not there to compare against", holder)
	}
	var differ, local []string
	for _, c := range changed {
		p := path.Clean(filepath.ToSlash(strings.TrimSpace(c)))
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

// staleRuns names, by [attemptKey], each of runs that read one of the job's changed paths other
// than it reads now in tree, with why: the job wrote after the run, so the run graded
// other files. The changed paths are those observed since the checkpoint and those the
// result names.
func staleRuns(ctx context.Context, tree string, seen Observed, result types.JobResult, runs ...types.JobAttempt) map[string]string {
	if tree == "" {
		return nil
	}
	var changed []string
	for _, c := range slices.Concat(seen.Changed, result.ChangedPaths) {
		if p := path.Clean(filepath.ToSlash(strings.TrimSpace(c))); filepath.IsLocal(filepath.FromSlash(p)) {
			changed = append(changed, p)
		}
	}
	changed = slices.Compact(slices.Sorted(slices.Values(changed)))
	stale := map[string]string{}
	for _, run := range runs {
		if !run.Found {
			continue
		}
		if why := unreadSince(ctx, tree, changed, run); why != "" {
			stale[attemptKey(run)] = why
		}
	}
	return stale
}

// attemptKey names one recorded run: its attempt id, which no later run takes over, else its ref.
func attemptKey(run types.JobAttempt) string { return cmp.Or(run.AttemptID, run.Ref) }

// unreadSince says why run did not read each of paths, local slash paths under here, as
// they read now, or "" when it did.
func unreadSince(ctx context.Context, here string, paths []string, run types.JobAttempt) string {
	name := attemptKey(run)
	var driver types.VCSDriver
	if run.Revision != "" && !run.Dirty {
		driver = revisionReader(ctx, here, run.Revision)
	}
	started := time.UnixMilli(run.TimestampMs - run.DurationMs)
	var moved, later []string
	for _, p := range paths {
		if driver != nil {
			if same, decided := readsAsRevision(ctx, driver, here, run.Revision, p); decided {
				if !same {
					moved = append(moved, p)
				}
				continue
			}
		}
		if modifiedSince(here, filepath.FromSlash(p), started) {
			later = append(later, p)
		}
	}
	switch {
	case len(moved) > 0:
		return fmt.Sprintf("the changed paths read now as the run read them,"+
			" and %d in %s differ from %s, the clean revision run %s read: %s", len(moved), here, run.Revision, name, listPaths(moved))
	case len(later) > 0:
		return fmt.Sprintf("the changed paths did not change after it started,"+
			" and %d changed in %s after %s, when run %s started: %s", len(later), here, started.UTC().Format(time.RFC3339), name, listPaths(later))
	}
	return ""
}

// revisionReader is the VCS that reads revision in root, or nil when none can: without one
// a path missing at the revision and a revision missing from the store read alike.
func revisionReader(ctx context.Context, root, revision string) types.VCSDriver {
	driver, _ := resolveDriver(ctx, root)
	if driver == nil {
		return nil
	}
	if _, err := driver.FindCommit(ctx, root, revision); err != nil {
		return nil
	}
	return driver
}

// readsAsRevision reports whether p, a local slash path under root, reads now as it does at
// revision, a path absent from both reading the same. decided is false for a directory
// here: a revision holds one as a tree, or a submodule as another repository's commit,
// and neither compares with a file.
func readsAsRevision(ctx context.Context, driver types.VCSDriver, root, revision, p string) (same, decided bool) {
	full := filepath.Join(root, filepath.FromSlash(p))
	info, err := os.Lstat(full)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		_, err := driver.ReadFileAt(ctx, root, revision, p)
		return err != nil, true
	case err != nil:
		return false, true
	case info.IsDir():
		return false, false
	}
	then, err := driver.ReadFileAt(ctx, root, revision, p)
	if err != nil {
		return false, true
	}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(full)
		return err == nil && target == then, true
	case info.Mode().IsRegular():
		now, err := os.ReadFile(full)
		return err == nil && string(now) == then, true
	}
	return false, true
}

// listPaths names the first five of paths and counts the rest.
func listPaths(paths []string) string {
	shown := paths[:min(len(paths), 5)]
	if rest := len(paths) - len(shown); rest > 0 {
		return fmt.Sprintf("%s and %d more", strings.Join(shown, ", "), rest)
	}
	return strings.Join(shown, ", ")
}

// modifiedSince reports whether rel, a local path under root, anything beneath it, or any
// directory above it up to and including root was modified after t. The directories count
// because removing or renaming an entry stamps its directory alone: a deletion leaves no
// file to date, and a file renamed into place keeps its old time. A read failure other
// than absence reports true: an unread file proves nothing unchanged.
func modifiedSince(root, rel string, t time.Time) bool {
	for dir := filepath.Dir(rel); ; dir = filepath.Dir(dir) {
		info, err := os.Lstat(filepath.Join(root, dir))
		if err != nil && !errors.Is(err, fs.ErrNotExist) || err == nil && info.ModTime().After(t) {
			return true
		}
		if dir == "." {
			break
		}
	}
	full := filepath.Join(root, rel)
	if _, err := os.Lstat(full); errors.Is(err, fs.ErrNotExist) {
		return false
	}
	later := false
	err := filepath.WalkDir(full, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(t) {
			later = true
			return filepath.SkipAll
		}
		return nil
	})
	return later || err != nil
}

// sameContent reports whether left and right hold the same thing: the same bytes for a
// file, the same target for a symbolic link, which is never followed, and for a directory
// the same entries, each the same. A directory holding .git is a nested repository, such
// as a submodule, and is the same at the same HEAD with the same other entries: its .git
// points at metadata of its own checkout, which never matches another's. Both absent is
// the same; any other read failure is not.
func sameContent(ctx context.Context, left, right string) bool {
	li, lerr := os.Lstat(left)
	ri, rerr := os.Lstat(right)
	if lerr != nil || rerr != nil {
		return errors.Is(lerr, fs.ErrNotExist) && errors.Is(rerr, fs.ErrNotExist)
	}
	if li.Mode().Type() != ri.Mode().Type() {
		return false
	}
	switch {
	case li.Mode()&fs.ModeSymlink != 0:
		l, lerr := os.Readlink(left)
		r, rerr := os.Readlink(right)
		return lerr == nil && rerr == nil && l == r
	case li.IsDir():
		l, lerr := os.ReadDir(left)
		r, rerr := os.ReadDir(right)
		if lerr != nil || rerr != nil {
			return false
		}
		isGit := func(e fs.DirEntry) bool { return e.Name() == ".git" }
		if slices.ContainsFunc(l, isGit) || slices.ContainsFunc(r, isGit) {
			if !sameHead(ctx, left, right) {
				return false
			}
			l, r = slices.DeleteFunc(l, isGit), slices.DeleteFunc(r, isGit)
		}
		if len(l) != len(r) {
			return false
		}
		for i := range l {
			if l[i].Name() != r[i].Name() || !sameContent(ctx, filepath.Join(left, l[i].Name()), filepath.Join(right, r[i].Name())) {
				return false
			}
		}
		return true
	case li.Mode().IsRegular():
		l, lerr := os.ReadFile(left)
		r, rerr := os.ReadFile(right)
		return lerr == nil && rerr == nil && bytes.Equal(l, r)
	}
	return false
}

// sameHead reports whether the repositories checked out at left and right both read a
// HEAD, and the same one.
func sameHead(ctx context.Context, left, right string) bool {
	head := func(dir string) string {
		driver, _ := resolveDriver(ctx, dir)
		if driver == nil {
			return ""
		}
		c, err := driver.FindCommit(ctx, dir, "")
		if err != nil {
			return ""
		}
		return c.ID
	}
	l := head(left)
	return l != "" && l == head(right)
}

// resolveResultAttempts takes a portable snapshot for every evidence reference the
// holder supplied. It deliberately does not infer which gates exist from the result:
// verification compares these snapshots to the row's declared gates under the store
// lock, so an extra or missing reference never becomes authority. where names the
// checkouts resolve searched, for the refusal.
func resolveResultAttempts(ctx context.Context, result types.JobResult, resolve AttemptResolver, where string) (types.JobAttempt, []types.JobGateAttempt, error) {
	if resolve == nil {
		return types.JobAttempt{}, nil, errors.New("job: no output resolver files a result's evidence")
	}
	var primary types.JobAttempt
	if ref := result.Validation.OutputRef; ref != "" {
		attempt, err := resolve(ctx, ref)
		if err != nil {
			return types.JobAttempt{}, nil, err
		}
		if !attempt.Found {
			return types.JobAttempt{}, nil, fmt.Errorf("job: the result's output ref %q names no run %s recorded", ref, where)
		}
		primary = attempt
	}
	seen := map[string]bool{}
	gates := make([]types.JobGateAttempt, 0, len(result.GateEvidence))
	for _, evidence := range result.GateEvidence {
		if evidence.GateID == "" || evidence.OutputRef == "" {
			return types.JobAttempt{}, nil, errors.New("job: each gate_evidence entry requires gate_id and output_ref")
		}
		if seen[evidence.GateID] {
			return types.JobAttempt{}, nil, fmt.Errorf("job: the result carries duplicate evidence for goal %q", evidence.GateID)
		}
		seen[evidence.GateID] = true
		attempt, err := resolve(ctx, evidence.OutputRef)
		if err != nil {
			return types.JobAttempt{}, nil, err
		}
		if !attempt.Found {
			return types.JobAttempt{}, nil, fmt.Errorf("job: goal %q output ref %q names no run %s recorded", evidence.GateID, evidence.OutputRef, where)
		}
		gates = append(gates, types.JobGateAttempt{GateID: evidence.GateID, Attempt: attempt})
	}
	return primary, gates, nil
}

// acrossCheckouts is resolve, falling back to the output stores of the checkouts job id
// and its descendants were taken in (see [Location.Outputs]) when resolve finds no run,
// and the phrase naming where it looked. A fallback that fails is skipped: a removed
// worktree has no store left to ask.
func (s *Store) acrossCheckouts(rows []types.Job, id string, resolve AttemptResolver) (AttemptResolver, string) {
	const here = "this checkout"
	if resolve == nil || s.outputs == nil {
		return resolve, here
	}
	self := ""
	if s.root != "" {
		self, _ = filepath.Abs(s.root)
	}
	var roots []string
	for _, row := range rows {
		if row.ID != id && !slices.ContainsFunc(types.JobAncestors(rows, row.ID), func(a types.Job) bool { return a.ID == id }) {
			continue
		}
		if r := row.CheckoutRoot; r != "" && r != self && !slices.Contains(roots, r) {
			roots = append(roots, r)
		}
	}
	if len(roots) == 0 {
		return resolve, here
	}
	return func(ctx context.Context, ref string) (types.JobAttempt, error) {
		attempt, err := resolve(ctx, ref)
		if err != nil || attempt.Found {
			return attempt, err
		}
		for _, root := range roots {
			if found, ferr := s.outputs(root)(ctx, ref); ferr == nil && found.Found {
				return found, nil
			}
		}
		return attempt, nil
	}, here + " or " + strings.Join(roots, ", ")
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
//
// observe reads store's footprint cache and fills it, unless store is a dry run's.
func GradeGates(ctx context.Context, store *Store, id string, observe Observer) (types.JobStatus, error) {
	ctx = withFootprintCache(ctx, store.footprintCache())
	jobs, err := store.List()
	if err != nil {
		return types.JobStatus{}, err
	}
	i := slices.IndexFunc(jobs, func(row types.Job) bool { return row.ID == id })
	if i < 0 {
		return types.JobStatus{}, fmt.Errorf("job: there is no job %q", id)
	}
	row := inheritGoals(jobs[i], jobs)

	ev, prior := evidenceAt(jobs[i])
	var seen Observed
	if observe != nil {
		if seen, err = observe(ctx, row); err != nil {
			return types.JobStatus{}, err
		}
	}
	seen.Stale = staleRuns(ctx, workTree(store.root, row), seen, ev.result, ev.runs()...)
	// Graded against a row whose state is cleared, so the "already verified" rule does not
	// fire on a job that legitimately passed: this asks where the gates stand, and a job
	// that finished stands with all of them met.
	row.State = ""
	status := VerifyGates(row, ev.result, ev.attempt, ev.gateAttempts, jobs, seen)
	status.ObservedGeneration, status.PriorGeneration = max(row.Generation, 1), prior

	// The GATES decide this verdict, not the whole result. VerifyGates also applies the
	// rules a filed result must satisfy (a change set that is not empty, paths inside the
	// declared boundary, descendants the store carries), and a job still in flight has filed no result
	// to hold to them: inherited, they report every unfinished job as failing for reasons
	// that have nothing to do with what was asked. The violations stay in the status, so a
	// reader still sees them; only the verdict is narrowed to the question.
	status.Verified = !slices.ContainsFunc(status.Gates, func(g types.GateStatus) bool { return !g.Verified })
	return status, nil
}

// ending is one live row the sweep ends, and the reason it records.
type ending struct {
	id, reason string
}

// sweeper decides which live rows are provably dead as of one instant. It carries what
// one sweep should compute once however many passes it makes: the stat of each bound
// checkout, and jobs.stale_after, which is read only if some row needs it.
type sweeper struct {
	now    int64
	window func() (time.Duration, error)
	gone   map[string]bool
	// landed is the exited rows whose work is on the base, probed once before the lock
	// because the probe reads the VCS; see [Store.landings].
	landed map[string]landing
}

// landing is a probe's verdict on one exited row, valid while the row's Updated is the
// one it was probed at.
type landing struct {
	updated int64
	reason  string
}

// dead returns the live rows nobody will finish, in store order, with why. Ending one row
// can orphan another, so it repeats until a pass ends nothing. A row is dead when:
//
//  1. an ancestor ended (pass, fail or no_return): the tree dies with its root;
//  2. a holder took it with `magus job exec` and that checkout's directory is gone;
//  3. it is declared, nobody ever took it, it was not updated within jobs.stale_after,
//     and no live child hangs under it, so a root outlives the children still working;
//     a row queued on a live dependency is waiting, and its clock starts when the last
//     one ends (see [types.JobIdleSince]);
//  4. its holder exited, its work landed on the base branch (see [Store.landedOnBase]),
//     and no live child hangs under it;
//  5. a holder took it and its deadline passed (see [types.Job.Overdue]): the declared
//     bound is obeyed, not only reported.
//
// A landed row ends as no_return rather than pass: landing is not grading, and pass
// stays the verdict only `magus job wait` records from the job's own evidence. Its
// end_reason names the base commit.
//
// Server rows are the server's own maintenance and never swept. An exited row skips rule
// 2: its holder already returned, and removing the worktree is the normal end of that. A
// checkout whose stat fails for any reason but absence stays live, since magus cannot
// tell a gone directory from an unreadable mount.
func (w *sweeper) dead(rows []types.Job) ([]ending, error) {
	rows = slices.Clone(rows)
	var out []ending
	for {
		x := indexRows(rows)
		var round []ending
		for _, row := range rows {
			if !row.State.Live() || row.Holder.OrSession() == types.HolderServer || readOnly(row) != nil {
				continue
			}
			reason, err := w.reason(x, rows, row)
			if err != nil {
				return nil, err
			}
			if reason != "" {
				round = append(round, ending{id: row.ID, reason: reason})
			}
		}
		if len(round) == 0 {
			return out, nil
		}
		for _, e := range round {
			i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == e.id })
			rows[i].State = types.StateNoReturn
		}
		out = append(out, round...)
	}
}

// rowIndex is one sweep round's rows by id, and the ids with a live child. Built once per
// round: asked of every live row, a scan of the plan per question made the sweep
// quadratic in the plan's size.
type rowIndex struct {
	byID      map[string]types.Job
	liveChild map[string]bool
}

func indexRows(rows []types.Job) rowIndex {
	x := rowIndex{byID: make(map[string]types.Job, len(rows)), liveChild: map[string]bool{}}
	for _, r := range rows {
		x.byID[r.ID] = r
		if r.Parent != "" && r.State.Live() {
			x.liveChild[r.Parent] = true
		}
	}
	return x
}

// endedAncestor is [types.EndedAncestor] over the index.
func (x rowIndex) endedAncestor(id string) (types.Job, bool) {
	seen := map[string]bool{id: true}
	cur, ok := x.byID[id]
	for ok && cur.Parent != "" && !seen[cur.Parent] {
		seen[cur.Parent] = true
		if cur, ok = x.byID[cur.Parent]; ok && cur.State.Terminal() {
			return cur, true
		}
	}
	return types.Job{}, false
}

// reason is why row is dead, or "" when it is not. See [sweeper.dead].
func (w *sweeper) reason(x rowIndex, rows []types.Job, row types.Job) (string, error) {
	if a, ok := x.endedAncestor(row.ID); ok {
		kin := "ancestor"
		if a.ID == row.Parent {
			kin = "parent"
		}
		return fmt.Sprintf("%s %s ended as %s, and its tree ends with it", kin, a.ID, a.State), nil
	}
	liveChild := x.liveChild[row.ID]
	if l, ok := w.landed[row.ID]; ok && row.State == types.StateExited && l.updated == row.Updated && !liveChild {
		return l.reason, nil
	}
	if row.State != types.StateExited && w.checkoutGone(row.CheckoutRoot) {
		return fmt.Sprintf("taken in %s, which no longer exists", row.CheckoutRoot), nil
	}
	if Editing(row) && row.Registered != 0 && row.Overdue(w.now) {
		return overdueReason(row, w.now), nil
	}
	// Only a registration says a holder took it; a checkout_root without one names no holder.
	if row.Registered != 0 || row.State != types.StateDeclared {
		return "", nil
	}
	if liveChild {
		return "", nil
	}
	window, err := w.window()
	if err != nil {
		return "", err
	}
	if !types.JobStaleAt(rows, row, w.now, window) {
		return "", nil
	}
	since, _ := types.JobIdleSince(rows, row)
	return fmt.Sprintf("declared and never taken, untouched for %s (jobs.stale_after is %s)",
		time.Duration(w.now-since)*time.Second, window), nil
}

// checkoutGone reports whether root names a directory that provably does not exist.
func (w *sweeper) checkoutGone(root string) bool {
	if root == "" {
		return false
	}
	gone, ok := w.gone[root]
	if !ok {
		_, err := os.Stat(root)
		gone = errors.Is(err, fs.ErrNotExist)
		w.gone[root] = gone
	}
	return gone
}

// sweep ends every row in f the [sweeper] proves dead, tombstones the binding records
// whose job's checkout is gone (see [Store.sweepRecords]), and returns the plan as written.
// A dry-run store ends the rows in the returned plan only.
func (s *Store) sweep(f jobsFile) (jobsFile, error) {
	clock := s.clock
	if clock == nil {
		clock = time.Now
	}
	w := &sweeper{now: clock().Unix(), window: sync.OnceValues(s.resolveStaleAfter), gone: map[string]bool{}}
	w.landed = s.landings(f.Jobs, w.now)
	if s.dryRun {
		dead, err := w.dead(f.Jobs)
		if err != nil {
			return jobsFile{}, err
		}
		endRows(f.Jobs, dead, w.now)
		return f, nil
	}
	f, ended, err := s.endDead(w, f)
	if err != nil {
		return jobsFile{}, err
	}
	s.sweepRecords(w, f.Jobs, ended)
	return f, nil
}

// endDead ends every row in f that w proves dead, and reports whether it ended any. The
// verdict is reached twice: once lock-free, so a plan with nothing to end costs no lock,
// and again under the file lock against a fresh read, so a row another process revived in
// between is not ended on a stale view.
func (s *Store) endDead(w *sweeper, f jobsFile) (jobsFile, bool, error) {
	dead, err := w.dead(f.Jobs)
	if err != nil || len(dead) == 0 {
		return f, false, err
	}
	var ended endings
	err = s.update(context.Background(), func(cur *jobsFile) error {
		ended = endings{}
		var err error
		if dead, err = w.dead(cur.Jobs); err != nil {
			return err
		}
		endRows(cur.Jobs, dead, w.now)
		f = *cur
		if len(dead) == 0 {
			return file.ErrSkipWrite
		}
		for _, e := range dead {
			i := slices.IndexFunc(cur.Jobs, func(r types.Job) bool { return r.ID == e.id })
			if err := ended.hold(s, cur.Jobs, cur.Jobs[i]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return jobsFile{}, false, err
	}
	out := s.notices
	if out == nil {
		out = os.Stderr
	}
	for _, e := range dead {
		fmt.Fprintf(out, "ended %s: %s\n", e.id, e.reason)
		if strings.HasPrefix(e.reason, overdueReasonPrefix) {
			s.noteEnded(e.id, e.reason)
		}
	}
	if err := ended.record(context.Background(), w.now*1000); err != nil {
		return jobsFile{}, false, err
	}
	return f, len(dead) > 0, nil
}

// endRows ends each of dead in rows as no_return at now.
func endRows(rows []types.Job, dead []ending, now int64) {
	for _, e := range dead {
		i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == e.id })
		rows[i].State = types.StateNoReturn
		rows[i].EndReason = e.reason
		rows[i].Updated = now
	}
}

// Ending is one live row [Store.Prune] ends, and the end_reason it records.
type Ending struct {
	ID     string `json:"id"     yaml:"id"`
	Reason string `json:"reason" yaml:"reason"`
}

// PruneOptions widens or previews [Store.Prune].
type PruneOptions struct {
	// All also ends a row a holder took and nobody touched within jobs.stale_after.
	All bool
	// DryRun decides what would end and writes nothing.
	DryRun bool
}

// Prune ends, as no_return, every live row the store can show nobody is working, and
// returns them in the order they ended. A row ends for the first of:
//
//   - a reason the read sweep ends rows for (see [sweeper.dead]);
//   - its holder exited and nobody collected the result with `magus job wait`;
//   - it is overdue ([types.Job.Overdue]);
//   - it is stale ([types.JobStaleAt] against jobs.stale_after) and nobody took it, or
//     opts.All is set.
//
// A declared or running row a holder took and touched within jobs.stale_after is never
// ended, and neither is one it took that is not overdue unless opts.All is set. Nor is
// any ancestor of such a row, since its tree would end with it. Server rows and rows this
// magus cannot write are left alone, as the sweep leaves them.
//
// A bound actor is refused: pruning ends other leases' rows.
func (s *Store) Prune(ctx context.Context, opts PruneOptions) ([]Ending, error) {
	if actor := s.Actor(); actor.Bound() {
		return nil, refuse(actor, actor.Lease, "pruning ends other leases' rows, so it belongs to whoever declared the plan")
	}
	window, err := s.resolveStaleAfter()
	if err != nil {
		return nil, err
	}
	clock := s.clock
	if clock == nil {
		clock = time.Now
	}
	w := &sweeper{now: clock().Unix(), window: func() (time.Duration, error) { return window, nil }, gone: map[string]bool{}}

	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil {
		return nil, err
	}
	// Probed outside the file lock, as the sweep does: the probe runs the VCS.
	w.landed = s.landings(f.Jobs, w.now)

	var out []Ending
	var ended endings
	err = s.update(ctx, func(cur *jobsFile) error {
		ended = endings{}
		var err error
		if out, err = w.prunable(cur.Jobs, window, opts.All); err != nil {
			return err
		}
		if opts.DryRun || len(out) == 0 {
			return file.ErrSkipWrite
		}
		for _, e := range out {
			i := slices.IndexFunc(cur.Jobs, func(r types.Job) bool { return r.ID == e.ID })
			cur.Jobs[i].State = types.StateNoReturn
			cur.Jobs[i].EndReason = e.Reason
			cur.Jobs[i].Updated = w.now
			if err := ended.hold(s, cur.Jobs, cur.Jobs[i]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := ended.record(ctx, w.now*1000); err != nil {
		return out, err
	}
	return out, nil
}

// prunable is what [Store.Prune] ends, repeated to a fixed point like [sweeper.dead]
// because ending a row orphans its children.
func (w *sweeper) prunable(rows []types.Job, window time.Duration, all bool) ([]Ending, error) {
	rows = slices.Clone(rows)
	var out []Ending
	for {
		flagged := types.JobList{Jobs: rows}.Flag(w.now, window)
		kept := w.worked(rows, window, all)
		x := indexRows(rows)
		var round []Ending
		for _, row := range rows {
			if !row.State.Live() || row.Holder.OrSession() == types.HolderServer || readOnly(row) != nil || kept[row.ID] {
				continue
			}
			reason, err := w.reason(x, rows, row)
			if err != nil {
				return nil, err
			}
			if reason == "" {
				reason = w.pruneReason(row, flagged, window)
			}
			if reason != "" {
				round = append(round, Ending{ID: row.ID, Reason: reason})
			}
		}
		if len(round) == 0 {
			return out, nil
		}
		for _, e := range round {
			i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == e.ID })
			rows[i].State = types.StateNoReturn
		}
		out = append(out, round...)
	}
}

// worked is the ids Prune keeps: every row a holder took and may still be working, and
// every ancestor of one.
func (w *sweeper) worked(rows []types.Job, window time.Duration, all bool) map[string]bool {
	kept := map[string]bool{}
	for _, row := range rows {
		if !Editing(row) || row.Registered == 0 {
			continue
		}
		if row.Overdue(w.now) || (all && window > 0 && w.now-row.Updated >= int64(window/time.Second)) {
			continue
		}
		kept[row.ID] = true
		for _, a := range types.JobAncestors(rows, row.ID) {
			kept[a.ID] = true
		}
	}
	return kept
}

// pruneReason is why Prune ends a row the sweep would keep, or "" when it keeps it too.
func (w *sweeper) pruneReason(row types.Job, flagged types.JobList, window time.Duration) string {
	idle := time.Duration(w.now-row.Updated) * time.Second
	switch {
	case row.State == types.StateExited:
		return fmt.Sprintf("exited %s ago and nobody collected its result with `magus job wait`", idle)
	case slices.Contains(flagged.Overdue, row.ID):
		return overdueReason(row, w.now)
	case !slices.Contains(flagged.Stale, row.ID):
		return ""
	case row.Registered == 0:
		since, _ := types.JobIdleSince(flagged.Jobs, row)
		return fmt.Sprintf("declared and never taken, untouched for %s (jobs.stale_after is %s)", time.Duration(w.now-since)*time.Second, window)
	default:
		return fmt.Sprintf("taken, then untouched for %s (jobs.stale_after is %s)", idle, window)
	}
}

// overdueReasonPrefix opens the end_reason of a row ended for passing its deadline.
const overdueReasonPrefix = "overdue: its deadline passed "

func overdueReason(row types.Job, now int64) string {
	return fmt.Sprintf("%s%s ago", overdueReasonPrefix, time.Duration(now-row.Deadline)*time.Second)
}

// resolveStaleAfter is jobs.stale_after from the workspace's own magus.yaml. Workspace
// only, like every rule that acts rather than reports: a value in one person's global
// config must not end rows in a repository that never chose it.
func (s *Store) resolveStaleAfter() (time.Duration, error) {
	if s.staleAfter != nil {
		return *s.staleAfter, nil
	}
	cfg, err := config.LoadWorkspaceOnly(s.root)
	if err != nil {
		return 0, fmt.Errorf("job: read jobs.stale_after to end untaken jobs: %w", err)
	}
	return cfg.Jobs.StaleAfter, nil
}

// landingInterval is how long one exited row goes unprobed after a probe. Every List
// sweeps, the guard lists on every hook call, and a probe runs the VCS, so a probe per
// read would put subprocesses on every tool call.
const landingInterval = 5 * time.Minute

// landingProbesFile logs when each exited row was last probed, one line per probe beside
// jobs.jsonl, so the interval holds across processes. A row's last line is its entry.
const landingProbesFile = "landing-probes.jsonl"

// landingProbe is one row's entry in landingProbesFile: when it was probed, and the row's
// Updated then, so a row that exits again is probed at once.
type landingProbe struct {
	At      int64 `json:"at"`
	Updated int64 `json:"updated"`
}

// landingProbeLine is one line of landingProbesFile.
type landingProbeLine struct {
	ID      string `json:"id"`
	At      int64  `json:"at"`
	Updated int64  `json:"updated"`
}

// probeLog is the probe history as read, and what a write needs to append to it.
type probeLog struct {
	probes  map[string]landingProbe
	lines   int
	partial bool
}

// landingCandidate reports an exited row whose result names changed paths in a checkout,
// the only rows a landing can be judged for.
func landingCandidate(row types.Job) bool {
	return row.State == types.StateExited && row.Holder.OrSession() != types.HolderServer && !row.ReadOnly &&
		row.Result != nil && len(row.Result.ChangedPaths) > 0 && row.CheckoutRoot != "" &&
		checkpointRevision(row.Checkpoint) != ""
}

// landings probes the exited rows not probed within landingInterval, at most
// landingBudget of them, least recently probed first, in at most one round per
// landingRound, and returns the ones whose work landed. The probes are recorded BEFORE
// they run: a process killed mid-probe (a guard hook past its deadline) has still taken
// its turn, so the next reader moves on to the rows after it rather than starting the
// same probes again, and two readers at once split the rows rather than both probing
// every one. A probe history that cannot be read or written only costs the interval,
// never a verdict.
func (s *Store) landings(rows []types.Job, now int64) map[string]landing {
	probe := s.landed
	if probe == nil {
		probe = s.landedOnBase
	}
	var log probeLog
	var history map[string]landingProbe
	read := false
	next := map[string]landingProbe{}
	var due []types.Job
	for _, row := range rows {
		if !landingCandidate(row) {
			continue
		}
		if !read {
			log, read = s.readLandingProbes(), true
			history = log.probes
		}
		last, ok := history[row.ID]
		if ok {
			next[row.ID] = last
		}
		if !ok || last.Updated != row.Updated || now-last.At >= int64(landingInterval/time.Second) {
			due = append(due, row)
		}
	}
	if since := now - lastProbeRound(history); since >= 0 && since < int64(landingRound/time.Second) {
		due = nil
	}
	slices.SortStableFunc(due, func(a, b types.Job) int { return cmp.Compare(history[a.ID].At, history[b.ID].At) })
	due = due[:min(len(due), landingBudget)]
	for _, row := range due {
		next[row.ID] = landingProbe{At: now, Updated: row.Updated}
	}
	if read && !s.dryRun && !maps.Equal(history, next) {
		s.writeLandingProbes(log, next)
	}
	out := map[string]landing{}
	for _, row := range due {
		if reason := probe(context.Background(), row); reason != "" {
			out[row.ID] = landing{updated: row.Updated, reason: reason}
		}
	}
	return out
}

// landingBudget is how many exited rows one sweep probes. Every List sweeps, the guard
// lists on every hook call, and a probe runs several VCS subprocesses, so a pass over
// every exited row held a hook past its deadline. The rest wait for later sweeps.
const landingBudget = 4

// landingRound is the least time between two probe rounds, whichever processes read. A
// store holding more unprobed rows than one budget had every read probe four of them, so
// each fork, exit and hook call ran a few dozen VCS subprocesses.
const landingRound = time.Minute

// lastProbeRound is when the latest recorded probe ran, in unix seconds; 0 for none.
func lastProbeRound(history map[string]landingProbe) int64 {
	var last int64
	for _, p := range history {
		last = max(last, p.At)
	}
	return last
}

// readLandingProbes reads the probe history. A line that does not parse is skipped: the
// history only paces the probes, and losing an entry costs one early probe.
func (s *Store) readLandingProbes() probeLog {
	if s.err != nil || s.path == "" {
		return probeLog{}
	}
	read, err := file.ReadLines(filepath.Join(filepath.Dir(s.path), landingProbesFile))
	if err != nil {
		return probeLog{}
	}
	log := probeLog{probes: map[string]landingProbe{}, lines: len(read.Lines), partial: read.Partial}
	for _, line := range read.Lines {
		var p landingProbeLine
		if json.Unmarshal(line.Text, &p) == nil && p.ID != "" {
			log.probes[p.ID] = landingProbe{At: p.At, Updated: p.Updated}
		}
	}
	return log
}

// writeLandingProbes records next against log, the history it was decided from: the
// entries that changed are appended, and the file is rewritten to next alone when next
// drops an entry or the log is [file.CompactDue].
func (s *Store) writeLandingProbes(log probeLog, next map[string]landingProbe) {
	if s.err != nil || s.path == "" {
		return
	}
	dropped := false
	for id := range log.probes {
		if _, ok := next[id]; !ok {
			dropped = true
		}
	}
	var changed, all [][]byte
	for _, id := range slices.Sorted(maps.Keys(next)) {
		p := next[id]
		line, err := json.Marshal(landingProbeLine{ID: id, At: p.At, Updated: p.Updated})
		if err != nil {
			return
		}
		all = append(all, line)
		if prev, ok := log.probes[id]; !ok || prev != p {
			changed = append(changed, line)
		}
	}
	path := filepath.Join(filepath.Dir(s.path), landingProbesFile)
	if !dropped && !log.partial && !file.CompactDue(log.lines+len(changed), len(next)) {
		_ = file.AppendLines(path, changed, 0o644)
		return
	}
	_ = file.RewriteLines(path, all, 0o644)
}

// landedOnBase reports why row's work is on the base branch, or "" when that is not
// proven. Offline: it reads local refs and file content, never a remote.
//
// CONTENT, not ancestry, because the base usually gains the work as a squash commit
// holding several jobs' branches, which no ancestry walk connects to any of them. The
// work landed when every path the checkout's diff since the checkpoint shows reads the
// same at the row's checkout HEAD as at the base, and differs at HEAD from the row's
// checkpoint: a path equal at HEAD and checkpoint is uncommitted, so HEAD says nothing
// about it. A path that reads at neither side (a deletion, or a checkout already removed)
// leaves the row unproven.
//
// The diff is read, never the result's changed_paths: a result naming the part of the work
// that landed would otherwise end the job while the rest has not.
func (s *Store) landedOnBase(ctx context.Context, row types.Job) string {
	if s.root == "" {
		return ""
	}
	res, err := vcs.Resolve(ctx, s.root, "", types.VCSOptions{})
	if err != nil || res.VCS == nil || res.Base == "" {
		return ""
	}
	driver := res.VCS
	if info, err := os.Stat(row.CheckoutRoot); err != nil || !info.IsDir() {
		return ""
	}
	head, err := driver.FindCommit(ctx, row.CheckoutRoot, "")
	if err != nil {
		return ""
	}
	base, err := driver.FindCommit(ctx, s.root, res.Base)
	if err != nil {
		return ""
	}
	checkpoint := checkpointRevision(row.Checkpoint)
	// The paths alone: changedSince also places every changed line in its declaration,
	// a VCS read per path this verdict never looks at.
	paths, err := driver.ChangedFiles(ctx, row.CheckoutRoot, checkpoint)
	if err != nil || len(paths) == 0 {
		return ""
	}
	for _, changed := range paths {
		p := path.Clean(filepath.ToSlash(strings.TrimSpace(changed)))
		ours, err := driver.ReadFileAt(ctx, row.CheckoutRoot, head.ID, p)
		if err != nil {
			return ""
		}
		if theirs, err := driver.ReadFileAt(ctx, s.root, base.ID, p); err != nil || theirs != ours {
			return ""
		}
		if before, err := driver.ReadFileAt(ctx, row.CheckoutRoot, checkpoint, p); err == nil && before == ours {
			return ""
		}
	}
	return fmt.Sprintf("its work landed on %s at %s: the %d changed path(s) at %s in %s read the same there",
		res.Base, shortRevision(base), len(paths), shortRevision(head), row.CheckoutRoot)
}

// shortRevision is the commit's short id, or its id cut to 12 when the backend gave none.
func shortRevision(c types.Commit) string {
	if c.Short != "" {
		return c.Short
	}
	return c.ID[:min(len(c.ID), 12)]
}
