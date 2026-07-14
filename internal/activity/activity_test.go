package activity

import (
	"path/filepath"
	"testing"
)

func TestLog_RecordAndReadRecent_NewestFirst(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir)
	if l == nil {
		t.Fatal("Open returned nil for a writable dir")
	}
	l.Record(Event{TimeMs: 1, Kind: KindMCPToolCall, Actor: "a", Action: "query", Outcome: OutcomeOK})
	l.Record(Event{TimeMs: 2, Kind: KindTokenLifecycle, Actor: "cli", Action: "connector.create", Outcome: OutcomeOK})
	l.Record(Event{TimeMs: 3, Kind: KindMCPToolCall, Actor: "a", Action: "run", Outcome: OutcomeError, Error: "boom"})
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	events, err := ReadRecent(dir, 10)
	if err != nil {
		t.Fatalf("ReadRecent: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}
	// Newest first.
	if events[0].Action != "run" || events[2].Action != "query" {
		t.Errorf("order = %q..%q, want run..query", events[0].Action, events[2].Action)
	}
	if events[0].Outcome != OutcomeError || events[0].Error != "boom" {
		t.Errorf("error event not round-tripped: %+v", events[0])
	}
}

func TestReadRecent_LimitKeepsTail(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir)
	for i := 1; i <= 5; i++ {
		l.Record(Event{TimeMs: int64(i), Kind: KindMCPToolCall, Action: string(rune('a' + i - 1))})
	}
	l.Close()

	events, err := ReadRecent(dir, 2)
	if err != nil {
		t.Fatalf("ReadRecent: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d, want 2 (the tail)", len(events))
	}
	if events[0].Action != "e" || events[1].Action != "d" {
		t.Errorf("tail = %q,%q, want e,d", events[0].Action, events[1].Action)
	}
}

func TestReadRecent_MissingTrailIsEmpty(t *testing.T) {
	events, err := ReadRecent(t.TempDir(), 10)
	if err != nil {
		t.Fatalf("ReadRecent on empty dir: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("got %d events, want 0", len(events))
	}
}

func TestLog_NilIsNoop(t *testing.T) {
	var l *Log
	l.Record(Event{Action: "x"}) // must not panic
	if ref, n := l.PutBlob("mcp", []byte("x")); ref != "" || n != 1 {
		t.Errorf("nil PutBlob = %q,%d; want \"\",1", ref, n)
	}
	if err := l.Close(); err != nil {
		t.Errorf("nil Close: %v", err)
	}
}

func TestBlob_PutGetRoundTripAndDedup(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir)
	ref, n := l.PutBlob("mcp", []byte("payload one"))
	if ref == "" || n != int64(len("payload one")) {
		t.Fatalf("PutBlob = %q,%d", ref, n)
	}
	if ref[:3] != "mcp" {
		t.Errorf("ref %q missing provenance prefix", ref)
	}
	body, err := Blob(dir, ref)
	if err != nil {
		t.Fatalf("Blob: %v", err)
	}
	if string(body) != "payload one" {
		t.Errorf("round trip = %q", body)
	}
	// Content-addressed: identical bytes dedupe to the same ref.
	if again, _ := l.PutBlob("mcp", []byte("payload one")); again != ref {
		t.Errorf("dedup failed: %q != %q", again, ref)
	}
}

func TestPutBlob_RejectsBadPrefix(t *testing.T) {
	l := Open(t.TempDir())
	for _, bad := range []string{"", "m", "MCP", "toolong99", "a1"} {
		if ref, _ := l.PutBlob(bad, []byte("x")); ref != "" {
			t.Errorf("PutBlob(prefix=%q) = %q, want empty", bad, ref)
		}
	}
}

func TestBlob_RejectsUnsafeRefs(t *testing.T) {
	dir := t.TempDir()
	Open(dir) // create the blobs dir
	// None is a valid prefix + exactly 16 hex, so none may reach the filesystem.
	for _, bad := range []string{"", "..", "mcp/../x", "mcpZZZZZZZZZZZZZZZZ", "mcp0123", "MCP0123456789abcd", filepath.Join("mcp0123456789abcd")} {
		if _, err := Blob(dir, bad); err == nil {
			t.Errorf("Blob(%q) = nil error, want rejected", bad)
		}
	}
}
