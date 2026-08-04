package cache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egladman/magus/internal/journal"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/secret"
	"github.com/egladman/magus/types"
)

// RefPrefix begins every target-output reference id ("out1a2b3c"). It is the provenance tag in
// the shared ref namespace - "out" for a target OUTPUT, alongside "mcp" for an MCP call payload
// (internal/trail) - so a ref names where it came from at a glance. No delimiter, matching the
// other prefixes. LooksLikeRef validates the shape of the argument to `magus query output <ref>`;
// it is not a router (retrieval is an explicit subcommand), so a shape-collision with a search
// term is impossible.
const RefPrefix = "out"

// RunsDir is the cache subdir holding one union event log per invocation
// (<cacheDir>/runs/<inv>.jsonl). Shared by the writer (magus.BeginInvocation) and the reader
// (InvocationByID) so the two never drift on the path.
const RunsDir = "runs"

// runExt is the extension of an invocation journal file (runs/<inv>.jsonl).
const runExt = ".jsonl"

// DefaultMaxRuns bounds how many invocation journals the runs dir retains, so a long-lived
// daemon's run-log dir stays bounded. Coarser than the per-key output cap (defaultOutputKeepLast):
// one file per invocation, kept newest-first by modtime. The RotateLogs job trims to this.
const DefaultMaxRuns = 500

// refHexLen is the hex-digit count of a portable ref after the prefix. The ref is a
// truncation of the step's cache key, so the SAME inputs yield the SAME ref on every
// machine: an inspect line pasted from CI resolves locally, and ref equality is input
// equality. 12 hex = 48 bits; at a million distinct step keys the birthday-collision
// odds are ~1e-3, acceptable for a per-workspace namespace, and the full key (any
// longer prefix, up to all 64 hex) still resolves git-style.
const refHexLen = 12

// attemptHexLen is the hex-digit count of an attempt id after the prefix. Attempts are
// execution-unique (nonce-derived) file stems under the step's key directory, so the
// keep-last-K executions of ONE step stay independently addressable; a full attempt id
// from `--attempts` resolves directly.
const attemptHexLen = 8

// defaultOutputKeepLast bounds how many executions per cache key the store retains,
// so a nondeterministic target's recent failures stay independently addressable
// without unbounded growth. (Open decision in the plan; small by design.)
const defaultOutputKeepLast = 5

// OutputDescriptor is a stored execution's identity and outcome, written beside its verbatim
// output blob. It gives `magus query output <ref> -o json`, the MCP tool, and the viewer header
// the run's project/target/status/timing without anyone parsing the output bytes.
type OutputDescriptor struct {
	Ref         string `json:"ref"`
	Project     string `json:"project"`
	Target      string `json:"target,omitempty"`
	Inv         string `json:"inv,omitempty"` // invocation id of the run that produced this output
	Failed      bool   `json:"failed"`
	ErrMsg      string `json:"error,omitempty"` // failure message; empty on success
	TimestampMs int64  `json:"timestamp_ms"`    // unix milliseconds, matching DurationMs' unit
	DurationMs  int64  `json:"duration_ms"`

	// Schema v2 (portable refs): Ref is the key-derived portable id shared by every
	// attempt of the step, and the fields below carry the identity it derives from.
	// A v1 descriptor (Schema zero) predates portable refs: its Ref is the
	// execution-unique file stem and these fields are empty.
	Schema       int    `json:"schema,omitempty"`
	Key          string `json:"key,omitempty"`         // full cache key hash (64 hex)
	KeyVersion   int    `json:"key_version,omitempty"` // hashStep keyVersion that produced Key
	Attempt      string `json:"attempt,omitempty"`     // execution-unique id; the file stem
	MagusVersion string `json:"magus_version,omitempty"`
}

// descriptorSchema is the OutputDescriptor schema this store writes. v2 introduced
// portable (key-derived) refs; v1 descriptors carry no schema field.
const descriptorSchema = 2

// AmbiguousRefError is returned by output lookup when a ref (or prefix) matches more
// than one stored identity. Candidates are pasteable-back refs, sorted: a step
// candidate renders as its portable ref (lengthened past refHexLen if two keys
// collide), an attempt candidate as its full file stem - so the CLI can list them for
// the user to disambiguate (git-style).
type AmbiguousRefError struct {
	Prefix     string
	Candidates []string
}

func (e *AmbiguousRefError) Error() string {
	return fmt.Sprintf("output ref %q is ambiguous; matches %d executions: %s",
		e.Prefix, len(e.Candidates), strings.Join(e.Candidates, ", "))
}

// OutputStore is the cache's output-retrieval repository: it persists each execution's captured
// output VERBATIM under <cacheDir>/outputs and resolves refs back to bytes/metadata. The
// user-facing ref is PORTABLE: a truncation of the step's cache key, so equal inputs mint the
// same ref on every machine. One execution is two sibling files named by an execution-unique
// attempt id:
//
//	outputs/<cacheKey>/<attempt>.out    the exact bytes the process wrote
//	outputs/<cacheKey>/<attempt>.json   its OutputDescriptor (identity + outcome)
//
// The .out blob is the source of truth: ByRef reads it straight through - no reconstruction,
// byte-for-byte what ran. Per-line structured events are NOT kept here; they live once in the
// invocation journal (runs/<inv>.jsonl) for the viewer/filter/live paths, so output is never
// stored twice. Grouping by cache key keeps a nondeterministic target's recent runs together, so
// keep-last-K retention is a per-directory prune (by blob modtime) and a ref lookup scans a
// shallow tree. Safe for concurrent Persist calls (each mints a distinct attempt).
type OutputStore struct {
	cacheDir string // cache ROOT; outputsDir/RunsDir are joined off this
	seq      atomic.Uint64
}

// NewOutputStore builds a store rooted at the cache ROOT dir (not the outputs subdir). It joins
// "outputs" and RunsDir itself, so ByRef works on an Inspect workspace with no live cache.
func NewOutputStore(cacheDir string) *OutputStore {
	return &OutputStore{cacheDir: cacheDir}
}

// outputsDir is where the per-execution blobs live: <cacheDir>/outputs.
func (s *OutputStore) outputsDir() string { return filepath.Join(s.cacheDir, "outputs") }

// portableRef derives the user-facing reference id from the cache key alone:
// RefPrefix + the key's first refHexLen hex digits. Same inputs -> same key -> same
// ref on every machine, so an inspect line from CI or a teammate's terminal resolves
// locally, and two machines printing DIFFERENT refs for one target proves their
// inputs differ. The ref names the STEP (the outputs/<cacheKey>/ directory);
// execution-level identity lives a level down in attempt ids.
func portableRef(cacheKey string) string {
	if len(cacheKey) < refHexLen {
		return RefPrefix + cacheKey
	}
	return RefPrefix + cacheKey[:refHexLen]
}

// mintAttempt derives an execution-unique attempt id from the cache key and a process-
// unique nonce. Attempts name the files under the step's key directory, so the
// keep-last-K executions of ONE cache key each stay independently addressable (K
// failing attempts of a volatile target must not collapse to one record). The id
// keeps the ref prefix and hex shape so a full attempt id from `--attempts` routes
// and resolves exactly like a ref - and so pre-portable stores, whose file stems
// were minted the same way, resolve unchanged.
func (s *OutputStore) mintAttempt(cacheKey string) string {
	nonce := strconv.FormatInt(time.Now().UnixNano(), 10) + "-" + strconv.FormatUint(s.seq.Add(1), 10)
	sum := sha256.Sum256([]byte(cacheKey + "\x00" + nonce))
	return RefPrefix + hex.EncodeToString(sum[:])[:attemptHexLen]
}

// outExt is the verbatim output blob; descExt is its descriptor sidecar.
const (
	outExt  = ".out"
	descExt = ".json"
)

// Persist writes the execution's captured output VERBATIM as outputs/<cacheKey>/<attempt>.out
// (byte-for-byte what the process wrote, so `magus query output <ref>` is a straight read - never
// a reconstruction) plus an <attempt>.json descriptor, then prunes the cache key's directory to
// keep-last-K. Per-line structured events are NOT stored here - they live in the invocation
// journal, so no output is stored twice. Returns the descriptor as stamped and stored: Ref is
// the step's portable ref (shared by every attempt of this key), Attempt the execution-unique
// id that names the files just written. Best-effort at the call site: on error the caller keeps
// the run's own outcome.
func (s *OutputStore) Persist(ctx context.Context, cacheKey string, output []byte, d OutputDescriptor) (OutputDescriptor, error) {
	d.Ref = portableRef(cacheKey)
	d.Schema = descriptorSchema
	d.Key = cacheKey
	d.KeyVersion = keyVersion
	d.Attempt = s.mintAttempt(cacheKey)
	d.MagusVersion = types.MagusVersionFromContext(ctx)
	dir := filepath.Join(s.outputsDir(), cacheKey)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return OutputDescriptor{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, d.Attempt+outExt), output, 0o644); err != nil {
		return OutputDescriptor{}, err
	}
	descriptor, err := json.Marshal(d)
	if err != nil {
		return OutputDescriptor{}, err
	}
	// The descriptor's free-form fields (ErrMsg today) are Go-side strings that never pass
	// the capture tap, so this is their only write boundary. Redacting the marshaled bytes
	// rather than each field keeps a field added later covered by construction.
	descriptor = secret.Redact(ctx, descriptor)
	if err := os.WriteFile(filepath.Join(dir, d.Attempt+descExt), descriptor, 0o644); err != nil {
		return OutputDescriptor{}, err
	}
	s.pruneKey(dir, defaultOutputKeepLast)
	return d, nil
}

// descriptorPath maps a resolved <ref>.out path to its sibling <ref>.json descriptor.
func descriptorPath(outPath string) string {
	return strings.TrimSuffix(outPath, outExt) + descExt
}

// readDescriptor reads and decodes a <ref>.json descriptor.
func readDescriptor(path string) (OutputDescriptor, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return OutputDescriptor{}, err
	}
	var m OutputDescriptor
	if err := json.Unmarshal(data, &m); err != nil {
		return OutputDescriptor{}, err
	}
	return m, nil
}

// StepRef returns the step's portable ref when at least one execution is stored for
// cacheKey, or "" if none. A cache HIT reuses it instead of re-persisting identical
// output under a fresh attempt - so hits point at the existing events, not bloat the
// store. Pre-portable directories qualify too: the ref derives from the key, not from
// what any stored descriptor says.
func (s *OutputStore) StepRef(cacheKey string) string {
	files, err := os.ReadDir(filepath.Join(s.outputsDir(), cacheKey))
	if err != nil {
		return ""
	}
	for _, f := range files {
		if strings.HasSuffix(f.Name(), outExt) {
			return portableRef(cacheKey)
		}
	}
	return ""
}

// newestAttemptBlob returns the path of the newest attempt's .out blob in a cache-key
// directory, or "" when the directory holds none. It is how a step-level ref resolves
// to bytes: the ref names the directory, the newest attempt answers for it. "Newest"
// is the descriptor comparator (newerDescriptor) - the SAME ordering `--attempts`
// lists by, so the bare ref and the top row never disagree; a blob whose descriptor is
// missing or unreadable (a Persist that died between its two writes) falls back to
// file modtime, ranked beneath every descriptor-backed attempt.
func newestAttemptBlob(dir string) string {
	files, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var best string
	var bestDesc OutputDescriptor
	var haveDesc bool
	var bestOrphan, bestOrphanName string
	var bestOrphanMod time.Time
	for _, f := range files {
		stem, ok := strings.CutSuffix(f.Name(), outExt)
		if !ok {
			continue
		}
		path := filepath.Join(dir, f.Name())
		d, derr := readDescriptor(filepath.Join(dir, stem+descExt))
		if derr == nil {
			if d.Attempt == "" {
				d.Attempt = stem
			}
			if !haveDesc || newerDescriptor(d, bestDesc) {
				haveDesc, bestDesc, best = true, d, path
			}
			continue
		}
		info, ierr := f.Info()
		if ierr != nil {
			continue
		}
		// Equal modtimes (a coarse-granularity filesystem) break by the higher name
		// so the pick is deterministic.
		if info.ModTime().After(bestOrphanMod) || (info.ModTime().Equal(bestOrphanMod) && f.Name() > bestOrphanName) {
			bestOrphanMod = info.ModTime()
			bestOrphan, bestOrphanName = path, f.Name()
		}
	}
	if haveDesc {
		return best
	}
	return bestOrphan
}

// LatestRefsByTarget returns the newest stored execution per (project, target): one
// OutputDescriptor each, the most recent by TimestampMs (ties broken by attempt id,
// then ref, so the choice is stable regardless of directory iteration order). It scans every cache-key
// directory's descriptor sidecars. This is what folds each target's last output ref onto
// its knowledge-graph node without the graph builder parsing the store's on-disk layout.
//
// Descriptors store the REPRO target (bare name plus charm suffix, e.g. "build:rw", the
// exact re-runnable invocation - see reproTarget); this collapses that back to the bare
// declared target so the newest run is picked across a target's charm variants and the
// returned Target matches a knowledge-graph target node. Descriptors without a target
// (project-scoped output) are skipped. Best-effort: an absent or unreadable store, or an
// undecodable descriptor, is skipped - fewer entries, never an error. The result is
// sorted by project then bare target for deterministic assembly.
func (s *OutputStore) LatestRefsByTarget() []OutputDescriptor {
	keys, err := os.ReadDir(s.outputsDir())
	if err != nil {
		return nil
	}
	latest := map[string]OutputDescriptor{}
	for _, k := range keys {
		if !k.IsDir() {
			continue
		}
		dir := filepath.Join(s.outputsDir(), k.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), descExt) {
				continue
			}
			d, err := readDescriptor(filepath.Join(dir, f.Name()))
			if err != nil || d.Ref == "" || d.Target == "" {
				continue
			}
			d.Target = bareTarget(d.Target)
			key := d.Project + "\x00" + d.Target
			if cur, ok := latest[key]; ok && !newerDescriptor(d, cur) {
				continue
			}
			latest[key] = d
		}
	}
	out := make([]OutputDescriptor, 0, len(latest))
	for _, d := range latest {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		return out[i].Target < out[j].Target
	})
	return out
}

// ListDescriptors returns every stored execution's descriptor, newest run first, across all cache
// keys - the feed for the console's run browser (the log-viewer tree groups them project -> target ->
// run so a reader can browse recent runs and open any one's captured output). Unlike
// LatestRefsByTarget, which collapses to the single newest run per target, this keeps every retained
// execution (the store holds keep-last-K per cache key), so a target's recent history is browsable.
// The REPRO target is preserved verbatim (with any charm suffix) so a run's exact invocation stays
// visible; the caller collapses to the bare name for grouping if it wants. Best-effort: an absent or
// unreadable store, or an undecodable descriptor, yields fewer entries, never an error.
func (s *OutputStore) ListDescriptors() []OutputDescriptor {
	keys, err := os.ReadDir(s.outputsDir())
	if err != nil {
		return nil
	}
	var out []OutputDescriptor
	for _, k := range keys {
		if !k.IsDir() {
			continue
		}
		dir := filepath.Join(s.outputsDir(), k.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), descExt) {
				continue
			}
			d, err := readDescriptor(filepath.Join(dir, f.Name()))
			if err != nil || d.Ref == "" {
				continue
			}
			out = append(out, d)
		}
	}
	// Newest run first (ties broken by attempt id then ref, via newerDescriptor, so the order is
	// stable regardless of directory-iteration order), so the browser's default top-of-list is the
	// most recent output.
	sort.Slice(out, func(i, j int) bool { return newerDescriptor(out[i], out[j]) })
	return out
}

// bareTarget strips the charm suffix reproTarget appends ("build:rw" -> "build"), so a
// stored repro target maps back to the declared target name. reproTarget builds
// "<target>:<charms>", and declared target names carry no colon, so cutting at the first
// colon recovers the name; a suffix-less target (no charms) is returned unchanged.
func bareTarget(reproTarget string) string {
	name, _, _ := strings.Cut(reproTarget, ":")
	return name
}

// newerDescriptor reports whether a is the more recent execution than b: a later
// timestamp wins, and an equal timestamp is broken by the higher attempt id, then the
// higher ref, so the pick is deterministic (two runs minted in the same millisecond
// still resolve the same way - the ref alone no longer discriminates attempts of one
// step, which share it).
func newerDescriptor(a, b OutputDescriptor) bool {
	if a.TimestampMs != b.TimestampMs {
		return a.TimestampMs > b.TimestampMs
	}
	if a.Attempt != b.Attempt {
		return a.Attempt > b.Attempt
	}
	return a.Ref > b.Ref
}

// resolveRef resolves a ref - or a unique ref prefix, git-style - to the path of its .out
// blob. Two namespaces answer, reflecting the two identity levels:
//
//   - STEP refs (portable, key-derived): the hex tail prefix-matches a cache-key
//     directory name; the step resolves to its newest attempt's blob.
//   - ATTEMPT ids (execution-unique file stems, and every pre-portable ref): the whole
//     ref prefix-matches a file stem.
//
// An exact attempt-id match wins outright (a full id copied from `--attempts` must never
// be hijacked by a colliding directory prefix). Otherwise one total match across both
// namespaces resolves; several return *AmbiguousRefError naming each candidate (the
// portable ref for a step, the stem for an attempt); none returns fs.ErrNotExist.
func (s *OutputStore) resolveRef(ref string) (string, error) {
	ref = strings.ToLower(strings.TrimSpace(ref))
	if ref == "" {
		return "", fs.ErrNotExist
	}
	keys, err := os.ReadDir(s.outputsDir())
	if err != nil {
		return "", err // fs.ErrNotExist bubbles when outputs/ is absent
	}
	hexTail, hasPrefix := strings.CutPrefix(ref, RefPrefix)
	var exacts []string      // .out paths whose stem IS ref (attempt ids collide only by birthday accident)
	var exactDirs []string   // their parent key dirs, for disambiguation if several
	var dirMatches []string  // cache-key dirs the ref names as a step (holding at least one blob)
	var fileMatches []string // .out paths the ref names as an attempt (outside matched dirs)
	for _, k := range keys {
		if !k.IsDir() {
			continue
		}
		matchedDir := hasPrefix && hexTail != "" && strings.HasPrefix(k.Name(), hexTail)
		files, err := os.ReadDir(filepath.Join(s.outputsDir(), k.Name()))
		if err != nil {
			continue
		}
		hasBlob := false
		for _, f := range files {
			id, ok := strings.CutSuffix(f.Name(), outExt)
			if !ok {
				continue
			}
			hasBlob = true
			switch {
			case id == ref:
				exacts = append(exacts, filepath.Join(s.outputsDir(), k.Name(), f.Name()))
				exactDirs = append(exactDirs, k.Name())
			// Inside a matched dir every stem belongs to the already-counted step,
			// so collecting it would double-report one identity as two candidates.
			case !matchedDir && strings.HasPrefix(id, ref):
				fileMatches = append(fileMatches, filepath.Join(s.outputsDir(), k.Name(), f.Name()))
			}
		}
		// A blob-less dir (interrupted Persist, half-finished removal) names no
		// retrievable step; counting it would fabricate ambiguity or a phantom hit.
		if matchedDir && hasBlob {
			dirMatches = append(dirMatches, k.Name())
		}
	}
	if len(exacts) == 1 {
		return exacts[0], nil
	}
	if len(exacts) > 1 {
		// The same full stem exists under several keys (32-bit birthday accident).
		// Neither answer is safely "the one the user meant"; name each stem's STEP
		// so the next query is unambiguous.
		return "", &AmbiguousRefError{Prefix: ref, Candidates: uniqueDirRefs(exactDirs)}
	}
	switch {
	case len(dirMatches) == 1 && len(fileMatches) == 0:
		if p := newestAttemptBlob(filepath.Join(s.outputsDir(), dirMatches[0])); p != "" {
			return p, nil
		}
		return "", fs.ErrNotExist
	case len(dirMatches) == 0 && len(fileMatches) == 1:
		return fileMatches[0], nil
	case len(dirMatches)+len(fileMatches) == 0:
		return "", fs.ErrNotExist
	default:
		ids := uniqueDirRefs(dirMatches)
		for _, p := range fileMatches {
			ids = append(ids, strings.TrimSuffix(filepath.Base(p), outExt))
		}
		sort.Strings(ids)
		return "", &AmbiguousRefError{Prefix: ref, Candidates: ids}
	}
}

// uniqueDirRefs renders cache-key dir names as refs a user can paste back
// unambiguously: the standard truncation, lengthened (git-style) until the listed
// candidates are mutually distinct. Without this, two keys colliding in their first
// refHexLen digits - the one case that MAKES a step ref ambiguous - would list as
// identical strings, a dead end at exactly the moment disambiguation is needed.
func uniqueDirRefs(dirs []string) []string {
	n := refHexLen
	for ; n < 64; n++ {
		seen := make(map[string]struct{}, len(dirs))
		distinct := true
		for _, d := range dirs {
			p := d
			if len(p) > n {
				p = p[:n]
			}
			if _, dup := seen[p]; dup {
				distinct = false
				break
			}
			seen[p] = struct{}{}
		}
		if distinct {
			break
		}
	}
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if len(d) > n {
			d = d[:n]
		}
		out = append(out, RefPrefix+d)
	}
	return out
}

// Attempts lists every stored execution of the step ref names, newest first - the
// keep-last-K history behind one portable ref (`magus query output <ref> --attempts`).
// ref may be the step ref, a unique prefix, or any attempt id within the step; the
// whole directory answers either way. Pre-portable descriptors carry no Attempt field;
// their file stem (which was the v1 ref) fills it so every row is addressable. A blob
// whose descriptor is missing or unreadable (a Persist that died between its two
// writes) still rows up - minimally, from the blob itself - because a listing that
// silently omits a retrievable execution reads as "it does not exist".
func (s *OutputStore) Attempts(ref string) ([]OutputDescriptor, error) {
	path, err := s.resolveRef(ref)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(path)
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []OutputDescriptor
	for _, f := range files {
		stem, ok := strings.CutSuffix(f.Name(), outExt)
		if !ok {
			continue
		}
		d, derr := readDescriptor(filepath.Join(dir, stem+descExt))
		if derr != nil {
			d = OutputDescriptor{Ref: portableRef(filepath.Base(dir))}
			if info, ierr := f.Info(); ierr == nil {
				d.TimestampMs = info.ModTime().UnixMilli()
			}
		}
		if d.Attempt == "" {
			d.Attempt = stem
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return newerDescriptor(out[i], out[j]) })
	return out, nil
}

// readEvents parses every JSONL line of path into events.
func readEvents(path string) ([]journal.Event, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var events []journal.Event
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var r journal.Event
		if json.Unmarshal(line, &r) == nil {
			events = append(events, r)
		}
	}
	return events, nil
}

// pruneKey keeps the keepLast newest executions in a cache-key directory (by the .out blob's
// modtime, newest first) and removes the rest - each blob together with its .json descriptor.
// Best-effort.
func (s *OutputStore) pruneKey(dir string, keepLast int) {
	if keepLast <= 0 {
		return
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type entry struct {
		path string
		mod  time.Time
	}
	var entries []entry
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), outExt) {
			continue
		}
		info, err := f.Info()
		if err != nil {
			continue
		}
		entries = append(entries, entry{path: filepath.Join(dir, f.Name()), mod: info.ModTime()})
	}
	if len(entries) <= keepLast {
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].mod.After(entries[j].mod) })
	for _, e := range entries[keepLast:] {
		_ = os.Remove(e.path)
		_ = os.Remove(descriptorPath(e.path))
	}
}

// runsDir is <cacheDir>/runs, the flat directory of invocation journals.
func (s *OutputStore) runsDir() string { return filepath.Join(s.cacheDir, RunsDir) }

// RotateRuns keeps the keepLast newest invocation journals (runs/<inv>.jsonl, by modtime) and
// removes the rest, returning how many it deleted and the bytes that freed. The runs dir is flat
// (one file per invocation, not keyed like outputs/), so this is a single keep-last-K over the
// whole directory - the run-log analogue of pruneKey, and the worker behind the rotate-logs job.
// Best-effort: an unreadable dir or a failed remove is skipped, never fatal. keepLast <= 0 is a
// no-op (never wipe the whole dir by accident).
func (s *OutputStore) RotateRuns(keepLast int) (removed int, bytesFreed int64) {
	if keepLast <= 0 {
		return 0, 0
	}
	files, err := os.ReadDir(s.runsDir())
	if err != nil {
		return 0, 0
	}
	type entry struct {
		path string
		mod  time.Time
		size int64
	}
	var entries []entry
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), runExt) {
			continue
		}
		info, err := f.Info()
		if err != nil {
			continue
		}
		entries = append(entries, entry{path: filepath.Join(s.runsDir(), f.Name()), mod: info.ModTime(), size: info.Size()})
	}
	if len(entries) <= keepLast {
		return 0, 0
	}
	slices.SortFunc(entries, func(a, b entry) int { return b.mod.Compare(a.mod) }) // newest first
	for _, e := range entries[keepLast:] {
		if os.Remove(e.path) == nil {
			removed++
			bytesFreed += e.size
		}
	}
	return removed, bytesFreed
}

// RunsStat reports the invocation-journal directory's current footprint: total bytes across every
// runs/<inv>.jsonl and the number of such files. Best-effort and read-only; a missing dir is
// (0, 0). It is what the RotateLogs job reports as its target size.
func (s *OutputStore) RunsStat() (bytes int64, count int64) {
	files, err := os.ReadDir(s.runsDir())
	if err != nil {
		return 0, 0
	}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), runExt) {
			continue
		}
		if info, err := f.Info(); err == nil {
			bytes += info.Size()
			count++
		}
	}
	return bytes, count
}

// removeForProject deletes every stored execution whose descriptor names project (blob +
// descriptor), and drops a cache-key directory once it holds nothing else. Used by Clean for a
// per-project wipe (the store is keyed by cache key, not project path).
func (s *OutputStore) removeForProject(project string) {
	keys, err := os.ReadDir(s.outputsDir())
	if err != nil {
		return
	}
	for _, k := range keys {
		if !k.IsDir() {
			continue
		}
		dir := filepath.Join(s.outputsDir(), k.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		remaining := 0
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), outExt) {
				continue
			}
			path := filepath.Join(dir, f.Name())
			meta, _ := readDescriptor(descriptorPath(path))
			if meta.Project == project {
				_ = os.Remove(path)
				_ = os.Remove(descriptorPath(path))
			} else {
				remaining++
			}
		}
		if remaining == 0 {
			_ = os.Remove(dir)
		}
	}
}

// ByRef resolves a ref (or unique prefix) to the target's VERBATIM captured output bytes plus
// its metadata. The bytes are read straight from the <ref>.out blob - exactly what the process
// wrote, no reconstruction. This is the retrieval entry point for `magus query output <ref>`
// (print path).
func (s *OutputStore) ByRef(ref string) ([]byte, OutputDescriptor, error) {
	path, err := s.resolveRef(ref)
	if err != nil {
		return nil, OutputDescriptor{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, OutputDescriptor{}, err
	}
	meta, _ := readDescriptor(descriptorPath(path)) // best-effort; missing descriptor still yields bytes
	return raw, meta, nil
}

// InvocationByID reads the union run log (<cacheDir>/runs/<inv>.jsonl) for one invocation
// id and rebuilds its header: the command lineage (subcommand/args/trigger), timing, and outcome.
// It is how a stored output (OutputDescriptor.Inv) is traced back to the run that produced it -
// `magus query output <ref> --meta` and the viewer surface this lineage. Reads off the cache
// ROOT (RunsDir), not outputsDir. Returns fs.ErrNotExist when the run log has aged out.
func (s *OutputStore) InvocationByID(inv string) (journal.Invocation, error) {
	events, err := readEvents(filepath.Join(s.cacheDir, RunsDir, inv+".jsonl"))
	if err != nil {
		return journal.Invocation{}, err
	}
	return journal.InvocationFromEvents(inv, events), nil
}

// refPattern matches a full ref id or a hex prefix of one: the literal "out" then
// one or more lowercase hex digits, anchored. The anchored hex tail is what makes
// the no-delimiter prefix safe as a router key.
var refPattern = regexp.MustCompile("^" + RefPrefix + "[0-9a-f]+$")

// LooksLikeRef reports whether s is shaped like a target-output reference id (or a
// hex prefix of one). It is the `magus query` router discriminator: a match routes
// to output retrieval, while a real free-text query like "refactor" (non-hex tail)
// falls through to the graph grammar.
func LooksLikeRef(s string) bool {
	return refPattern.MatchString(s)
}

// IsMintedRef reports whether s is a fully-minted reference id: the "out" prefix followed
// by exactly refHexLen hex digits (a portable step ref) or attemptHexLen digits (an attempt
// id, and every pre-portable ref). Unlike LooksLikeRef, which accepts any-length hex prefix
// so `magus query output` can take a git-style short ref, this rejects prefixes. Use it when
// scanning free text for a chainable ref, so short English words whose tail is coincidentally
// hex ("outed", "outface") are not mistaken for a ref.
func IsMintedRef(s string) bool {
	n := len(s) - len(RefPrefix)
	return (n == refHexLen || n == attemptHexLen) && refPattern.MatchString(s)
}
