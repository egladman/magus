package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditLog_RecordsJSONL(t *testing.T) {
	var buf bytes.Buffer
	a := newAuditLog(&buf)
	a.record(auditEvent{Ts: 1000, Agent: "claude", Tool: "query", Args: json.RawMessage(`{"q":"ref1a2b"}`), DurMs: 12, Outcome: "ok"})
	a.record(auditEvent{Ts: 2000, Agent: "claude", Tool: "run_target", DurMs: 400, Outcome: "error", Error: "boom"})

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 JSONL lines, got %d: %q", len(lines), buf.String())
	}

	var first auditEvent
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("unmarshal line 0: %v", err)
	}
	want := auditEvent{Ts: 1000, Agent: "claude", Tool: "query", Args: json.RawMessage(`{"q":"ref1a2b"}`), DurMs: 12, Outcome: "ok"}
	if first.Ts != want.Ts || first.Agent != want.Agent || first.Tool != want.Tool || first.DurMs != want.DurMs || first.Outcome != want.Outcome || string(first.Args) != string(want.Args) {
		t.Errorf("line 0 = %+v, want %+v", first, want)
	}

	// The error line carries outcome+error and omits the empty args field.
	if !strings.Contains(lines[1], `"outcome":"error"`) || !strings.Contains(lines[1], `"error":"boom"`) {
		t.Errorf("line 1 missing error fields: %q", lines[1])
	}
	if strings.Contains(lines[1], `"args"`) {
		t.Errorf("line 1 should omit empty args: %q", lines[1])
	}
}

func TestAuditLog_NilIsNoop(t *testing.T) {
	var a *auditLog
	a.record(auditEvent{Tool: "x"}) // must not panic
	if err := a.Close(); err != nil {
		t.Errorf("nil Close: %v", err)
	}
}

func TestOpenAuditLog_WritesUnderCacheDir(t *testing.T) {
	dir := t.TempDir()
	a := openAuditLog(dir)
	if a == nil {
		t.Fatal("openAuditLog returned nil for a writable dir")
	}
	a.record(auditEvent{Ts: 1, Agent: "a", Tool: "status", Outcome: "ok"})
	if err := a.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	path := filepath.Join(dir, AuditDir, "mcp.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	if !strings.Contains(string(data), `"tool":"status"`) {
		t.Errorf("audit file missing record: %q", string(data))
	}
}

func TestOpenAuditLog_EmptyDirIsNil(t *testing.T) {
	if a := openAuditLog(""); a != nil {
		t.Errorf("openAuditLog(\"\") = %v, want nil no-op", a)
	}
}

func TestAuditArgs_OmitsEmpty(t *testing.T) {
	if got := auditArgs(nil); got != nil {
		t.Errorf("auditArgs(nil) = %q, want nil", got)
	}
	if got := auditArgs(map[string]any{}); got != nil {
		t.Errorf("auditArgs(empty) = %q, want nil", got)
	}
	got := auditArgs(map[string]any{"ref": "ref1a2b"})
	if string(got) != `{"ref":"ref1a2b"}` {
		t.Errorf("auditArgs = %q", string(got))
	}
}

func TestAuditBlobs_PutGetRoundTrip(t *testing.T) {
	b := openAuditBlobs(t.TempDir())
	if b == nil {
		t.Fatal("openAuditBlobs returned nil for a writable dir")
	}
	ref := b.put([]byte("payload one"))
	if ref == "" {
		t.Fatal("put returned an empty ref")
	}
	body, err := b.get(ref)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(body) != "payload one" {
		t.Errorf("round trip = %q, want %q", body, "payload one")
	}
}

func TestAuditBlobs_DedupesByContent(t *testing.T) {
	b := openAuditBlobs(t.TempDir())
	r1 := b.put([]byte("same"))
	r2 := b.put([]byte("same"))
	if r1 != r2 {
		t.Errorf("identical content got different refs: %q vs %q", r1, r2)
	}
	if r3 := b.put([]byte("different")); r3 == r1 {
		t.Errorf("different content collided on ref %q", r3)
	}
}

func TestAuditBlobs_EmptyAndNilAreNoOps(t *testing.T) {
	b := openAuditBlobs(t.TempDir())
	if got := b.put(nil); got != "" {
		t.Errorf("put(nil) = %q, want empty", got)
	}
	if got := b.put([]byte{}); got != "" {
		t.Errorf("put(empty) = %q, want empty", got)
	}
	var nilStore *auditBlobs
	if got := nilStore.put([]byte("x")); got != "" {
		t.Errorf("nil store put = %q, want empty", got)
	}
}

func TestAuditBlobs_GetRejectsUnsafeRefs(t *testing.T) {
	b := openAuditBlobs(t.TempDir())
	// None of these is exactly 16 lowercase-hex chars, so none may reach the filesystem.
	for _, bad := range []string{"", "..", "../mcp.jsonl", "abc/def0123456789", "ABCDEF0123456789", "short", "zzzzzzzzzzzzzzzz"} {
		if _, err := b.get(bad); err == nil {
			t.Errorf("get(%q) = nil error, want rejected", bad)
		}
	}
}

func TestPreview(t *testing.T) {
	if got := preview("hello", 10); got != "hello" {
		t.Errorf("short preview = %q, want %q", got, "hello")
	}
	if got := preview("hello world", 5); got != "hello..." {
		t.Errorf("truncated preview = %q, want %q", got, "hello...")
	}
	if got := preview("abc", 0); got != "" {
		t.Errorf("zero-max preview = %q, want empty", got)
	}
	// Rune-aware: cutting at 2 keeps two full runes, never splitting the multibyte one.
	if got := preview("héllo", 2); got != "hé..." {
		t.Errorf("rune preview = %q, want %q", got, "hé...")
	}
}
