// Package trail is the magus activity trail: a durable, append-only record of consequential
// actions taken against a workspace or its daemon - who did what, and did it succeed - kept
// next to the execution journal under the cache dir. It is the writer / reader / store behind
// the magus.activity.v1 "activity view"; producers (the MCP handler today; jobs, config, and
// token lifecycle later) Record events and Put payload blobs, and the console's ActivityService
// handler Reads them for the log viewer.
//
// It is the governance sibling of internal/journal (which records what a build executed) and
// mirrors its on-disk shape: a hand-rolled Event struct, snake_case json, one json.Marshal line
// per event, plus a content-addressed blob store for large payloads. The magus.activity.v1 proto
// is the WIRE format only; the handler maps Event to it - this package never depends on the
// proto or the handler stack. Storage only: no HTTP here.
package trail

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Dir is the cache-dir subdirectory holding the trail, a sibling of the journal's runs/.
const Dir = "activity"

const (
	eventsFile  = "events.jsonl" // append-only JSONL, one Event per line
	blobsSubDir = "blobs"        // content-addressed request/response payloads
)

// refHexLen is how many hex chars of a payload's SHA-256 name its blob. 16 (64 bits) is
// ample against content collisions among one machine's payloads while staying short in a ref.
const refHexLen = 16

// maxEvents caps the trail on Open: the most recent maxEvents events are kept and blobs no kept
// event references are garbage-collected. This bounds growth across daemon restarts. A single
// long-lived daemon still grows between restarts; the live-daemon fix is a periodic prune job
// (the same background-job mechanism that drives reindex/refresh) calling a future Log.Prune
// that compacts under the writer's mutex, so it never rewrites the file out from under an
// in-flight append.
const maxEvents = 10000

// Kind values name an action's source; they map to the magus.activity.v1 Kind enum at the
// wire. Kept as readable strings on disk, like the journal's status strings. Only
// KindMCPToolCall is emitted today; the rest name the sources the envelope is built to hold
// (a producer records into the trail and the reader maps its string to the wire enum).
const (
	KindMCPToolCall    = "mcp_tool_call"
	KindJob            = "job"             // a daemon background job (SCIP reindex, graph build, VCS refresh)
	KindConfigChange   = "config_change"   // magus.yaml changed on reload, or a config-set mutation
	KindTokenLifecycle = "token_lifecycle" // a connector token minted or revoked
	KindSandboxDenial  = "sandbox_denial"  // a target attempted a disallowed filesystem write
)

// Outcome values; map to the wire Outcome enum.
const (
	OutcomeOK    = "ok"
	OutcomeError = "error"
)

// Event is one recorded action, the on-disk atom of the trail. The envelope (Ts/Kind/Actor/
// Action/Outcome) is common to every kind; the payload refs point into the blob store so a
// large request/response body never bloats the line. Field names are snake_case and match the
// journal's Event where they overlap (Ts, DurMs).
type Event struct {
	Ts            int64  `json:"ts"`                      // unix milliseconds at the action's start
	Kind          string `json:"kind"`                    // one of the Kind* constants
	Actor         string `json:"actor"`                   // who: an agent id, "cli", a user
	Action        string `json:"action"`                  // the specific action: a tool name, "connector.create"
	Outcome       string `json:"outcome"`                 // one of the Outcome* constants
	Error         string `json:"error,omitempty"`         // error text when Outcome is OutcomeError
	DurMs         int64  `json:"dur_ms,omitempty"`        // wall-clock, on call-shaped actions
	RequestRef    string `json:"request_ref,omitempty"`   // blob ref for the request body (mcp<hash>)
	ResponseRef   string `json:"response_ref,omitempty"`  // blob ref for the response body
	Preview       string `json:"preview,omitempty"`       // opening characters of the response, for list views
	RequestBytes  int64  `json:"request_bytes,omitempty"` // full request length
	ResponseBytes int64  `json:"response_bytes,omitempty"`
}

// Log is the trail's append-only writer. A nil *Log is a no-op, so a producer can hold one
// unconditionally even when it could not be opened (a read-only or dir-less workspace).
type Log struct {
	mu    sync.Mutex
	f     *os.File
	blobs *blobStore
}

// Open prunes the trail to maxEvents, then opens (creating as needed) its append handle under
// cacheDir. Best-effort: any failure returns a nil no-op writer, because the trail is a
// convenience, never a precondition for serving.
func Open(cacheDir string) *Log {
	if cacheDir == "" {
		return nil
	}
	dir := filepath.Join(cacheDir, Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	prune(dir, maxEvents) // bound growth before we start appending
	f, err := os.OpenFile(filepath.Join(dir, eventsFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil
	}
	return &Log{f: f, blobs: openBlobStore(dir)}
}

// Record appends one event as a json line. Best-effort: a marshal or write error is dropped so
// an audit failure never fails or slows the action being recorded. Concurrent producers (the
// MCP server handles calls on separate goroutines) are serialized so lines never interleave.
func (l *Log) Record(e Event) {
	if l == nil {
		return
	}
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.f.Write(append(line, '\n'))
}

// PutBlob stores a payload under a provenance prefix (e.g. "mcp") and returns its ref and byte
// size. A nil log, nil store, invalid prefix, empty data, or write failure yields an empty ref
// (the caller omits it) while still reporting the size.
func (l *Log) PutBlob(prefix string, data []byte) (ref string, size int64) {
	size = int64(len(data))
	if l == nil {
		return "", size
	}
	return l.blobs.put(prefix, data), size
}

// Close closes the append handle. Safe on a nil log.
func (l *Log) Close() error {
	if l == nil {
		return nil
	}
	return l.f.Close()
}

// Append records a single event to the trail under cacheDir without holding a handle or
// pruning - for low-frequency, payload-less producers (a daemon job, a config change, a token
// mint) that record occasionally, unlike the MCP path which holds a *Log for every call. Best-
// effort: an empty cacheDir or any I/O error is dropped, because the trail is never a
// precondition for the action it records.
func Append(cacheDir string, e Event) {
	if cacheDir == "" {
		return
	}
	dir := filepath.Join(cacheDir, Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, eventsFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	// A single write() of a short line is atomic on a local fs; concurrent Appends from
	// different producers do not interleave the way arbitrary large writes could.
	_, _ = f.Write(append(line, '\n'))
	_ = f.Close()
}

// ReadRecent returns up to limit events from the tail of the trail, newest first. A missing or
// empty trail yields no events and no error. The file is opened read-only, so it is safe to
// call while a writer appends. A corrupt line is skipped, not fatal.
func ReadRecent(cacheDir string, limit int) ([]Event, error) {
	if cacheDir == "" || limit <= 0 {
		return nil, nil
	}
	f, err := os.Open(filepath.Join(cacheDir, Dir, eventsFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	// A fixed-size window over the tail: keep the last `limit` lines in a circular buffer
	// (head marks the oldest slot) so a long file costs O(N) scan and O(limit) memory, with
	// no per-line shift.
	buf := make([]string, 0, limit)
	head := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // ref lines stay small; allow headroom
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		if len(buf) < limit {
			buf = append(buf, line)
		} else {
			buf[head] = line
			head = (head + 1) % limit
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	out := make([]Event, 0, len(buf))
	for i := 0; i < len(buf); i++ { // walk oldest->newest, then reverse
		idx := (head + i) % len(buf)
		var e Event
		if json.Unmarshal([]byte(buf[idx]), &e) == nil {
			out = append(out, e)
		}
	}
	// out is oldest-first; reverse to newest-first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// ReadBlob returns a stored payload by ref, for the ActivityService to serve. The ref is
// validated as a bare provenance-prefixed hash before it touches the filesystem.
func ReadBlob(cacheDir, ref string) ([]byte, error) {
	return openBlobStore(filepath.Join(cacheDir, Dir)).get(ref)
}

// prune keeps the last max events and deletes blobs no kept event references. Best-effort: any
// error leaves the trail untouched. Only rewrites when the file actually exceeds the cap.
func prune(dir string, max int) {
	path := filepath.Join(dir, eventsFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) <= max {
		return
	}
	kept := lines[len(lines)-max:]

	tmp, err := os.CreateTemp(dir, eventsFile+".*")
	if err != nil {
		return
	}
	for _, l := range kept {
		if l == "" {
			continue
		}
		if _, err := tmp.WriteString(l + "\n"); err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return
		}
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return
	}
	gcBlobs(dir, kept)
}

// gcBlobs removes blob files not referenced by any of the kept event lines. Temp files (from an
// in-flight PutBlob) fail validRef and are left alone.
func gcBlobs(dir string, keptLines []string) {
	referenced := make(map[string]struct{})
	for _, l := range keptLines {
		var e Event
		if json.Unmarshal([]byte(l), &e) != nil {
			continue
		}
		if e.RequestRef != "" {
			referenced[e.RequestRef] = struct{}{}
		}
		if e.ResponseRef != "" {
			referenced[e.ResponseRef] = struct{}{}
		}
	}
	blobsDir := filepath.Join(dir, blobsSubDir)
	entries, err := os.ReadDir(blobsDir)
	if err != nil {
		return
	}
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !validRef(name) {
			continue
		}
		if _, ok := referenced[name]; !ok {
			os.Remove(filepath.Join(blobsDir, name))
		}
	}
}

// blobStore is the content-addressed payload store under <trailDir>/blobs/. A payload's ref is
// a provenance prefix plus a short hash of its bytes, so a ref names both where it came from and
// dedupes identical bodies.
type blobStore struct {
	dir string
}

func openBlobStore(trailDir string) *blobStore {
	if trailDir == "" {
		return nil
	}
	dir := filepath.Join(trailDir, blobsSubDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	return &blobStore{dir: dir}
}

// blobRef is the content-addressed name for a payload: its provenance prefix plus the first
// refHexLen hex chars of its SHA-256.
func blobRef(prefix string, data []byte) string {
	sum := sha256.Sum256(data)
	return prefix + hex.EncodeToString(sum[:])[:refHexLen]
}

// put writes data under prefix and returns its ref. Idempotent by content and atomic (temp file
// then rename), so a concurrent reader never observes a partial blob.
func (b *blobStore) put(prefix string, data []byte) string {
	if b == nil || len(data) == 0 || !validPrefix(prefix) {
		return ""
	}
	ref := blobRef(prefix, data)
	path := filepath.Join(b.dir, ref)
	if _, err := os.Stat(path); err == nil {
		return ref // already stored
	}
	tmp, err := os.CreateTemp(b.dir, ref+".*")
	if err != nil {
		return ""
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return ""
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return ""
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return ""
	}
	return ref
}

// get returns a stored payload by ref, rejecting anything that is not exactly the shape put
// mints so a hostile ref cannot escape the blob directory.
func (b *blobStore) get(ref string) ([]byte, error) {
	if b == nil {
		return nil, errors.New("trail: no blob store")
	}
	if !validRef(ref) {
		return nil, errors.New("trail: invalid ref")
	}
	return os.ReadFile(filepath.Join(b.dir, ref))
}

// validPrefix accepts 2 to 8 lowercase letters - a short provenance tag like "mcp".
func validPrefix(prefix string) bool {
	if len(prefix) < 2 || len(prefix) > 8 {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		if prefix[i] < 'a' || prefix[i] > 'z' {
			return false
		}
	}
	return true
}

// validRef matches the GetPayload wire pattern: a valid prefix followed by exactly refHexLen
// hex chars. The hash is a FIXED-length suffix, so the split is len-refHexLen - a greedy
// letter-scan would misfire because hex digits a-f are also lowercase letters. The stricter
// length (vs the proto's [0-9a-f]+) is the filesystem guard.
func validRef(ref string) bool {
	if len(ref) < 2+refHexLen || len(ref) > 8+refHexLen {
		return false
	}
	split := len(ref) - refHexLen
	if !validPrefix(ref[:split]) {
		return false
	}
	for _, c := range ref[split:] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
