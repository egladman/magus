package cache

import (
	"bufio"
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

// RefPrefix begins every target-output reference id ("ref1a2b3c"). It doubles as
// the `magus query` router's discriminator: a positional matching ^ref[0-9a-f]+$
// is an output lookup, so a real free-text query like "refactor" (non-hex tail)
// falls through to the graph grammar untouched. No delimiter - "ref" reads as
// "reference" the way MGSxxxx codes read as diagnostics.
const RefPrefix = "ref"

// refHexLen is the hex-digit count after the prefix. 8 hex = 32 bits, matched to
// shortHash; ample for a local, keep-last-K, age-bounded store, and prefix-matchable
// like a git short hash.
const refHexLen = 8

// defaultOutputKeepLast bounds how many executions per cache key the store retains,
// so a nondeterministic target's recent failures stay independently addressable
// without unbounded growth. (Open decision in the plan; small by design.)
const defaultOutputKeepLast = 5

// OutputMeta is the derived view of a stored execution - built from its `result`
// record plus the cache key its directory is named for. It powers `--meta`, the MCP
// tool, and the viewer header without the caller parsing raw records.
type OutputMeta struct {
	Ref        string `json:"ref"`
	CacheKey   string `json:"cache_key"`
	Project    string `json:"project"`
	Target     string `json:"target,omitempty"`
	Failed     bool   `json:"failed"`
	Err        string `json:"error,omitempty"`
	Timestamp  int64  `json:"timestamp"` // unix seconds
	DurationMs int64  `json:"duration_ms"`
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

// outputStore persists per-execution captured output as STRUCTURED records under
// <cacheDir>/outputs, keyed by a short reference id. Each execution is one JSONL file:
//
//	outputs/<cacheKey>/<ref>.jsonl   the target's output events + its result event
//
// Events ([journal.Event]) are the source of truth: `magus query ref` reconstructs
// the raw text by concatenating the output events, `--meta` reads the result event,
// and the web viewer ingests the JSONL directly. Grouping by cache key keeps a
// nondeterministic target's recent runs together, so keep-last-K retention is a
// per-directory prune (by file modtime) and a ref lookup scans a shallow tree. Safe
// for concurrent persist calls (each mints a distinct ref).
type outputStore struct {
	dir string // <cacheDir>/outputs
	seq atomic.Uint64
}

func newOutputStore(cacheDir string) *outputStore {
	return &outputStore{dir: filepath.Join(cacheDir, "outputs")}
}

// mintRef derives a per-execution reference id from the cache key and a process-
// unique nonce, so the keep-last-K executions of ONE cache key each get a distinct,
// addressable ref while staying cache-key-flavored. Deriving from the bare key would
// collapse those K executions to a single id and lose nondeterministic-failure history.
func (s *outputStore) mintRef(cacheKey string) string {
	nonce := strconv.FormatInt(time.Now().UnixNano(), 10) + "-" + strconv.FormatUint(s.seq.Add(1), 10)
	sum := sha256.Sum256([]byte(cacheKey + "\x00" + nonce))
	return RefPrefix + hex.EncodeToString(sum[:])[:refHexLen]
}

// persist writes the execution's records (output lines followed by the result event,
// with the freshly minted ref stamped on it) as JSONL under outputs/<cacheKey>/, and
// prunes the cache key's directory to keep-last-K. Returns the minted ref. Best-effort:
// on error it returns an empty ref, and the caller keeps the run's own outcome.
func (s *outputStore) persist(cacheKey string, output []journal.Event, result journal.Event) (string, error) {
	ref := s.mintRef(cacheKey)
	result.Ref = ref
	dir := filepath.Join(s.dir, cacheKey)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	f, err := os.Create(filepath.Join(dir, ref+".jsonl"))
	if err != nil {
		return "", err
	}
	w := bufio.NewWriter(f)
	for _, r := range output {
		writeEventLine(w, r)
	}
	writeEventLine(w, result)
	if err := w.Flush(); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	s.pruneKey(dir, defaultOutputKeepLast)
	return ref, nil
}

func writeEventLine(w *bufio.Writer, r journal.Event) {
	line, err := json.Marshal(r)
	if err != nil {
		return
	}
	_, _ = w.Write(line)
	_ = w.WriteByte('\n')
}

// latestRef returns the ref of the newest execution stored for cacheKey (by file
// modtime), or "" if none. A cache HIT reuses it instead of re-persisting identical
// output under a fresh ref - so hits point at the existing records, not bloat the store.
func (s *outputStore) latestRef(cacheKey string) string {
	dir := filepath.Join(s.dir, cacheKey)
	files, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var bestID string
	var bestMod time.Time
	for _, f := range files {
		id, ok := strings.CutSuffix(f.Name(), ".jsonl")
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

// resolveRef resolves a ref - or a unique ref prefix, git-style - to the path of its
// JSONL file. Exact id wins; else a unique prefix resolves; an ambiguous prefix
// returns *AmbiguousRefError; no match returns fs.ErrNotExist.
func (s *outputStore) resolveRef(ref string) (string, error) {
	ref = strings.ToLower(strings.TrimSpace(ref))
	if ref == "" {
		return "", fs.ErrNotExist
	}
	keys, err := os.ReadDir(s.dir)
	if err != nil {
		return "", err // fs.ErrNotExist bubbles when outputs/ is absent
	}
	var exact string
	var prefixes []string
	for _, k := range keys {
		if !k.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(s.dir, k.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			id, ok := strings.CutSuffix(f.Name(), ".jsonl")
			if !ok {
				continue
			}
			switch {
			case id == ref:
				exact = filepath.Join(s.dir, k.Name(), f.Name())
			case strings.HasPrefix(id, ref):
				prefixes = append(prefixes, filepath.Join(s.dir, k.Name(), f.Name()))
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
			ids[i] = strings.TrimSuffix(filepath.Base(p), ".jsonl")
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
	var recs []journal.Event
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var r journal.Event
		if json.Unmarshal(line, &r) == nil {
			recs = append(recs, r)
		}
	}
	return recs, nil
}

// reconstructText rebuilds a target's raw output by concatenating its output events
// (one line each, newline-terminated) - so `magus query ref` prints exactly what the
// target wrote, pipe-clean.
func reconstructText(recs []journal.Event) []byte {
	var b bytes.Buffer
	for _, r := range recs {
		if r.Kind == journal.KindOutput {
			b.WriteString(r.Text)
			b.WriteByte('\n')
		}
	}
	return b.Bytes()
}

// metaFrom derives the OutputMeta view from an execution's records + its cache key.
func metaFrom(recs []journal.Event, cacheKey string) OutputMeta {
	m := OutputMeta{CacheKey: cacheKey}
	for _, r := range recs {
		if r.Kind != journal.KindResult {
			continue
		}
		m.Ref = r.Ref
		m.Project = r.Project
		m.Target = r.Target
		m.Failed = r.Status == journal.StatusFail
		if m.Failed {
			m.Err = r.Text
		}
		m.Timestamp = r.Ts / 1000
		m.DurationMs = r.DurMs
	}
	return m
}

// cacheKeyOf returns the cache-key directory name of a resolved ref path
// (<store>/<cacheKey>/<ref>.jsonl).
func cacheKeyOf(path string) string {
	return filepath.Base(filepath.Dir(path))
}

// pruneKey keeps the keepLast newest executions in a cache-key directory (by file
// modtime, newest first) and removes the rest. Best-effort.
func (s *outputStore) pruneKey(dir string, keepLast int) {
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
		if !strings.HasSuffix(f.Name(), ".jsonl") {
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
	}
}

// removeForProject deletes every stored execution whose result event names project,
// and drops a cache-key directory once it holds nothing else. Used by Clean for a
// per-project wipe (the store is keyed by cache key, not project path).
func (s *outputStore) removeForProject(project string) {
	keys, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, k := range keys {
		if !k.IsDir() {
			continue
		}
		dir := filepath.Join(s.dir, k.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		remaining := 0
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			path := filepath.Join(dir, f.Name())
			recs, _ := readEvents(path)
			if metaFrom(recs, k.Name()).Project == project {
				_ = os.Remove(path)
			} else {
				remaining++
			}
		}
		if remaining == 0 {
			_ = os.Remove(dir)
		}
	}
}

// LookupOutput resolves a ref (or unique prefix) to the target's reconstructed raw
// output text and metadata, reading the store rooted at cacheDir without opening a
// full cache. This is the retrieval entry point for `magus query ref...` (print path).
func LookupOutput(cacheDir, ref string) ([]byte, OutputMeta, error) {
	path, err := newOutputStore(cacheDir).resolveRef(ref)
	if err != nil {
		return nil, OutputMeta{}, err
	}
	recs, err := readEvents(path)
	if err != nil {
		return nil, OutputMeta{}, err
	}
	return reconstructText(recs), metaFrom(recs, cacheKeyOf(path)), nil
}

// LookupEvents resolves a ref (or unique prefix) to the execution's DOMAIN records
// ([]journal.Event) plus its metadata - the structured form the handler layer maps
// onto the wire proto. The repository returns the domain type directly; the handler
// maps it, so there is no intermediate byte/DTO representation to keep in sync.
func LookupEvents(cacheDir, ref string) ([]journal.Event, OutputMeta, error) {
	path, err := newOutputStore(cacheDir).resolveRef(ref)
	if err != nil {
		return nil, OutputMeta{}, err
	}
	recs, err := readEvents(path)
	if err != nil {
		return nil, OutputMeta{}, err
	}
	return recs, metaFrom(recs, cacheKeyOf(path)), nil
}

// refPattern matches a full ref id or a hex prefix of one: the literal "ref" then
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
