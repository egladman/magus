package job

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/hint"
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
	return fmt.Sprintf(landedReasonPrefix+"on %s at %s: the %d changed path(s) at %s in %s read the same there",
		res.Base,shortRevision(base), paths, shortRevision(head), row.CheckoutRoot)
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
			return fmt.Sprintf(landedReasonPrefix+"in integration job %s at %s: the %d changed path(s) at %s in %s read the same there",
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

// landedReasonPrefix opens the end_reason of a row a landing probe ended.
const landedReasonPrefix = "its work landed "

// Unmerged reports what the branch checked out at root does not hold, for job
// trees forked from root. Content counts as well as ancestry, so a squash
// reads as merged. It reads the version control per row, so it stays off the
// hot paths. Repo policy decides what a fact stops.
func Unmerged(ctx context.Context, root string, rows []types.Job) types.JobUnmergedReport {
	var report types.JobUnmergedReport
	var work []types.Job
	for _, row := range rows {
		if row.Holder.OrSession() == types.HolderServer || row.LeftUnmerged != "" || !ownTree(rows, root, row) {
			continue
		}
		report.Rows = append(report.Rows, unanswered(rows, row)...)
		if !row.ReadOnly && !row.State.Editing() && (row.Result != nil || row.State.Terminal()) && !strings.HasPrefix(row.EndReason, landedReasonPrefix) {
			work = append(work, row)
		}
	}
	if len(work) == 0 {
		return report
	}
	driver, ranges, err := rangeDriver(ctx, root)
	if err != nil {
		report.Unknown = err.Error()
		return report
	}
	branch, err := driver.Ref(ctx, root)
	if err != nil {
		report.Unknown = fmt.Sprintf("the branch of %s: %s", root, err)
		return report
	}
	if branch == "" {
		report.Unknown = root + " has no branch checked out, so nothing can be merged onto one"
		return report
	}
	meta, err := driver.Metadata(ctx, root)
	if err != nil {
		report.Unknown = fmt.Sprintf("the revision of %s: %s", root, err)
		return report
	}
	report.Branch = branch
	for _, row := range work {
		report.Rows = append(report.Rows, workUnmerged(ctx, driver, ranges, root, branch, meta.ID, rows, row)...)
	}
	return report
}

// unanswered are row's deferred items no answer settles. An answer naming a
// row the store lost settles nothing.
func unanswered(rows []types.Job, row types.Job) []types.JobUnmerged {
	if row.Result == nil {
		return nil
	}
	var out []types.JobUnmerged
	for _, item := range slices.Concat(row.Result.UnresolvedRisks, row.Result.FollowUps) {
		i := slices.IndexFunc(row.Deferred, func(d types.JobDeferred) bool { return d.Item == item })
		if i >= 0 && (row.Deferred[i].Reason != "" || slices.ContainsFunc(rows, func(r types.Job) bool { return r.ID == row.Deferred[i].Row })) {
			continue
		}
		out = append(out, types.JobUnmerged{
			Job: row.ID, Item: item, Why: "its result defers this and no row carries it",
			Remedy: fmt.Sprintf("declare a row for it, or dismiss it: `%s` with {\"schema_version\":%d,\"id\":%q,\"deferred\":[{\"item\":\"<item>\",\"row\":\"<row>\"}]}, or \"reason\":\"<why>\" in place of row",
				hint.JobApply.With("-f", "-"), types.JobSchemaVersion, row.ID),
		})
	}
	return out
}

// workUnmerged is what row's work leaves off branch: uncommitted paths,
// commits, or an unreadable head. A row with no checkout, no branch and no
// filed path has nothing to lose, nor one a passed integration names.
func workUnmerged(ctx context.Context, driver types.VCSDriver, ranges types.RangeReporter, root, branch, tip string, rows []types.Job, row types.Job) []types.JobUnmerged {
	state := fmt.Sprintf("or state why it stays off: `%s` with {\"schema_version\":%d,\"id\":%q,\"left_unmerged\":\"<why>\"}",
		hint.JobApply.With("-f", "-"), types.JobSchemaVersion, row.ID)
	fact := func(head, why, remedy string) types.JobUnmerged {
		return types.JobUnmerged{Job: row.ID, Head: head, Why: why, Remedy: fmt.Sprintf("%s, %s", remedy, state)}
	}
	head, where, err := jobHead(ctx, driver, root, row)
	if err != nil {
		if where == "" && (row.Result == nil || len(row.Result.ChangedPaths) == 0 || integratedByPass(rows, row.ID)) {
			return nil
		}
		return []types.JobUnmerged{fact("", fmt.Sprintf("its head cannot be read: %s", err), "restore its branch "+row.ID)}
	}
	var out []types.JobUnmerged
	short := shortRevision(head)
	if where != "" {
		dirty, unread := uncommitted(ctx, where, row)
		switch {
		case unread != "":
			out = append(out, fact(short, unread, fmt.Sprintf("commit what %s holds", where)))
		case len(dirty) > 0:
			out = append(out, fact(short, fmt.Sprintf("%s holds %d uncommitted path(s), first %s", where, len(dirty), dirty[0]), "commit them in "+where))
		}
	}
	merge := fmt.Sprintf("merge %s into %s", short, branch)
	commits, err := ranges.RangeCommits(ctx, root, tip, head.ID, nil)
	switch {
	case err != nil:
		out = append(out, fact(short, fmt.Sprintf("whether %s holds %s cannot be read: %s", branch, short, err), merge))
	case len(commits) > 0 && !changesOnBase(ctx, driver, ranges, root, tip, head.ID):
		out = append(out, fact(short, fmt.Sprintf("%d commit(s) at %s are not on %s", len(commits), short, branch), merge))
	}
	return out
}

// jobHead is the commit holding row's work and the checkout it was read in,
// "" when the branch named for the job answered instead.
func jobHead(ctx context.Context, driver types.VCSDriver, root string, row types.Job) (types.Commit, string, error) {
	if where := heldCheckout(row); where != "" {
		if info, err := os.Stat(where); err == nil && info.IsDir() {
			head, err := driver.FindCommit(ctx, where, "")
			return head, where, err
		}
	}
	head, err := driver.FindCommit(ctx, root, row.ID)
	return head, "", err
}

func integratedByPass(rows []types.Job, id string) bool {
	return slices.ContainsFunc(rows, func(r types.Job) bool {
		return r.State == types.StatePass && slices.Contains(r.Integrates, id)
	})
}

// Settle writes each record's answers onto the row it names, ended or not.
// Only an orchestrator settles: a holder cannot wave off what it deferred.
// Every record is checked before any is written.
func (s *Store) Settle(ctx context.Context, records []types.Declaration, dryRun bool) ([]types.Job, error) {
	if lease := s.Actor().Lease; lease != "" {
		return nil, fmt.Errorf("%s holds a lease, and only its orchestrator settles a row", lease)
	}
	rows, err := s.List()
	if err != nil {
		return nil, err
	}
	var errs []error
	for _, rec := range records {
		if !rec.Settles() {
			errs = append(errs, fmt.Errorf("%s declares a spec, and a record settling a row applies with others settling rows only", rec.ID))
			continue
		}
		if err := rec.Validate(); err != nil {
			errs = append(errs, err)
			continue
		}
		i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == rec.ID })
		if i < 0 {
			errs = append(errs, fmt.Errorf("nothing declares job %s, so nothing settles", rec.ID))
			continue
		}
		errs = append(errs, settleErrors(rows, rows[i], rec)...)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	settled := make([]types.Job, 0, len(records))
	for _, rec := range records {
		apply := func(u *types.Job) {
			if rec.LeftUnmerged != "" {
				u.LeftUnmerged = strings.TrimSpace(rec.LeftUnmerged)
			}
			for _, d := range rec.Deferred {
				u.Deferred = slices.DeleteFunc(u.Deferred, func(have types.JobDeferred) bool { return have.Item == d.Item })
				u.Deferred = append(u.Deferred, d)
			}
		}
		if dryRun {
			row := rows[slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == rec.ID })].Clone()
			apply(&row)
			settled = append(settled, row)
			continue
		}
		row, err := s.Update(ctx, rec.ID, apply)
		if err != nil {
			return settled, fmt.Errorf("settled %d of %d record(s), then %s: %w", len(settled), len(records), rec.ID, err)
		}
		settled = append(settled, row)
	}
	return settled, nil
}

// settleErrors refuses an answer naming an item the result lacks or a row
// nobody declared, so a typo never reads as settled.
func settleErrors(rows []types.Job, row types.Job, rec types.Declaration) []error {
	var errs []error
	var items []string
	if row.Result != nil {
		items = slices.Concat(row.Result.UnresolvedRisks, row.Result.FollowUps)
	}
	for _, d := range rec.Deferred {
		if !slices.Contains(items, d.Item) {
			errs = append(errs, fmt.Errorf("%s's result defers no item %q", rec.ID, d.Item))
		}
		if d.Row != "" && !slices.ContainsFunc(rows, func(r types.Job) bool { return r.ID == d.Row }) {
			errs = append(errs, fmt.Errorf("%s answers %q with row %s, which nothing declares", rec.ID, d.Item, d.Row))
		}
	}
	return errs
}

// shortRevision is the commit's short id, or its id cut to 12 when the backend gave none.
func shortRevision(c types.Commit) string {
	if c.Short != "" {
		return c.Short
	}
	return c.ID[:min(len(c.ID), 12)]
}
