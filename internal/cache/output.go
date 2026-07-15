package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egladman/magus/internal/journal"
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

// refHexLen is the hex-digit count after the prefix. 8 hex = 32 bits, matched to
// shortHash; ample for a local, keep-last-K, age-bounded store, and prefix-matchable
// like a git short hash.
const refHexLen = 8

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
}

// AmbiguousRefError is returned by output lookup when a ref prefix matches more than
// one stored execution. Candidates are the full ref ids, sorted, so the CLI can list
// them for the user to disambiguate (git-style).
type AmbiguousRefError struct {
	Prefix     string
	Candidates []string
}

func (e *AmbiguousRefError) Error() string {
	return fmt.Sprintf("output ref %q is ambiguous; matches %d executions: %s",
		e.Prefix, len(e.Candidates), strings.Join(e.Candidates, ", "))
}

// OutputStore is the cache's output-retrieval repository: it persists each execution's captured
// output VERBATIM under <cacheDir>/outputs, keyed by a short reference id, and resolves refs back
// to bytes/metadata. One execution is two sibling files:
//
//	outputs/<cacheKey>/<ref>.out    the exact bytes the process wrote
//	outputs/<cacheKey>/<ref>.json   its OutputDescriptor (identity + outcome)
//
// The .out blob is the source of truth: ByRef reads it straight through - no reconstruction,
// byte-for-byte what ran. Per-line structured events are NOT kept here; they live once in the
// invocation journal (runs/<inv>.jsonl) for the viewer/filter/live paths, so output is never
// stored twice. Grouping by cache key keeps a nondeterministic target's recent runs together, so
// keep-last-K retention is a per-directory prune (by blob modtime) and a ref lookup scans a
// shallow tree. Safe for concurrent Persist calls (each mints a distinct ref).
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

// mintRef derives a per-execution reference id from the cache key and a process-
// unique nonce, so the keep-last-K executions of ONE cache key each get a distinct,
// addressable ref while staying cache-key-flavored. Deriving from the bare key would
// collapse those K executions to a single id and lose nondeterministic-failure history.
func (s *OutputStore) mintRef(cacheKey string) string {
	nonce := strconv.FormatInt(time.Now().UnixNano(), 10) + "-" + strconv.FormatUint(s.seq.Add(1), 10)
	sum := sha256.Sum256([]byte(cacheKey + "\x00" + nonce))
	return RefPrefix + hex.EncodeToString(sum[:])[:refHexLen]
}

// outExt is the verbatim output blob; descExt is its descriptor sidecar.
const (
	outExt  = ".out"
	descExt = ".json"
)

// Persist writes the execution's captured output VERBATIM as outputs/<cacheKey>/<ref>.out
// (byte-for-byte what the process wrote, so `magus query output <ref>` is a straight read - never
// a reconstruction) plus a <ref>.json descriptor, then prunes the cache key's directory to
// keep-last-K. Per-line structured events are NOT stored here - they live in the invocation
// journal, so no output is stored twice. Returns the minted ref. Best-effort: on error it
// returns an empty ref and the caller keeps the run's own outcome.
func (s *OutputStore) Persist(cacheKey string, output []byte, d OutputDescriptor) (string, error) {
	ref := s.mintRef(cacheKey)
	d.Ref = ref
	dir := filepath.Join(s.outputsDir(), cacheKey)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, ref+outExt), output, 0o644); err != nil {
		return "", err
	}
	descriptor, err := json.Marshal(d)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, ref+descExt), descriptor, 0o644); err != nil {
		return "", err
	}
	s.pruneKey(dir, defaultOutputKeepLast)
	return ref, nil
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

// LatestRef returns the ref of the newest execution stored for cacheKey (by file
// modtime), or "" if none. A cache HIT reuses it instead of re-persisting identical
// output under a fresh ref - so hits point at the existing events, not bloat the store.
func (s *OutputStore) LatestRef(cacheKey string) string {
	dir := filepath.Join(s.outputsDir(), cacheKey)
	files, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var bestID string
	var bestMod time.Time
	for _, f := range files {
		id, ok := strings.CutSuffix(f.Name(), outExt)
		if !ok {
			continue
		}
		info, err := f.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(bestMod) {
			bestMod = info.ModTime()
			bestID = id
		}
	}
	return bestID
}

// LatestRefsByTarget returns the newest stored execution per (project, target): one
// OutputDescriptor each, the most recent by TimestampMs (ties broken by ref id so the
// choice is stable regardless of directory iteration order). It scans every cache-key
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

// bareTarget strips the charm suffix reproTarget appends ("build:rw" -> "build"), so a
// stored repro target maps back to the declared target name. reproTarget builds
// "<target>:<charms>", and declared target names carry no colon, so cutting at the first
// colon recovers the name; a suffix-less target (no charms) is returned unchanged.
func bareTarget(reproTarget string) string {
	name, _, _ := strings.Cut(reproTarget, ":")
	return name
}

// newerDescriptor reports whether a is the more recent execution than b: a later
// timestamp wins, and an equal timestamp is broken by the higher ref id so the pick is
// deterministic (two runs minted in the same millisecond still resolve the same way).
func newerDescriptor(a, b OutputDescriptor) bool {
	if a.TimestampMs != b.TimestampMs {
		return a.TimestampMs > b.TimestampMs
	}
	return a.Ref > b.Ref
}

// resolveRef resolves a ref - or a unique ref prefix, git-style - to the path of its .out
// blob. Exact id wins; else a unique prefix resolves; an ambiguous prefix returns
// *AmbiguousRefError; no match returns fs.ErrNotExist.
func (s *OutputStore) resolveRef(ref string) (string, error) {
	ref = strings.ToLower(strings.TrimSpace(ref))
	if ref == "" {
		return "", fs.ErrNotExist
	}
	keys, err := os.ReadDir(s.outputsDir())
	if err != nil {
		return "", err // fs.ErrNotExist bubbles when outputs/ is absent
	}
	var exact string
	var prefixes []string
	for _, k := range keys {
		if !k.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(s.outputsDir(), k.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			id, ok := strings.CutSuffix(f.Name(), outExt)
			if !ok {
				continue
			}
			switch {
			case id == ref:
				exact = filepath.Join(s.outputsDir(), k.Name(), f.Name())
			case strings.HasPrefix(id, ref):
				prefixes = append(prefixes, filepath.Join(s.outputsDir(), k.Name(), f.Name()))
			}
		}
	}
	if exact != "" {
		return exact, nil
	}
	switch len(prefixes) {
	case 0:
		return "", fs.ErrNotExist
	case 1:
		return prefixes[0], nil
	default:
		ids := make([]string, len(prefixes))
		for i, p := range prefixes {
			ids[i] = strings.TrimSuffix(filepath.Base(p), outExt)
		}
		sort.Strings(ids)
		return "", &AmbiguousRefError{Prefix: ref, Candidates: ids}
	}
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
// by exactly refHexLen hex digits. Unlike LooksLikeRef, which accepts any-length hex prefix
// so `magus query output` can take a git-style short ref, this rejects prefixes. Use it when
// scanning free text for a chainable ref, so short English words whose tail is coincidentally
// hex ("outed", "outface") are not mistaken for a ref.
func IsMintedRef(s string) bool {
	return len(s) == len(RefPrefix)+refHexLen && refPattern.MatchString(s)
}
