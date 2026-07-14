package trail

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppendAndReadRecent_NewestFirst(t *testing.T) {
	dir := t.TempDir()
	Append(dir, Event{Ts: 1, Kind: KindMCPToolCall, Actor: "a", Action: "query", Outcome: OutcomeOK})
	Append(dir, Event{Ts: 2, Kind: KindTokenLifecycle, Actor: "cli", Action: "connector.create", Outcome: OutcomeOK})
	Append(dir, Event{Ts: 3, Kind: KindJob, Actor: "daemon", Workspace: "/ws", Action: "graph build", Outcome: OutcomeError, Error: "boom"})

	events, err := ReadRecent(dir, 10)
	if err != nil {
		t.Fatalf("ReadRecent: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}
	if events[0].Action != "graph build" || events[2].Action != "query" { // newest first
		t.Errorf("order = %q..%q, want graph build..query", events[0].Action, events[2].Action)
	}
	if events[0].Outcome != OutcomeError || events[0].Error != "boom" || events[0].Workspace != "/ws" {
		t.Errorf("event not round-tripped: %+v", events[0])
	}
}

func TestReadRecent_LimitKeepsTailNewestFirst(t *testing.T) {
	dir := t.TempDir()
	for i := 1; i <= 5; i++ {
		Append(dir, Event{Ts: int64(i), Kind: KindMCPToolCall, Action: string(rune('a' + i - 1))})
	}
	events, err := ReadRecent(dir, 2)
	if err != nil {
		t.Fatalf("ReadRecent: %v", err)
	}
	if len(events) != 2 || events[0].Action != "e" || events[1].Action != "d" {
		t.Fatalf("tail = %+v, want [e d]", events)
	}
}

func TestReadRecent_MissingOrEmptyBase(t *testing.T) {
	if evs, err := ReadRecent(t.TempDir(), 10); err != nil || len(evs) != 0 {
		t.Errorf("missing trail: got %d events, err %v; want 0, nil", len(evs), err)
	}
	if evs, err := ReadRecent("", 10); err != nil || evs != nil {
		t.Errorf("empty base: got %v, %v; want nil, nil", evs, err)
	}
	if evs, err := ReadRecent(t.TempDir(), 0); err != nil || evs != nil {
		t.Errorf("zero limit: got %v, %v; want nil, nil", evs, err)
	}
}

func TestAppend_EmptyBaseIsNoop(t *testing.T) {
	Append("", Event{Action: "x"}) // must not panic or create anything
}

func TestWriteBlob_RoundTripAndDedup(t *testing.T) {
	dir := t.TempDir()
	ref, size := WriteBlob(dir, "mcp", []byte("payload one"))
	if ref == "" || size != int64(len("payload one")) || ref[:3] != "mcp" {
		t.Fatalf("WriteBlob = %q,%d", ref, size)
	}
	body, err := ReadBlob(dir, ref)
	if err != nil || string(body) != "payload one" {
		t.Fatalf("ReadBlob = %q, %v", body, err)
	}
	if again, _ := WriteBlob(dir, "mcp", []byte("payload one")); again != ref { // content-addressed dedup
		t.Errorf("dedup failed: %q != %q", again, ref)
	}
}

func TestWriteBlob_RejectsBadPrefixOrEmpty(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{"", "m", "MCP", "toolong99", "a1"} {
		if ref, _ := WriteBlob(dir, bad, []byte("x")); ref != "" {
			t.Errorf("WriteBlob(prefix=%q) = %q, want empty", bad, ref)
		}
	}
	if ref, size := WriteBlob(dir, "mcp", nil); ref != "" || size != 0 {
		t.Errorf("empty data = %q,%d; want \"\",0", ref, size)
	}
	if ref, _ := WriteBlob("", "mcp", []byte("x")); ref != "" {
		t.Errorf("empty base = %q, want empty", ref)
	}
}

func TestReadBlob_RejectsUnsafeRefs(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{"", "..", "mcp/../x", "mcpZZZZZZZZZZZZZZZZ", "mcp0123", "MCP0123456789abcd"} {
		if _, err := ReadBlob(dir, bad); err == nil {
			t.Errorf("ReadBlob(%q) = nil error, want rejected", bad)
		}
	}
}

func TestPrune_CapsEventsAndGCsOrphanBlobs(t *testing.T) {
	dir := t.TempDir()
	refs := make([]string, 0, 5)
	for i := 1; i <= 5; i++ {
		ref, _ := WriteBlob(dir, "mcp", []byte(string(rune('a'+i-1))+"-body"))
		refs = append(refs, ref)
		Append(dir, Event{Ts: int64(i), Kind: KindMCPToolCall, Action: string(rune('a' + i - 1)), ResponseRef: ref})
	}

	prune(dir, 2) // keep the last 2 events

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

	// A temp file left by an in-flight WriteBlob (its name fails validRef) survives GC.
	tmp := filepath.Join(blobsPath(dir), "mcp0123456789abcd.tmp999")
	if err := os.WriteFile(tmp, []byte("x"), 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	gcBlobs(dir, nil) // nothing referenced: valid-ref blobs go, the temp file stays
	if _, err := os.Stat(tmp); err != nil {
		t.Errorf("gcBlobs deleted a non-ref temp file: %v", err)
	}
}

func TestPrune_UnderCapAndEmptyBaseAreNoops(t *testing.T) {
	dir := t.TempDir()
	Append(dir, Event{Ts: 1, Action: "a"})
	prune(dir, 10) // under cap: no rewrite
	if evs, _ := ReadRecent(dir, 10); len(evs) != 1 {
		t.Errorf("under-cap prune changed the trail: %d events", len(evs))
	}
	prune("", 2) // must not panic
}
