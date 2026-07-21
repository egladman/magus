// Package trail is the magus activity trail: a durable, append-only record of consequential
// actions taken against the daemon - who did what, and did it succeed - kept next to the
// execution journal under a base directory. It is the store behind the magus.activity.v1
// "activity view"; producers (the MCP handler and background jobs today; config and token
// lifecycle later) append events and store payload blobs, and the console's ActivityService
// reads them for the log viewer.
//
// It is the governance sibling of internal/journal (which records what a build executed) and
// mirrors its Event/JSONL shape: a hand-rolled Event struct, snake_case json, one json.Marshal
// line per event, plus a content-addressed blob store for large payloads. The magus.activity.v1
// proto is the WIRE format only; the handler maps Event to it - this package never depends on
// the proto or the handler stack.
//
// Every operation is a stateless free function taking the trail's base directory. Writes are
// low-frequency (agent tool calls, background jobs), so a per-call open/append/close is cheap
// and avoids a long-lived handle, a lock, and a second write path. POSIX append of a short line
// is atomic, so concurrent producers do not interleave; the "lines stay small" invariant
// (events carry payload REFS, never bodies) is what keeps that true.
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
)

// dir is the base-dir subdirectory holding the trail, a sibling of the journal's runs/.
const dir = "activity"

const (
	eventsFile  = "events.jsonl" // append-only JSONL, one Event per line
	blobsSubDir = "blobs"        // content-addressed request/response payloads
)

// refHexLen is how many hex chars of a payload's SHA-256 name its blob. 16 (64 bits) is ample
// against content collisions among one machine's payloads while staying short in a ref.
const refHexLen = 16

// maxEvents caps the trail: Rotate keeps the most recent maxEvents events and garbage-collects
// blobs no kept event references. Rotate runs at daemon start and thereafter every rotateEvery
// appends (via RotateOnCount) - not on every append - so a long-lived daemon's trail stays
// bounded without a rewrite per write.
const maxEvents = 10000

// rotateEvery is how many recorded events trigger the next Rotate. The trail is stateless and
// lock-free by design (append is a bare POSIX append; there is no long-lived handle to hang a
// count on), so the write-triggered rotate cannot count inside Append without re-scanning the
// whole file per write. Instead RotateOnCount lets the caller - which already increments a counter
// per append - drive the rotate, keeping the policy and its maxEvents sibling in one place. This
// bounds the trail at roughly maxEvents + rotateEvery events between rotates; 512 keeps the rewrite
// rare relative to tool-call traffic.
const rotateEvery = 512

// Kind names an action's source; the values map to the magus.activity.v1 Kind enum at the wire.
// Readable strings on disk, like the journal's status strings. It is a NAMED string (not a bare string)
// so a producer or the audit interceptor cannot pass an arbitrary label where a Kind is wanted - the
// param is type-checked against these consts. MCP tool calls and jobs have producers today; the rest name
// the governance sources the envelope is built to hold, so a reader can switch on kind and a producer adds
// one without a schema change.
type Kind string

const (
	KindMCPToolCall    Kind = "mcp_tool_call"
	KindJob            Kind = "job"             // a daemon background job (SCIP reindex, graph build, VCS refresh)
	KindConfigChange   Kind = "config_change"   // magus.yaml changed on reload, or a config-set mutation
	KindTokenLifecycle Kind = "token_lifecycle" // a connector token minted or revoked
	KindSandboxDenial  Kind = "sandbox_denial"  // a target attempted a disallowed filesystem write
)

// Outcome values; map to the wire Outcome enum.
const (
	OutcomeOK    = "ok"
	OutcomeError = "error"
)

// Event is one recorded action, the on-disk atom of the trail. The envelope (Ts/Kind/Actor/
// Action/Outcome) is common to every kind; the payload refs point into the blob store so a large
// body never bloats the line. Field names are snake_case and match the journal's Event where
// they overlap (Ts, DurMs).
type Event struct {
	Ts            int64  `json:"ts"`                      // unix milliseconds at the action's start
	Kind          Kind   `json:"kind"`                    // one of the Kind* constants
	Actor         string `json:"actor"`                   // who: an agent id, "daemon", a user
	UserAgent     string `json:"user_agent,omitempty"`    // caller's HTTP User-Agent, when known (MCP over HTTP)
	Workspace     string `json:"workspace,omitempty"`     // repo-relative or absolute root the action pertained to; "" for daemon-wide (an MCP call is not bound to one workspace)
	Action        string `json:"action"`                  // the specific action: a tool name, a job command, "connector.create"
	Outcome       string `json:"outcome"`                 // one of the Outcome* constants
	Error         string `json:"error,omitempty"`         // error text when Outcome is OutcomeError
	DurMs         int64  `json:"dur_ms,omitempty"`        // wall-clock, on call-shaped actions
	RequestRef    string `json:"request_ref,omitempty"`   // blob ref for the request body (mcp<hash>)
	ResponseRef   string `json:"response_ref,omitempty"`  // blob ref for the response body
	Preview       string `json:"preview,omitempty"`       // opening characters of the response, for list views
	RequestBytes  int64  `json:"request_bytes,omitempty"` // full request length
	ResponseBytes int64  `json:"response_bytes,omitempty"`
}

func eventsPath(base string) string { return filepath.Join(base, dir, eventsFile) }
func blobsPath(base string) string  { return filepath.Join(base, dir, blobsSubDir) }

// Append records one event under base. Best-effort: an empty base or any I/O error is dropped,
// because the trail is never a precondition for the action it records. Concurrent Appends from
// different producers do not interleave: each is a single POSIX append of one short line.
func Append(base string, e Event) {
	if base == "" {
		return
	}
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	d := filepath.Join(base, dir)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(d, eventsFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	_, _ = f.Write(append(line, '\n'))
	_ = f.Close()
}

// WriteBlob stores a payload under a provenance prefix (e.g. "mcp") and returns its ref and byte
// size. An empty base, invalid prefix, empty data, or write failure yields an empty ref (the
// caller omits it) while still reporting the size. Idempotent by content and atomic (temp file
// then rename), so a concurrent reader never observes a partial blob.
func WriteBlob(base, prefix string, data []byte) (ref string, size int64) {
	size = int64(len(data))
	if base == "" || len(data) == 0 || !validPrefix(prefix) {
		return "", size
	}
	d := blobsPath(base)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", size
	}
	ref = blobRef(prefix, data)
	path := filepath.Join(d, ref)
	if _, err := os.Stat(path); err == nil {
		return ref, size // already stored
	}
	tmp, err := os.CreateTemp(d, ref+".*")
	if err != nil {
		return "", size
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", size
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", size
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return "", size
	}
	return ref, size
}

// ReadBlob returns a stored payload by ref, for the ActivityService to serve. The ref is
// validated as a bare provenance-prefixed hash before it touches the filesystem, and the read
// creates nothing.
func ReadBlob(base, ref string) ([]byte, error) {
	if !validRef(ref) {
		return nil, errors.New("trail: invalid ref")
	}
	return os.ReadFile(filepath.Join(blobsPath(base), ref))
}

// LastRun returns the most recent KIND_JOB event whose Action equals action - the space-joined
// worker argv recorded for a background job (e.g. "graph build") - and whether one was found.
// It is how a caller shows a job's last outcome. Scans the retained trail newest-first; a job
// that has not run within it is (zero, false).
func LastRun(base, action string) (Event, bool) {
	events, _ := ReadRecent(base, maxEvents)
	for _, e := range events { // newest-first
		if e.Kind == KindJob && e.Action == action {
			return e, true
		}
	}
	return Event{}, false
}

// Stat reports the trail's current on-disk footprint under base: total bytes (the events file
// plus every payload blob) and the number of recorded events. It is what a caller shows to
// judge whether a rotate is worth running. Best-effort and read-only: a missing or empty trail
// is (0, 0), and an unreadable directory is skipped rather than erroring - a size readout is
// never a precondition for anything.
func Stat(base string) (bytes int64, count int64) {
	if base == "" {
		return 0, 0
	}
	if f, err := os.Open(eventsPath(base)); err == nil {
		if fi, err := f.Stat(); err == nil {
			bytes += fi.Size()
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			if len(sc.Bytes()) > 0 {
				count++
			}
		}
		f.Close()
	}
	if entries, err := os.ReadDir(blobsPath(base)); err == nil {
		for _, ent := range entries {
			if info, err := ent.Info(); err == nil && !ent.IsDir() {
				bytes += info.Size()
			}
		}
	}
	return bytes, count
}

// ReadRecent returns up to limit events from the tail of the trail, newest first. A missing or
// empty trail yields no events and no error. The file is opened read-only, so it is safe to call
// while a producer appends. A corrupt line is skipped, not fatal.
func ReadRecent(base string, limit int) ([]Event, error) {
	if base == "" || limit <= 0 {
		return nil, nil
	}
	f, err := os.Open(eventsPath(base))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	// A fixed-size window over the tail: keep the last `limit` lines in a circular buffer (head
	// marks the oldest slot) so a long file costs O(N) scan and O(limit) memory, no per-line shift.
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
	for i := 0; i < len(buf); i++ { // oldest -> newest
		var e Event
		if json.Unmarshal([]byte(buf[(head+i)%len(buf)]), &e) == nil {
			out = append(out, e)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 { // reverse to newest-first
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// Rotate keeps the last maxEvents events and deletes blobs that no kept event references. Called
// at daemon start and, thereafter, by RotateOnCount. Best-effort: any error leaves the trail
// as-is, and it only rewrites when the file exceeds the cap. It takes no lock, so a concurrent
// Append racing the read-rewrite window can be dropped - acceptable for a best-effort governance
// trail, and the price of keeping the trail lock-free.
func Rotate(base string) { rotate(base, maxEvents) }

// RotateOnCount rotates iff n - the caller's running append count (the value returned by its
// atomic increment) - lands on a rotateEvery boundary. Passing the count keeps the trail itself
// stateless: the counter lives with the producer, not here. n == 0 never rotates, so a stray zero
// cannot force a rewrite before the first real append. It is the write-triggered rotate, distinct
// from the boot-time Rotate.
func RotateOnCount(base string, n uint64) {
	if n != 0 && n%rotateEvery == 0 {
		Rotate(base)
	}
}

func rotate(base string, max int) {
	if base == "" {
		return
	}
	path := eventsPath(base)
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) <= max {
		return
	}
	kept := lines[len(lines)-max:]

	tmp, err := os.CreateTemp(filepath.Join(base, dir), eventsFile+".*")
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
	gcBlobs(base, kept)
}

// gcBlobs removes blob files that none of the kept event lines reference. Temp files (from an
// in-flight WriteBlob) fail validRef and are left alone.
func gcBlobs(base string, keptLines []string) {
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
	entries, err := os.ReadDir(blobsPath(base))
	if err != nil {
		return
	}
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !validRef(name) {
			continue
		}
		if _, ok := referenced[name]; !ok {
			os.Remove(filepath.Join(blobsPath(base), name))
		}
	}
}

// blobRef is the content-addressed name for a payload: its provenance prefix plus the first
// refHexLen hex chars of its SHA-256.
func blobRef(prefix string, data []byte) string {
	sum := sha256.Sum256(data)
	return prefix + hex.EncodeToString(sum[:])[:refHexLen]
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

// validRef matches the GetPayload wire pattern: a valid prefix followed by exactly refHexLen hex
// chars. The hash is a FIXED-length suffix, so the split is len-refHexLen - a greedy letter-scan
// would misfire because hex digits a-f are also lowercase letters. It rejects any separator or
// dot, so ReadBlob cannot escape the blob dir.
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
