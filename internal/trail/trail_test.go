package trail

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
	l.Record(Event{Ts: 1, Kind: KindMCPToolCall, Actor: "a", Action: "query", Outcome: OutcomeOK})
	l.Record(Event{Ts: 2, Kind: KindTokenLifecycle, Actor: "cli", Action: "connector.create", Outcome: OutcomeOK})
	l.Record(Event{Ts: 3, Kind: KindMCPToolCall, Actor: "a", Action: "run", Outcome: OutcomeError, Error: "boom"})
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
	if events[0].Action != "run" || events[2].Action != "query" { // newest first
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
		l.Record(Event{Ts: int64(i), Kind: KindMCPToolCall, Action: string(rune('a' + i - 1))})
	}
	l.Close()

	events, err := ReadRecent(dir, 2)
	if err != nil {
		t.Fatalf("ReadRecent: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d, want 2 (the tail)", len(events))
	}
	if events[0].Action != "e" || events[1].Action != "d" { // newest first, from the tail
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
	if ref, size := l.PutBlob("mcp", []byte("x")); ref != "" || size != 1 {
		t.Errorf("nil PutBlob = %q,%d; want \"\",1", ref, size)
	}
	if err := l.Close(); err != nil {
		t.Errorf("nil Close: %v", err)
	}
}

func TestBlob_PutGetRoundTripAndDedup(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir)
	ref, size := l.PutBlob("mcp", []byte("payload one"))
	if ref == "" || size != int64(len("payload one")) {
		t.Fatalf("PutBlob = %q,%d", ref, size)
	}
	if ref[:3] != "mcp" {
		t.Errorf("ref %q missing provenance prefix", ref)
	}
	body, err := ReadBlob(dir, ref)
	if err != nil {
		t.Fatalf("ReadBlob: %v", err)
	}
	if string(body) != "payload one" {
		t.Errorf("round trip = %q", body)
	}
	if again, _ := l.PutBlob("mcp", []byte("payload one")); again != ref { // content-addressed dedup
		t.Errorf("dedup failed: %q != %q", again, ref)
	}
}

func TestPrune_CapsEventsAndGCsOrphanBlobs(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir)
	refs := make([]string, 0, 5)
	for i := 1; i <= 5; i++ {
		body := string(rune('a'+i-1)) + "-body"
		ref, _ := l.PutBlob("mcp", []byte(body))
		refs = append(refs, ref)
		l.Record(Event{Ts: int64(i), Kind: KindMCPToolCall, Action: string(rune('a' + i - 1)), Outcome: OutcomeOK, ResponseRef: ref})
	}
	l.Close()

	prune(filepath.Join(dir, Dir), 2) // keep the last 2 events

	events, _ := ReadRecent(dir, 10)
	if len(events) != 2 {
		t.Fatalf("after prune got %d events, want 2", len(events))
	}
	if _, err := ReadBlob(dir, refs[4]); err != nil { // newest kept event's blob survives
		t.Errorf("kept event's blob was GC'd: %v", err)
	}
	if _, err := ReadBlob(dir, refs[0]); err == nil { // oldest, now orphaned, is GC'd
		t.Errorf("orphaned blob not garbage-collected")
	}
}

func TestAppend_StatelessAndReadable(t *testing.T) {
	dir := t.TempDir()
	Append(dir, Event{Ts: 1, Kind: KindJob, Actor: "daemon", Action: "graph build", Outcome: OutcomeOK, DurMs: 50})
	Append(dir, Event{Ts: 2, Kind: KindJob, Actor: "daemon", Action: "reindex", Outcome: OutcomeError, Error: "boom"})

	events, err := ReadRecent(dir, 10)
	if err != nil {
		t.Fatalf("ReadRecent: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if events[0].Action != "reindex" || events[0].Kind != KindJob || events[0].Outcome != OutcomeError {
		t.Errorf("appended event not round-tripped: %+v", events[0])
	}

	// Append coexists with a held Log writing the same trail.
	l := Open(dir)
	l.Record(Event{Ts: 3, Kind: KindMCPToolCall, Action: "q"})
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	events, _ = ReadRecent(dir, 10)
	if len(events) != 3 {
		t.Errorf("Append + Log.Record share the trail: got %d events, want 3", len(events))
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

func TestReadBlob_RejectsUnsafeRefs(t *testing.T) {
	dir := t.TempDir()
	Open(dir) // create the blobs dir
	// None is a valid prefix + exactly 16 hex, so none may reach the filesystem.
	for _, bad := range []string{"", "..", "mcp/../x", "mcpZZZZZZZZZZZZZZZZ", "mcp0123", "MCP0123456789abcd"} {
		if _, err := ReadBlob(dir, bad); err == nil {
			t.Errorf("ReadBlob(%q) = nil error, want rejected", bad)
		}
	}
}
