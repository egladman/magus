package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AuditDir is the cache-dir subdirectory holding the MCP audit log. It sits next to
// the journal's runs/ so both share one cache root and retention regime.
const AuditDir = "audit"

// auditFile is the append-only JSONL file (one auditEvent per line) inside AuditDir.
const auditFile = "mcp.jsonl"

// auditEvent is one persisted MCP tool call: which agent invoked which tool, with what
// arguments, how long it took, and how it ended. It is the durable form of the ephemeral
// "[AGENT] tool called/done" stderr banners - the thin capture that a later /dashboard
// activity view (magus.audit.v1) reads. One JSON object per JSONL line; field names are
// snake_case to match the journal's on-disk schema.
type auditEvent struct {
	Ts      int64           `json:"ts"`              // unix milliseconds at call start
	Agent   string          `json:"agent"`           // agent id from origin.Origin.Agent
	Tool    string          `json:"tool"`            // MCP tool name
	Args    json.RawMessage `json:"args,omitempty"`  // the call's arguments object, verbatim (the context sent TO the tool)
	DurMs   int64           `json:"dur_ms"`          // wall-clock duration of the call
	Outcome string          `json:"outcome"`         // "ok" or "error"
	Error   string          `json:"error,omitempty"` // error text when Outcome is "error"
	// The response the tool sent back (the context returned FROM it). The full text
	// is content-addressed into the audit blob store and referenced by RespRef so the
	// JSONL line stays small; RespPreview carries the opening characters for list views
	// that should not fetch every body, and RespBytes is the full length.
	RespRef     string `json:"resp_ref,omitempty"`
	RespBytes   int64  `json:"resp_bytes,omitempty"`
	RespPreview string `json:"resp_preview,omitempty"`
}

// auditLog appends auditEvents to an io.Writer, one JSON line each. It mirrors the
// journal FileHandler: a mutex serializes concurrent tool calls (the MCP server handles
// them on separate goroutines) so lines never interleave, and every write is best-effort
// - an audit failure must never fail or slow a tool call. A nil *auditLog is a no-op, so
// callers can hold one unconditionally even when the log could not be opened.
type auditLog struct {
	mu    sync.Mutex
	w     io.Writer
	c     io.Closer   // non-nil when w is a file we own; nil for an injected writer
	blobs *auditBlobs // stores call payloads by ref; nil when unavailable (a no-op)
}

// newAuditLog wraps w (used by tests with an in-memory buffer).
func newAuditLog(w io.Writer) *auditLog {
	return &auditLog{w: w}
}

// openAuditLog opens <cacheDir>/audit/mcp.jsonl for append, creating the directory. On
// any failure it returns a nil *auditLog (a no-op recorder) rather than an error: the
// audit trail is a convenience, never a precondition for serving tools.
func openAuditLog(cacheDir string) *auditLog {
	if cacheDir == "" {
		return nil
	}
	dir := filepath.Join(cacheDir, AuditDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(dir, auditFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil
	}
	return &auditLog{w: f, c: f, blobs: openAuditBlobs(cacheDir)}
}

// putBlob stores a call payload and returns its ref and full byte length. Safe on a
// nil log or nil blob store: the ref is "" (the caller omits it) but the length is
// still reported, so the size is recorded even when the body could not be persisted.
func (a *auditLog) putBlob(data []byte) (ref string, n int64) {
	n = int64(len(data))
	if a == nil {
		return "", n
	}
	return a.blobs.put(data), n
}

// blob returns a stored payload by ref for the activity endpoint to serve.
func (a *auditLog) blob(ref string) ([]byte, error) {
	if a == nil {
		return nil, errors.New("audit: no log")
	}
	return a.blobs.get(ref)
}

// record appends one event. Best-effort: marshal or write errors are dropped.
func (a *auditLog) record(e auditEvent) {
	if a == nil {
		return
	}
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, _ = a.w.Write(append(line, '\n'))
}

// Close closes the underlying file when this log owns one; a no-op otherwise.
func (a *auditLog) Close() error {
	if a == nil || a.c == nil {
		return nil
	}
	return a.c.Close()
}

// auditArgs marshals a tool call's arguments map for the audit record. An empty map or a
// marshal failure yields nil (the field is then omitted), never a panic.
func auditArgs(args map[string]any) json.RawMessage {
	if len(args) == 0 {
		return nil
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil
	}
	return raw
}

// nowMillis is the audit clock. Package-level so tests can pin it if needed.
func nowMillis() int64 {
	return time.Now().UnixMilli()
}

// blobSubDir holds content-addressed MCP call payloads under the audit dir, so they
// share the cache retention regime and never mix with the target OutputStore's refs.
const blobSubDir = "blobs"

// refLen is how many hex chars of the SHA-256 name a blob. 16 (64 bits) is ample to
// avoid collisions among a single machine's call payloads while staying short in a URL.
const refLen = 16

// auditBlobs is a content-addressed store for MCP call payloads (request and response
// bodies): a payload is written to <cacheDir>/audit/blobs/<ref>, where ref is a short
// hash of the bytes, so identical bodies dedupe. Best-effort like the rest of the audit
// trail - a nil store and every failure degrade to an empty ref, never a hard error.
type auditBlobs struct {
	dir string
}

func openAuditBlobs(cacheDir string) *auditBlobs {
	if cacheDir == "" {
		return nil
	}
	dir := filepath.Join(cacheDir, AuditDir, blobSubDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	return &auditBlobs{dir: dir}
}

// blobRef is the content-addressed name for a payload: the first refLen hex chars of
// its SHA-256. Deliberately not shaped like an OutputStore ref, so nothing routes an
// MCP payload through magus_output; the activity endpoint resolves these.
func blobRef(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:refLen]
}

// put writes data and returns its ref. A nil store, empty data, or a write failure
// yields "". The write is idempotent by content and atomic (temp file then rename) so
// a concurrent reader never observes a partial blob.
func (b *auditBlobs) put(data []byte) string {
	if b == nil || len(data) == 0 {
		return ""
	}
	ref := blobRef(data)
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

// get returns a stored payload by ref. The ref is validated as a bare hash before it
// touches the filesystem, so a hostile value from the activity endpoint cannot escape
// the blob directory.
func (b *auditBlobs) get(ref string) ([]byte, error) {
	if b == nil {
		return nil, errors.New("audit blobs: no store")
	}
	if !validRef(ref) {
		return nil, errors.New("audit blobs: invalid ref")
	}
	return os.ReadFile(filepath.Join(b.dir, ref))
}

// validRef reports whether ref is exactly refLen lowercase hex characters - the only
// shape put mints, and the guard that keeps get's lookup inside the blob dir.
func validRef(ref string) bool {
	if len(ref) != refLen {
		return false
	}
	for _, c := range ref {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
