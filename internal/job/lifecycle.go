package job

import (
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
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
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
	var rows []types.Job
	if store.outputs != nil {
		var err error
		if rows, err = store.List(); err != nil {
			return types.Job{}, err
		}
	}
	resolve, where := store.acrossCheckouts(rows, id, resolve)
	attempt, gateAttempts, err := resolveResultAttempts(ctx, *result, resolve, where)
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
		resolve, where := store.acrossCheckouts(jobs, id, resolve)
		attempt, gateAttempts, err = resolveResultAttempts(ctx, *result, resolve, where)
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
// lock, so an extra or missing reference never becomes authority. where names the
// checkouts resolve searched, for the refusal.
func resolveResultAttempts(ctx context.Context, result types.JobResult, resolve AttemptResolver, where string) (types.JobAttempt, []types.JobGateAttempt, error) {
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
			return types.JobAttempt{}, nil, fmt.Errorf("job: the result's output ref %q names no run %s recorded", ref, where)
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
			return types.JobAttempt{}, nil, fmt.Errorf("job: gate %q output ref %q names no run %s recorded", evidence.GateID, evidence.OutputRef, where)
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
//  4. its holder exited, its work landed on the base branch (see [Store.landedOnBase]),
//     and no live child hangs under it.
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
		var round []ending
		for _, row := range rows {
			if !row.State.Live() || row.Holder.OrSession() == types.HolderServer || readOnly(row) != nil {
				continue
			}
			reason, err := w.reason(rows, row)
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

// reason is why row is dead, or "" when it is not. See [sweeper.dead].
func (w *sweeper) reason(rows []types.Job, row types.Job) (string, error) {
	if a, ok := types.EndedAncestor(rows, row.ID); ok {
		kin := "ancestor"
		if a.ID == row.Parent {
			kin = "parent"
		}
		return fmt.Sprintf("%s %s ended as %s, and its tree ends with it", kin, a.ID, a.State), nil
	}
	liveChild := slices.ContainsFunc(rows, func(r types.Job) bool { return r.Parent == row.ID && r.State.Live() })
	if l, ok := w.landed[row.ID]; ok && row.State == types.StateExited && l.updated == row.Updated && !liveChild {
		return l.reason, nil
	}
	if row.State != types.StateExited && w.checkoutGone(row.CheckoutRoot) {
		return fmt.Sprintf("taken in %s, which no longer exists", row.CheckoutRoot), nil
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
	if !row.StaleAt(w.now, window) {
		return "", nil
	}
	return fmt.Sprintf("declared and never taken, untouched for %s (jobs.stale_after is %s)",
		time.Duration(w.now-row.Updated)*time.Second, window), nil
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

// sweep ends every row in f the [sweeper] proves dead, and returns the plan as written.
// The verdict is reached twice: once lock-free, so a plan with nothing to end costs no
// lock, and again under the file lock against a fresh read, so a row another process
// revived in between is not ended on a stale view.
func (s *Store) sweep(f jobsFile) (jobsFile, error) {
	clock := s.clock
	if clock == nil {
		clock = time.Now
	}
	w := &sweeper{now: clock().Unix(), window: sync.OnceValues(s.resolveStaleAfter), gone: map[string]bool{}}
	w.landed = s.landings(f.Jobs, w.now)
	dead, err := w.dead(f.Jobs)
	if err != nil || len(dead) == 0 {
		return f, err
	}
	err = s.withFileLock(context.Background(), func() error {
		cur, err := s.read()
		if err != nil {
			return err
		}
		if dead, err = w.dead(cur.Jobs); err != nil {
			return err
		}
		for _, e := range dead {
			i := slices.IndexFunc(cur.Jobs, func(r types.Job) bool { return r.ID == e.id })
			cur.Jobs[i].State = types.StateNoReturn
			cur.Jobs[i].EndReason = e.reason
			cur.Jobs[i].Updated = w.now
		}
		if len(dead) > 0 {
			if err := s.write(cur); err != nil {
				return err
			}
		}
		f = cur
		return nil
	})
	if err != nil {
		return jobsFile{}, err
	}
	out := s.notices
	if out == nil {
		out = os.Stderr
	}
	for _, e := range dead {
		fmt.Fprintf(out, "ended %s: %s\n", e.id, e.reason)
	}
	return f, nil
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

// landingProbesFile holds when each exited row was last probed, beside jobs.json, so the
// interval holds across processes.
const landingProbesFile = "landing-probes.json"

// landingProbe is one row's entry in landingProbesFile: when it was probed, and the row's
// Updated then, so a row that exits again is probed at once.
type landingProbe struct {
	At      int64 `json:"at"`
	Updated int64 `json:"updated"`
}

// landingCandidate reports an exited row whose result names changed paths in a checkout,
// the only rows a landing can be judged for.
func landingCandidate(row types.Job) bool {
	return row.State == types.StateExited && row.Holder.OrSession() != types.HolderServer && !row.ReadOnly &&
		row.Result != nil && len(row.Result.ChangedPaths) > 0 && row.CheckoutRoot != "" &&
		checkpointRevision(row.Checkpoint) != ""
}

// landings probes every exited row not probed within landingInterval and returns the
// ones whose work landed. A probe history that cannot be read or written only costs the
// interval, never a verdict.
func (s *Store) landings(rows []types.Job, now int64) map[string]landing {
	probe, throttled := s.landed, false
	if probe == nil {
		probe, throttled = s.landedOnBase, true
	}
	var history map[string]landingProbe
	read := false
	next := map[string]landingProbe{}
	out := map[string]landing{}
	for _, row := range rows {
		if !landingCandidate(row) {
			continue
		}
		if throttled && !read {
			history, read = s.readLandingProbes(), true
		}
		if last, ok := history[row.ID]; ok && last.Updated == row.Updated && now-last.At < int64(landingInterval/time.Second) {
			next[row.ID] = last
			continue
		}
		next[row.ID] = landingProbe{At: now, Updated: row.Updated}
		if reason := probe(context.Background(), row); reason != "" {
			out[row.ID] = landing{updated: row.Updated, reason: reason}
		}
	}
	if read && !maps.Equal(history, next) {
		s.writeLandingProbes(next)
	}
	return out
}

func (s *Store) readLandingProbes() map[string]landingProbe {
	if s.err != nil || s.path == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(s.path), landingProbesFile))
	if err != nil {
		return nil
	}
	var out map[string]landingProbe
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}

func (s *Store) writeLandingProbes(probes map[string]landingProbe) {
	if s.err != nil || s.path == "" {
		return
	}
	raw, err := json.Marshal(probes)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.path), 0o755)
	_ = file.WriteFileAtomic(filepath.Join(filepath.Dir(s.path), landingProbesFile), raw, 0o644)
}

// landedOnBase reports why row's work is on the base branch, or "" when that is not
// proven. Offline: it reads local refs and file content, never a remote.
//
// CONTENT, not ancestry, because the base usually gains the work as a squash commit
// holding several jobs' branches, which no ancestry walk connects to any of them. The
// work landed when every changed path the result names reads the same at the row's
// checkout HEAD as at the base, and differs at HEAD from the row's checkpoint: a path
// equal at HEAD and checkpoint is uncommitted, so HEAD says nothing about it. A path
// that reads at neither side (a deletion, or a checkout already removed) leaves the row
// unproven.
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
	for _, changed := range row.Result.ChangedPaths {
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
		res.Base, shortRevision(base), len(row.Result.ChangedPaths), shortRevision(head), row.CheckoutRoot)
}

// shortRevision is the commit's short id, or its id cut to 12 when the backend gave none.
func shortRevision(c types.Commit) string {
	if c.Short != "" {
		return c.Short
	}
	return c.ID[:min(len(c.ID), 12)]
}
