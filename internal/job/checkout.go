package job

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/describe"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// LeaseMarkerName is the file that binds a lease to a checkout for a caller its host names
// no session or agent for. The environment cannot carry a lease into a hook, since a host
// runs its hooks with its own environment, so a file keyed by the checkout is the one
// channel such a caller's shell, the host's hook and the sandbox all read. Only the guard
// writes it (see [Store.Bind]), and only an identity-less caller reads it.
const LeaseMarkerName = "lease"

// markerKind is the user state directory's subdirectory the markers live under.
const markerKind = "checkouts"

// MarkerPath is where the checkout record for the checkout whose cache dir is
// loc.CacheDir lives, under loc.StateBase, or "" when either is empty.
//
// NOT in the cache dir. The sandbox grants a run write access to its workspace, and the
// cache dir sits inside it by default, so a marker there is one a confined run could
// rewrite to name a broader lease, or delete, and the next run in the checkout, sandbox
// and guard alike, would act on what it wrote. The sandbox grants the user state dir to
// nobody.
func (loc Location) MarkerPath() string {
	if loc.CacheDir == "" || loc.StateBase == "" {
		return ""
	}
	key, err := filepath.Abs(loc.CacheDir)
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(key); err == nil {
		key = resolved
	}
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(loc.StateBase, "magus", markerKind, hex.EncodeToString(sum[:12]), LeaseMarkerName)
}

// LeaseQuery is what a caller knows about itself when it asks which lease it acts under.
// Each field is empty when the caller has no such answer.
type LeaseQuery struct {
	// Flag is an explicit --lease the caller's wiring passed.
	Flag string
	// CallerJob is the job an identified caller's own record names (see [Store.Bound]).
	CallerJob string
	// CheckoutJob is the job the checkout record names. Only a caller with no identity
	// may pass it: an identified caller reading it would be graded as whichever
	// identity-less caller last took a job in the same checkout.
	CheckoutJob string
	// Claim is the lease the process says it acts under, from its BAGGAGE or from the
	// client an adopted run was forwarded from.
	Claim string
}

// Resolve returns the lease the caller acts under and which source answered, or "" and
// no source when none did. It is the one order the guard, the job store, doctor, the
// sandbox and the journal all grade by: an explicit flag, the caller's record, the
// checkout's record, and only then the claim.
//
// A record outranks the claim because the claim is the one answer a worker can rewrite
// from its own shell: a worker bound to one job that could export another's id would be
// graded against that job's write paths. When a lower source named a different lease than
// the one that answered, the source is [types.LeaseSourceContested], so a client can say
// something was overruled.
func (q LeaseQuery) Resolve() (string, types.LeaseSource) {
	sources := []struct {
		lease string
		from  types.LeaseSource
	}{
		{q.Flag, types.LeaseSourceFlag},
		{q.CallerJob, types.LeaseSourceAgent},
		{q.CheckoutJob, types.LeaseSourceMarker},
		{q.Claim, types.LeaseSourceEnv},
	}
	for i, s := range sources {
		if s.lease == "" {
			continue
		}
		for _, lower := range sources[i+1:] {
			if lower.lease != "" && lower.lease != s.lease {
				return s.lease, types.LeaseSourceContested
			}
		}
		return s.lease, s.from
	}
	return "", ""
}

// tombstoneTag opens a tombstone record: "gone <job> <unix seconds>".
const tombstoneTag = "gone"

// Binding is what a caller's record holds.
type Binding struct {
	// Job is the job the record names, "" when there is no record or it does not read.
	Job string
	// Gone marks a tombstone: the sweep found Job's row or its checkout removed and ended the binding.
	// The caller is not unbound, and the guard refuses its work until it binds again.
	Gone bool
	// swept is when the tombstone was written, in unix seconds.
	swept int64
}

// readBinding reads one binding record: zero when it is absent, cannot be read, or holds
// neither a lease id nor a tombstone. Only the guard and the sweep write records, through
// a rename, so one that does not read was damaged from outside magus, and reading it as
// none grades the call exactly as a caller nobody bound.
func readBinding(path string) Binding {
	if path == "" {
		return Binding{}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Binding{}
	}
	line := strings.TrimSpace(string(raw))
	if types.ValidJobID(line) {
		return Binding{Job: line}
	}
	f := strings.Fields(line)
	if len(f) != 3 || f[0] != tombstoneTag || !types.ValidJobID(f[1]) {
		return Binding{}
	}
	swept, err := strconv.ParseInt(f[2], 10, 64)
	if err != nil {
		return Binding{}
	}
	return Binding{Job: f[1], Gone: true, swept: swept}
}

// readRecord is the job the record at path binds its caller to, "" for none or a tombstone.
func readRecord(path string) string {
	if b := readBinding(path); !b.Gone {
		return b.Job
	}
	return ""
}

// Caller is who a hook call comes from, as its host names it on every call: the host, the
// session, and the subagent inside that session. A root session has no Agent; a host
// that reports neither id leaves the caller identity-less.
type Caller struct {
	Host    string
	Session string
	Agent   string
}

// Identified reports whether the host named the caller at all.
func (c Caller) Identified() bool { return c.Session != "" || c.Agent != "" }

// agentRecordDir holds the identified callers' records, beside the jobs file.
const agentRecordDir = "agents"

// record names the file c's binding lives in: an identified caller's in the repository's
// state directory, keyed on exactly (host, session, agent), and an identity-less caller's
// under the checkout. "" when there is nowhere to key it.
//
// Hashed rather than sanitized: the ids are host-chosen strings that may hold anything,
// separators included, and a filename assembled from one is a filename the input picked.
func (s *Store) record(c Caller) string {
	if !c.Identified() {
		return s.place().MarkerPath()
	}
	if s.err != nil || s.path == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s", c.Host, c.Session, c.Agent)))
	return filepath.Join(filepath.Dir(s.path), agentRecordDir, hex.EncodeToString(sum[:12]))
}

// Bind records that caller c acts under job id, replacing whatever it held. An identified
// caller's record is the repository's and answers only for that exact (host, session,
// agent), from whichever checkout the call runs in; an identity-less caller's is this
// Store's checkout's.
//
// The repository's state directory rather than any checkout's cache dir: a worker in its
// own worktree has a cache dir of its own, so a record filed in the spawner's was one the
// worker's hook never found, and the sandbox grants a run its cache dir, so a record
// there was one a confined run could rewrite.
//
// Whether c may take id is the guard's judgment; this only records it.
func (s *Store) Bind(c Caller, id string) error {
	if !types.ValidJobID(id) {
		return fmt.Errorf("job: %q is not a lease id (letters, digits and -_./: only)", id)
	}
	path := s.record(c)
	if path == "" {
		return fmt.Errorf("job: bind: %w", cmp.Or(s.err, errors.New("no state dir or cache dir to key the binding by")))
	}
	// Replaced whole: the caller's own hooks may be reading it already.
	if err := file.ReplaceFile(path, []byte(id+"\n"), 0o600); err != nil {
		return fmt.Errorf("job: bind: %w", err)
	}
	return nil
}

// Release removes c's record if it still binds c to job id, so c is unbound from then on;
// a record naming any other job, a tombstone included, stays. It reports nothing: a
// record already gone or rebound is the outcome asked for.
//
// Only the guard calls it, when a caller ends its own job: a binding nothing released
// graded the caller under a row it had finished with.
func (s *Store) Release(c Caller, id string) {
	if path := s.record(c); path != "" {
		dropRecord(path, Binding{Job: id})
	}
}

// Bound is the job c's own record binds it to, "" when none does or the record is a
// tombstone. Exact: a subagent never reads its session's record, a session never reads a
// subagent's, and an identified caller never reads the checkout's.
func (s *Store) Bound(c Caller) string {
	return readRecord(s.record(c))
}

// Binding is c's own record as [Store.Bound] finds it, tombstone included.
func (s *Store) Binding(c Caller) Binding {
	return readBinding(s.record(c))
}

// sweepRecords turns into a tombstone each identified caller's record whose row is gone
// from rows, or whose job was taken in a checkout w proves gone, and removes each
// tombstone older than jobs.stale_after. A record naming a row never taken stays. Best
// effort: a read or write that fails leaves the record as it was.
//
// A caller that removes its own row is released by the guard before the sweep sees it, so
// a record left naming a missing row is one whose row somebody else removed.
//
// A tombstone rather than a removal: the worker may still be running from the removed
// checkout, and a caller with no record is graded as the orchestrator, which no lease
// bounds.
//
// Checkout records are not swept: MarkerPath keys one on a hash of its cache dir, and
// nothing maps the hash back to a checkout.
//
// A pass reads every record, one file per caller that ever bound, so it runs only when
// this read ended a row, a row the last pass saw is gone, or recordSweepEvery has passed
// since it (see [Store.recordsSwept]). A checkout removed under a row that is already
// over, and a record naming a row that never existed, wait for that interval.
func (s *Store) sweepRecords(w *sweeper, rows []types.Job, ended bool) {
	if s.err != nil || s.path == "" {
		return
	}
	if !ended && s.recordsSwept(w.now, rows) {
		return
	}
	dir := filepath.Join(filepath.Dir(s.path), agentRecordDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	defer s.writeRecordsSwept(w.now, rows)
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		b := readBinding(path)
		if b.Gone {
			if window, err := w.window(); err == nil && window > 0 && w.now-b.swept >= int64(window/time.Second) {
				dropRecord(path, b)
			}
			continue
		}
		i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == b.Job })
		if b.Job == "" || i >= 0 && !w.checkoutGone(rows[i].CheckoutRoot) {
			continue
		}
		// Replaced in place, never moved aside first: while no record stands the caller
		// reads as unbound, the opening a tombstone exists to close. A bind landing since
		// the read is overwritten, and the guard's refusal tells that worker to bind again.
		_ = file.ReplaceFile(path, fmt.Appendf(nil, "%s %s %d\n", tombstoneTag, b.Job, w.now), 0o600)
	}
}

// recordSweepEvery is the longest a full pass over the binding records waits once no row
// it saw has gone.
const recordSweepEvery = 10 * time.Minute

// recordsSweptFile logs the full passes over the binding records, one line per pass with
// when it ran and the rows it saw, beside jobs.jsonl, so the interval holds across
// processes. Only the last line is read.
const recordsSweptFile = "records-swept.jsonl"

type recordsPass struct {
	At   int64    `json:"at"`
	Rows []string `json:"rows"`
}

// recordsSweptLog keys every pass alike, so the last one is the log's one entry.
func (s *Store) recordsSweptLog() file.JSONL[recordsPass] {
	return file.JSONL[recordsPass]{Path: filepath.Join(filepath.Dir(s.path), recordsSweptFile),
		Key: func(recordsPass) string { return "" }}
}

// recordsSwept reports whether a pass within recordSweepEvery of now saw only rows that
// are still in rows. A history that cannot be read reads as no pass, which costs one early
// pass.
func (s *Store) recordsSwept(now int64, rows []types.Job) bool {
	passes, err := s.recordsSweptLog().Load()
	if err != nil || len(passes) == 0 {
		return false
	}
	last := passes[0]
	if since := now - last.At; since < 0 || since >= int64(recordSweepEvery/time.Second) {
		return false
	}
	present := make(map[string]bool, len(rows))
	for _, r := range rows {
		present[r.ID] = true
	}
	return !slices.ContainsFunc(last.Rows, func(id string) bool { return !present[id] })
}

func (s *Store) writeRecordsSwept(now int64, rows []types.Job) {
	pass := recordsPass{At: now, Rows: make([]string, len(rows))}
	for i, r := range rows {
		pass.Rows[i] = r.ID
	}
	_ = s.recordsSweptLog().Update(context.Background(), func([]recordsPass) []recordsPass { return []recordsPass{pass} })
}

// dropRecord removes the record at path if it still holds b.
//
// Moved aside before it is read rather than re-read and removed: [Store.Bind] may rebind
// the caller in between, and deleting that record would leave a bound worker unbound. A
// record found to hold anything else is linked back, unless a newer bind already took its
// place.
func dropRecord(path string, b Binding) {
	aside := fmt.Sprintf("%s.%s.sweep", path, rand.Text())
	if os.Rename(path, aside) != nil {
		return
	}
	if readBinding(aside) != b {
		_ = os.Link(aside, path)
	}
	_ = os.Remove(aside)
}

// ActingLease resolves the lease for a process that is not a hook, and so knows no session
// and no subagent: this checkout's record, else claim. claim is the lease the process says
// it acts under: trail.LeaseFromEnv for a process reading its own environment, or the
// lease an adopted run was forwarded with. See [LeaseQuery.Resolve].
func ActingLease(loc Location, claim string) (string, types.LeaseSource) {
	return LeaseQuery{CheckoutJob: readRecord(loc.MarkerPath()), Claim: claim}.Resolve()
}

// HeldIn returns the rows with write paths whose holder recorded its base in the checkout
// at root and is still [Editing]: the workers already working there, whoever they are.
func HeldIn(rows []types.Job, root string) []types.Job {
	if root == "" {
		return nil
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	var out []types.Job
	for _, row := range rows {
		// A row that is over holds nothing, or a checkout would stay refused over every job
		// that ever passed in it.
		if row.CheckoutRoot == abs && Editing(row) && len(row.WritePaths) > 0 {
			out = append(out, row)
		}
	}
	return out
}

// Editing reports whether row's holder may still be writing its paths: declared or
// running. An exited row stays live until it is graded, but its holder returned, so its
// write paths no longer collide with another fork's.
func Editing(row types.Job) bool {
	return row.State == types.StateDeclared || row.State == types.StateRunning
}

// heldHere is [HeldIn] without the job being forked.
func heldHere(rows []types.Job, root, forking string) []types.Job {
	return slices.DeleteFunc(HeldIn(rows, root), func(row types.Job) bool { return row.ID == forking })
}

// sharedCheckoutProblems is every write path of row that covers a file the WORKSPACE has to
// load while another live job with write paths is already working in this checkout, as
// CheckoutConfigHeld, each reported against the first load file it covers.
//
// THE ONE COLLISION A WORKER CANNOT SEE. Two jobs overlapping costs the two workers
// involved a conflict they can read; a half-saved magusfile costs every worker in the
// checkout `magus run` itself, including the tests they are being graded on, and the
// worker that caused it is doing exactly what its declaration told it to. It is refused
// rather than advised for that reason, and the fix it names is a worktree rather than a
// narrower declaration: the file has one owner, so the only way two jobs can both be right
// is for one of them to be somewhere else.
//
// Narrow by construction. It says nothing about write paths that merely overlap (that is
// recorded, see WriteProof), nothing about a checkout holding no other live job, and
// nothing about a job whose write paths cover no load file. Refusing more than magus can
// prove is how a guard becomes something to route around.
func sharedCheckoutProblems(store *Store, rows []types.Job, row types.Job) types.JobProblems {
	if store == nil || len(row.WritePaths) == 0 {
		return nil
	}
	held := heldHere(rows, WorkingCheckout(store.root, row), row.ID)
	if len(held) == 0 {
		return nil
	}
	var (
		ps       types.JobProblems
		unused   []string
		measured bool
	)
	holder := held[0]
	// No caller passes a context here yet; the footprint read is the only thing that takes one.
	ctx := context.Background()
	reported := map[string]bool{}
	// Read only once something else holds the checkout: the walk touches the tree, and a
	// fork into an unshared checkout is the common case and must stay free.
	for _, load := range describe.WorkspaceLoadFiles(store.root) {
		declared, covered := writePathCovering(row.WritePaths, load)
		if !covered || reported[declared] {
			continue
		}
		reported[declared] = true
		if !measured {
			unused, measured = unusedWritePaths(ctx, store, rows, holder), true
		}
		store.requestRelease(ctx, holder.ID, load, []string{declared}, unused)
		ps = append(ps, types.JobProblem{
			Code:  types.CheckoutConfigHeld,
			Field: fmt.Sprintf("write_paths[%d]", slices.Index(row.WritePaths, declared)),
			Message: fmt.Sprintf("%s declares %q, which covers %s, and %s is live in this checkout (%s; %s, updated %s ago):"+
				" a magusfile, magus.yaml or spell source that is half-saved stops the whole workspace loading,"+
				" so every job here loses `magus run` until it lands, not just this one. %s",
				row.ID, declared, load, holder.ID, store.root, holder.State, updatedAgo(holder), describeUnused(holder.ID, unused)),
			Fix: fmt.Sprintf("fork %s into its own worktree with `%s`, fork it with `--depends-on %s` to wait for the holder,"+
				" or drop %q from write_paths (`%s` names what else it covers)%s",
				row.ID, hint.JobFork.With(row.ID, "--checkout", "<dir>"), holder.ID, declared, hint.DescribeFile.With(load),
				narrowAdvice(holder.ID, unused)),
		})
	}
	return ps
}

// directoryWritePathProblems is every write path of row that names an existing directory,
// as WritePathIsDirectory, project roots and the workspace root included. A write path names
// files: a directory claims every file beneath it, so the job overlaps every job that edits
// anything there, and the overlap report fills with pairs that share no file. Every fork door
// runs it, and there is no waiver.
//
// Declarable: a file, and a path that does not exist yet, which the job creates. A glob
// whose last segments are only wildcards (`docs/**`, `docs/*`, `docs/**/*`) names everything
// under its directory, so it is judged as that directory, each match when the directory
// part is itself a pattern. Any other glob (`docs/*.md`) names files and passes.
//
// A directory whose every file the workspace declares an output (the role `magus describe
// file` reports) passes too: nobody edits a generated tree by hand, so claiming it overlaps
// no one. One other file keeps the refusal and the message names it. The workspace root
// never passes.
//
// A nil store, or one with no workspace root, has no tree to read and finds nothing.
func directoryWritePathProblems(store *Store, row types.Job) types.JobProblems {
	if store == nil || store.root == "" {
		return nil
	}
	var ps types.JobProblems
	for i, decl := range row.WritePaths {
		for _, dir := range claimedDirs(store.root, decl) {
			stray, generated := nonOutputFile(store, dir)
			if generated {
				continue
			}
			named := fmt.Sprintf("%q", decl)
			if dir != path.Clean(strings.TrimSpace(decl)) {
				named = fmt.Sprintf("%q (matching the directory %q)", decl, dir)
			}
			why := ""
			if stray != "" {
				why = fmt.Sprintf("; %s is not a declared output", stray)
			}
			switch {
			case dir == ".":
				named += ", the workspace root"
			case describe.IsProjectRoot(store.root, dir):
				named += fmt.Sprintf(", the root of project %q", dir)
			default:
				named += fmt.Sprintf(", inside project %q", enclosingProject(store.root, dir))
			}
			ps = append(ps, types.JobProblem{
				Code:    types.WritePathIsDirectory,
				Field:   fmt.Sprintf("write_paths[%d]", i),
				Message: fmt.Sprintf("%s is a directory, and a write path names files: a directory claims every file under it, so the job would overlap every job editing anything there%s", named, why),
				Fix:     "list the files the job will edit in its place, and name each file it creates by its path",
			})
		}
	}
	return ps
}

// nonOutputFile reports whether every file under dir is a declared output. When it is not,
// stray is the first file that is not, empty for a directory with no file at all, which
// shows nothing generated. The workspace root is never generated.
func nonOutputFile(store *Store, dir string) (stray string, generated bool) {
	if dir == "." || store.workspace == nil {
		return "", false
	}
	var outputs []types.Glob
	for _, p := range store.workspace.Projects {
		for _, g := range p.AllOutputs() {
			outputs = append(outputs, g.Root(p.Path))
		}
	}
	var (
		found bool
		files int
	)
	tree := os.DirFS(store.root)
	_ = fs.WalkDir(tree, dir, func(rel string, d fs.DirEntry, err error) error {
		// An unreadable entry cannot be shown generated, so it counts as the stray.
		if err == nil && d.IsDir() {
			return nil
		}
		files++
		if err != nil || !types.MatchGlobs(outputs, rel) {
			stray, found = rel, true
			return fs.SkipAll
		}
		return nil
	})
	if found {
		return stray, false
	}
	return "", files > 0
}

// claimedDirs returns the existing directories, workspace-relative, that the write path
// decl claims whole. See [directoryWritePathProblems] for how a glob is read.
func claimedDirs(root, decl string) []string {
	decl, _ = types.SplitClaim(decl)
	if decl == "" {
		return nil
	}
	segs := strings.Split(path.Clean(decl), "/")
	base := len(segs)
	for base > 0 && strings.Trim(segs[base-1], "*") == "" {
		base--
	}
	dir := path.Join(segs[:base]...)
	if dir == "" {
		dir = "."
	}
	isGlob := strings.ContainsAny(dir, "*?[{")
	if base == len(segs) && isGlob {
		return nil
	}
	if !isGlob {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir))); err == nil && info.IsDir() {
			return []string{dir}
		}
		return nil
	}
	matches, err := doublestar.Glob(os.DirFS(root), dir)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, m := range matches {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(m))); err == nil && info.IsDir() {
			dirs = append(dirs, m)
		}
	}
	return dirs
}

// enclosingProject returns the nearest project root at or above dir, "." when none is.
func enclosingProject(root, dir string) string {
	for dir != "." && dir != "/" {
		dir = path.Dir(dir)
		if describe.IsProjectRoot(root, dir) {
			return dir
		}
	}
	return "."
}

// writePathCovering reports which declared write path covers rel, using the same reading the
// guard gives a write: a declaration covers the subtree beneath what it names.
func writePathCovering(paths []string, rel string) (string, bool) {
	for _, decl := range paths {
		if covers(decl, rel) {
			return decl, true
		}
	}
	return "", false
}

// WriteProof grades row's write paths against every other job in rows still editing with
// write paths, that is working in this checkout or not yet taken by any. An untaken job
// counts because only `job exec` binds one: two jobs forked back to back are both
// untaken, and proving the second "alone" would be a claim about a plan nobody has
// started. See [types.JobWriteProof] for why the three answers are distinct. A pattern
// does not reach a file its row released (see [NarrowConfigHold]).
//
// A nil Store, or one with no root, answers alone: a caller with no checkout knows of no
// other holder.
func (s *Store) WriteProof(rows []types.Job, row types.Job) types.JobWriteProof {
	if s == nil || s.root == "" {
		return types.WriteProofAlone
	}
	here, err := filepath.Abs(WorkingCheckout(s.root, row))
	if err != nil {
		return types.WriteProofAlone
	}
	peers := slices.DeleteFunc(slices.Clone(rows), func(peer types.Job) bool {
		there := WorkingCheckout(peer.CheckoutRoot, peer)
		return peer.ID == row.ID || !Editing(peer) || len(peer.HardPaths()) == 0 ||
			there != "" && !samePath(there, here)
	})
	if len(peers) == 0 {
		return types.WriteProofAlone
	}
	for _, peer := range peers {
		for _, mine := range row.HardPaths() {
			for _, theirs := range peer.HardPaths() {
				if types.PathsIntersect(mine, theirs) && !releasedReach(peer, theirs, mine) && !releasedReach(row, mine, theirs) {
					return types.WriteProofOverlapping
				}
			}
		}
	}
	return types.WriteProofDisjoint
}

// HardenWritten is rows with each live holder's soft paths its diff has touched made hard,
// and the change recorded on the row. Read it before asking who owns a path: [Store.List]
// returns a soft path its holder already wrote as soft. A footprint that cannot be read
// hardens nothing; a record that fails leaves the row soft in the store for the next
// reader to record again. A nil store, or one with no root, returns rows unchanged.
func HardenWritten(ctx context.Context, store *Store, rows []types.Job) []types.Job {
	if store == nil || store.root == "" {
		return rows
	}
	ctx = withFootprintCache(ctx, store.footprintCache())
	var driver types.VCSDriver
	out := slices.Clone(rows)
	for i, row := range rows {
		if len(row.SoftPaths) == 0 || !Editing(row) || row.CheckoutRoot == "" {
			continue
		}
		if driver == nil {
			driver, _ = resolveDriver(ctx, store.root)
		}
		fp := readFootprint(ctx, driver, rows, row.ID)
		if !fp.known {
			continue
		}
		written := slices.DeleteFunc(slices.Clone(row.SoftPaths), func(entry string) bool {
			return !slices.ContainsFunc(fp.regions, func(r types.RegionChange) bool { return usesEntry(entry, r.Location()) })
		})
		if len(written) == 0 {
			continue
		}
		harden := func(soft []string) []string {
			return slices.DeleteFunc(slices.Clone(soft), func(p string) bool { return slices.Contains(written, p) })
		}
		out[i].SoftPaths = harden(row.SoftPaths)
		if store.dryRun {
			continue
		}
		_, _ = store.mutate(ctx, row.ID, asObservation, func(cur *types.Job, exists bool, _ int64) error {
			if exists {
				cur.SoftPaths = harden(cur.SoftPaths)
			}
			return nil
		})
	}
	return out
}

// claiming is row as the checks that read WritePaths for a collision see it: soft paths out.
func claiming(row types.Job) types.Job {
	row.WritePaths = row.HardPaths()
	return row
}

func claimingRows(rows []types.Job) []types.Job {
	out := make([]types.Job, len(rows))
	for i, r := range rows {
		out[i] = claiming(r)
	}
	return out
}

// IntegrationWritePaths is the write set of an integration job: every file one of branches
// changed since it diverged from checkpoint's revision, sorted, once each. A merge writes
// exactly those, so the set is computed and never declared. An empty checkpoint reads the
// revision root has checked out, the one a --checkout fork starts from, and base says which
// revision answered. A branch the version control cannot resolve is an error, never an
// empty set: a merge of nothing would read the same as a merge that changed nothing.
func IntegrationWritePaths(ctx context.Context, root, checkpoint string, branches []string) (paths []string, base string, err error) {
	driver, ranges, err := rangeDriver(ctx, root)
	if err != nil {
		return nil, "", err
	}
	if base = checkpointRevision(checkpoint); base == "" {
		meta, err := driver.Metadata(ctx, root)
		if err != nil {
			return nil, "", fmt.Errorf("the checkout's revision: %w", err)
		}
		base = meta.ID
	}
	seen := map[string]bool{}
	for _, branch := range branches {
		changed, err := ranges.RangeFiles(ctx, root, base, branch, nil)
		if err != nil {
			return nil, "", fmt.Errorf("branch %s: %w", branch, err)
		}
		for _, p := range changed {
			if p = path.Clean(filepath.ToSlash(strings.TrimSpace(p))); p != "." && !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
		}
	}
	slices.Sort(paths)
	return paths, base, nil
}

// rangeDriver is the version control answering at root as the range reader it must be, or
// why it cannot read one.
func rangeDriver(ctx context.Context, root string) (types.VCSDriver, types.RangeReporter, error) {
	driver, why := resolveDriver(ctx, root)
	if driver == nil {
		return nil, nil, errors.New(why)
	}
	ranges, ok := driver.(types.RangeReporter)
	if !ok {
		return nil, nil, fmt.Errorf("%s reads no revision range, so no branch can be compared with the checkout", driver.Name())
	}
	return driver, ranges, nil
}

// IntegrationOverlaps names, for an integration row, each live job holding one of its write
// paths, one note per holder and file. Notes and never refusals: the branches are committed
// work, and a merge settles their conflicts where the work integrates (ADR 0010), so a lease
// elsewhere is something to tell its holder, not a reason to keep the work apart. The row's
// ancestors are left out, since a parent holds its children's paths by design.
func IntegrationOverlaps(rows []types.Job, row types.Job) []string {
	if !row.Integrating() {
		return nil
	}
	ancestors := types.JobAncestors(rows, row.ID)
	var notes []string
	for _, holder := range rows {
		if holder.ID == row.ID || !Editing(holder) ||
			slices.ContainsFunc(ancestors, func(a types.Job) bool { return a.ID == holder.ID }) {
			continue
		}
		for _, mine := range row.WritePaths {
			i := slices.IndexFunc(holder.WritePaths, func(theirs string) bool { return types.PathsIntersect(mine, theirs) })
			if i < 0 {
				continue
			}
			theirs, kind := holder.WritePaths[i], "holds"
			if slices.Contains(holder.SoftPaths, theirs) {
				kind = "soft-claims"
			}
			notes = append(notes, fmt.Sprintf("%s (%s) %s %q, which the merge writes as %q: the merge settles it, so tell %s before it edits that file again",
				holder.ID, holder.State, kind, theirs, mine, holder.ID))
		}
	}
	return notes
}

// Unintegrated is a finished job whose branch holds commits the checkout has not got.
type Unintegrated struct {
	Job     string
	Branch  string
	Commits int
}

// UnintegratedBranches is every job at pass or exited whose branch holds commits the
// revision checked out at the store's root does not, in store order. A job's branch is the
// one its standing checkout has checked out, else a branch named for the job. Skipped as
// integrated:
//
//   - a branch any integration job names, whatever that job's state: a merge that failed
//     is retried by another integration job, not by counting the branch again;
//   - an integration job's own branch once the job passed;
//   - a branch whose changes are on the base by content (see [changesOnBase]), as a squash
//     merge leaves them with no commit of the branch an ancestor.
//
// An error means no range could be read at all: a gap read as zero would hide the backlog.
func (s *Store) UnintegratedBranches(ctx context.Context) ([]Unintegrated, error) {
	rows, err := s.List()
	if err != nil {
		return nil, err
	}
	var finished []types.Job
	integrating := map[string]bool{}
	for _, row := range rows {
		for _, b := range row.Integrates {
			integrating[b] = true
		}
		switch {
		case row.Integrating() && row.State == types.StatePass:
		case row.State == types.StatePass || row.State == types.StateExited:
			finished = append(finished, row)
		}
	}
	if len(finished) == 0 {
		return nil, nil
	}
	driver, ranges, err := rangeDriver(ctx, s.root)
	if err != nil {
		return nil, err
	}
	meta, err := driver.Metadata(ctx, s.root)
	if err != nil {
		return nil, fmt.Errorf("the checkout's revision: %w", err)
	}
	var out []Unintegrated
	for _, row := range finished {
		branch := jobBranch(ctx, driver, row)
		if integrating[branch] {
			continue
		}
		// One read per job: a branch that does not resolve errs, and nothing says where its work is.
		commits, err := ranges.RangeCommits(ctx, s.root, meta.ID, branch, nil)
		if err != nil || len(commits) == 0 || changesOnBase(ctx, driver, ranges, s.root, meta.ID, branch) {
			continue
		}
		out = append(out, Unintegrated{Job: row.ID, Branch: branch, Commits: len(commits)})
	}
	return out, nil
}

// changesOnBase reports whether every file branch changed since it diverged from base reads
// the same at branch as at base. Content, not ancestry or patch identity: a squash merge of
// several branches holds no commit of any of them and no patch equal to one. A file either
// side cannot read, a deletion among them, leaves it unproven.
func changesOnBase(ctx context.Context, driver types.VCSDriver, ranges types.RangeReporter, root, base, branch string) bool {
	tip, err := driver.FindCommit(ctx, root, branch)
	if err != nil {
		return false
	}
	paths, err := ranges.RangeFiles(ctx, root, base, tip.ID, nil)
	if err != nil || len(paths) == 0 {
		return false
	}
	for _, changed := range paths {
		p := path.Clean(filepath.ToSlash(strings.TrimSpace(changed)))
		ours, err := driver.ReadFileAt(ctx, root, tip.ID, p)
		if err != nil {
			return false
		}
		if theirs, err := driver.ReadFileAt(ctx, root, base, p); err != nil || theirs != ours {
			return false
		}
	}
	return true
}

// IntegrationAdvice is what a fork of anything but an integration job says about the
// finished branches waiting off the base: "" below limits.IntegrateAdviseAt, and from there
// a line naming each branch and the integration fork that takes them. refuse is true from
// limits.IntegrateRefuseAt on, until an integration job names the branches. An unset
// threshold, as in a config nothing loaded, never fires, like every unset jobs limit.
func (s *Store) IntegrationAdvice(ctx context.Context, limits config.Jobs) (advice string, refuse bool, err error) {
	if limits.IntegrateAdviseAt <= 0 {
		return "", false, nil
	}
	waiting, err := s.UnintegratedBranches(ctx)
	if err != nil || len(waiting) < limits.IntegrateAdviseAt {
		return "", false, err
	}
	branches := make([]string, len(waiting))
	named := make([]string, len(waiting))
	for i, w := range waiting {
		branches[i] = w.Branch
		named[i] = fmt.Sprintf("%s (job %s, %d commit(s))", w.Branch, w.Job, w.Commits)
	}
	record, _ := json.Marshal(map[string]any{
		"schema_version": types.JobSchemaVersion,
		"id":             fmt.Sprintf("integrate-%s-branches", strconv.Itoa(len(waiting))),
		"criteria":       "merge each finished branch with --no-ff, resolve, regenerate, and pass the check",
		"integrates":     branches,
		"check":          map[string]string{"target": "<target>", "project": "<project>"},
	})
	advice = fmt.Sprintf("%d finished branch(es) wait off the base (jobs.integrate_advise_at %d, jobs.integrate_refuse_at %d): %s. Integrate them first: echo '%s' | %s",
		len(waiting), limits.IntegrateAdviseAt, limits.IntegrateRefuseAt, strings.Join(named, ", "), record,
		hint.JobFork.With("--stdin", "--checkout", "<dir>"))
	return advice, limits.IntegrateRefuseAt > 0 && len(waiting) >= limits.IntegrateRefuseAt, nil
}

// jobBranch is the branch row's work is on: the one its standing checkout has checked out,
// else the branch named row.ID, which the range read then finds or not.
func jobBranch(ctx context.Context, driver types.VCSDriver, row types.Job) string {
	if row.Worktree.Standing() {
		if ref, err := driver.Ref(ctx, row.Worktree.Root); err == nil && ref != "" {
			return ref
		}
	}
	return row.ID
}
