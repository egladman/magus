package job

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/describe"
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
// the one that answered, the source is [types.LeaseSourceContested], so a surface can say
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

// readRecord reads one binding record: "" when it is absent, cannot be read, or holds
// anything but a lease id. Only the guard writes records, through a rename, so one that
// does not read was damaged from outside magus, and reading it as none grades the call
// exactly as a caller nobody bound.
func readRecord(path string) string {
	if path == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	id := strings.TrimSpace(string(raw))
	if !types.ValidJobID(id) {
		return ""
	}
	return id
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("job: bind: %w", err)
	}
	// Through a rename: the caller's own hooks may be reading it already.
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("job: bind: %w", err)
	}
	_, werr := tmp.WriteString(id + "\n")
	cerr := tmp.Close()
	if err := cmp.Or(werr, cerr, os.Rename(tmp.Name(), path)); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("job: bind: %w", err)
	}
	return nil
}

// Bound is the job c's own record names, "" when none does. Exact: a subagent never reads
// its session's record, a session never reads a subagent's, and an identified caller never
// reads the checkout's.
//
// TODO: records are never swept; one small file per binding.
func (s *Store) Bound(c Caller) string {
	return readRecord(s.record(c))
}

// ActingLease resolves the lease for a process that is not a hook, and so knows no session
// and no subagent: this checkout's record, else claim. claim is the lease the process says
// it acts under: trail.LeaseFromEnv for a process reading its own environment, or the
// lease an adopted run was forwarded with. See [LeaseQuery.Resolve].
func ActingLease(cacheDir, claim string) (string, types.LeaseSource) {
	return LeaseQuery{CheckoutJob: readRecord(MarkerPath(cacheDir)), Claim: claim}.Resolve()
}

// HeldIn returns the live rows with write paths whose holder recorded its base in the
// checkout at root: the workers already working there, whoever they are.
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
		if row.CheckoutRoot == abs && row.State.Live() && len(row.WritePaths) > 0 {
			out = append(out, row)
		}
	}
	return out
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
