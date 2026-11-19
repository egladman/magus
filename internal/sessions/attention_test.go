package sessions

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// attRecord builds one session record with an explicit timestamp. The fold's rules
// turn on the ORDER records arrive in, and a writer stamps wall-clock milliseconds,
// so two appends in one millisecond would order by session name instead, which is
// the wrong axis to hang a first-dispose-wins test on.
func attRecord(t *testing.T, invocation string, seq uint64, ts int64, kind string, payload any) session.Record {
	t.Helper()
	raw, err := codec.Marshal(payload)
	require.NoError(t, err)
	return session.Record{V: session.APIVersion, Invocation: invocation, Seq: seq, Kind: kind, Ts: ts, Payload: raw}
}

func openPayload(id, message string) AttentionOpen {
	return AttentionOpen{Request: id, Outcome: "waiting", Severity: "warning", Source: "agent/claude", Where: "/repo", Message: message}
}

type AttentionSuite struct{ suite.Suite }

func TestAttention(t *testing.T) { suite.Run(t, new(AttentionSuite)) }

func (s *AttentionSuite) TestRequestIDIsStableAndFieldSafe() {
	t := s.T()

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
func (s *AttentionSuite) TestRequestIDDigestsTheSameBytesAsTheFourArgumentForm() {
	t := s.T()

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
			Files:    []types.FileRef{{Value: "cmd/magus/notify.go"}},
			Message:  "needs a decision",
		}))
}

func (s *AttentionSuite) TestAttentionOpenKeepsTheFilesItWasRaisedWith() {
	t := s.T()

	payload := openPayload("att-1", "needs approval")
	payload.Files = []types.FileRef{{Value: "cmd/magus/notify.go"}, {Value: "internal/sessions", IsDir: true}}
	fold := session.Fold{Records: []session.Record{
		attRecord(t, "agent1", 1, 100, KindAttentionOpen, payload),
	}}

	open := AttentionQueue(fold)
	require.Len(t, open, 1)
	assert.Equal(t, payload.Files, open[0].Files)
}

func (s *AttentionSuite) TestAttentionOpenAppearsInTheQueue() {
	t := s.T()

	fold := session.Fold{Records: []session.Record{
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

func (s *AttentionSuite) TestAttentionDisposeEmptiesTheQueue() {
	t := s.T()

	fold := session.Fold{Records: []session.Record{
		attRecord(t, "agent1", 1, 100, KindAttentionOpen, openPayload("att-1", "waiting on a decision")),
		attRecord(t, "human1", 1, 200, KindAttentionDispose, AttentionDispose{Request: "att-1", Note: "approved by hand"}),
	}}

	assert.Empty(t, AttentionQueue(fold))

	all := Attention(fold)
	require.Len(t, all, 1, "a disposed request is closed, never deleted")
	assert.Equal(t, AttentionRequest{
		ID:         "att-1",
		Invocation: "agent1",
		OpenedMs:   100,
		Outcome:    "waiting",
		Severity:   "warning",
		Source:     "agent/claude",
		Where:      "/repo",
		Message:    "waiting on a decision",
		Disposed:   true,
		DisposedMs: 200,
		DisposedBy: "human1",
		Note:       "approved by hand",
		Disposes:   1,
	}, all[0])
}

func (s *AttentionSuite) TestAttentionCollapsesARepeatedOpenIntoOneRequest() {
	t := s.T()

	fold := session.Fold{Records: []session.Record{
		attRecord(t, "agent1", 1, 100, KindAttentionOpen, openPayload("att-1", "waiting on a decision")),
		attRecord(t, "agent1", 2, 150, KindAttentionOpen, openPayload("att-1", "waiting on a decision")),
		attRecord(t, "agent1", 3, 190, KindAttentionOpen, openPayload("att-1", "waiting on a decision")),
	}}

	open := AttentionQueue(fold)
	require.Len(t, open, 1, "an agent re-firing one block must not queue three interruptions")
	assert.Equal(t, int64(100), open[0].OpenedMs, "the age shown is how long the block has actually waited")
}

func (s *AttentionSuite) TestAttentionFirstDisposeWins() {
	t := s.T()

	fold := session.Fold{Records: []session.Record{
		attRecord(t, "agent1", 1, 100, KindAttentionOpen, openPayload("att-1", "waiting on a decision")),
		attRecord(t, "sessA", 1, 200, KindAttentionDispose, AttentionDispose{Request: "att-1", Note: "first"}),
		attRecord(t, "sessB", 1, 300, KindAttentionDispose, AttentionDispose{Request: "att-1", Note: "second"}),
	}}

	all := Attention(fold)
	require.Len(t, all, 1)
	assert.Equal(t, AttentionRequest{
		ID:         "att-1",
		Invocation: "agent1",
		OpenedMs:   100,
		Outcome:    "waiting",
		Severity:   "warning",
		Source:     "agent/claude",
		Where:      "/repo",
		Message:    "waiting on a decision",
		Disposed:   true,
		DisposedMs: 200,
		DisposedBy: "sessA",
		Note:       "first",
		Disposes:   2, // the second dispose is recorded, so a reader can see two people answered one request
	}, all[0])
	assert.Empty(t, AttentionQueue(fold))
}

// A block that comes back after somebody dealt with it is a live wait again. Folding
// it into the answered request would hide it behind a disposition given to a
// different occurrence.
func (s *AttentionSuite) TestAttentionReopensAfterADispose() {
	t := s.T()

	fold := session.Fold{Records: []session.Record{
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

func (s *AttentionSuite) TestAttentionIgnoresADisposeWithNoOpen() {
	t := s.T()

	fold := session.Fold{Records: []session.Record{
		attRecord(t, "human1", 1, 100, KindAttentionDispose, AttentionDispose{Request: "att-nothing"}),
	}}

	assert.Empty(t, Attention(fold))
}

func (s *AttentionSuite) TestAttentionIgnoresUnknownKindsAndMalformedPayloads() {
	t := s.T()

	fold := session.Fold{Records: []session.Record{
		attRecord(t, "agent1", 1, 100, "attention_escalate_v9", map[string]string{"request": "att-1"}),
		attRecord(t, "agent1", 2, 110, KindAttentionOpen, map[string]string{"outcome": "waiting"}),
		attRecord(t, "agent1", 3, 120, KindAttentionOpen, openPayload("att-1", "waiting on a decision")),
		attRecord(t, "agent1", 4, 130, session.KindTargetResult, session.TargetResult{Target: "build", Outcome: session.OutcomePass}),
	}}

	open := AttentionQueue(fold)
	require.Len(t, open, 1, "a kind from a newer magus and an open with no request id are both skipped, not fatal")
	assert.Equal(t, "att-1", open[0].ID)
}

// The queue is a view over the SAME store `magus session` reads, so an attention
// record has to survive the file round trip and still count as session activity.
func (s *AttentionSuite) TestAttentionRoundTripsThroughTheStore() {
	t := s.T()
	dir := t.TempDir()

	block := AttentionOpen{
		Outcome: "permission", Severity: "critical", Source: "agent/claude", Where: "/repo", Message: "needs approval to push",
	}
	block.Request = RequestID("agent1", block)

	agent, err := session.Open(t.Context(), StoreConfig(dir, "agent1", session.InvocationStart{Workspace: "/repo", Command: "notify"}))
	require.NoError(t, err)
	require.NoError(t, agent.Append(t.Context(), KindAttentionOpen, block))

	fold, err := session.ReadAll(t.Context(), dir)
	require.NoError(t, err)
	open := AttentionQueue(fold)
	require.Len(t, open, 1)
	assert.Equal(t, block.Request, open[0].ID)
	assert.Equal(t, "permission", open[0].Outcome)

	human, err := session.Open(t.Context(), StoreConfig(dir, "human1", session.InvocationStart{Workspace: "/repo", Command: "attention dispose"}))
	require.NoError(t, err)
	require.NoError(t, human.Append(t.Context(), KindAttentionDispose, AttentionDispose{Request: block.Request, Note: "pushed it myself"}))

	fold, err = session.ReadAll(t.Context(), dir)
	require.NoError(t, err)
	assert.Empty(t, AttentionQueue(fold))
	assert.Equal(t, 2, fold.Invocations)

	sessions := session.Summarize(fold)
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
	fold, err := session.ReadAll(t.Context(), dir)
	require.NoError(t, err)
	var ids []string
	for _, req := range AttentionQueue(fold) {
		ids = append(ids, req.ID)
	}
	return ids
}

func (s *AttentionSuite) TestSourceLabelMatchesWhatRequestIDDigests() {
	t := s.T()
	assert.Equal(t, "agent/claude", SourceLabel("agent", "claude"))
	assert.Equal(t, "magus", SourceLabel("magus", ""))
}

// AppendRequestSuite pins the append a hook files a request with.
type AppendRequestSuite struct {
	suite.Suite
	dir string
}

func TestAppendRequestSuite(t *testing.T) {
	suite.Run(t, new(AppendRequestSuite))
}

func (s *AppendRequestSuite) SetupTest() {
	s.dir = s.T().TempDir()
}

func (s *AppendRequestSuite) TestItFillsTheIDOpenRequestWould() {
	s.Require().NoError(AppendRequest(s.T().Context(), s.dir, "sess-1", blocked("needs a decision"), session.InvocationStart{}))
	s.Equal([]string{RequestID("sess-1", blocked("needs a decision"))}, queueIDs(s.T(), s.dir))
}

// A second open on disk shows the store was not read first.
func (s *AppendRequestSuite) TestARepeatAppendsAndCollapsesInTheFold() {
	s.Require().NoError(AppendRequest(s.T().Context(), s.dir, "sess-1", blocked("needs a decision"), session.InvocationStart{}))
	s.Require().NoError(AppendRequest(s.T().Context(), s.dir, "sess-1", blocked("needs a decision"), session.InvocationStart{}))

	fold, err := session.ReadAll(s.T().Context(), s.dir)
	s.Require().NoError(err)
	opens := slices.DeleteFunc(slices.Clone(fold.Records), func(r session.Record) bool { return r.Kind != KindAttentionOpen })
	s.Len(opens, 2)
	s.Len(queueIDs(s.T(), s.dir), 1)
}

// A session file from before the group rename must not stop a reclaim's request: the file
// is removed and the request opens.
func (s *AttentionSuite) TestOpenRequestRemovesAFilePredatingTheGroupRename() {
	t := s.T()
	dir := t.TempDir()
	old := filepath.Join(dir, "old.jsonl")
	body := `{"v":"magus.sessions/v1alpha1","invocation":"old","seq":1,"kind":"target_result","ts":10}` + "\n"
	require.NoError(t, os.WriteFile(old, []byte(body), 0o644))

	id, opened, err := OpenRequest(t.Context(), dir, "sess-1", blocked("needs a decision"), session.InvocationStart{Workspace: "/repo"})
	require.NoError(t, err)
	assert.True(t, opened)
	assert.Equal(t, []string{id}, queueIDs(t, dir))
	assert.NoFileExists(t, old)
}

func (s *AttentionSuite) TestOpenRequestWritesOneRequestAndReportsIt() {
	t := s.T()
	dir := t.TempDir()

	id, opened, err := OpenRequest(t.Context(), dir, "sess-1", blocked("needs a decision"), session.InvocationStart{Workspace: "/repo"})
	require.NoError(t, err)
	assert.True(t, opened)
	assert.Equal(t, RequestID("sess-1", blocked("needs a decision")), id)
	assert.Equal(t, []string{id}, queueIDs(t, dir))
}

// An agent hook may fire on every prompt, so the queue has to hold one row per block
// rather than one per attempt.
func (s *AttentionSuite) TestOpenRequestIsANoOpForABlockAlreadyOpen() {
	t := s.T()
	dir := t.TempDir()

	first, opened, err := OpenRequest(t.Context(), dir, "sess-1", blocked("needs a decision"), session.InvocationStart{})
	require.NoError(t, err)
	require.True(t, opened)

	again, opened, err := OpenRequest(t.Context(), dir, "sess-1", blocked("needs a decision"), session.InvocationStart{})
	require.NoError(t, err)
	assert.False(t, opened, "the block was already queued")
	assert.Equal(t, first, again, "a re-fire is addressable by the id already in the queue")
	assert.Len(t, queueIDs(t, dir), 1)
}

// A block that was answered and has come back is a live wait again. Swallowing it would
// hide it behind an answer that was given to a different block.
func (s *AttentionSuite) TestOpenRequestRaisesABlockAgainAfterItWasDisposed() {
	t := s.T()
	dir := t.TempDir()

	id, _, err := OpenRequest(t.Context(), dir, "sess-1", blocked("needs a decision"), session.InvocationStart{})
	require.NoError(t, err)
	_, err = DisposeRequest(t.Context(), dir, id, "answered", session.InvocationStart{})
	require.NoError(t, err)
	require.Empty(t, queueIDs(t, dir))

	again, opened, err := OpenRequest(t.Context(), dir, "sess-1", blocked("needs a decision"), session.InvocationStart{})
	require.NoError(t, err)
	assert.True(t, opened)
	assert.Equal(t, id, again)
	assert.Equal(t, []string{id}, queueIDs(t, dir))
}

func (s *AttentionSuite) TestDisposeRequestClosesItAndReportsWhatTheStoreRecorded() {
	t := s.T()
	dir := t.TempDir()

	id, _, err := OpenRequest(t.Context(), dir, "sess-1", blocked("needs a decision"), session.InvocationStart{})
	require.NoError(t, err)

	req, err := DisposeRequest(t.Context(), dir, id, "approved by hand", session.InvocationStart{Workspace: "/repo"})
	require.NoError(t, err)
	assert.NotEmpty(t, req.DisposedBy, "the disposing session is read back off the store, not assumed")
	assert.Equal(t, AttentionRequest{
		ID:         id,
		Invocation: req.Invocation, // a generated invocation id, not the session name
		OpenedMs:   req.OpenedMs,
		Outcome:    "waiting",
		Source:     "agent/claude",
		Where:      "/repo",
		Message:    "needs a decision",
		Disposed:   true,
		DisposedMs: req.DisposedMs,
		DisposedBy: req.DisposedBy,
		Note:       "approved by hand",
		Disposes:   1,
	}, req)
	assert.Empty(t, queueIDs(t, dir))
}

// A second dispose is recorded but changes nothing, so reporting success would credit
// this caller with a closure it did not perform.
func (s *AttentionSuite) TestDisposeRequestRefusesAnAlreadyClosedRequest() {
	t := s.T()
	dir := t.TempDir()

	id, _, err := OpenRequest(t.Context(), dir, "sess-1", blocked("needs a decision"), session.InvocationStart{})
	require.NoError(t, err)
	first, err := DisposeRequest(t.Context(), dir, id, "answered", session.InvocationStart{})
	require.NoError(t, err)

	_, err = DisposeRequest(t.Context(), dir, id, "answered again", session.InvocationStart{})
	var disposed *DisposedError
	require.ErrorAs(t, err, &disposed)
	assert.Equal(t, id, disposed.ID)
	assert.Equal(t, first.DisposedBy, disposed.DisposedBy, "the error names who actually closed it")
}

func (s *AttentionSuite) TestDisposeRequestAcceptsAnUnambiguousPrefix() {
	t := s.T()
	dir := t.TempDir()

	id, _, err := OpenRequest(t.Context(), dir, "sess-1", blocked("needs a decision"), session.InvocationStart{})
	require.NoError(t, err)

	req, err := DisposeRequest(t.Context(), dir, id[:8], "", session.InvocationStart{})
	require.NoError(t, err)
	assert.Equal(t, id, req.ID, "the prefix resolves to the full id, and the record names that")
}

func (s *AttentionSuite) TestResolveRequestIDReportsEveryCandidateForAnAmbiguousPrefix() {
	t := s.T()
	dir := t.TempDir()

	first, _, err := OpenRequest(t.Context(), dir, "sess-1", blocked("one"), session.InvocationStart{})
	require.NoError(t, err)
	second, _, err := OpenRequest(t.Context(), dir, "sess-2", blocked("two"), session.InvocationStart{})
	require.NoError(t, err)

	fold, err := session.ReadAll(t.Context(), dir)
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
func (s *AttentionSuite) TestResolveRequestIDPrefersAnExactID() {
	t := s.T()

	fold := session.Fold{Records: []session.Record{
		attRecord(t, "s1", 1, 10, KindAttentionOpen, openPayload("att-abc", "one")),
		attRecord(t, "s1", 2, 20, KindAttentionOpen, openPayload("att-abcdef", "two")),
	}}
	got, err := ResolveRequestID(fold, "att-abc")
	require.NoError(t, err)
	assert.Equal(t, "att-abc", got)
}

// A prefix naming a CLOSED request has to report the closure, not read as a typo, so
// resolution deliberately sees disposed requests too.
func (s *AttentionSuite) TestResolveRequestIDSeesDisposedRequests() {
	t := s.T()
	dir := t.TempDir()

	id, _, err := OpenRequest(t.Context(), dir, "sess-1", blocked("needs a decision"), session.InvocationStart{})
	require.NoError(t, err)
	_, err = DisposeRequest(t.Context(), dir, id, "", session.InvocationStart{})
	require.NoError(t, err)

	_, err = DisposeRequest(t.Context(), dir, id[:8], "", session.InvocationStart{})
	var disposed *DisposedError
	assert.ErrorAs(t, err, &disposed, "a prefix that resolves to a closed request reports the closure")
}

func (s *AttentionSuite) TestResolveRequestIDReportsNothingMatchedAsNoRequest() {
	t := s.T()

	fold := session.Fold{Records: []session.Record{attRecord(t, "s1", 1, 10, KindAttentionOpen, openPayload("att-abc", "one"))}}
	for _, ref := range []string{"", "att-zzz", "nope"} {
		_, err := ResolveRequestID(fold, ref)
		assert.True(t, errors.Is(err, ErrNoRequest), "%q names nothing", ref)
	}
}

// The suffix is what keeps two processes that started in the same millisecond from
// sharing an invocation file, which is the collision the underlying invocation id cannot
// rule out on its own.
func (s *AttentionSuite) TestNewIDIsAValidIDAndCarriesAProcessSuffix() {
	t := s.T()

	id := NewID()
	assert.True(t, session.ValidFileID(id), "the id names the invocation's file, so it must not escape the store")

	_, suffix, found := strings.Cut(id, "-")
	require.True(t, found, "the id carries a per-process suffix")
	assert.NotEmpty(t, suffix)
	assert.Equal(t, suffix, processSuffix(), "one process stamps one suffix")
	assert.NotEqual(t, id, NewID(), "two ids from one process still differ")
}

// Two disposals in one store must not collapse into one session, or the store says one
// command did what two did.
func (s *AttentionSuite) TestTwoDisposalsRecordTwoSessions() {
	t := s.T()
	dir := t.TempDir()

	first, _, err := OpenRequest(t.Context(), dir, "sess-1", blocked("one"), session.InvocationStart{})
	require.NoError(t, err)
	second, _, err := OpenRequest(t.Context(), dir, "sess-2", blocked("two"), session.InvocationStart{})
	require.NoError(t, err)

	a, err := DisposeRequest(t.Context(), dir, first, "", session.InvocationStart{})
	require.NoError(t, err)
	b, err := DisposeRequest(t.Context(), dir, second, "", session.InvocationStart{})
	require.NoError(t, err)
	assert.NotEqual(t, a.DisposedBy, b.DisposedBy)
}

func (s *AttentionSuite) TestRenderAttentionTextRendersAnUnattributedRequestWithADash() {
	t := s.T()

	var out strings.Builder
	require.NoError(t, RenderAttentionText(&out, []AttentionRequest{
		{ID: "att-0123456789ab", Outcome: "waiting", Source: "agent/claude", Where: "/repo", Message: "needs a decision"},
	}, t.TempDir()))

	// ID, AGE, OUTCOME, SOURCE, LEASE, WHERE, FILES, MESSAGE...
	var cells []string
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "att-") {
			cells = strings.Fields(line)
			break
		}
	}
	require.Greater(t, len(cells), 4, "no request row in:\n%s", out.String())
	assert.Equal(t, "-", cells[4], "an unattributed request reads like an unknown LEASE, not like a blank")
}

func (s *AttentionSuite) TestRenderAttentionTextLeadsWithTheFilesTheEventNamed() {
	t := s.T()

	var out strings.Builder
	require.NoError(t, RenderAttentionText(&out, []AttentionRequest{{
		ID:      "att-0123456789ab",
		Message: "needs a decision on the schema",
		Files:   []types.FileRef{{Value: "cmd/magus/notify.go"}, {Value: "cmd/magus/attention.go"}},
	}}, t.TempDir()))

	assert.Contains(t, out.String(), "FILES")
	assert.Contains(t, out.String(), "cmd/magus/notify.go, cmd/magus/attention.go")
	assert.Contains(t, out.String(), "needs a decision on the schema", "the message stays on the row, after the paths")
	assert.Contains(t, out.String(), "magus session dispose", "the listing names the one command that closes a request")
	assert.Contains(t, out.String(), "Nothing here closes on its own.")
}

func (s *AttentionSuite) TestAttentionWhereNarrowsToTheProject() {
	t := s.T()

	assert.Empty(t, AttentionWhere(nil))
	assert.Equal(t, "/repo", AttentionWhere(&types.EventLocation{Workspace: types.Path{Value: "/repo", IsDir: true}}))
	assert.Equal(t, "/repo", AttentionWhere(&types.EventLocation{
		Workspace: types.Path{Value: "/repo", IsDir: true},
		Project:   &types.ProjectRef{Path: ".", Name: "repo"},
	}))
	assert.Equal(t, "/repo [apps/web]", AttentionWhere(&types.EventLocation{
		Workspace: types.Path{Value: "/repo", IsDir: true},
		Project:   &types.ProjectRef{Path: "apps/web", Name: "apps/web"},
	}))
}

func (s *AttentionSuite) TestDisposeErrorNamesTheStoreAndTheListingForAnUnknownID() {
	t := s.T()
	dir := t.TempDir()

	_, err := DisposeRequest(t.Context(), dir, "att-doesnotexist", "", session.InvocationStart{})
	err = DisposeError(err, "att-doesnotexist", dir)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "att-doesnotexist")
	assert.Contains(t, err.Error(), dir, "the error names the store it looked in")
	assert.Contains(t, err.Error(), "magus session attention", "and the command that lists the ids that do exist")
}

func (s *AttentionSuite) TestDisposeErrorRefusesASecondClosureAndPointsAtTheListing() {
	t := s.T()
	dir := t.TempDir()
	id, _, err := OpenRequest(t.Context(), dir, "sess-1", blocked("needs approval to push"), session.InvocationStart{})
	require.NoError(t, err)
	_, err = DisposeRequest(t.Context(), dir, id, "pushed it myself", session.InvocationStart{})
	require.NoError(t, err)

	_, err = DisposeRequest(t.Context(), dir, id, "", session.InvocationStart{})
	err = DisposeError(err, id, dir)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "already disposed")
	assert.Contains(t, err.Error(), "magus session attention")
	assert.Empty(t, queueIDs(t, dir), "a disposed request leaves the queue")
}

func (s *AttentionSuite) TestDisposeErrorRefusesAnAmbiguousPrefixAndNamesTheCandidates() {
	t := s.T()
	dir := t.TempDir()
	first, _, err := OpenRequest(t.Context(), dir, "sess-1", blocked("one"), session.InvocationStart{})
	require.NoError(t, err)
	second, _, err := OpenRequest(t.Context(), dir, "sess-2", blocked("two"), session.InvocationStart{})
	require.NoError(t, err)

	_, err = DisposeRequest(t.Context(), dir, "att-", "", session.InvocationStart{})
	err = DisposeError(err, "att-", dir)

	require.Error(t, err)
	for _, id := range []string{first, second} {
		assert.Contains(t, err.Error(), id, "a person cannot choose between candidates the error does not name")
	}
	assert.Len(t, queueIDs(t, dir), 2, "an ambiguous prefix closes nothing")
}

func (s *AttentionSuite) TestDisposeErrorReportsAPrefixThatMatchesNothing() {
	t := s.T()
	dir := t.TempDir()
	_, _, err := OpenRequest(t.Context(), dir, "sess-1", blocked("needs a decision"), session.InvocationStart{})
	require.NoError(t, err)

	_, err = DisposeRequest(t.Context(), dir, "att-zzzz", "", session.InvocationStart{})
	err = DisposeError(err, "att-zzzz", dir)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no request matches")
	assert.Contains(t, err.Error(), "magus session attention", "the refusal names how to see the open ids")
}
