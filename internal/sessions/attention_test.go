package sessions

import (
	"testing"

	json "github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// attRecord builds one session record with an explicit timestamp. The fold's rules
// turn on the ORDER records arrive in, and a writer stamps wall-clock milliseconds,
// so two appends in one millisecond would order by session name instead - which is
// the wrong axis to hang a first-dispose-wins test on.
func attRecord(t *testing.T, session string, seq uint64, ts int64, kind string, payload any) Record {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	return Record{V: SchemaVersion, Session: session, Seq: seq, Kind: kind, Ts: ts, Payload: raw}
}

func openPayload(id, message string) AttentionOpen {
	return AttentionOpen{Request: id, Outcome: "waiting", Severity: "warning", Source: "agent/claude", Where: "/repo", Message: message}
}

func TestRequestIDIsStableAndFieldSafe(t *testing.T) {
	t.Parallel()

	block := AttentionOpen{Source: "agent/claude", Where: "/repo", Message: "needs a decision"}
	id := RequestID("sess1", block)
	assert.Equal(t, id, RequestID("sess1", block),
		"the same block must re-derive the same id, or a re-firing agent opens a second request")
	assert.NotEqual(t, id, RequestID("sess2", block))
	assert.Regexp(t, `^att-[0-9a-f]{12}$`, id)

	// Field boundaries are real: shifting a character across one must not produce the
	// same digest.
	assert.NotEqual(t,
		RequestID("a", AttentionOpen{Source: "b", Where: "c", Message: "d"}),
		RequestID("ab", AttentionOpen{Where: "c", Message: "d"}))
}

// An id is a wire value: it is printed, disposed of by hand, and re-derived by every
// producer that re-fires a block. Digesting different bytes would orphan every open
// request in every repository at once, with no error anywhere to say so.
//
// These vectors are the digests the four-string RequestID produced before it took the
// payload instead, so they fail if this form feeds the hash anything different.
func TestRequestIDDigestsTheSameBytesAsTheFourArgumentForm(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "att-4003a0391273",
		RequestID("sess1", AttentionOpen{Source: "agent/claude", Where: "/repo", Message: "needs a decision"}))
	assert.Equal(t, "att-1c6d2c629024",
		RequestID("agent1", AttentionOpen{Source: "agent/claude", Where: "/repo", Message: "needs approval to push"}))
	assert.Equal(t, "att-cf92326f85f5",
		RequestID("a", AttentionOpen{Source: "b", Where: "c", Message: "d"}))

	// Only Source, Where and Message are read. A payload field the digest must ignore
	// is now one struct hop from one it must not, so the two are pinned apart here.
	assert.Equal(t, "att-4003a0391273",
		RequestID("sess1", AttentionOpen{
			Request:    "att-whatever",
			Outcome:    "permission",
			Severity:   "critical",
			Delegation: "fleet/f9",
			Source:     "agent/claude",
			Where:      "/repo",
			Message:    "needs a decision",
		}))
}

func TestAttentionOpenAppearsInTheQueue(t *testing.T) {
	t.Parallel()

	fold := Fold{Records: []Record{
		attRecord(t, "agent1", 1, 100, KindAttentionOpen, openPayload("att-1", "waiting on a decision")),
	}}

	open := AttentionQueue(fold)
	require.Len(t, open, 1)
	assert.Equal(t, AttentionRequest{
		ID:       "att-1",
		Session:  "agent1",
		OpenedMs: 100,
		Outcome:  "waiting",
		Severity: "warning",
		Source:   "agent/claude",
		Where:    "/repo",
		Message:  "waiting on a decision",
	}, open[0])
}

func TestAttentionDisposeEmptiesTheQueue(t *testing.T) {
	t.Parallel()

	fold := Fold{Records: []Record{
		attRecord(t, "agent1", 1, 100, KindAttentionOpen, openPayload("att-1", "waiting on a decision")),
		attRecord(t, "human1", 1, 200, KindAttentionDispose, AttentionDispose{Request: "att-1", Note: "approved by hand"}),
	}}

	assert.Empty(t, AttentionQueue(fold))

	all := Attention(fold)
	require.Len(t, all, 1, "a disposed request is closed, never deleted")
	assert.True(t, all[0].Disposed)
	assert.Equal(t, int64(200), all[0].DisposedMs)
	assert.Equal(t, "human1", all[0].DisposedBy)
	assert.Equal(t, "approved by hand", all[0].Note)
	assert.Equal(t, 1, all[0].Disposes)
}

func TestAttentionCollapsesARepeatedOpenIntoOneRequest(t *testing.T) {
	t.Parallel()

	fold := Fold{Records: []Record{
		attRecord(t, "agent1", 1, 100, KindAttentionOpen, openPayload("att-1", "waiting on a decision")),
		attRecord(t, "agent1", 2, 150, KindAttentionOpen, openPayload("att-1", "waiting on a decision")),
		attRecord(t, "agent1", 3, 190, KindAttentionOpen, openPayload("att-1", "waiting on a decision")),
	}}

	open := AttentionQueue(fold)
	require.Len(t, open, 1, "an agent re-firing one block must not queue three interruptions")
	assert.Equal(t, int64(100), open[0].OpenedMs, "the age shown is how long the block has actually waited")
}

func TestAttentionFirstDisposeWins(t *testing.T) {
	t.Parallel()

	fold := Fold{Records: []Record{
		attRecord(t, "agent1", 1, 100, KindAttentionOpen, openPayload("att-1", "waiting on a decision")),
		attRecord(t, "sessA", 1, 200, KindAttentionDispose, AttentionDispose{Request: "att-1", Note: "first"}),
		attRecord(t, "sessB", 1, 300, KindAttentionDispose, AttentionDispose{Request: "att-1", Note: "second"}),
	}}

	all := Attention(fold)
	require.Len(t, all, 1)
	assert.Equal(t, "sessA", all[0].DisposedBy)
	assert.Equal(t, int64(200), all[0].DisposedMs)
	assert.Equal(t, "first", all[0].Note)
	assert.Equal(t, 2, all[0].Disposes, "the second dispose is recorded, so a reader can see two people answered one request")
	assert.Empty(t, AttentionQueue(fold))
}

// A block that comes back after somebody dealt with it is a live wait again. Folding
// it into the answered request would hide it behind a disposition given to a
// different occurrence.
func TestAttentionReopensAfterADispose(t *testing.T) {
	t.Parallel()

	fold := Fold{Records: []Record{
		attRecord(t, "agent1", 1, 100, KindAttentionOpen, openPayload("att-1", "waiting on a decision")),
		attRecord(t, "human1", 1, 200, KindAttentionDispose, AttentionDispose{Request: "att-1", Note: "answered"}),
		attRecord(t, "agent1", 2, 300, KindAttentionOpen, openPayload("att-1", "waiting on a decision")),
	}}

	open := AttentionQueue(fold)
	require.Len(t, open, 1)
	assert.Equal(t, int64(300), open[0].OpenedMs)
	assert.Zero(t, open[0].Disposes, "the earlier disposition does not carry over to the new wait")
	assert.Empty(t, open[0].Note)
}

func TestAttentionIgnoresADisposeWithNoOpen(t *testing.T) {
	t.Parallel()

	fold := Fold{Records: []Record{
		attRecord(t, "human1", 1, 100, KindAttentionDispose, AttentionDispose{Request: "att-nothing"}),
	}}

	assert.Empty(t, Attention(fold))
}

func TestAttentionIgnoresUnknownKindsAndMalformedPayloads(t *testing.T) {
	t.Parallel()

	fold := Fold{Records: []Record{
		attRecord(t, "agent1", 1, 100, "attention_escalate_v9", map[string]string{"request": "att-1"}),
		attRecord(t, "agent1", 2, 110, KindAttentionOpen, map[string]string{"outcome": "waiting"}),
		attRecord(t, "agent1", 3, 120, KindAttentionOpen, openPayload("att-1", "waiting on a decision")),
		attRecord(t, "agent1", 4, 130, KindTargetResult, TargetResult{Target: "build", Outcome: OutcomePass}),
	}}

	open := AttentionQueue(fold)
	require.Len(t, open, 1, "a kind from a newer magus and an open with no request id are both skipped, not fatal")
	assert.Equal(t, "att-1", open[0].ID)
}

// The queue is a view over the SAME store `magus sessions` reads, so an attention
// record has to survive the file round trip and still count as session activity.
func TestAttentionRoundTripsThroughTheStore(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	block := AttentionOpen{
		Outcome: "permission", Severity: "critical", Source: "agent/claude", Where: "/repo", Message: "needs approval to push",
	}
	block.Request = RequestID("agent1", block)

	agent, err := Open(dir, "agent1", SessionStart{Workspace: "/repo", Command: "notify"})
	require.NoError(t, err)
	require.NoError(t, agent.Append(KindAttentionOpen, block))

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	open := AttentionQueue(fold)
	require.Len(t, open, 1)
	assert.Equal(t, block.Request, open[0].ID)
	assert.Equal(t, "permission", open[0].Outcome)

	human, err := Open(dir, "human1", SessionStart{Workspace: "/repo", Command: "attention dispose"})
	require.NoError(t, err)
	require.NoError(t, human.Append(KindAttentionDispose, AttentionDispose{Request: block.Request, Note: "pushed it myself"}))

	fold, err = ReadAll(dir)
	require.NoError(t, err)
	assert.Empty(t, AttentionQueue(fold))
	assert.Equal(t, 2, fold.Sessions)

	sessions := Summarize(fold)
	require.Len(t, sessions, 2)
	for _, s := range sessions {
		assert.Equal(t, 2, s.Facts, "an attention record is session activity even though it names no target")
		assert.Empty(t, s.Targets)
	}
}
