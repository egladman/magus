package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	json "github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppendReadRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	w, err := Open(dir, "sess1", SessionStart{Workspace: "/repo", Command: "run build"})
	require.NoError(t, err)
	require.NoError(t, w.Append(KindTargetResult, TargetResult{Target: "build", Project: "api", Outcome: OutcomePass, DurMs: 12}))

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	require.Len(t, fold.Records, 2, "the first append also writes the session-start record")
	assert.Zero(t, fold.Skipped)
	assert.Equal(t, 1, fold.Sessions)

	assert.Equal(t, Record{V: SchemaVersion, Session: "sess1", Seq: 1, Kind: KindSessionStart, Ts: fold.Records[0].Ts, Payload: fold.Records[0].Payload}, fold.Records[0])
	assert.Equal(t, uint64(2), fold.Records[1].Seq)

	var got TargetResult
	require.NoError(t, json.Unmarshal(fold.Records[1].Payload, &got))
	assert.Equal(t, TargetResult{Target: "build", Project: "api", Outcome: OutcomePass, DurMs: 12}, got)
}

// Open must not create the file: a magus command that records no fact should leave
// no session behind, or `magus session` fills up with runs that did nothing.
func TestOpenWritesNothingUntilTheFirstFact(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	w, err := Open(dir, "sess1", SessionStart{})
	require.NoError(t, err)

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	assert.Empty(t, fold.Records)
	assert.Zero(t, fold.Sessions)

	require.NoError(t, w.Append(KindTargetResult, TargetResult{Target: "test", Outcome: OutcomePass}))
	fold, err = ReadAll(dir)
	require.NoError(t, err)
	assert.Len(t, fold.Records, 2)
}

// A message is whatever a hook piped to `magus session notify`, so an unbounded one is one
// process's decision that every later read of this repository pays for. The bound is
// applied at write time, which is what makes it hold for a producer that never heard
// of it.
func TestAppendBoundsAnOversizedAttentionMessage(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	w, err := Open(dir, "sess1", SessionStart{})
	require.NoError(t, err)
	require.NoError(t, w.Append(KindAttentionOpen, AttentionOpen{
		Request: "att-1",
		Outcome: "waiting",
		Message: strings.Repeat("x", MaxMessageBytes*4),
	}))

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	require.Len(t, fold.Records, 2)

	var got AttentionOpen
	require.NoError(t, json.Unmarshal(fold.Records[1].Payload, &got))
	// The marker is what lets a reader tell a short message from a long one nobody kept.
	assert.Equal(t, strings.Repeat("x", MaxMessageBytes)+messageTruncated, got.Message)
}

func TestBoundedMessageCutsOnARuneBoundary(t *testing.T) {
	t.Parallel()

	// U+4E16 encodes to three bytes, and the bound is not a multiple of three, so the
	// cut lands mid-rune unless it is walked back.
	cut := AttentionOpen{Message: strings.Repeat(string(rune(0x4e16)), MaxMessageBytes)}.bounded()
	assert.True(t, utf8.ValidString(cut.Message), "a mid-rune cut stores bytes no reader can decode")
	assert.LessOrEqual(t, len(cut.Message), MaxMessageBytes+len(messageTruncated))

	short := AttentionOpen{Request: "att-1", Message: "needs a decision"}
	assert.Equal(t, short, short.bounded(), "a message inside the bound is stored verbatim")
}

// One oversized line must cost one record and nothing else. A bufio.Scanner abandons
// the rest of the file on ErrTooLong, which hides every LATER fact.
func TestReadAllSkipsAnOversizedLineAndKeepsReading(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "sess1.jsonl")

	body := strings.Join([]string{
		`{"v":1,"session":"sess1","seq":1,"kind":"target_result","ts":1,"payload":{"target":"build","outcome":"pass"}}`,
		`{"v":1,"session":"sess1","seq":2,"kind":"target_result","ts":2,"payload":{"target":"` + strings.Repeat("x", maxLineBytes*2) + `"}}`,
		`{"v":1,"session":"sess1","seq":3,"kind":"target_result","ts":3,"payload":{"target":"lint","outcome":"pass"}}`,
	}, "\n")
	require.NoError(t, os.WriteFile(path, []byte(body+"\n"), 0o644))

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	assert.Equal(t, 1, fold.Skipped)
	require.Len(t, fold.Records, 2)
	assert.Equal(t, uint64(1), fold.Records[0].Seq)
	assert.Equal(t, uint64(3), fold.Records[1].Seq, "the record after the oversized line is the one a scanner loses")
}

// The composed case: a request opened on an oversized line is unreadable, and the
// normal open after it must still reach the queue. A file that hid its own tail would
// also defeat attention dedupe, because dedupe IS a search for a later record.
//
// Both lines are written raw, because the write-time bound means no current magus
// produces the first one - an older binary, or a hand-edited file, does.
func TestOversizedOpenDoesNotHideALaterOne(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "sess1.jsonl")

	body := strings.Join([]string{
		`{"v":1,"session":"sess1","seq":1,"kind":"attention_open","ts":1,"payload":{"request":"att-huge","outcome":"waiting","message":"` + strings.Repeat("x", maxLineBytes*2) + `"}}`,
		`{"v":1,"session":"sess1","seq":2,"kind":"attention_open","ts":2,"payload":{"request":"att-small","outcome":"waiting","message":"needs a decision"}}`,
	}, "\n")
	require.NoError(t, os.WriteFile(path, []byte(body+"\n"), 0o644))

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	assert.Equal(t, 1, fold.Skipped)

	open := AttentionQueue(fold)
	require.Len(t, open, 1, "the open after the oversized line must still fold")
	assert.Equal(t, "att-small", open[0].ID)
	assert.Equal(t, "needs a decision", open[0].Message)
}

// A session id is reached twice whenever it is reused - a resumed agent session, a
// retried command - and the second writer has to continue the numbering rather than
// restart it, or (Session, Seq) stops identifying one record.
func TestOpenResumesTheSequenceOfAnExistingSession(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	first, err := Open(dir, "sess1", SessionStart{Command: "run build"})
	require.NoError(t, err)
	require.NoError(t, first.Append(KindTargetResult, TargetResult{Target: "build", Outcome: OutcomePass}))
	require.NoError(t, first.Append(KindTargetResult, TargetResult{Target: "test", Outcome: OutcomePass}))

	second, err := Open(dir, "sess1", SessionStart{Command: "run lint"})
	require.NoError(t, err)
	require.NoError(t, second.Append(KindTargetResult, TargetResult{Target: "lint", Outcome: OutcomePass}))

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	require.Len(t, fold.Records, 5, "each writer declares its own command line")

	var seqs []uint64
	for _, rec := range fold.Records {
		seqs = append(seqs, rec.Seq)
	}
	assert.Equal(t, []uint64{1, 2, 3, 4, 5}, seqs, "the second writer continues the numbering instead of reusing seq 1")
	assert.Equal(t, KindSessionStart, fold.Records[0].Kind)
	assert.Equal(t, KindSessionStart, fold.Records[3].Kind)
}

func TestOpenRejectsASessionIDThatCouldEscapeTheStore(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"", "../escape", "a/b", "."} {
		_, err := Open(t.TempDir(), id, SessionStart{})
		require.Error(t, err, "session id %q", id)
		assert.Contains(t, err.Error(), "sessions:")
	}
}

// Two writers with distinct session ids in one store are both visible to one fold.
// This is the two-worktree done-check in miniature: the worktrees differ, the store
// does not, so each sees the other's facts.
func TestFoldSeesEveryWriterInTheStore(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	left, err := Open(dir, "wt-left", SessionStart{Workspace: "/repo/a"})
	require.NoError(t, err)
	right, err := Open(dir, "wt-right", SessionStart{Workspace: "/repo/b"})
	require.NoError(t, err)

	require.NoError(t, left.Append(KindTargetResult, TargetResult{Target: "build", Outcome: OutcomePass}))
	require.NoError(t, right.Append(KindTargetResult, TargetResult{Target: "test", Outcome: OutcomeFail}))
	require.NoError(t, left.Append(KindTargetResult, TargetResult{Target: "lint", Outcome: OutcomePass}))

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	assert.Equal(t, 2, fold.Sessions)
	assert.Len(t, fold.Records, 5)

	seen := map[string]int{}
	for _, rec := range fold.Records {
		seen[rec.Session]++
	}
	assert.Equal(t, map[string]int{"wt-left": 3, "wt-right": 2}, seen)

	sessions := Summarize(fold)
	require.Len(t, sessions, 2)
	byID := map[string]Summary{}
	for _, s := range sessions {
		byID[s.Session] = s
	}
	assert.Equal(t, "/repo/a", byID["wt-left"].Workspace)
	assert.Equal(t, "/repo/b", byID["wt-right"].Workspace)
	assert.Len(t, byID["wt-left"].Targets, 2)
	assert.Len(t, byID["wt-right"].Targets, 1)
}

// Ordering is by (Ts, Session, Seq). The tie-break matters more than the timestamp:
// two worktrees appending in one millisecond must still fold to one stable order.
func TestFoldOrdersByTimeThenSessionThenSeq(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	writeRaw(t, dir, "beta", []Record{
		{V: 1, Session: "beta", Seq: 1, Kind: KindTargetResult, Ts: 100},
		{V: 1, Session: "beta", Seq: 2, Kind: KindTargetResult, Ts: 100},
	})
	writeRaw(t, dir, "alpha", []Record{
		{V: 1, Session: "alpha", Seq: 1, Kind: KindTargetResult, Ts: 100},
		{V: 1, Session: "alpha", Seq: 2, Kind: KindTargetResult, Ts: 300},
	})

	fold, err := ReadAll(dir)
	require.NoError(t, err)

	var order []string
	for _, rec := range fold.Records {
		order = append(order, rec.Session+"/"+string(rune('0'+rec.Seq)))
	}
	assert.Equal(t, []string{"alpha/1", "beta/1", "beta/2", "alpha/2"}, order)
}

// A session file is written by a process that can be killed mid-line, so a truncated
// tail is expected. The read must surrender the bad line and nothing else.
func TestReadAllToleratesACorruptTail(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	w, err := Open(dir, "sess1", SessionStart{})
	require.NoError(t, err)
	require.NoError(t, w.Append(KindTargetResult, TargetResult{Target: "build", Outcome: OutcomePass}))

	path := filepath.Join(dir, "sess1.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(`{"v":1,"session":"sess1","seq":3,"kind":"target_res`)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	fold, err := ReadAll(dir)
	require.NoError(t, err, "a half-written line must never fail the read")
	assert.Len(t, fold.Records, 2)
	assert.Equal(t, 1, fold.Skipped)

	// The session survives the damage rather than vanishing with it.
	sessions := Summarize(fold)
	require.Len(t, sessions, 1)
	assert.Len(t, sessions[0].Targets, 1)
}

func TestReadAllCountsEveryUnusableLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "sess1.jsonl")
	body := strings.Join([]string{
		`{"v":1,"session":"sess1","seq":1,"kind":"target_result","ts":1}`,
		`not json at all`,
		``, // a blank line is not damage; it is skipped without counting
		`{"v":1,"seq":2,"kind":"target_result","ts":2}`, // no session: unattributable
		`{"v":1,"session":"sess1","seq":3,"ts":3}`,      // no kind: uninterpretable
	}, "\n")
	require.NoError(t, os.WriteFile(path, []byte(body+"\n"), 0o644))

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	assert.Len(t, fold.Records, 1)
	assert.Equal(t, 3, fold.Skipped)
}

// An unknown kind and an unknown payload field are what a NEWER magus writes. An
// older reader must show less, never fail, and must not drop the session.
func TestUnknownKindsAndFieldsAreIgnoredNotRejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "sess1.jsonl")
	body := strings.Join([]string{
		`{"v":1,"session":"sess1","seq":1,"kind":"session_start","ts":10,"payload":{"workspace":"/repo","invented_field":"ignored"}}`,
		`{"v":9,"session":"sess1","seq":2,"kind":"attention_requested","ts":20,"payload":{"whatever":true}}`,
		`{"v":1,"session":"sess1","seq":3,"kind":"target_result","ts":30,"payload":{"target":"build","outcome":"pass","future":1}}`,
	}, "\n")
	require.NoError(t, os.WriteFile(path, []byte(body+"\n"), 0o644))

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	assert.Len(t, fold.Records, 3)
	assert.Zero(t, fold.Skipped, "an unknown kind is not damage")

	sessions := Summarize(fold)
	require.Len(t, sessions, 1)
	assert.Equal(t, "/repo", sessions[0].Workspace)
	assert.Equal(t, 3, sessions[0].Facts, "an unrecognized fact still counts as activity")
	assert.Equal(t, int64(30), sessions[0].LastMs, "an unrecognized fact still advances the clock")
	require.Len(t, sessions[0].Targets, 1)
	assert.Equal(t, "build", sessions[0].Targets[0].Target)
}

func TestSummarizeOrdersMostRecentFirst(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRaw(t, dir, "old", []Record{{V: 1, Session: "old", Seq: 1, Kind: KindTargetResult, Ts: 100}})
	writeRaw(t, dir, "new", []Record{{V: 1, Session: "new", Seq: 1, Kind: KindTargetResult, Ts: 900}})
	writeRaw(t, dir, "mid", []Record{{V: 1, Session: "mid", Seq: 1, Kind: KindTargetResult, Ts: 500}})

	fold, err := ReadAll(dir)
	require.NoError(t, err)

	var ids []string
	for _, s := range Summarize(fold) {
		ids = append(ids, s.Session)
	}
	assert.Equal(t, []string{"new", "mid", "old"}, ids)
}

// The lease rides BOTH payloads, and the target result is the one that matters: a reader joining
// one fact to a lease does not hold the session-start record that opened the file.
func TestLeaseSurvivesTheStoreOnBothPayloads(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	w, err := Open(dir, "sess1", SessionStart{Workspace: "/repo", Command: "run build", Lease: "fleet/f3"})
	require.NoError(t, err)
	require.NoError(t, w.Append(KindTargetResult, TargetResult{Target: "build", Outcome: OutcomePass, Lease: "fleet/f3"}))

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	require.Len(t, fold.Records, 2)

	var start SessionStart
	require.NoError(t, json.Unmarshal(fold.Records[0].Payload, &start))
	assert.Equal(t, "fleet/f3", start.Lease)

	var result TargetResult
	require.NoError(t, json.Unmarshal(fold.Records[1].Payload, &result))
	assert.Equal(t, "fleet/f3", result.Lease)

	sessions := Summarize(fold)
	require.Len(t, sessions, 1)
	assert.Equal(t, "fleet/f3", sessions[0].Lease, "the view reads the lease off the session, not off each fact")
}

// The trace context survives the store and reaches the view, so a child session's parent span can
// be joined to the session that minted it. The ancestry is that relation and nothing else: no
// record holds a chain, the way no process holds its ancestors' pids.
func TestTraceContextSurvivesTheStoreAndSummarizes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	parent := SessionStart{Workspace: "/repo", Command: "run build", SpanID: "00f067aa0ba902b7"}
	child := SessionStart{
		Workspace:    "/repo",
		Command:      "run test",
		Lease:        "fleet/f3",
		TraceID:      "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:       "a1b2c3d4e5f60718",
		ParentSpanID: "00f067aa0ba902b7",
		Spawner:      "claude code",
	}
	for id, start := range map[string]SessionStart{"sess1": parent, "sess2": child} {
		w, err := Open(dir, id, start)
		require.NoError(t, err)
		require.NoError(t, w.Append(KindTargetResult, TargetResult{Target: "build", Outcome: OutcomePass}))
	}

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	byID := make(map[string]Summary)
	for _, s := range Summarize(fold) {
		byID[s.Session] = s
	}
	require.Len(t, byID, 2)

	assert.Equal(t, child.TraceID, byID["sess2"].TraceID)
	assert.Equal(t, child.Spawner, byID["sess2"].Spawner)
	assert.Equal(t, byID["sess1"].SpanID, byID["sess2"].ParentSpanID, "the child names the span the parent minted")
	assert.Empty(t, byID["sess1"].ParentSpanID, "a session nothing spawned claims no parent")
}

// The field is additive: a session file written before it existed still reads, and the sessions in it
// summarize as unattributed rather than as damage.
func TestASessionWrittenWithoutALeaseStillReads(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "sess1.jsonl")
	body := strings.Join([]string{
		`{"v":1,"session":"sess1","seq":1,"kind":"session_start","ts":10,"payload":{"workspace":"/repo"}}`,
		`{"v":1,"session":"sess1","seq":2,"kind":"target_result","ts":20,"payload":{"target":"build","outcome":"pass"}}`,
	}, "\n")
	require.NoError(t, os.WriteFile(path, []byte(body+"\n"), 0o644))

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	assert.Zero(t, fold.Skipped)

	sessions := Summarize(fold)
	require.Len(t, sessions, 1)
	assert.Empty(t, sessions[0].Lease)
	require.Len(t, sessions[0].Targets, 1)
	assert.Empty(t, sessions[0].Targets[0].Lease)
}

func TestReadAllMissingStoreIsEmptyNotAnError(t *testing.T) {
	t.Parallel()
	fold, err := ReadAll(filepath.Join(t.TempDir(), "never-created"))
	require.NoError(t, err, "no session has run yet is not a failure")
	assert.Empty(t, fold.Records)
	assert.Zero(t, fold.Sessions)
}

// Worktrees of one repo must resolve to one store, or a fact recorded in one is
// invisible from the other and the whole feature is pointless.
func TestDirIsSharedAcrossWorktreesOfOneRepo(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	main := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(main, ".git", "worktrees", "feature"), 0o755))

	linked := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(linked, ".git"),
		[]byte("gitdir: "+filepath.Join(main, ".git", "worktrees", "feature")+"\n"), 0o644))

	mainDir, err := Dir(main)
	require.NoError(t, err)
	linkedDir, err := Dir(linked)
	require.NoError(t, err)
	assert.Equal(t, mainDir, linkedDir)

	unrelated, err := Dir(t.TempDir())
	require.NoError(t, err)
	assert.NotEqual(t, mainDir, unrelated)
}

func TestDirIsUnderTheUserStateDir(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)

	dir, err := Dir(t.TempDir())
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(dir, filepath.Join(state, "magus", "sessions")),
		"store %s is not under the user state dir", dir)
}

// writeRaw lays down a session file directly, so a test can pin timestamps and
// sequence numbers the Writer would otherwise stamp from the clock.
func writeRaw(t *testing.T, dir, session string, records []Record) {
	t.Helper()
	var b strings.Builder
	for _, rec := range records {
		line, err := json.Marshal(rec)
		require.NoError(t, err)
		b.Write(line)
		b.WriteByte('\n')
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, session+fileExt), []byte(b.String()), 0o644))
}
