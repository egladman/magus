//go:build mcp

package mcp

import (
	"encoding/json"
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
	Args    json.RawMessage `json:"args,omitempty"`  // the call's arguments object, verbatim
	DurMs   int64           `json:"dur_ms"`          // wall-clock duration of the call
	Outcome string          `json:"outcome"`         // "ok" or "error"
	Error   string          `json:"error,omitempty"` // error text when Outcome is "error"
}

// auditLog appends auditEvents to an io.Writer, one JSON line each. It mirrors the
// journal FileHandler: a mutex serializes concurrent tool calls (the MCP server handles
// them on separate goroutines) so lines never interleave, and every write is best-effort
// - an audit failure must never fail or slow a tool call. A nil *auditLog is a no-op, so
// callers can hold one unconditionally even when the log could not be opened.
type auditLog struct {
	mu sync.Mutex
	w  io.Writer
	c  io.Closer // non-nil when w is a file we own; nil for an injected writer
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
	return &auditLog{w: f, c: f}
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
