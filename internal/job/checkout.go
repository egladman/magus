package job

import (
	"cmp"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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

// MarkerPath is where the checkout record for the checkout whose cache dir is cacheDir
// lives, or "" when there is no cache dir or no user state dir.
//
// NOT in the cache dir. The sandbox grants a run write access to its workspace, and the
// cache dir sits inside it by default, so a marker there is one a confined run could
// rewrite to name a broader lease, or delete, and the next run in the checkout, sandbox
// and guard alike, would act on what it wrote. The sandbox grants the user state dir to
// nobody.
func MarkerPath(cacheDir string) string {
	if cacheDir == "" {
		return ""
	}
	base, err := config.UserStateDir()
	if err != nil {
		return ""
	}
	key, err := filepath.Abs(cacheDir)
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(key); err == nil {
		key = resolved
	}
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(base, "magus", markerKind, hex.EncodeToString(sum[:12]), LeaseMarkerName)
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
		return MarkerPath(s.cacheDir)
	}
	if s.err != nil || s.path == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(c.Host + "\x00" + c.Session + "\x00" + c.Agent))
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
func (s *Store) sweepRecords(w *sweeper, rows []types.Job) {
	if s.err != nil || s.path == "" {
		return
	}
	dir := filepath.Join(filepath.Dir(s.path), agentRecordDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
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

// dropRecord removes the record at path if it still holds b.
//
// Moved aside before it is read rather than re-read and removed: [Store.Bind] may rebind
// the caller in between, and deleting that record would leave a bound worker unbound. A
// record found to hold anything else is linked back, unless a newer bind already took its
// place.
func dropRecord(path string, b Binding) {
	aside := path + "." + rand.Text() + ".sweep"
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
func ActingLease(cacheDir, claim string) (string, types.LeaseSource) {
	return LeaseQuery{CheckoutJob: readRecord(MarkerPath(cacheDir)), Claim: claim}.Resolve()
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

// RefuseSharedCheckout refuses a fork whose write paths cover a file the WORKSPACE has to
// load, while another live job with write paths is already working in this checkout.
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
func RefuseSharedCheckout(store *Store, rows []types.Job, id string, candidate types.Job) error {
	if store == nil || len(candidate.WritePaths) == 0 {
		return nil
	}
	held := heldHere(rows, store.root, id)
	if len(held) == 0 {
		return nil
	}
	// Read only once something else holds the checkout: the walk touches the tree, and a
	// fork into an unshared checkout is the common case and must stay free.
	for _, load := range describe.WorkspaceLoadFiles(store.root) {
		declared, covered := writePathCovering(candidate.WritePaths, load)
		if !covered {
			continue
		}
		return fmt.Errorf("job: %s declares %q, which covers %s, and %s is live in this checkout (%s)."+
			" A magusfile, magus.yaml or spell source that is half-saved stops the whole workspace loading,"+
			" so every job here loses `magus run` until it lands, not just this one."+
			" Give %s its own worktree and fork it there, or leave %s out of its write paths."+
			" `%s` names what else that write path covers",
			id, declared, load, held[0].ID, store.root, id, load, hint.DescribeFile.With(load))
	}
	return nil
}

// RefuseDirectoryWritePaths refuses a fork whose write paths name an existing directory
// (MGS3018), project roots and the workspace root included. A write path names files: a
// directory claims every file beneath it, so the job overlaps every job that edits anything
// there, and the overlap report fills with pairs that share no file. Every fork door runs
// it, and there is no override.
//
// Declarable: a file, and a path that does not exist yet, which the job creates. A glob
// whose last segments are only wildcards (`docs/**`, `docs/*`, `docs/**/*`) names everything
// under its directory, so it is judged as that directory, each match when the directory
// part is itself a pattern. Any other glob (`docs/*.md`) names files and passes.
//
// A nil store, or one with no workspace root, has no tree to read and refuses nothing.
func RefuseDirectoryWritePaths(store *Store, id string, candidate types.Job) error {
	if store == nil || store.root == "" {
		return nil
	}
	var refused []string
	for _, decl := range candidate.WritePaths {
		for _, dir := range claimedDirs(store.root, decl) {
			named := fmt.Sprintf("%q", decl)
			if dir != path.Clean(strings.TrimSpace(decl)) {
				named = fmt.Sprintf("%q (matching the directory %q)", decl, dir)
			}
			switch {
			case dir == ".":
				named += ", the workspace root"
			case describe.IsProjectRoot(store.root, dir):
				named += fmt.Sprintf(", the root of project %q", dir)
			default:
				named += fmt.Sprintf(", inside project %q", enclosingProject(store.root, dir))
			}
			refused = append(refused, named)
		}
	}
	if len(refused) == 0 {
		return nil
	}
	return types.DiagnosticErrorf(types.WritePathIsDirectory,
		"job: %s declares a directory as a write path: %s. A write path names files: a directory, a project"+
			" root included, claims every file under it, so the job would overlap every job editing anything there."+
			" List the files the job will edit, and name each file it creates by its path",
		id, strings.Join(refused, "; "))
}

// claimedDirs returns the existing directories, workspace-relative, that the write path
// decl claims whole. See [RefuseDirectoryWritePaths] for how a glob is read.
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

// WriteProof grades candidate's write paths against every other live job working in this
// checkout. See [types.JobWriteProof] for why the three answers are distinct.
//
// A nil Store answers alone: a caller with no store knows of no other holder.
func (s *Store) WriteProof(rows []types.Job, id string, candidate types.Job) types.JobWriteProof {
	if s == nil {
		return types.WriteProofAlone
	}
	held := heldHere(rows, s.root, id)
	if len(held) == 0 {
		return types.WriteProofAlone
	}
	for _, row := range held {
		for _, mine := range candidate.WritePaths {
			for _, theirs := range row.WritePaths {
				if types.PathsIntersect(mine, theirs) {
					return types.WriteProofOverlapping
				}
			}
		}
	}
	return types.WriteProofDisjoint
}
