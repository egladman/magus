package sessions

import (
	"errors"
	"strings"
	"testing"

	json "github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// attRecord builds one session record with an explicit timestamp. The fold's rules
// turn on the ORDER records arrive in, and a writer stamps wall-clock milliseconds,
// so two appends in one millisecond would order by session name instead, which is
// the wrong axis to hang a first-dispose-wins test on.
func attRecord(t *testing.T, session string, seq uint64, ts int64, kind string, payload any) Record {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	return Record{V: SchemaVersion, Invocation: session, Seq: seq, Kind: kind, Ts: ts, Payload: raw}
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
			Request:  "att-whatever",
			Outcome:  "permission",
			Severity: "critical",
			Lease:    "fleet/f9",
			Source:   "agent/claude",
			Where:    "/repo",
			Message:  "needs a decision",
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
		ID:         "att-1",
		Invocation: "agent1",
		OpenedMs:   100,
		Outcome:    "waiting",
		Severity:   "warning",
		Source:     "agent/claude",
		Where:      "/repo",
		Message:    "waiting on a decision",
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

// The queue is a view over the SAME store `magus session` reads, so an attention
// record has to survive the file round trip and still count as session activity.
func TestAttentionRoundTripsThroughTheStore(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	block := AttentionOpen{
		Outcome: "permission", Severity: "critical", Source: "agent/claude", Where: "/repo", Message: "needs approval to push",
	}
	block.Request = RequestID("agent1", block)

	agent, err := Open(dir, "agent1", InvocationStart{Workspace: "/repo", Command: "notify"})
	require.NoError(t, err)
	require.NoError(t, agent.Append(KindAttentionOpen, block))

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	open := AttentionQueue(fold)
	require.Len(t, open, 1)
	assert.Equal(t, block.Request, open[0].ID)
	assert.Equal(t, "permission", open[0].Outcome)

	human, err := Open(dir, "human1", InvocationStart{Workspace: "/repo", Command: "attention dispose"})
	require.NoError(t, err)
	require.NoError(t, human.Append(KindAttentionDispose, AttentionDispose{Request: block.Request, Note: "pushed it myself"}))

	fold, err = ReadAll(dir)
	require.NoError(t, err)
	assert.Empty(t, AttentionQueue(fold))
	assert.Equal(t, 2, fold.Invocations)

	sessions := Summarize(fold)
	require.Len(t, sessions, 2)
	for _, s := range sessions {
		assert.Equal(t, 2, s.Facts, "an attention record is session activity even though it names no target")
		assert.Empty(t, s.Targets)
	}
}

// blocked is one raised block as a producer hands it over: no Request, because
// OpenRequest derives that.
func blocked(message string) AttentionOpen {
	return AttentionOpen{
		Outcome: "waiting",
		Source:  "agent/claude",
		Where:   "/repo",
		Message: message,
	}
}

func queueIDs(t *testing.T, dir string) []string {
	t.Helper()
	fold, err := ReadAll(dir)
	require.NoError(t, err)
	var ids []string
	for _, req := range AttentionQueue(fold) {
		ids = append(ids, req.ID)
	}
	return ids
}

func TestSourceLabelMatchesWhatRequestIDDigests(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "agent/claude", SourceLabel("agent", "claude"))
	assert.Equal(t, "magus", SourceLabel("magus", ""))
}

func TestOpenRequestWritesOneRequestAndReportsIt(t *testing.T) {
	dir := t.TempDir()

	id, opened, err := OpenRequest(dir, "sess-1", blocked("needs a decision"), InvocationStart{Workspace: "/repo"})
	require.NoError(t, err)
	assert.True(t, opened)
	assert.Equal(t, RequestID("sess-1", blocked("needs a decision")), id)
	assert.Equal(t, []string{id}, queueIDs(t, dir))
}

// An agent hook may fire on every prompt, so the queue has to hold one row per block
// rather than one per attempt.
func TestOpenRequestIsANoOpForABlockAlreadyOpen(t *testing.T) {
	dir := t.TempDir()

	first, opened, err := OpenRequest(dir, "sess-1", blocked("needs a decision"), InvocationStart{})
	require.NoError(t, err)
	require.True(t, opened)

	again, opened, err := OpenRequest(dir, "sess-1", blocked("needs a decision"), InvocationStart{})
	require.NoError(t, err)
	assert.False(t, opened, "the block was already queued")
	assert.Equal(t, first, again, "a re-fire is addressable by the id already in the queue")
	assert.Len(t, queueIDs(t, dir), 1)
}

// A block that was answered and has come back is a live wait again. Swallowing it would
// hide it behind an answer that was given to a different block.
func TestOpenRequestRaisesABlockAgainAfterItWasDisposed(t *testing.T) {
	dir := t.TempDir()

	id, _, err := OpenRequest(dir, "sess-1", blocked("needs a decision"), InvocationStart{})
	require.NoError(t, err)
	_, err = DisposeRequest(dir, id, "answered", InvocationStart{})
	require.NoError(t, err)
	require.Empty(t, queueIDs(t, dir))

	again, opened, err := OpenRequest(dir, "sess-1", blocked("needs a decision"), InvocationStart{})
	require.NoError(t, err)
	assert.True(t, opened)
	assert.Equal(t, id, again)
	assert.Equal(t, []string{id}, queueIDs(t, dir))
}

func TestDisposeRequestClosesItAndReportsWhatTheStoreRecorded(t *testing.T) {
	dir := t.TempDir()

	id, _, err := OpenRequest(dir, "sess-1", blocked("needs a decision"), InvocationStart{})
	require.NoError(t, err)

	req, err := DisposeRequest(dir, id, "approved by hand", InvocationStart{Workspace: "/repo"})
	require.NoError(t, err)
	assert.Equal(t, id, req.ID)
	assert.True(t, req.Disposed)
	assert.Equal(t, "approved by hand", req.Note)
	assert.NotEmpty(t, req.DisposedBy, "the disposing session is read back off the store, not assumed")
	assert.Empty(t, queueIDs(t, dir))
}

// A second dispose is recorded but changes nothing, so reporting success would credit
// this caller with a closure it did not perform.
func TestDisposeRequestRefusesAnAlreadyClosedRequest(t *testing.T) {
	dir := t.TempDir()

	id, _, err := OpenRequest(dir, "sess-1", blocked("needs a decision"), InvocationStart{})
	require.NoError(t, err)
	first, err := DisposeRequest(dir, id, "answered", InvocationStart{})
	require.NoError(t, err)

	_, err = DisposeRequest(dir, id, "answered again", InvocationStart{})
	var disposed *DisposedError
	require.ErrorAs(t, err, &disposed)
	assert.Equal(t, id, disposed.ID)
	assert.Equal(t, first.DisposedBy, disposed.DisposedBy, "the error names who actually closed it")
}

func TestDisposeRequestAcceptsAnUnambiguousPrefix(t *testing.T) {
	dir := t.TempDir()

	id, _, err := OpenRequest(dir, "sess-1", blocked("needs a decision"), InvocationStart{})
	require.NoError(t, err)

	req, err := DisposeRequest(dir, id[:8], "", InvocationStart{})
	require.NoError(t, err)
	assert.Equal(t, id, req.ID, "the prefix resolves to the full id, and the record names that")
}

func TestResolveRequestIDReportsEveryCandidateForAnAmbiguousPrefix(t *testing.T) {
	dir := t.TempDir()

	first, _, err := OpenRequest(dir, "sess-1", blocked("one"), InvocationStart{})
	require.NoError(t, err)
	second, _, err := OpenRequest(dir, "sess-2", blocked("two"), InvocationStart{})
	require.NoError(t, err)

	fold, err := ReadAll(dir)
	require.NoError(t, err)

	// Every id shares the "att-" tag, which is the shortest prefix guaranteed ambiguous
	// once two requests exist.
	_, err = ResolveRequestID(fold, "att-")
	var ambiguous *AmbiguousRequestError
	require.ErrorAs(t, err, &ambiguous)
	assert.Equal(t, "att-", ambiguous.Prefix)
	assert.ElementsMatch(t, []string{first, second}, ambiguous.Candidates)
	assert.Contains(t, ambiguous.Error(), first, "the message names what to pick between")
}

// A full id must win over a prefix scan, or a request whose id is a prefix of another's
// could never be addressed at all.
func TestResolveRequestIDPrefersAnExactID(t *testing.T) {
	t.Parallel()

	fold := Fold{Records: []Record{
		attRecord(t, "s1", 1, 10, KindAttentionOpen, openPayload("att-abc", "one")),
		attRecord(t, "s1", 2, 20, KindAttentionOpen, openPayload("att-abcdef", "two")),
	}}
	got, err := ResolveRequestID(fold, "att-abc")
	require.NoError(t, err)
	assert.Equal(t, "att-abc", got)
}

// A prefix naming a CLOSED request has to report the closure, not read as a typo, so
// resolution deliberately sees disposed requests too.
func TestResolveRequestIDSeesDisposedRequests(t *testing.T) {
	dir := t.TempDir()

	id, _, err := OpenRequest(dir, "sess-1", blocked("needs a decision"), InvocationStart{})
	require.NoError(t, err)
	_, err = DisposeRequest(dir, id, "", InvocationStart{})
	require.NoError(t, err)

	_, err = DisposeRequest(dir, id[:8], "", InvocationStart{})
	var disposed *DisposedError
	assert.ErrorAs(t, err, &disposed, "a prefix that resolves to a closed request reports the closure")
}

func TestResolveRequestIDReportsNothingMatchedAsErrNoRequest(t *testing.T) {
	t.Parallel()

	fold := Fold{Records: []Record{attRecord(t, "s1", 1, 10, KindAttentionOpen, openPayload("att-abc", "one"))}}
	for _, ref := range []string{"", "att-zzz", "nope"} {
		_, err := ResolveRequestID(fold, ref)
		assert.True(t, errors.Is(err, ErrNoRequest), "%q names nothing", ref)
	}
}

// The suffix is what keeps two processes that started in the same millisecond from
// sharing an invocation file, which is the collision the underlying invocation id cannot
// rule out on its own.
func TestNewIDIsAValidIDAndCarriesAProcessSuffix(t *testing.T) {
	t.Parallel()

	id := NewID()
	assert.True(t, ValidFileID(id), "the id names the invocation's file, so it must not escape the store")

	_, suffix, found := strings.Cut(id, "-")
	require.True(t, found, "the id carries a per-process suffix")
	assert.NotEmpty(t, suffix)
	assert.Equal(t, suffix, processSuffix(), "one process stamps one suffix")
	assert.NotEqual(t, id, NewID(), "two ids from one process still differ")
}

// Two disposals in one store must not collapse into one session, or the store says one
// command did what two did.
func TestTwoDisposalsRecordTwoSessions(t *testing.T) {
	dir := t.TempDir()

	first, _, err := OpenRequest(dir, "sess-1", blocked("one"), InvocationStart{})
	require.NoError(t, err)
	second, _, err := OpenRequest(dir, "sess-2", blocked("two"), InvocationStart{})
	require.NoError(t, err)

	a, err := DisposeRequest(dir, first, "", InvocationStart{})
	require.NoError(t, err)
	b, err := DisposeRequest(dir, second, "", InvocationStart{})
	require.NoError(t, err)
	assert.NotEqual(t, a.DisposedBy, b.DisposedBy)
}
