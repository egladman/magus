package trail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/secret"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedEvents writes n events (Ts 1..n) straight into the trail file, bypassing Append so a
// large fixture is one write, not n opens. Returns the events in append order (oldest first).
func seedEvents(t *testing.T, base string, n int) []Event {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(base, dir), 0o755))
	events := make([]Event, 0, n)
	var buf []byte
	for i := 1; i <= n; i++ {
		e := Event{Ts: int64(i), Kind: KindMCPToolCall, Actor: "a", Action: "t", Outcome: OutcomeOK}
		events = append(events, e)
		line, err := json.Marshal(e)
		require.NoError(t, err)
		buf = append(append(buf, line...), '\n')
	}
	require.NoError(t, os.WriteFile(eventsPath(base), buf, 0o644))
	return events
}

func TestAppendAndReadRecent_NewestFirst(t *testing.T) {
	dir := t.TempDir()
	Append(t.Context(), dir, Event{Ts: 1, Kind: KindMCPToolCall, Actor: "a", Action: "query", Outcome: OutcomeOK})
	Append(t.Context(), dir, Event{Ts: 2, Kind: KindTokenLifecycle, Actor: "cli", Action: "connector.create", Outcome: OutcomeOK})
	Append(t.Context(), dir, Event{Ts: 3, Kind: KindJob, Actor: "daemon", Workspace: "/ws", Action: "graph build", Outcome: OutcomeError, Error: "boom"})

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
		Append(t.Context(), dir, Event{Ts: int64(i), Kind: KindMCPToolCall, Action: string(rune('a' + i - 1))})
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
	Append(t.Context(), "", Event{Action: "x"}) // must not panic or create anything
}

func TestAppendAgentCommand_NormalizesHookObservation(t *testing.T) {
	dir := t.TempDir()
	AppendAgentCommand(t.Context(), dir, AgentCommand{
		Actor:     "session:abc123",
		Workspace: "/repo/magus",
		Host:      "codex",
		Session:   "abc123",
		Event:     "PreToolUse",
		Tool:      "Bash",
		Command:   "magus run test .",
		Decision:  "pass",
	})

	events, err := ReadRecent(dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	event := events[0]
	require.Equal(t, KindAgentCommand, event.Kind)
	require.Equal(t, "session:abc123", event.Actor)
	// Host and session ride the event LINE as well as the request blob, so a reader can group a
	// page of observations by host without a blob fetch per row.
	require.Equal(t, "codex", event.Host)
	require.Equal(t, "abc123", event.Session)
	require.Equal(t, "/repo/magus", event.Workspace)
	require.Equal(t, "Bash", event.Action)
	require.Equal(t, OutcomeOK, event.Outcome)
	require.Equal(t, "guard: pass", event.Preview)

	request, err := ReadBlob(dir, event.RequestRef)
	require.NoError(t, err)
	var gotRequest agentCommandRequest
	require.NoError(t, json.Unmarshal(request, &gotRequest))
	require.Equal(t, agentCommandRequest{
		SchemaVersion: agentCommandSchemaVersion,
		Host:          "codex",
		Session:       "abc123",
		Event:         "PreToolUse",
		Tool:          "Bash",
		Command:       "magus run test .",
	}, gotRequest)

	response, err := ReadBlob(dir, event.ResponseRef)
	require.NoError(t, err)
	var gotResponse agentCommandResponse
	require.NoError(t, json.Unmarshal(response, &gotResponse))
	require.Equal(t, agentCommandResponse{SchemaVersion: agentCommandSchemaVersion, Decision: "pass"}, gotResponse)
}

func TestAppendAgentCommand_RequiresCommandOrPath(t *testing.T) {
	dir := t.TempDir()
	AppendAgentCommand(t.Context(), dir, AgentCommand{Tool: "Bash", Decision: "pass"})
	events, err := ReadRecent(dir, 1)
	require.NoError(t, err)
	require.Empty(t, events)
}

func TestAppendAgentCommand_PathUsesFallbackActorAndAction(t *testing.T) {
	dir := t.TempDir()
	AppendAgentCommand(t.Context(), dir, AgentCommand{Path: "AGENTS.md", Decision: "advise", Context: "record the decision"})

	events, err := ReadRecent(dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	event := events[0]
	require.Equal(t, "agent", event.Actor)
	require.Equal(t, "command", event.Action)
	require.Equal(t, "guard: advise", event.Preview)

	request, err := ReadBlob(dir, event.RequestRef)
	require.NoError(t, err)
	var gotRequest agentCommandRequest
	require.NoError(t, json.Unmarshal(request, &gotRequest))
	require.Equal(t, agentCommandRequest{SchemaVersion: agentCommandSchemaVersion, Path: "AGENTS.md"}, gotRequest)

	response, err := ReadBlob(dir, event.ResponseRef)
	require.NoError(t, err)
	var gotResponse agentCommandResponse
	require.NoError(t, json.Unmarshal(response, &gotResponse))
	require.Equal(t, agentCommandResponse{
		SchemaVersion: agentCommandSchemaVersion,
		Decision:      "advise",
		Context:       "record the decision",
	}, gotResponse)
}

func TestWriteBlob_RoundTripAndDedup(t *testing.T) {
	dir := t.TempDir()
	ref, size := WriteBlob(t.Context(), dir, "mcp", []byte("payload one"))
	if ref == "" || size != int64(len("payload one")) || ref[:3] != "mcp" {
		t.Fatalf("WriteBlob = %q,%d", ref, size)
	}
	body, err := ReadBlob(dir, ref)
	if err != nil || string(body) != "payload one" {
		t.Fatalf("ReadBlob = %q, %v", body, err)
	}
	if again, _ := WriteBlob(t.Context(), dir, "mcp", []byte("payload one")); again != ref { // content-addressed dedup
		t.Errorf("dedup failed: %q != %q", again, ref)
	}
}

func TestWriteBlob_RejectsBadPrefixOrEmpty(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{"", "m", "MCP", "toolong99", "a1"} {
		if ref, _ := WriteBlob(t.Context(), dir, bad, []byte("x")); ref != "" {
			t.Errorf("WriteBlob(prefix=%q) = %q, want empty", bad, ref)
		}
	}
	if ref, size := WriteBlob(t.Context(), dir, "mcp", nil); ref != "" || size != 0 {
		t.Errorf("empty data = %q,%d; want \"\",0", ref, size)
	}
	if ref, _ := WriteBlob(t.Context(), "", "mcp", []byte("x")); ref != "" {
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

// backdateBlob rewinds a stored blob's mtime by age, so a test can put it past
// blobGraceWindow deterministically instead of sleeping.
func backdateBlob(t *testing.T, base, ref string, age time.Duration) {
	t.Helper()
	old := time.Now().Add(-age)
	require.NoError(t, os.Chtimes(filepath.Join(blobsPath(base), ref), old, old))
}

func TestRotate_CapsEventsAndGCsOrphanBlobs(t *testing.T) {
	dir := t.TempDir()
	refs := make([]string, 0, 5)
	for i := 1; i <= 5; i++ {
		ref, _ := WriteBlob(t.Context(), dir, "mcp", []byte(string(rune('a'+i-1))+"-body"))
		refs = append(refs, ref)
		Append(t.Context(), dir, Event{Ts: int64(i), Kind: KindMCPToolCall, Action: string(rune('a' + i - 1)), ResponseRef: ref})
		// Past blobGraceWindow, so this test exercises real orphan collection rather
		// than the grace-window protection covered by TestGCBlobs_ProtectsPendingBlob.
		backdateBlob(t, dir, ref, blobGraceWindow+time.Second)
	}

	rotate(dir, 2) // keep the last 2 events

	events, _ := ReadRecent(dir, 10)
	if len(events) != 2 {
		t.Fatalf("after rotate got %d events, want 2", len(events))
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

// TestGCBlobs_ProtectsPendingBlob reproduces the T-1 window: WriteBlob finalizes a blob
// before the producer's Append writes the event referencing it. A rotate landing in that
// gap sees a fresh, unreferenced blob - exactly what gcBlobs must not collect. Bounded and
// deterministic: no sleep, the blob's real age (just-created) is what protects it.
func TestGCBlobs_ProtectsPendingBlob(t *testing.T) {
	dir := t.TempDir()
	ref, _ := WriteBlob(t.Context(), dir, "mcp", []byte("pending payload"))

	gcBlobs(dir, nil) // simulate rotate's events snapshot: taken before Append landed

	if _, err := ReadBlob(dir, ref); err != nil {
		t.Fatalf("gcBlobs collected a blob still within its grace window: %v", err)
	}
}

// TestGCBlobs_CollectsOrphanPastGraceWindow pins the other half of the T-1 fix: the grace
// window only delays collection, it must not disable it, or gcBlobs stops doing its job.
func TestGCBlobs_CollectsOrphanPastGraceWindow(t *testing.T) {
	dir := t.TempDir()
	ref, _ := WriteBlob(t.Context(), dir, "mcp", []byte("truly orphaned"))
	backdateBlob(t, dir, ref, blobGraceWindow+time.Second)

	gcBlobs(dir, nil)

	if _, err := ReadBlob(dir, ref); err == nil {
		t.Fatalf("gcBlobs kept an orphaned blob past its grace window")
	}
}

func TestRotate_UnderCapAndEmptyBaseAreNoops(t *testing.T) {
	dir := t.TempDir()
	Append(t.Context(), dir, Event{Ts: 1, Action: "a"})
	rotate(dir, 10) // under cap: no rewrite
	if evs, _ := ReadRecent(dir, 10); len(evs) != 1 {
		t.Errorf("under-cap rotate changed the trail: %d events", len(evs))
	}
	rotate("", 2) // must not panic
}

func TestRotate_ExportedWrapperUnderCapKeepsAll(t *testing.T) {
	dir := t.TempDir()
	for i := 1; i <= 3; i++ {
		Append(t.Context(), dir, Event{Ts: int64(i), Kind: KindJob, Action: "x"})
	}
	Rotate(dir) // maxEvents is 10000, so this is a no-op that must not lose events
	if evs, _ := ReadRecent(dir, 10); len(evs) != 3 {
		t.Errorf("Rotate under cap changed the trail: got %d events, want 3", len(evs))
	}
}

// TestRotate_TrimsAnOverCapTrail is what the deleted RotateOnCount test used to cover: that a
// rotate actually trims, and to the right window. It reaches Rotate directly now, because the
// daemon's maintenance schedule is the only thing that triggers one - there is no longer a
// write-driven path, and the counter that drove it belonged to a single producer.
func TestRotate_TrimsAnOverCapTrail(t *testing.T) {
	dir := t.TempDir()
	seedEvents(t, dir, maxEvents+5) // over the cap so a rotate is observable

	Rotate(dir)
	got, err := ReadRecent(dir, maxEvents+100)
	require.NoError(t, err)
	require.Len(t, got, maxEvents)
	// Whole-struct assertions on the window edges (ReadRecent is newest-first).
	require.Equal(t, Event{Ts: int64(maxEvents + 5), Kind: KindMCPToolCall, Actor: "a", Action: "t", Outcome: OutcomeOK}, got[0])
	require.Equal(t, Event{Ts: 6, Kind: KindMCPToolCall, Actor: "a", Action: "t", Outcome: OutcomeOK}, got[len(got)-1])
}

// TestRotate_SkipsTheReadWhenTheFileIsTooSmall pins the stat fast path, which is what makes an
// hourly schedule affordable: a trail too small to hold maxEvents events is not read at all.
//
// The bound must stay SOUND rather than approximate - it may only skip when trimming is
// impossible - so this asserts the arithmetic that makes it so: a full cap's worth of the
// smallest event magus can serialize still exceeds the threshold, meaning no reachable trail is
// ever skipped while over cap.
func TestRotate_SkipsTheReadWhenTheFileIsTooSmall(t *testing.T) {
	dir := t.TempDir()
	Append(t.Context(), dir, Event{Ts: 1, Action: "a"})

	info, err := os.Stat(eventsPath(dir))
	require.NoError(t, err)
	require.Less(t, info.Size(), int64(maxEvents)*minEventBytes, "a one-event trail is below the skip threshold")

	Rotate(dir)
	got, err := ReadRecent(dir, 10)
	require.NoError(t, err)
	require.Len(t, got, 1, "the fast path must leave a small trail exactly as it found it")

	// The floor is not a guess: the smallest event that can reach the file - every
	// no-omitempty field empty - must still be at least minEventBytes, or the skip could
	// fire on a trail that genuinely needed trimming.
	line, err := json.Marshal(Event{})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(line)+1, minEventBytes, "minEventBytes must not exceed the smallest serializable event line")
}

func TestRotate_EmptyBaseIsNoop(t *testing.T) {
	Rotate("") // must not panic
}

// TestSelectKept_ReservesAFloorForEveryKind is the governance guarantee: a loud kind must not be
// able to evict a quiet one. Before the floor, rotation kept the newest N lines blind to kind, so
// a burst of agent reads pushed every sandbox_denial out of the window - and gcBlobs then deleted
// their payloads, which is not recoverable.
func TestSelectKept_ReservesAFloorForEveryKind(t *testing.T) {
	var lines []string
	line := func(kind Kind, ts int) string {
		b, err := json.Marshal(Event{Ts: int64(ts), Kind: kind, Actor: "a", Action: "x", Outcome: OutcomeOK})
		require.NoError(t, err)
		return string(b)
	}
	// Three rare governance events, then a flood that would bury them under plain recency.
	for i := 1; i <= 3; i++ {
		lines = append(lines, line(KindSandboxDenial, i))
	}
	for i := 4; i <= 103; i++ {
		lines = append(lines, line(KindAgentCommand, i))
	}

	kept := selectKept(lines, 50)
	require.Len(t, kept, 50)

	var denials int
	for _, l := range kept {
		if strings.Contains(l, string(KindSandboxDenial)) {
			denials++
		}
	}
	assert.Equal(t, 3, denials, "every sandbox_denial survives: there are fewer of them than the floor")

	// Order is preserved - the file is append-ordered and ReadRecent/LastRun depend on it.
	assert.True(t, strings.Contains(kept[0], string(KindSandboxDenial)), "kept output stays oldest-first")
}

// TestSelectKept_SingleKindIsPlainTruncation pins that the floor changes nothing for a trail with
// one producer: the common case must keep behaving exactly as it did before the policy existed.
func TestSelectKept_SingleKindIsPlainTruncation(t *testing.T) {
	var lines []string
	for i := 1; i <= 100; i++ {
		b, err := json.Marshal(Event{Ts: int64(i), Kind: KindMCPToolCall, Actor: "a", Action: "t", Outcome: OutcomeOK})
		require.NoError(t, err)
		lines = append(lines, string(b))
	}
	kept := selectKept(lines, 10)
	require.Len(t, kept, 10)
	assert.Contains(t, kept[0], `"ts":91`)
	assert.Contains(t, kept[9], `"ts":100`)
}

func TestReadRecent_SkipsCorruptLines(t *testing.T) {
	dir := t.TempDir()
	Append(t.Context(), dir, Event{Ts: 1, Kind: KindJob, Action: "good-one"})
	// Splice a non-JSON line into the middle of the trail; ReadRecent must skip it, not fail.
	f, err := os.OpenFile(eventsPath(dir), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open trail: %v", err)
	}
	if _, err := f.WriteString("this is not json\n"); err != nil {
		t.Fatalf("write corrupt line: %v", err)
	}
	f.Close()
	Append(t.Context(), dir, Event{Ts: 2, Kind: KindJob, Action: "good-two"})

	events, err := ReadRecent(dir, 10)
	if err != nil {
		t.Fatalf("ReadRecent: %v", err)
	}
	if len(events) != 2 || events[0].Action != "good-two" || events[1].Action != "good-one" {
		t.Fatalf("corrupt line not skipped cleanly: %+v", events)
	}
}

// TestTrailRedactsThroughContext is the reason these functions grew a ctx parameter.
// The trail is DURABLE and append-only, so a credential landing here sits on disk until
// someone deletes the file - strictly worse than the same value reaching a terminal.
// Blobs are the sharpest edge: an MCP request/response pair is a whole tool payload,
// persisted verbatim, and nothing else on that path scrubs it.
//
// Delete the secret.Redact call in WriteBlob (or redactEvent in Append) and this fails.
func TestTrailRedactsThroughContext(t *testing.T) {
	const credential = "sk-live-must-not-be-persisted"
	base := t.TempDir()

	res := secret.New()
	t.Setenv("TRAIL_TEST_TOKEN", credential)
	ctx := secret.ContextWithResolver(t.Context(), res)
	// Reading is what marks the value as a secret - provenance, not shape.
	got, err := res.Read(ctx, "TRAIL_TEST_TOKEN")
	require.NoError(t, err)
	require.Equal(t, credential, got.Reveal())

	ref, _ := WriteBlob(ctx, base, "mcp", []byte(`{"headers":{"Authorization":"Bearer `+credential+`"}}`))
	require.NotEmpty(t, ref)

	Append(ctx, base, Event{
		Ts:      time.Now().UnixMilli(),
		Kind:    KindAgentCommand,
		Actor:   "agent",
		Action:  "curl -H 'Authorization: Bearer " + credential + "'",
		Outcome: OutcomeOK,
		Preview: "sent " + credential,
		Error:   "failed with " + credential,
	})

	AppendAgentCommand(ctx, base, AgentCommand{
		Actor:   "agent",
		Event:   "pre-tool",
		Tool:    "Bash",
		Command: "deploy --token " + credential,
	})

	// Nothing anywhere under the trail directory may contain the value.
	var found []string
	require.NoError(t, filepath.WalkDir(filepath.Join(base, dir), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(b), credential) {
			found = append(found, path)
		}
		return nil
	}))
	assert.Empty(t, found, "the credential was persisted to the trail in these files")
}

// TestTrailKeepsStructuralFields: redaction must not shred the fields the activity view
// filters on by exact match. Those are enumerated values, identities and content
// addresses - a credential cannot occupy them, and masking them would break the reader
// to protect nothing.
func TestTrailKeepsStructuralFields(t *testing.T) {
	base := t.TempDir()
	res := secret.New()
	t.Setenv("TRAIL_TEST_TOKEN", "sk-live-must-not-be-persisted")
	ctx := secret.ContextWithResolver(t.Context(), res)
	_, err := res.Read(ctx, "TRAIL_TEST_TOKEN")
	require.NoError(t, err)

	Append(ctx, base, Event{
		Ts:        time.Now().UnixMilli(),
		Kind:      KindAgentCommand,
		Actor:     "agent-7",
		Workspace: "/repos/magus",
		Action:    "Bash",
		Outcome:   OutcomeOK,
	})

	b, err := os.ReadFile(filepath.Join(base, dir, eventsFile))
	require.NoError(t, err)
	line := string(b)
	assert.Contains(t, line, "agent-7")
	assert.Contains(t, line, "/repos/magus")
	assert.Contains(t, line, string(KindAgentCommand))
}
