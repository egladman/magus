package job

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/describe"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// LeaseMarkerName is the file, in a checkout's cache dir, that binds a lease to that
// checkout for a caller that reports NO session. It exists because the environment cannot
// carry a lease into a hook: a host runs its hooks with its own environment, so a worker
// exporting BAGGAGE for its shell is invisible to the guard judging its commands. A file
// in the checkout is the one channel the worker's shell, the host's hook and the sandbox
// all read. BAGGAGE and an explicit --lease still win; the marker is the last resort.
const LeaseMarkerName = "lease"

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
	if c.CacheDir == "" {
		return ""
	}
	if c.Session == "" {
		return filepath.Join(c.CacheDir, LeaseMarkerName)
	}
	sum := sha256.Sum256([]byte(c.Session))
	return filepath.Join(c.CacheDir, leaseMarkerDir, hex.EncodeToString(sum[:12]))
}

// Marker reads the lease bound to this session in this checkout, or "" when none is bound
// or the file does not hold a lease id.
//
// A session with no marker of its own falls back to the CHECKOUT-WIDE one. That is what
// keeps a worktree an orchestrator bound by hand grading the sessions inside it, and it
// errs in the safe direction: the fallback grades a write that would otherwise be graded
// by nobody. A session that HAS its own marker never reads the other one, which is the
// boundary: a worker bound to one job cannot be graded, or act, under another.
func (c Checkout) Marker() string {
	if id := readMarker(c.marker()); id != "" {
		return id
	}
	if c.Session == "" {
		return ""
	}
	return readMarker(Checkout{CacheDir: c.CacheDir}.marker())
}

// ActingLease is the lease this session acts under in this checkout: the marker Bind
// wrote, else the W3C baggage the process inherited, else "". The guard hook and the
// sandbox both resolve through this one method so the two enforcement tiers cannot
// disagree about who is acting.
func (c Checkout) ActingLease() string {
	// The MARKER wins. It is written by `magus job exec` into the checkout that took the
	// lease, so it is a fact about where the work is happening; the env member is a claim
	// the worker makes about itself, and letting a claim override the record meant a
	// worker bound to one job could be graded against another job's write paths by exporting
	// its id.
	//
	// Not a fail-open case: a checkout with neither answer is a question magus cannot ask
	// and stays advisory, which is unchanged. This is the case where magus CAN ask and got
	// two answers, and preferring the one nobody can rewrite from a shell is the whole of
	// the fix. Conflict reports the disagreement so a caller can say so.
	if marker := c.Marker(); marker != "" {
		return marker
	}
	return trail.LeaseFromEnv()
}

// Conflict names the two ids when this session's marker and the environment disagree, or
// returns false. The env member is ignored in that case (see ActingLease); this is how a
// surface tells the reader that, rather than grading against one and never mentioning the
// other.
func (c Checkout) Conflict() (marker, claimed string, conflicted bool) {
	marker, claimed = c.Marker(), trail.LeaseFromEnv()
	return marker, claimed, marker != "" && claimed != "" && marker != claimed
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
	bound := readMarker(c.marker())
	if bound == "" {
		bound = trail.LeaseFromEnv()
	}
	if bound != "" && bound != id {
		return &RefusedError{
			Lease: id, Actor: Actor{Lease: bound, Session: c.Session},
			Rule: fmt.Sprintf("re-binding it to %s is how a worker would be graded against another lease's paths", id),
		}
	}
	path := c.marker()
	if path == "" {
		return fmt.Errorf("job: bind lease: no cache dir to write the marker into")
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
	id := readMarker(path)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("job: vacate lease: %w", err)
	}
	return id, nil
}

// readMarker reads one marker file, or "" when it is absent or holds no lease id.
func readMarker(path string) string {
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

// BoundLeases names every lease bound in this checkout, whichever session holds it,
// sorted and deduplicated. It is what lets a fork see the workers already here, which no
// single session can answer about the others.
func BoundLeases(cacheDir string) []string {
	if cacheDir == "" {
		return nil
	}
	seen := map[string]bool{}
	if id := readMarker(filepath.Join(cacheDir, LeaseMarkerName)); id != "" {
		seen[id] = true
	}
	entries, err := os.ReadDir(filepath.Join(cacheDir, leaseMarkerDir))
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if id := readMarker(filepath.Join(cacheDir, leaseMarkerDir, e.Name())); id != "" {
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

// ActingLease is the checkout-wide resolution, for a caller that reports no session. See
// [Checkout.ActingLease].
func ActingLease(cacheDir string) string { return Checkout{CacheDir: cacheDir}.ActingLease() }

// LeaseConflict is the checkout-wide reading. See [Checkout.Conflict].
func LeaseConflict(cacheDir string) (marker, claimed string, conflicted bool) {
	return Checkout{CacheDir: cacheDir}.Conflict()
}

// LeaseFromMarker is the checkout-wide marker. See [Checkout.Marker].
func LeaseFromMarker(cacheDir string) string { return Checkout{CacheDir: cacheDir}.Marker() }

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
