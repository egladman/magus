package job

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/libs/xdg"
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

// landingCandidate reports a running or exited row with a filed result in a checkout,
// the only rows a landing can be judged for.
func landingCandidate(row types.Job) bool {
	return (row.State == types.StateExited || row.State == types.StateRunning) && row.Holder.OrSession() != types.HolderServer && !row.ReadOnly &&
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
		if err := s.writeLandingProbes(log, next); err != nil {
			// No file lock is held, and the loss costs only the interval.
			slog.Warn("landing probes not recorded", attr.Component("magus"), attr.Error(err))
		}
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
// drops an entry or the log is [file.CompactDue]. It returns the write's error.
func (s *Store) writeLandingProbes(log probeLog, next map[string]landingProbe) error {
	if s.err != nil || s.path == "" {
		return nil
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
			return err
		}
		all = append(all, line)
		if prev, ok := log.probes[id]; !ok || prev != p {
			changed = append(changed, line)
		}
	}
	path := filepath.Join(filepath.Dir(s.path), landingProbesFile)
	if !dropped && !log.partial && !file.CompactDue(log.lines+len(changed), len(next)) {
		return file.AppendLines(path, changed, 0o644)
	}
	return file.RewriteLines(path, all, 0o644)
}

// landedOnBase reports why row's work is on the base branch, or "" when that is not
// proven. Offline: it reads local refs and file content, never a remote.
//
// CONTENT, not ancestry, because the base usually gains the work as a squash commit
// holding several jobs' branches, which no ancestry walk connects to any of them. The
// work landed when every path the checkout's diff since the checkpoint shows reads the
// same at the row's checkout HEAD as at the base, and differs at HEAD from the row's
// checkpoint: a path equal at HEAD and checkpoint is uncommitted, so HEAD says nothing
// about it. A path deleted at both HEAD and the base reads the same (see
// [vcs.DifferingPaths]).
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
	if dirty, err := driver.Dirty(ctx, row.CheckoutRoot, nil); err != nil || dirty {
		return 0
	}
	// The paths alone: changedSince also places every changed line in its declaration,
	// a VCS read per path this verdict never looks at.
	paths, err := driver.ChangedFiles(ctx, row.CheckoutRoot, checkpoint)
	if err != nil || len(paths) == 0 {
		return 0
	}
	off, err := vcs.DifferingPaths(ctx, driver, targetRoot, head.ID, target.ID, paths)
	if err != nil || len(off) > 0 {
		return 0
	}
	committed, err := vcs.DifferingPaths(ctx, driver, row.CheckoutRoot, checkpoint, head.ID, paths)
	if err != nil || len(committed) < len(paths) {
		return 0
	}
	return len(paths)
}

// Unmerged reports what the branch checked out at root does not hold, for job
// trees forked from root. Content counts as well as ancestry, so a squash or a
// replay reads as merged. A row's answer is stored and read again until its
// head or the branch moves, and a read spends [unmergedBudget] at most, so a
// stop hook can call it. What the budget left unread is named in Unknown.
// Repo policy decides what a fact stops.
func Unmerged(ctx context.Context, root string, rows []types.Job) types.JobUnmergedReport {
	var report types.JobUnmergedReport
	var work []types.Job
	for _, row := range rows {
		if row.Holder.OrSession() == types.HolderServer || row.LeftUnmerged != "" || !ownTree(rows, root, row) {
			continue
		}
		if row.Result != nil {
			report.Rows = append(report.Rows, unanswered(rows, row, row.Result.FollowUps, "its result defers this and no row carries it")...)
			report.Risks = append(report.Risks, unanswered(rows, row, row.Result.UnresolvedRisks, "its result names this risk and no row carries it")...)
		}
		if !row.ReadOnly && !row.State.Editing() && (row.Result != nil || row.State.Terminal()) && !row.Landed {
			work = append(work, row)
		}
	}
	if len(work) == 0 {
		return report
	}
	driver, why := resolveDriver(ctx, root)
	if driver == nil {
		report.Unknown = why
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
	read := unmergedRead{driver: driver, root: root, branch: branch, tip: meta.ID, now: time.Now().Unix()}
	read.run(ctx, rows, work)
	for _, row := range work {
		report.Rows = append(report.Rows, read.facts(rows, row)...)
	}
	report.Unknown = read.unknown()
	return report
}

// unanswered are the items no answer on row settles. An answer naming a row
// the store lost settles nothing.
func unanswered(rows []types.Job, row types.Job, items []string, why string) []types.JobUnmerged {
	var out []types.JobUnmerged
	for _, item := range items {
		i := slices.IndexFunc(row.Deferred, func(d types.JobDeferred) bool { return d.Item == item })
		if i >= 0 && (row.Deferred[i].Reason != "" || slices.ContainsFunc(rows, func(r types.Job) bool { return r.ID == row.Deferred[i].Row })) {
			continue
		}
		out = append(out, types.JobUnmerged{
			Job: row.ID, Item: item, Why: why,
			Remedy: fmt.Sprintf("declare a row for it, or dismiss it: `%s` with {\"schema_version\":%d,\"id\":%q,\"deferred\":[{\"item\":\"<item>\",\"row\":\"<row>\"}]}, or \"reason\":\"<why>\" in place of row",
				hint.JobApply.With("--stdin"), types.JobSchemaVersion, row.ID),
		})
	}
	return out
}

// unmergedBudget is what one read spends on the version control. The stop hook
// has seven seconds, and a row past this waits for the next read.
const unmergedBudget = 3 * time.Second

// unmergedRowBudget stops one wide row spending a whole read, every read.
// A row a read already cut may spend what is left, or it never answers.
const unmergedRowBudget = time.Second

// unmergedFactsFile holds each row's answer beside jobs.jsonl, one line per
// answer. A row's last line for a branch is its entry.
const unmergedFactsFile = "unmerged-facts.jsonl"

// unmergedFact is what the version control last said of one row's head.
type unmergedFact struct {
	ID       string   `json:"id"`
	Branch   string   `json:"branch"`
	Head     string   `json:"head"`
	Short    string   `json:"short,omitempty"`
	Updated  int64    `json:"updated"`
	Where    string   `json:"where,omitempty"`
	Dirty    []string `json:"dirty,omitempty"`
	DirtyAt  int64    `json:"dirty_at,omitempty"`
	Tip      string   `json:"tip,omitempty"`
	Merged   bool     `json:"merged,omitempty"`
	Commits  int      `json:"commits,omitempty"`
	MergedAt int64    `json:"merged_at,omitempty"`
	// CutAt is when a read gave up on the row. The next starts elsewhere.
	CutAt int64 `json:"cut_at,omitempty"`
}

// unmergedRow is one row during one read.
type unmergedRow struct {
	fact                unmergedFact
	where, unread       string
	rangeErr            string
	headErr             error
	dirtyDue, mergedDue bool
	seen                bool
}

// unmergedRead answers [Unmerged] for one branch of one checkout.
type unmergedRead struct {
	driver    types.VCSDriver
	root      string
	branch    string
	tip       string
	now       int64
	checkouts map[string]string
	branches  map[string]string
	rows      map[string]*unmergedRow
	unread    int
	stale     int
	recut     int
}

// run answers every row in work from the stored facts, then asks the version
// control about what moved: unseen heads first, then the oldest answers. rows
// says which stored answers still have a row.
func (r *unmergedRead) run(ctx context.Context, rows, work []types.Job) {
	ctx, cancel := context.WithTimeout(ctx, unmergedBudget)
	defer cancel()
	path := unmergedFactsPath(r.root)
	stored := readUnmergedFacts(path)
	r.readHeads(ctx)
	r.rows = make(map[string]*unmergedRow, len(work))
	var due []types.Job
	for _, row := range work {
		rr := r.place(ctx, row, stored.facts[unmergedKey(row.ID, r.branch)])
		r.rows[row.ID] = rr
		if rr.dirtyDue || rr.mergedDue {
			due = append(due, row)
		}
	}
	slices.SortStableFunc(due, func(a, b types.Job) int {
		x, y := r.rows[a.ID], r.rows[b.ID]
		return cmp.Or(cmp.Compare(x.fact.CutAt, y.fact.CutAt), cmp.Compare(boolRank(x.seen), boolRank(y.seen)), cmp.Compare(x.fact.MergedAt, y.fact.MergedAt))
	})
	next := r.ask(ctx, due)
	for _, row := range due {
		rr := r.rows[row.ID]
		switch {
		case !rr.dirtyDue && !rr.mergedDue:
		case !rr.seen:
			r.unread++
		case rr.mergedDue && !rr.fact.Merged:
			r.stale++
		}
	}
	err := writeUnmergedFacts(path, stored, next, func(f unmergedFact) bool {
		return slices.ContainsFunc(rows, func(row types.Job) bool { return row.ID == f.ID }) && r.branchStands(f.Branch)
	})
	if err != nil {
		// Unmerged runs outside the job store lock and cannot return an error.
		slog.WarnContext(ctx, "unmerged answers not stored", attr.Component("magus"), attr.Error(err))
	}
}

// branchStands reports branch as the one read or one still listed. A backend
// that lists none leaves every branch standing.
func (r *unmergedRead) branchStands(branch string) bool {
	if r.branches == nil || branch == r.branch {
		return true
	}
	_, listed := r.branches[branch]
	return listed
}

// ask checks due in order until the budget ends, and returns what to store.
func (r *unmergedRead) ask(ctx context.Context, due []types.Job) map[string]unmergedFact {
	next := map[string]unmergedFact{}
	for _, row := range due {
		if ctx.Err() != nil {
			break
		}
		rr := r.rows[row.ID]
		one, done := ctx, context.CancelFunc(func() {})
		if rr.fact.CutAt == 0 {
			one, done = context.WithTimeout(ctx, unmergedRowBudget)
		}
		r.check(one, rr, row)
		done()
		switch {
		case rr.headErr != nil:
		case rr.dirtyDue || rr.mergedDue:
			if rr.fact.CutAt != 0 {
				r.recut++
			}
			cut := rr.fact
			cut.CutAt = r.now
			next[row.ID] = cut
		case rr.unread == "" && rr.rangeErr == "":
			rr.fact.CutAt = 0
			next[row.ID] = rr.fact
		}
	}
	return next
}

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

// readHeads reads every checkout's and branch's head in one call each. Left
// empty, each row reads its own.
func (r *unmergedRead) readHeads(ctx context.Context) {
	if listed, err := r.driver.RegisteredCheckouts(ctx, r.root); err == nil {
		r.checkouts = make(map[string]string, len(listed))
		for _, co := range listed {
			r.checkouts[resolved(co.Root)] = co.Head
		}
	}
	if heads, err := r.driver.BranchHeads(ctx, r.root); err == nil {
		r.branches = heads
	}
}

// place finds row's head and says which stored answers still stand. A stored
// merge stands until its head moves. A stored miss stands for one tip of the
// branch. A status stands for landingInterval, while the row is not edited.
func (r *unmergedRead) place(ctx context.Context, row types.Job, stored unmergedFact) *unmergedRow {
	rr := &unmergedRow{}
	head, where, err := r.head(ctx, row)
	rr.where = where
	if err != nil {
		rr.headErr = err
		rr.dirtyDue, rr.mergedDue = ctx.Err() != nil, false
		return rr
	}
	rr.seen = stored.Head == head && stored.Tip != "" && (where == "" || stored.DirtyAt != 0)
	rr.fact = stored
	if stored.Head != head {
		rr.fact = unmergedFact{ID: row.ID, Branch: r.branch, Head: head}
	}
	interval := int64(landingInterval / time.Second)
	moved := rr.fact.Tip != r.tip
	rr.mergedDue = rr.fact.Tip == "" || moved && (!rr.fact.Merged || r.now-rr.fact.MergedAt >= interval)
	rr.dirtyDue = where != "" && (rr.fact.DirtyAt == 0 || rr.fact.Updated != row.Updated || rr.fact.Where != where || r.now-rr.fact.DirtyAt >= interval)
	return rr
}

// head is the commit holding row's work and the checkout it was read in, ""
// when the branch named for the job answered instead.
func (r *unmergedRead) head(ctx context.Context, row types.Job) (string, string, error) {
	if where := heldCheckout(row); where != "" {
		if info, err := os.Stat(where); err == nil && info.IsDir() {
			if id := r.checkouts[resolved(where)]; id != "" {
				return id, where, nil
			}
			head, err := r.driver.FindCommit(ctx, where, "")
			return head.ID, where, err
		}
	}
	if r.branches != nil {
		id, ok := r.branches[row.ID]
		if !ok {
			return "", "", fmt.Errorf("no branch is named %s", row.ID)
		}
		return id, "", nil
	}
	head, err := r.driver.FindCommit(ctx, r.root, row.ID)
	return head.ID, "", err
}

// check asks the version control what place found due. Past the deadline it
// leaves a question due, so no half answer is stored.
func (r *unmergedRead) check(ctx context.Context, rr *unmergedRow, row types.Job) {
	f := &rr.fact
	if rr.dirtyDue {
		dirty, unread := uncommitted(ctx, rr.where, row)
		if ctx.Err() != nil {
			return
		}
		rr.unread, rr.dirtyDue = unread, false
		f.Dirty, f.DirtyAt, f.Updated, f.Where = dirty, r.now, row.Updated, rr.where
	}
	if rr.mergedDue {
		merged, commits, err := r.holds(ctx, f)
		if ctx.Err() != nil {
			return
		}
		rr.mergedDue = false
		if err != nil {
			rr.rangeErr = err.Error()
		} else {
			f.Merged, f.Commits, f.Tip, f.MergedAt = merged, commits, r.tip, r.now
		}
	}
	if f.Short == "" && (!f.Merged || len(f.Dirty) > 0) {
		if head, err := r.driver.FindCommit(ctx, cmp.Or(rr.where, r.root), f.Head); err == nil {
			f.Short = head.Short
		}
	}
}

// holds reports whether the branch has f's head, and else how many commits it
// lacks. A stored answer stands while the branch keeps the tip it was read at.
func (r *unmergedRead) holds(ctx context.Context, f *unmergedFact) (bool, int, error) {
	if f.Merged && f.Tip != "" {
		if kept, err := r.driver.IsAncestor(ctx, r.root, f.Tip, r.tip); err == nil && kept {
			return true, 0, nil
		}
	}
	missing, err := r.driver.MissingCommits(ctx, r.root, r.tip, f.Head)
	if err != nil {
		return false, 0, err
	}
	return missing == 0, missing, nil
}

// facts is what row's work leaves off the branch: uncommitted paths, commits,
// or an unreadable head. A row with no checkout, no branch and no filed path
// has nothing to lose, nor one a passed integration names.
func (r *unmergedRead) facts(rows []types.Job, row types.Job) []types.JobUnmerged {
	state := fmt.Sprintf("or state why it stays off: `%s` with {\"schema_version\":%d,\"id\":%q,\"left_unmerged\":\"<why>\"}",
		hint.JobApply.With("--stdin"), types.JobSchemaVersion, row.ID)
	fact := func(head, why, remedy string) types.JobUnmerged {
		return types.JobUnmerged{Job: row.ID, Head: head, Why: why, Remedy: fmt.Sprintf("%s, %s", remedy, state)}
	}
	rr := r.rows[row.ID]
	switch {
	case rr.headErr != nil && rr.dirtyDue:
		return nil
	case rr.headErr != nil:
		if rr.where == "" && (row.Result == nil || len(row.Result.ChangedPaths) == 0 || integratedByPass(rows, row.ID)) {
			return nil
		}
		return []types.JobUnmerged{fact("", fmt.Sprintf("its head cannot be read: %s", rr.headErr), "restore its branch "+row.ID)}
	case !rr.seen && (rr.dirtyDue || rr.mergedDue):
		return nil
	}
	var out []types.JobUnmerged
	f := rr.fact
	short := shortRevision(types.Commit{ID: f.Head, Short: f.Short})
	switch {
	case rr.unread != "":
		out = append(out, fact(short, rr.unread, fmt.Sprintf("commit what %s holds", rr.where)))
	case rr.where != "" && len(f.Dirty) > 0:
		out = append(out, fact(short, fmt.Sprintf("%s holds %d uncommitted path(s), first %s", rr.where, len(f.Dirty), f.Dirty[0]), "commit them in "+rr.where))
	}
	merge := fmt.Sprintf("merge %s into %s", short, r.branch)
	switch {
	case rr.rangeErr != "":
		out = append(out, fact(short, fmt.Sprintf("whether %s holds %s cannot be read: %s", r.branch, short, rr.rangeErr), merge))
	case !f.Merged:
		out = append(out, fact(short, fmt.Sprintf("%d commit(s) at %s are not on %s", f.Commits, short, r.branch), merge))
	}
	return out
}

// unknown says what this read left unanswered, "" when it answered everything.
func (r *unmergedRead) unknown() string {
	var parts []string
	if r.unread > 0 {
		parts = append(parts, fmt.Sprintf("%d row(s) were not read within %s, and the next read carries on from here", r.unread, unmergedBudget))
	}
	if r.stale > 0 {
		parts = append(parts, fmt.Sprintf("%d row(s) keep their answer from an older commit of %s", r.stale, r.branch))
	}
	if r.recut > 0 {
		parts = append(parts, fmt.Sprintf("%d of those a read had already cut, so they may need longer than %s", r.recut, unmergedBudget))
	}
	return strings.Join(parts, "; ")
}

func unmergedKey(id, branch string) string { return fmt.Sprintf("%s\x00%s", id, branch) }

// unmergedFactsPath is "" when no state dir answers, which only costs the
// stored answers.
func unmergedFactsPath(root string) string {
	base, err := xdg.StateDir()
	if err != nil {
		return ""
	}
	dir, err := vcs.StateDir(base, "jobs", root)
	if err != nil {
		return ""
	}
	return filepath.Join(dir, unmergedFactsFile)
}

// storedUnmergedFacts is the file as read, and what a write appends to.
type storedUnmergedFacts struct {
	facts   map[string]unmergedFact
	lines   int
	partial bool
}

// readUnmergedFacts skips a line that does not parse: losing one only asks the
// version control again.
func readUnmergedFacts(path string) storedUnmergedFacts {
	stored := storedUnmergedFacts{facts: map[string]unmergedFact{}}
	if path == "" {
		return stored
	}
	read, err := file.ReadLines(path)
	if err != nil {
		return stored
	}
	stored.lines, stored.partial = len(read.Lines), read.Partial
	for _, line := range read.Lines {
		var f unmergedFact
		if json.Unmarshal(line.Text, &f) == nil && f.ID != "" {
			stored.facts[unmergedKey(f.ID, f.Branch)] = f
		}
	}
	return stored
}

// writeUnmergedFacts appends next, or rewrites to the latest per row once the
// file is [file.CompactDue] or an answer keep refuses drops out. It returns the
// write's error, which only costs the stored answers.
func writeUnmergedFacts(path string, stored storedUnmergedFacts, next map[string]unmergedFact, keep func(unmergedFact) bool) error {
	if path == "" {
		return nil
	}
	dropped := false
	for key, f := range stored.facts {
		if !keep(f) {
			delete(stored.facts, key)
			dropped = true
		}
	}
	if !dropped && len(next) == 0 {
		return nil
	}
	var changed [][]byte
	for _, id := range slices.Sorted(maps.Keys(next)) {
		f := next[id]
		stored.facts[unmergedKey(f.ID, f.Branch)] = f
		if line, err := json.Marshal(f); err == nil {
			changed = append(changed, line)
		}
	}
	if !dropped && !stored.partial && !file.CompactDue(stored.lines+len(changed), len(stored.facts)) {
		return file.AppendLines(path, changed, 0o644)
	}
	var all [][]byte
	for _, key := range slices.Sorted(maps.Keys(stored.facts)) {
		if line, err := json.Marshal(stored.facts[key]); err == nil {
			all = append(all, line)
		}
	}
	return file.RewriteLines(path, all, 0o644)
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
	if err := checkSettles(rows, records); err != nil {
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

func checkSettles(rows []types.Job, records []types.Declaration) error {
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
	return errors.Join(errs...)
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
