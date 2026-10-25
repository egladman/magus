package job

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

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
		reason := probe(context.Background(), row)
		if reason == "" {
			reason = s.landedInIntegration(context.Background(), row, rows)
		}
		if reason != "" {
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
	paths := contentLanded(ctx, driver, row, head, s.root, base)
	if paths == 0 {
		return ""
	}
	return fmt.Sprintf("its work landed on %s at %s: the %d changed path(s) at %s in %s read the same there",
		res.Base, shortRevision(base), paths, shortRevision(head), row.CheckoutRoot)
}

// landedInIntegration reports why row's work is in the checkout of a live integration job
// that names row.ID in its Integrates, or "" when no such checkout holds it. The base only
// gains the work when the integration lands, so until then the integration's own checkout
// is where a merged worker shows. Same content check as [Store.landedOnBase].
func (s *Store) landedInIntegration(ctx context.Context, row types.Job, rows []types.Job) string {
	for _, into := range rows {
		if into.State != types.StateRunning || into.CheckoutRoot == "" || !slices.Contains(into.Integrates, row.ID) {
			continue
		}
		res, err := vcs.Resolve(ctx, into.CheckoutRoot, "", types.VCSOptions{})
		if err != nil || res.VCS == nil {
			continue
		}
		driver := res.VCS
		if info, err := os.Stat(row.CheckoutRoot); err != nil || !info.IsDir() {
			return ""
		}
		head, err := driver.FindCommit(ctx, row.CheckoutRoot, "")
		if err != nil {
			return ""
		}
		tip, err := driver.FindCommit(ctx, into.CheckoutRoot, "")
		if err != nil {
			continue
		}
		if paths := contentLanded(ctx, driver, row, head, into.CheckoutRoot, tip); paths > 0 {
			return fmt.Sprintf("its work landed in integration job %s at %s: the %d changed path(s) at %s in %s read the same there",
				into.ID, shortRevision(tip), paths, shortRevision(head), row.CheckoutRoot)
		}
	}
	return ""
}

// contentLanded is how many paths the row's checkout changed since its checkpoint, or 0
// when any of them does not read at target's commit as it reads at head (the row's
// checkout HEAD), or still reads at head as it did at the checkpoint.
func contentLanded(ctx context.Context, driver types.VCSDriver, row types.Job, head types.Commit, targetRoot string, target types.Commit) int {
	checkpoint := checkpointRevision(row.Checkpoint)
	// The paths alone: changedSince also places every changed line in its declaration,
	// a VCS read per path this verdict never looks at.
	paths, err := driver.ChangedFiles(ctx, row.CheckoutRoot, checkpoint)
	if err != nil || len(paths) == 0 {
		return 0
	}
	for _, changed := range paths {
		p := path.Clean(filepath.ToSlash(strings.TrimSpace(changed)))
		ours, err := driver.ReadFileAt(ctx, row.CheckoutRoot, head.ID, p)
		if err != nil {
			return 0
		}
		if theirs, err := driver.ReadFileAt(ctx, targetRoot, target.ID, p); err != nil || theirs != ours {
			return 0
		}
		if before, err := driver.ReadFileAt(ctx, row.CheckoutRoot, checkpoint, p); err == nil && before == ours {
			return 0
		}
	}
	return len(paths)
}

// shortRevision is the commit's short id, or its id cut to 12 when the backend gave none.
func shortRevision(c types.Commit) string {
	if c.Short != "" {
		return c.Short
	}
	return c.ID[:min(len(c.ID), 12)]
}
