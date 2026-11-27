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
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/egladman/magus/internal/describe"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
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
// the one that answered, the source is [job.LeaseSourceContested], so a client can say
// something was overruled.
func (q LeaseQuery) Resolve() (string, job.LeaseSource) {
	sources := []struct {
		lease string
		from  job.LeaseSource
	}{
		{q.Flag, job.LeaseSourceFlag},
		{q.CallerJob, job.LeaseSourceAgent},
		{q.CheckoutJob, job.LeaseSourceMarker},
		{q.Claim, job.LeaseSourceEnv},
	}
	for i, s := range sources {
		if s.lease == "" {
			continue
		}
		for _, lower := range sources[i+1:] {
			if lower.lease != "" && lower.lease != s.lease {
				return s.lease, job.LeaseSourceContested
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
	if job.ValidID(line) {
		return Binding{Job: line}
	}
	f := strings.Fields(line)
	if len(f) != 3 || f[0] != tombstoneTag || !job.ValidID(f[1]) {
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
	if !job.ValidID(id) {
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
func (s *Store) sweepRecords(ctx context.Context, w *sweeper, rows []job.Job, ended bool) {
	if s.err != nil || s.path == "" {
		return
	}
	if !ended && s.recordsSwept(ctx, w.now, rows) {
		return
	}
	dir := filepath.Join(filepath.Dir(s.path), agentRecordDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	defer s.writeRecordsSwept(ctx, w.now, rows)
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		b := readBinding(path)
		if b.Gone {
			if window, err := w.window(); err == nil && window > 0 && w.now-b.swept >= int64(window/time.Second) {
				dropRecord(path, b)
			}
			continue
		}
		i := slices.IndexFunc(rows, func(r job.Job) bool { return r.ID == b.Job })
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
func (s *Store) recordsSweptLog() (file.JSONL[recordsPass], error) {
	return file.NewJSONL(codec.JSONLConfig[recordsPass]{Log: slog.With(attr.Component("magus")), Path: filepath.Join(filepath.Dir(s.path), recordsSweptFile),
		Perm: 0o644, Key: func(recordsPass) string { return "" }})
}

// recordsSwept reports whether a pass within recordSweepEvery of now saw only rows that
// are still in rows. A history that cannot be read reads as no pass, which costs one early
// pass.
func (s *Store) recordsSwept(ctx context.Context, now int64, rows []job.Job) bool {
	log, err := s.recordsSweptLog()
	if err != nil {
		slog.With(attr.Component("magus")).WarnContext(ctx, "records-swept history not opened", attr.Error(err))
		return false
	}
	passes, err := log.Load()
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

func (s *Store) writeRecordsSwept(ctx context.Context, now int64, rows []job.Job) {
	pass := recordsPass{At: now, Rows: make([]string, len(rows))}
	for i, r := range rows {
		pass.Rows[i] = r.ID
	}
	log, err := s.recordsSweptLog()
	if err != nil {
		slog.With(attr.Component("magus")).WarnContext(ctx, "records-swept history not opened", attr.Error(err))
		return
	}
	_ = log.Update(context.Background(), func([]recordsPass) []recordsPass { return []recordsPass{pass} })
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
func ActingLease(loc Location, claim string) (string, job.LeaseSource) {
	return LeaseQuery{CheckoutJob: readRecord(loc.MarkerPath()), Claim: claim}.Resolve()
}

// HeldIn returns the rows with write paths whose holder recorded its base in the checkout
// at root and is still [Editing]: the workers already working there, whoever they are.
func HeldIn(rows []job.Job, root string) []job.Job {
	if root == "" {
		return nil
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	var out []job.Job
	for _, row := range rows {
		// A row that is over holds nothing, or a checkout would stay refused over every job
		// that ever passed in it.
		if row.CheckoutRoot == abs && Editing(row) && len(row.Paths.Write) > 0 {
			out = append(out, row)
		}
	}
	return out
}

// Editing reports whether row's holder may still be writing its paths: declared or
// running. An exited row stays live until it is graded, but its holder returned, so its
// write paths no longer collide with another fork's.
func Editing(row job.Job) bool {
	return row.State == job.StateDeclared || row.State == job.StateRunning
}

// heldHere is [HeldIn] without the job being forked.
func heldHere(rows []job.Job, root, forking string) []job.Job {
	return slices.DeleteFunc(HeldIn(rows, root), func(row job.Job) bool { return row.ID == forking })
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
func sharedCheckoutProblems(store *Store, rows []job.Job, row job.Job) types.JobProblems {
	if store == nil || len(row.Paths.Write) == 0 {
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
	defer store.phase("fork shared-checkout read", forkTreeBound, func() string {
		return fmt.Sprintf("walking the checkout's load files for %d write paths while %s holds it", len(row.Paths.Write), holder.ID)
	})()
	// No caller passes a context here yet; the footprint read is the only thing that takes one.
	ctx := context.Background()
	reported := map[string]bool{}
	// Read only once something else holds the checkout: the walk touches the tree, and a
	// fork into an unshared checkout is the common case and must stay free.
	for _, load := range describe.WorkspaceLoadFiles(store.root) {
		declared, covered := writePathCovering(row.Paths.Write, load)
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
			Field: fmt.Sprintf("paths.write[%d]", slices.Index(row.Paths.Write, declared)),
			Message: fmt.Sprintf("%s declares %q, which covers %s, and %s is live in this checkout (%s; %s, updated %s ago):"+
				" a magusfile, magus.yaml or spell source that is half-saved stops the whole workspace loading,"+
				" so every job here loses `magus run` until it lands, not just this one. %s",
				row.ID, declared, load, holder.ID, store.root, holder.State, updatedAgo(holder), describeUnused(holder.ID, unused)),
			Hint: fmt.Sprintf("fork %s into its own worktree with `%s`, fork it with `--depends-on %s` to wait for the holder,"+
				" or drop %q from paths.write (`%s` names what else it covers)%s",
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
// runs it, and there is no deviation.
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
func directoryWritePathProblems(store *Store, row job.Job) types.JobProblems {
	if store == nil || store.root == "" {
		return nil
	}
	var ps types.JobProblems
	defer store.phase("fork write-path read", forkTreeBound, func() string {
		return fmt.Sprintf("listing the directories %d write paths claim in the shared checkout", len(row.Paths.Write))
	})()
	for i, decl := range row.Paths.Write {
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
				Field:   fmt.Sprintf("paths.write[%d]", i),
				Message: fmt.Sprintf("%s is a directory, and a write path names files: a directory claims every file under it, so the job would overlap every job editing anything there%s", named, why),
				Hint:    "list the files the job will edit in its place, and name each file it creates by its path",
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
	decl, _ = job.SplitClaim(decl)
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
		if job.Covers(decl, rel) {
			return decl, true
		}
	}
	return "", false
}

// WriteProof grades row's write paths against every other job in rows still editing with
// write paths, that is working in this checkout or not yet taken by any. An untaken job
// counts because only `job exec` binds one: two jobs forked back to back are both
// untaken, and proving the second "alone" would be a claim about a plan nobody has
// started. See [job.WriteProof] for why the three answers are distinct. A pattern
// does not reach a file its row released (see [NarrowConfigHold]).
//
// A nil Store, or one with no root, answers alone: a caller with no checkout knows of no
// other holder.
func (s *Store) WriteProof(rows []job.Job, row job.Job) job.WriteProof {
	if s == nil || s.root == "" {
		return job.WriteProofAlone
	}
	here, err := filepath.Abs(WorkingCheckout(s.root, row))
	if err != nil {
		return job.WriteProofAlone
	}
	peers := slices.DeleteFunc(slices.Clone(rows), func(peer job.Job) bool {
		there := WorkingCheckout(peer.CheckoutRoot, peer)
		return peer.ID == row.ID || !Editing(peer) || len(peer.HardPaths()) == 0 ||
			there != "" && !samePath(there, here)
	})
	if len(peers) == 0 {
		return job.WriteProofAlone
	}
	for _, peer := range peers {
		for _, mine := range row.HardPaths() {
			for _, theirs := range peer.HardPaths() {
				if job.PathsIntersect(mine, theirs) && !job.ReleasedReach(peer, theirs, mine) && !job.ReleasedReach(row, mine, theirs) {
					return job.WriteProofOverlapping
				}
			}
		}
	}
	return job.WriteProofDisjoint
}

// HardenWritten is rows with each live holder's soft paths its diff has touched made hard,
// and the change recorded on the row. Read it before asking who owns a path: [Store.List]
// returns a soft path its holder already wrote as soft. A footprint that cannot be read
// hardens nothing; a record that fails leaves the row soft in the store for the next
// reader to record again. A nil store, or one with no root, returns rows unchanged.
func HardenWritten(ctx context.Context, store *Store, rows []job.Job) []job.Job {
	if store == nil || store.root == "" {
		return rows
	}
	ctx = withFootprintCache(ctx, store.footprintCache())
	defer store.phase("fork footprint grading", forkFootprintBound, func() string {
		return fmt.Sprintf("reading the diff of each of %d live rows with soft paths", len(rows))
	})()
	var driver types.VCSDriver
	out := slices.Clone(rows)
	for i, row := range rows {
		if len(row.Paths.Soft) == 0 || !Editing(row) || row.CheckoutRoot == "" {
			continue
		}
		if driver == nil {
			driver, _ = resolveDriver(ctx, store.root)
		}
		fp := readFootprint(ctx, driver, rows, row.ID)
		if !fp.known {
			continue
		}
		used := indexRegions(fp.regions)
		written := map[string]bool{}
		for _, entry := range row.Paths.Soft {
			if used.uses(entry) {
				written[entry] = true
			}
		}
		if len(written) == 0 {
			continue
		}
		harden := func(soft []string) []string {
			return slices.DeleteFunc(slices.Clone(soft), func(p string) bool { return written[p] })
		}
		out[i].Paths.Soft = harden(row.Paths.Soft)
		if store.dryRun {
			continue
		}
		_, _ = store.Mutate(ctx, row.ID, job.AsObservation, func(cur *job.Job, exists bool, _ int64) error {
			if exists {
				cur.Paths.Soft = harden(cur.Paths.Soft)
			}
			return nil
		})
	}
	return out
}

// claiming is row as the checks that read WritePaths for a collision see it: soft paths out.
func claiming(row job.Job) job.Job {
	row.Paths.Write = row.HardPaths()
	return row
}

func claimingRows(rows []job.Job) []job.Job {
	out := make([]job.Job, len(rows))
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
	driver, why := resolveDriver(ctx, root)
	if driver == nil {
		return nil, "", errors.New(why)
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
		changed, err := driver.RangeFiles(ctx, root, base, branch, nil)
		if err != nil {
			return nil, "", fmt.Errorf("branch %s: %w", branch, err)
		}
		for _, p := range changed {
			if p = job.CleanPath(p); p != "." && !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
		}
	}
	slices.Sort(paths)
	return paths, base, nil
}

// IntegrationOutputs is the write set of an integration job over paths, the
// files its branches changed: those plus every output ws declares, each of
// them soft. Regenerating them is that job's work, and a soft claim overlaps
// no sibling.
func IntegrationOutputs(ws Workspace, paths []string) (write, soft []string) {
	seen := map[string]bool{}
	for _, p := range paths {
		seen[p] = true
	}
	for _, project := range ws.Projects {
		for _, refs := range project.TargetOutputs {
			for _, ref := range refs {
				if g := ref.Rooted(project.Path).Pattern; !seen[g] {
					seen[g] = true
					soft = append(soft, g)
				}
			}
		}
	}
	slices.Sort(soft)
	return slices.Concat(paths, soft), soft
}

// IntegrationOverlaps names, for an integration row, each live job holding one of its write
// paths, one note per holder and file. Notes and never refusals: the branches are committed
// work, and a merge settles their conflicts where the work integrates (ADR 0010), so a lease
// elsewhere is something to tell its holder, not a reason to keep the work apart. The row's
// ancestors are left out, since a parent holds its children's paths by design.
func IntegrationOverlaps(rows []job.Job, row job.Job) []string {
	if !row.Integrating() {
		return nil
	}
	ancestors := job.Ancestors(rows, row.ID)
	var notes []string
	for _, holder := range rows {
		if holder.ID == row.ID || !Editing(holder) ||
			slices.ContainsFunc(ancestors, func(a job.Job) bool { return a.ID == holder.ID }) {
			continue
		}
		for _, mine := range row.Paths.Write {
			i := slices.IndexFunc(holder.Paths.Write, func(theirs string) bool { return job.PathsIntersect(mine, theirs) })
			if i < 0 {
				continue
			}
			theirs, kind := holder.Paths.Write[i], "holds"
			if slices.Contains(holder.Paths.Soft, theirs) {
				kind = "soft-claims"
			}
			notes = append(notes, fmt.Sprintf("%s (%s) %s %q, which the merge writes as %q: the merge settles it, so tell %s before it edits that file again",
				holder.ID, holder.State, kind, theirs, mine, holder.ID))
		}
	}
	return notes
}

// Unintegrated is a finished job whose branch holds commits the checkout has not got, or
// whose range could not be read. Unread then names the failing read, and Commits is
// unknown rather than zero.
type Unintegrated struct {
	Job     string
	Branch  string
	Commits int
	Unread  string
}

// ownTree reports whether row belongs to a job tree forked from the checkout at root. A
// tree's checkout is the source its topmost job's worktree records, else the nearest
// descendant's that records one. A tree none of whose jobs made a checkout cannot be told
// from anyone's, so it counts for every checkout rather than hiding a backlog.
func ownTree(rows []job.Job, root string, row job.Job) bool {
	chain := append([]job.Job{row}, job.Ancestors(rows, row.ID)...)
	for i := len(chain) - 1; i >= 0; i-- {
		if wt := chain[i].Worktree; wt != nil && wt.Source != "" {
			return samePath(wt.Source, root)
		}
	}
	return true
}

// UnintegratedBranches is the work of this checkout's tree that no base holds
// ([placements]), in store order, placed with no change list. An error means
// some row went unread: a gap read as zero would hide the backlog.
func (s *Store) UnintegratedBranches(ctx context.Context) ([]Unintegrated, error) {
	rows, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	placed, _, err := placements(ctx, s.root, rows, OpenChanges{}, time.Now())
	if err != nil {
		return nil, err
	}
	var waiting []Unintegrated
	var unread error
	for _, p := range placed {
		switch {
		case p.Place == placeUnread:
			unread = errors.Join(unread, fmt.Errorf("job %s: %s", p.Job, p.Why))
		case p.Waiting():
			waiting = append(waiting, Unintegrated{Job: p.Job, Branch: p.Name(), Commits: p.Commits})
		}
	}
	if unread != nil {
		return nil, unread
	}
	return waiting, nil
}

// integrationBases are the revisions a finished branch's work may have reached: the
// remote default branch's, which a pull request merged on the forge reaches first, and
// the checkout's own. note says why the remote one is not among them.
func integrationBases(ctx context.Context, driver types.VCSDriver, root string) ([]string, string, error) {
	meta, err := driver.Metadata(ctx, root)
	if err != nil {
		return nil, "", fmt.Errorf("the checkout's revision: %w", err)
	}
	remote, why := remoteDefault(ctx, driver, root)
	switch {
	case why != "":
		return []string{meta.ID}, fmt.Sprintf("%s, so only the checkout's revision %s was compared", why, shortRevision(types.Commit{ID: meta.ID})), nil
	case remote == "" || remote == meta.ID:
		return []string{meta.ID}, "", nil
	}
	return []string{remote, meta.ID}, "", nil
}

// remoteDefault is the commit of the remote default branch the VCS names for root
// (git's origin/main), or "" and why it does not read. With no remote there is none
// to lag behind, so no why.
func remoteDefault(ctx context.Context, driver types.VCSDriver, root string) (string, string) {
	switch url, err := driver.RemoteURL(ctx, root, ""); {
	case errors.Is(err, types.ErrVCSUnsupported) || err == nil && url == "":
		return "", ""
	case err != nil:
		return "", fmt.Sprintf("the remote does not read here: %v", err)
	}
	res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
	switch {
	case err != nil:
		return "", fmt.Sprintf("the remote default branch is not named: %v", err)
	case res.Base == "":
		return "", "the remote default branch is not named"
	}
	at, err := driver.FindCommit(ctx, root, res.Base)
	if err != nil {
		return "", fmt.Sprintf("the remote default branch %s does not read here: %v", res.Base, err)
	}
	return at.ID, ""
}

// branchReadWorkers bounds the git processes one pass runs at once. Eight hides process
// start-up; 250 rows must not fork 250 gits together.
const branchReadWorkers = 8

// upstreamReporter is a driver that names the upstream each local branch
// tracks. Git does; a driver without upstreams judges the local branch alone.
type upstreamReporter interface {
	BranchUpstreams(ctx context.Context, root string) (map[string]string, error)
}

// branchUpstreams is driver's [upstreamReporter] answer, empty when it has none.
func branchUpstreams(ctx context.Context, driver types.VCSDriver, root string) (map[string]string, error) {
	reporter, ok := driver.(upstreamReporter)
	if !ok {
		return map[string]string{}, nil
	}
	return reporter.BranchUpstreams(ctx, root)
}

// reached reports whether base holds what tip changed since they split, by content
// alone, so a squash reads as reached. Merging tip may change nothing, or a commit
// since the split may read as tip. Else missing counts tip's commits.
func reached(ctx context.Context, driver types.VCSDriver, root, base, tip string) (bool, int, error) {
	paths, err := driver.RangeFiles(ctx, root, base, tip, nil)
	if err != nil {
		return false, 0, err
	}
	if len(paths) == 0 {
		return true, 0, nil
	}
	differ, err := vcs.DifferingPaths(ctx, driver, root, base, tip, paths)
	if err != nil {
		return false, 0, err
	}
	if len(differ) == 0 || mergeChangesNothing(ctx, driver, root, "", types.Commit{ID: tip}, types.Commit{ID: base}) {
		return true, 0, nil
	}
	// A squash the base edited since, with a conflict, reads as tip at the commit that landed it.
	landed, err := driver.RangeCommits(ctx, root, tip, base, paths)
	if err != nil {
		return false, 0, err
	}
	asked := make(map[string][]string, len(landed))
	for _, c := range landed {
		asked[c.ID] = paths
	}
	answers, err := vcs.DifferingPathsByRevision(ctx, driver, root, tip, asked)
	if err != nil {
		return false, 0, err
	}
	for _, off := range answers {
		if len(off) == 0 {
			return true, 0, nil
		}
	}
	commits, err := driver.RangeCommits(ctx, root, base, tip, nil)
	if err != nil {
		return false, 0, err
	}
	return false, max(1, len(commits)), nil
}

// forkFactsCall is what [ForkFacts] times itself as: the host call
// magus\workspace().forkFacts() as std names it.
const forkFactsCall = "job.forkFacts"

// CallClock starts timing one named call and returns its end. A reader such
// as the guard puts one on ctx to learn which call spent a judgment's time.
type CallClock func(name string) (end func())

type callClockKey struct{}

// WithCallClock returns ctx carrying clock for [timeCall].
func WithCallClock(ctx context.Context, clock CallClock) context.Context {
	return context.WithValue(ctx, callClockKey{}, clock)
}

// timeCall starts name on ctx's clock and returns its end, which the caller
// must run. With no clock on ctx it times nothing.
func timeCall(ctx context.Context, name string) (end func()) {
	clock, ok := ctx.Value(callClockKey{}).(CallClock)
	if !ok || clock == nil {
		return func() {}
	}
	return clock(name)
}

// FullTest is the run [ForkFacts] counts commits from. An empty Ref means the
// checkout's store holds no such run.
type FullTest struct {
	Ref      string
	Revision string
}

// ForkFacts reads what a fork leaves waiting at root, from rows and the
// change list open. It refuses nothing, and a side it cannot read says why
// rather than reading as zero. FinishedBranches holds one entry per row
// whose work waits. BranchesUnknown names the unread rows, which count toward
// no place. It times itself on ctx's [CallClock].
func ForkFacts(ctx context.Context, root string, rows []job.Job, last FullTest, open OpenChanges) job.ForkFacts {
	defer timeCall(ctx, forkFactsCall)()
	facts := job.ForkFacts{LastTestRef: last.Ref, LastTestRevision: last.Revision}
	placed, note, err := placements(ctx, root, rows, open, time.Now())
	if err != nil {
		facts.BranchesUnknown = err.Error()
	}
	facts = withPlaces(facts, placed)
	facts.BranchesUnknown = joinNotes(facts.BranchesUnknown, note)
	facts.CommitsSinceTest, facts.TestUnknown = commitsSince(ctx, root, last)
	return facts
}

// withPlaces records each waiting or proposed row of placed with its place
// and change. A reader counts one place per open change holding rows and one
// per row off every change, so stacking on an open change adds nothing.
func withPlaces(facts job.ForkFacts, placed []workPlace) job.ForkFacts {
	var branches, unread []string
	for _, p := range placed {
		switch {
		case p.Place == placeUnread:
			unread = append(unread, fmt.Sprintf("job %s: %s; %s", p.Job, p.Why, p.Fix))
		case p.Place == placeProposed || p.Waiting():
			facts.FinishedBranches = append(facts.FinishedBranches, job.FinishedBranch{Branch: p.Name(), Job: p.Job, Commits: p.Commits, Place: string(p.Place), Change: p.Change})
			if p.Place != placeProposed && p.Branch != "" {
				branches = append(branches, p.Branch)
			}
		}
	}
	if len(unread) > 0 {
		facts.BranchesUnknown = fmt.Sprintf("%d finished row(s) unread, so not counted: %s", len(unread), strings.Join(unread, "; "))
	}
	if len(branches) > 0 {
		record, _ := codec.Marshal(map[string]any{
			"api_version": job.APIVersion,
			"id":          fmt.Sprintf("integrate-%s-branches", strconv.Itoa(len(branches))),
			"agent":       map[string]any{"instructions": "merge each finished branch with --no-ff, resolve, regenerate, and pass the check", "worker": map[string]string{"type": "resumed"}},
			"integrates":  branches,
			"check":       map[string]string{"target": "<target>", "project": "<project>"},
		})
		facts.IntegrateRecord = string(record)
	}
	return facts
}

// finishedPlaces names each place branches wait in: a row off every change by
// itself, an open change once for all its rows, in first-seen order.
func finishedPlaces(branches []job.FinishedBranch) []string {
	var names []string
	slot := map[string]int{}
	jobs := map[string][]string{}
	for _, b := range branches {
		if b.Change == "" {
			names = append(names, b.String())
			continue
		}
		if _, seen := slot[b.Change]; !seen {
			slot[b.Change] = len(names)
			names = append(names, "")
		}
		jobs[b.Change] = append(jobs[b.Change], b.Job)
	}
	for id, i := range slot {
		names[i] = fmt.Sprintf("#%s (open change, job(s) %s)", id, strings.Join(jobs[id], ", "))
	}
	return names
}

func commitsSince(ctx context.Context, root string, last FullTest) (int, string) {
	if last.Ref == "" {
		return 0, "this checkout's output store holds no passed full test"
	}
	driver, why := resolveDriver(ctx, root)
	if driver == nil {
		return 0, why
	}
	meta, err := driver.Metadata(ctx, root)
	if err != nil {
		return 0, fmt.Sprintf("the checkout's revision: %s", err)
	}
	answers := readMissingCache(root)
	commits, known := answers.get(last.Revision, meta.ID)
	if !known {
		commits, err = driver.MissingCommits(ctx, root, last.Revision, meta.ID)
		if err != nil {
			return 0, fmt.Sprintf("the commits since %s: %s", last.Revision, err)
		}
		answers.put(last.Revision, meta.ID, commits)
		if err := answers.write(meta.ID); err != nil {
			slog.WarnContext(ctx, "missing-commit answers not stored", attr.Component("magus"), attr.Error(err))
		}
	}
	return commits, ""
}

// ForkFactsLine renders facts as the one line a fork prints.
func ForkFactsLine(facts job.ForkFacts) string {
	branches := "0 places hold work off the base"
	if len(facts.FinishedBranches) > 0 {
		places := finishedPlaces(facts.FinishedBranches)
		branches = fmt.Sprintf("%d place(s) hold work off the base: %s", len(places), strings.Join(places, ", "))
	}
	if facts.BranchesUnknown != "" {
		branches += "; unread: " + facts.BranchesUnknown
	}
	commits := "commits since the last full test: unknown, " + facts.TestUnknown
	if facts.TestUnknown == "" {
		commits = fmt.Sprintf("%d commit(s) since the last full test (%s at %s)", facts.CommitsSinceTest, facts.LastTestRef, facts.LastTestRevision)
	}
	line := fmt.Sprintf("%s; %s", branches, commits)
	if facts.IntegrateRecord != "" {
		line += fmt.Sprintf(". Integrate them: echo '%s' | %s", facts.IntegrateRecord, hint.JobFork.With("--stdin", "--checkout", "<dir>"))
	}
	return line
}
