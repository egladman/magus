package job

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/describe"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// LeaseMarkerName is the file that binds a lease to a checkout for a caller that reports
// NO session. It exists because the environment cannot carry a lease into a hook: a host
// runs its hooks with its own environment, so a worker exporting BAGGAGE for its shell is
// invisible to the guard judging its commands. A file keyed by the checkout is the one
// channel the worker's shell, the host's hook and the sandbox all read. See
// [LeaseQuery.Resolve] for where it ranks, and [MarkerPath] for where it lives.
const LeaseMarkerName = "lease"

// markerKind is the user state directory's subdirectory the markers live under.
const markerKind = "checkouts"

// MarkerPath is where the checkout-wide marker for the checkout whose cache dir is
// cacheDir lives, or "" when there is no cache dir or no user state dir.
//
// NOT in the cache dir. The sandbox grants a run write access to its workspace, and the
// cache dir sits inside it by default, so a marker there is one a confined run could
// rewrite to name a broader lease, or delete, and the next run in the checkout, sandbox
// and guard alike, would act on what it wrote. The sandbox grants the user state dir to
// nobody.
func MarkerPath(cacheDir string) string {
	dir := markerDir(cacheDir)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, LeaseMarkerName)
}

// markerDir holds this checkout's markers: under the user state dir, in a directory
// named for the cache dir's resolved path, since that is what identifies a checkout here.
func markerDir(cacheDir string) string {
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
	return filepath.Join(base, "magus", markerKind, hex.EncodeToString(sum[:12]))
}

// leaseMarkerDir holds the per-session markers, one file each.
//
// A checkout used to hold exactly one, which made the binding a fact about the DIRECTORY.
// Three workers sharing a checkout therefore could not each hold a lease: the second
// bind was refused as a rebind, all three ran unattributed, and every write-path rule fell
// silent at once while looking exactly like a guarded session.
const leaseMarkerDir = "leases"

// Checkout is where a lease binds: one checkout's cache dir, and the session the caller
// reported inside it. The zero Session is the checkout-wide binding, which is what a
// caller that reports no session reads and writes, unchanged from when that was the only
// binding there was.
//
// SESSION, not process: the guard hook, the worker's shell and the sandbox are three
// processes of one session, and the id is what the host calls the conversation. A host
// that reports none still gets the old behavior rather than no behavior.
type Checkout struct {
	CacheDir string
	Session  string
}

// marker names the file this session's binding lives in.
//
// The session id is HASHED rather than sanitized, the same rule hint.MarkerPath applies
// to the same value: it is a host-chosen string that may hold anything at all, separators
// included, and a filename assembled from one is a filename the input picked.
func (c Checkout) marker() string {
	dir := markerDir(c.CacheDir)
	if dir == "" {
		return ""
	}
	if c.Session == "" {
		return filepath.Join(dir, LeaseMarkerName)
	}
	sum := sha256.Sum256([]byte(c.Session))
	return filepath.Join(dir, leaseMarkerDir, hex.EncodeToString(sum[:12]))
}

// Marker reads the lease bound to this session in this checkout, or "" when none is bound.
// A marker that cannot be read, or that holds anything but a lease id, is an error: the
// binding is unknown, and reading it as none would hand the call to a lower source.
//
// A session with no marker of its own falls back to the CHECKOUT-WIDE one. That is what
// keeps a worktree an orchestrator bound by hand grading the sessions inside it, and it
// errs in the safe direction: the fallback grades a write that would otherwise be graded
// by nobody. A session that HAS its own marker never reads the other one, which is the
// boundary: a worker bound to one job cannot be graded, or act, under another.
func (c Checkout) Marker() (string, error) {
	id, err := readMarker(c.marker())
	if id != "" || err != nil || c.Session == "" {
		return id, err
	}
	return readMarker(Checkout{CacheDir: c.CacheDir}.marker())
}

// LeaseQuery is what a caller knows about itself when it asks which lease it acts under.
// Checkout is where the call runs; the other fields are the answers only the caller can
// bring, each empty when it has none.
type LeaseQuery struct {
	Checkout Checkout
	// OwnMarkerOnly reads this session's own marker and never the checkout-wide fallback.
	// Bind sets it: whether a session may take a lease is a question about what that
	// session holds, and a hand-bound checkout must not refuse every worker in it.
	OwnMarkerOnly bool
	// Flag is an explicit --lease the caller's wiring passed.
	Flag string
	// AgentJob is the job magus recorded the calling subagent was spawned for.
	AgentJob string
	// Claim is the lease the process says it acts under, from its BAGGAGE or from the
	// client an adopted run was forwarded from.
	Claim string
}

// Resolve returns the lease the caller acts under and which source answered, or "" and
// no source when none did. It is the one order the guard, the job store, doctor, the
// sandbox and the journal all grade by: an explicit flag, the subagent's spawn record, the
// checkout's marker, and only then the claim.
//
// A record outranks the claim because the claim is the one answer a worker can rewrite
// from its own shell: a worker bound to one job that could export another's id would be
// graded against that job's write paths. When a lower source named a different lease than
// the one that answered, the source is [types.LeaseSourceContested], so a surface can say
// something was overruled.
//
// An unreadable or invalid marker is an error whatever outranks it.
func (q LeaseQuery) Resolve() (string, types.LeaseSource, error) {
	var marker string
	var err error
	if q.OwnMarkerOnly {
		marker, err = readMarker(q.Checkout.marker())
	} else {
		marker, err = q.Checkout.Marker()
	}
	if err != nil {
		return "", "", err
	}
	sources := []struct {
		lease string
		from  types.LeaseSource
	}{
		{q.Flag, types.LeaseSourceFlag},
		{q.AgentJob, types.LeaseSourceAgent},
		{marker, types.LeaseSourceMarker},
		{q.Claim, types.LeaseSourceEnv},
	}
	for i, s := range sources {
		if s.lease == "" {
			continue
		}
		for _, lower := range sources[i+1:] {
			if lower.lease != "" && lower.lease != s.lease {
				return s.lease, types.LeaseSourceContested, nil
			}
		}
		return s.lease, s.from, nil
	}
	return "", "", nil
}

// Bind writes the marker binding lease id to this session in this checkout, creating the
// directory if needed. An id ValidJobID rejects is refused, so the marker never holds a
// value Marker would read back as nothing.
//
// BINDING IS ONE-WAY for a worker. A session already acting under a lease cannot bind
// itself to a different one, because that is the whole boundary: a worker that can name
// the orchestrator's row is graded against the orchestrator's paths from its next command.
// Re-binding the lease it already holds is allowed and does nothing, so a worker that
// runs its bootstrap twice is not refused. Only an UNBOUND session, which is the
// orchestrator or the person, binds.
//
// It reads THIS session's own marker, never the checkout-wide fallback Marker reads. The
// fallback exists so an unbound session is still GRADED by whatever the checkout holds;
// letting it refuse a bind as well would make a checkout the orchestrator took by hand
// unusable by any worker in it, which is the deadlock the per-session marker exists to
// undo. The environment still refuses, because a worker that inherited one lease and
// names another is claiming to be two parties.
func (c Checkout) Bind(id string) error {
	if !types.ValidJobID(id) {
		return fmt.Errorf("job: %q is not a lease id (letters, digits and -_./: only)", id)
	}
	bound, _, err := LeaseQuery{Checkout: c, OwnMarkerOnly: true, Claim: trail.LeaseFromEnv()}.Resolve()
	if err != nil {
		return fmt.Errorf("job: bind lease: %w", err)
	}
	if bound != "" && bound != id {
		return &RefusedError{
			Lease: id, Actor: Actor{Lease: bound},
			Rule: fmt.Sprintf("re-binding it to %s is how a worker would be graded against another lease's paths", id),
		}
	}
	path := c.marker()
	if path == "" {
		return fmt.Errorf("job: bind lease: no cache dir or user state dir to key the marker by")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("job: bind lease: %w", err)
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0o644); err != nil {
		return fmt.Errorf("job: bind lease: %w", err)
	}
	return nil
}

// Vacate clears this session's marker, so a later Bind can take a different lease (or the
// same one again, which it already permitted). Returns the lease it cleared, or "" when
// the session held none.
//
// Idempotent by construction: a session with no marker already reads back "", and
// removing a file that is already gone is not an error here, so a repeated vacate, or one
// racing a sibling process clearing the same marker, is a no-op rather than a failure.
// This is the file half only; whether the lease it named is one a caller SHOULD be giving
// up is judged by the caller (see `magus job exec --vacate`), which reads the row first.
//
// It clears ONLY this session's marker. A session vacating on its way out must not
// release the binding a sibling session in the same checkout is still working under.
func (c Checkout) Vacate() (string, error) {
	path := c.marker()
	if path == "" {
		return "", nil
	}
	// A marker that does not hold a lease id is removed all the same: clearing it is the
	// fix for the error every resolution through it reports.
	id, _ := readMarker(path)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("job: vacate lease: %w", err)
	}
	return id, nil
}

// readMarker reads one marker file: "" when it is absent, and an error when it cannot be
// read or does not hold a lease id.
func readMarker(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("job: read lease marker: %w", err)
	}
	id := strings.TrimSpace(string(raw))
	if !types.ValidJobID(id) {
		return "", fmt.Errorf("job: lease marker %s holds %q, which is not a lease id; `%s` clears it", path, id, hint.JobExec.With("--vacate"))
	}
	return id, nil
}

// BoundLeases names every lease bound in this checkout, whichever session holds it,
// sorted and deduplicated. It is what lets a fork see the workers already here, which no
// single session can answer about the others.
func BoundLeases(cacheDir string) []string {
	dir := markerDir(cacheDir)
	if dir == "" {
		return nil
	}
	// A marker that does not read is skipped here: this lists who is bound, and every
	// resolution through that marker reports it as the error it is.
	seen := map[string]bool{}
	if id, _ := readMarker(filepath.Join(dir, LeaseMarkerName)); id != "" {
		seen[id] = true
	}
	entries, err := os.ReadDir(filepath.Join(dir, leaseMarkerDir))
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if id, _ := readMarker(filepath.Join(dir, leaseMarkerDir, e.Name())); id != "" {
				seen[id] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// ActingLease resolves the lease for a caller that reports no session and no subagent:
// this checkout's checkout-wide marker, else claim. claim is the lease the process says it
// acts under: trail.LeaseFromEnv for a process reading its own environment, or the lease
// an adopted run was forwarded with. See [LeaseQuery.Resolve].
func ActingLease(cacheDir, claim string) (string, types.LeaseSource, error) {
	return LeaseQuery{Checkout: Checkout{CacheDir: cacheDir}, Claim: claim}.Resolve()
}

// LeaseFromMarker is the checkout-wide marker. See [Checkout.Marker].
func LeaseFromMarker(cacheDir string) (string, error) { return Checkout{CacheDir: cacheDir}.Marker() }

// BindLease binds the checkout-wide marker. See [Checkout.Bind].
func BindLease(cacheDir, id string) error { return Checkout{CacheDir: cacheDir}.Bind(id) }

// VacateLease clears the checkout-wide marker. See [Checkout.Vacate].
func VacateLease(cacheDir string) (string, error) { return Checkout{CacheDir: cacheDir}.Vacate() }

// heldHere returns the live rows with write paths that are bound to this checkout,
// excluding the job being forked.
func heldHere(rows []types.Job, cacheDir, forking string) []types.Job {
	bound := BoundLeases(cacheDir)
	if len(bound) == 0 {
		return nil
	}
	var out []types.Job
	for _, row := range rows {
		if row.ID == forking || !slices.Contains(bound, row.ID) {
			continue
		}
		// A row that is over holds nothing. A checkout still carrying the marker of a job
		// that passed is a checkout nobody is working in, and refusing the next fork over
		// it would strand the worktree until somebody remembered to vacate.
		if !row.State.Live() || len(row.WritePaths) == 0 {
			continue
		}
		out = append(out, row)
	}
	return out
}

// RefuseSharedCheckout refuses a fork whose write paths cover a file the WORKSPACE has to
// load, while another live job with write paths is already bound to this checkout.
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
	held := heldHere(rows, store.cacheDir, id)
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

// RefuseDirectoryWritePaths refuses a fork whose write paths name an existing directory that
// is not a project root (MGS3018). A directory claims every file beneath it, so the job
// overlaps every job that edits anything there, and the overlap report fills with pairs
// that share no file. Every fork door runs it, and there is no override.
//
// Declarable: a file; a path that does not exist yet, which the job creates; and a project
// root, which the job owns whole ("." when the workspace root is a project). A glob whose
// last segments are only wildcards (`docs/**`, `docs/*`, `docs/**/*`) names everything
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
			if describe.IsProjectRoot(store.root, dir) {
				continue
			}
			named := fmt.Sprintf("%q", decl)
			if dir != path.Clean(strings.TrimSpace(decl)) {
				named = fmt.Sprintf("%q (matching the directory %q)", decl, dir)
			}
			refused = append(refused, fmt.Sprintf("%s, inside project %q", named, enclosingProject(store.root, dir)))
		}
	}
	if len(refused) == 0 {
		return nil
	}
	return types.DiagnosticErrorf(types.WritePathIsDirectory,
		"job: %s declares a directory as a write path: %s. A directory claims every file under it, so the job"+
			" would overlap every job editing anything there. List the files the job will edit, or declare the"+
			" root of the project it owns whole",
		id, strings.Join(refused, "; "))
}

// claimedDirs returns the existing directories, workspace-relative, that the write path
// decl claims whole. See [RefuseDirectoryWritePaths] for how a glob is read.
func claimedDirs(root, decl string) []string {
	decl = strings.TrimSpace(decl)
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

// WriteProof grades candidate's write paths against every other live job bound to this
// checkout. See [types.JobWriteProof] for why the three answers are distinct.
//
// A nil Store answers alone: a caller with no store knows of no other holder.
func (s *Store) WriteProof(rows []types.Job, id string, candidate types.Job) types.JobWriteProof {
	if s == nil {
		return types.WriteProofAlone
	}
	held := heldHere(rows, s.cacheDir, id)
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
