package sessions

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	id, opened, err := OpenRequest(dir, "sess-1", blocked("needs a decision"), SessionStart{Workspace: "/repo"})
	require.NoError(t, err)
	assert.True(t, opened)
	assert.Equal(t, RequestID("sess-1", blocked("needs a decision")), id)
	assert.Equal(t, []string{id}, queueIDs(t, dir))
}

// An agent hook may fire on every prompt, so the queue has to hold one row per block
// rather than one per attempt.
func TestOpenRequestIsANoOpForABlockAlreadyOpen(t *testing.T) {
	dir := t.TempDir()

	first, opened, err := OpenRequest(dir, "sess-1", blocked("needs a decision"), SessionStart{})
	require.NoError(t, err)
	require.True(t, opened)

	again, opened, err := OpenRequest(dir, "sess-1", blocked("needs a decision"), SessionStart{})
	require.NoError(t, err)
	assert.False(t, opened, "the block was already queued")
	assert.Equal(t, first, again, "a re-fire is addressable by the id already in the queue")
	assert.Len(t, queueIDs(t, dir), 1)
}

// A block that was answered and has come back is a live wait again. Swallowing it would
// hide it behind an answer that was given to a different block.
func TestOpenRequestRaisesABlockAgainAfterItWasDisposed(t *testing.T) {
	dir := t.TempDir()

	id, _, err := OpenRequest(dir, "sess-1", blocked("needs a decision"), SessionStart{})
	require.NoError(t, err)
	_, err = DisposeRequest(dir, id, "answered", SessionStart{})
	require.NoError(t, err)
	require.Empty(t, queueIDs(t, dir))

	again, opened, err := OpenRequest(dir, "sess-1", blocked("needs a decision"), SessionStart{})
	require.NoError(t, err)
	assert.True(t, opened)
	assert.Equal(t, id, again)
	assert.Equal(t, []string{id}, queueIDs(t, dir))
}

func TestDisposeRequestClosesItAndReportsWhatTheStoreRecorded(t *testing.T) {
	dir := t.TempDir()

	id, _, err := OpenRequest(dir, "sess-1", blocked("needs a decision"), SessionStart{})
	require.NoError(t, err)

	req, err := DisposeRequest(dir, id, "approved by hand", SessionStart{Workspace: "/repo"})
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

	id, _, err := OpenRequest(dir, "sess-1", blocked("needs a decision"), SessionStart{})
	require.NoError(t, err)
	first, err := DisposeRequest(dir, id, "answered", SessionStart{})
	require.NoError(t, err)

	_, err = DisposeRequest(dir, id, "answered again", SessionStart{})
	var disposed *DisposedError
	require.ErrorAs(t, err, &disposed)
	assert.Equal(t, id, disposed.ID)
	assert.Equal(t, first.DisposedBy, disposed.DisposedBy, "the error names who actually closed it")
}

func TestDisposeRequestAcceptsAnUnambiguousPrefix(t *testing.T) {
	dir := t.TempDir()

	id, _, err := OpenRequest(dir, "sess-1", blocked("needs a decision"), SessionStart{})
	require.NoError(t, err)

	req, err := DisposeRequest(dir, id[:8], "", SessionStart{})
	require.NoError(t, err)
	assert.Equal(t, id, req.ID, "the prefix resolves to the full id, and the record names that")
}

func TestResolveRequestIDReportsEveryCandidateForAnAmbiguousPrefix(t *testing.T) {
	dir := t.TempDir()

	first, _, err := OpenRequest(dir, "sess-1", blocked("one"), SessionStart{})
	require.NoError(t, err)
	second, _, err := OpenRequest(dir, "sess-2", blocked("two"), SessionStart{})
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

	id, _, err := OpenRequest(dir, "sess-1", blocked("needs a decision"), SessionStart{})
	require.NoError(t, err)
	_, err = DisposeRequest(dir, id, "", SessionStart{})
	require.NoError(t, err)

	_, err = DisposeRequest(dir, id[:8], "", SessionStart{})
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
// sharing a session file, which is the collision the underlying invocation id cannot
// rule out on its own.
func TestNewIDIsAValidSessionIDAndCarriesAProcessSuffix(t *testing.T) {
	t.Parallel()

	id := NewID()
	assert.True(t, sessionRE.MatchString(id), "the id names the session file, so it must not escape the store")

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

	first, _, err := OpenRequest(dir, "sess-1", blocked("one"), SessionStart{})
	require.NoError(t, err)
	second, _, err := OpenRequest(dir, "sess-2", blocked("two"), SessionStart{})
	require.NoError(t, err)

	a, err := DisposeRequest(dir, first, "", SessionStart{})
	require.NoError(t, err)
	b, err := DisposeRequest(dir, second, "", SessionStart{})
	require.NoError(t, err)
	assert.NotEqual(t, a.DisposedBy, b.DisposedBy)
}
