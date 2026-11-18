package viewer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/libs/safetyscissors/journal"
	queryv1 "github.com/egladman/magus/proto/gen/go/magus/query/v1alpha1"
	viewerv1 "github.com/egladman/magus/proto/gen/go/magus/viewer/v1alpha1"
	"github.com/egladman/magus/proto/gen/go/magus/viewer/v1alpha1/viewerv1alpha1connect"
)

// storedRuns is the runSource half of the handler's contract, filled in per test. It is a struct of
// stored answers rather than a mock: every RPC here reads, so what a test needs to say is what the
// store would have returned.
type storedRuns struct {
	logs []cache.RunLog
	desc map[string]cache.OutputDescriptor
}

func (f *storedRuns) ListRunLogs(context.Context, int) []cache.RunLog { return f.logs }

func (f *storedRuns) DescriptorByRef(ref string) (cache.OutputDescriptor, error) {
	d, ok := f.desc[ref]
	if !ok {
		return cache.OutputDescriptor{}, errors.New("no such ref")
	}
	return d, nil
}

// storedOutputs is the outputSource half. The ListEvents/GetInvocation tests never reach it.
type storedOutputs struct {
	descs []cache.OutputDescriptor
	body  []byte
}

func (f *storedOutputs) ListDescriptors() ([]cache.OutputDescriptor, error) { return f.descs, nil }

func (f *storedOutputs) ByRef(string) ([]byte, cache.OutputDescriptor, error) {
	return f.body, cache.OutputDescriptor{}, nil
}

// testInv names the journal every fixture here writes.
const testInv = "inv1a2b3c"

// runsWith writes records as testInv's journal in a fresh runs dir and returns
// the dir. The store is a file, so the fixture is a file.
func runsWith(t *testing.T, records ...journal.Record) string {
	t.Helper()
	dir := t.TempDir()
	appendRecords(t, dir, records...)
	return dir
}

// appendRecords appends records to testInv's journal under dir, as a run would.
func appendRecords(t *testing.T, dir string, records ...journal.Record) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, testInv+".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	defer func() { require.NoError(t, f.Close()) }()
	for _, r := range records {
		line, err := r.MarshalJSON()
		require.NoError(t, err)
		_, err = f.Write(append(line, '\n'))
		require.NoError(t, err)
	}
}

// TestListEventsUnfiltered checks the whole journal comes back when no filter is set, in stored
// order, with the next token empty because one page held everything.
func TestListEventsUnfiltered(t *testing.T) {
	dir := runsWith(t,
		at(1, journal.Output{Project: "web", Text: "compiling"}),
		at(2, journal.Output{Project: "api", Text: "linking"}),
		at(3, journal.Run{Project: "web", Status: journal.StatusFail}),
	)
	s := NewService(&storedOutputs{}, &storedRuns{}, dir)

	resp, err := s.ListEvents(context.Background(), connect.NewRequest(&viewerv1.ListEventsRequest{
		Parent: testInv,
	}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.GetEvents(), 3)
	assert.Equal(t, "compiling", resp.Msg.GetEvents()[0].GetText())
	assert.Empty(t, resp.Msg.GetNextPageToken())
}

// TestListEventsFilterNarrows is the regression for the filter being declared on the request and
// silently ignored: a filtered call returned the whole journal.
func TestListEventsFilterNarrows(t *testing.T) {
	dir := runsWith(t,
		at(1, journal.Output{Project: "web", Text: "compiling"}),
		at(2, journal.Output{Project: "api", Text: "linking"}),
		at(3, journal.Run{Project: "web", Status: journal.StatusFail}),
	)
	s := NewService(&storedOutputs{}, &storedRuns{}, dir)

	resp, err := s.ListEvents(context.Background(), connect.NewRequest(&viewerv1.ListEventsRequest{
		Parent: testInv,
		Filter: &viewerv1.EventQuery{Projects: []string{"web"}, Kinds: []string{string(journal.KindOutput)}},
	}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.GetEvents(), 1)
	assert.Equal(t, "compiling", resp.Msg.GetEvents()[0].GetText())
}

// TestListEventsFiltersBeforePaging pins the ORDER of the two operations. The one matching event
// sits past the first page, so cutting the page first would answer "none", a false absence a
// caller cannot tell from an empty journal.
func TestListEventsFiltersBeforePaging(t *testing.T) {
	records := make([]journal.Record, 0, 51)
	for i := range 50 {
		records = append(records, at(int64(i+1), journal.Output{Project: "noise"}))
	}
	records = append(records, at(99, journal.Output{Project: "web", Text: "needle"}))
	s := NewService(&storedOutputs{}, &storedRuns{}, runsWith(t, records...))

	resp, err := s.ListEvents(context.Background(), connect.NewRequest(&viewerv1.ListEventsRequest{
		Parent:   testInv,
		PageSize: 10,
		Filter:   &viewerv1.EventQuery{Projects: []string{"web"}},
	}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.GetEvents(), 1)
	assert.Equal(t, "needle", resp.Msg.GetEvents()[0].GetText())
	assert.Empty(t, resp.Msg.GetNextPageToken(), "page_size counts matching events, so one match is the whole list")
}

// TestListEventsPagesFilteredMatches checks page_size bounds the MATCHING events and the next
// token resumes at the right offset within them.
func TestListEventsPagesFilteredMatches(t *testing.T) {
	records := make([]journal.Record, 0, 6)
	for i := range 6 {
		p := "web"
		if i%2 == 1 {
			p = "api"
		}
		records = append(records, at(int64(i+1), journal.Output{Project: p}))
	}
	s := NewService(&storedOutputs{}, &storedRuns{}, runsWith(t, records...))
	filter := &viewerv1.EventQuery{Projects: []string{"web"}}

	first, err := s.ListEvents(context.Background(), connect.NewRequest(&viewerv1.ListEventsRequest{
		Parent: testInv, PageSize: 2, Filter: filter,
	}))
	require.NoError(t, err)
	require.Len(t, first.Msg.GetEvents(), 2)
	require.Equal(t, "2", first.Msg.GetNextPageToken())

	second, err := s.ListEvents(context.Background(), connect.NewRequest(&viewerv1.ListEventsRequest{
		Parent: testInv, PageSize: 2, PageToken: first.Msg.GetNextPageToken(), Filter: filter,
	}))
	require.NoError(t, err)
	require.Len(t, second.Msg.GetEvents(), 1, "three web events in all, two already served")
	assert.Empty(t, second.Msg.GetNextPageToken())
}

// TestGetInvocationResolvesRefAndID covers both spellings of a run's resource name: an invocation
// id used as-is, and an output ref resolved through the descriptor to the run that produced it.
func TestGetInvocationResolvesRefAndID(t *testing.T) {
	dir := runsWith(t,
		at(1700, journal.Started{
			Command: journal.Command{Arguments: []string{"magus", "run", "build"}, Trigger: journal.TriggerRun},
			Version: "0.1.0",
		}),
		at(1900, journal.Finished{Status: journal.StatusPass}),
	)
	runs := &storedRuns{desc: map[string]cache.OutputDescriptor{"out1a2b3c": {Ref: "out1a2b3c", Inv: testInv}}}
	s := NewService(&storedOutputs{}, runs, dir)

	byID, err := s.GetInvocation(context.Background(), connect.NewRequest(&viewerv1.GetInvocationRequest{Name: testInv}))
	require.NoError(t, err)
	assert.Equal(t, testInv, byID.Msg.GetId())
	assert.Equal(t, []string{"magus", "run", "build"}, byID.Msg.GetCommand().GetArguments())
	assert.Equal(t, viewerv1.Status_STATUS_PASS, byID.Msg.GetStatus())
	assert.Equal(t, int64(1700), byID.Msg.GetStartTime().AsTime().UnixMilli())

	byRef, err := s.GetInvocation(context.Background(), connect.NewRequest(&viewerv1.GetInvocationRequest{Name: "out1a2b3c"}))
	require.NoError(t, err)
	assert.Equal(t, testInv, byRef.Msg.GetId())
}

// TestGetInvocationRejectsPreJournalRef pins the deliberate NotFound for an output whose run
// predates journalling: an empty journal would read as "this run did nothing".
func TestGetInvocationRejectsPreJournalRef(t *testing.T) {
	s := NewService(&storedOutputs{}, &storedRuns{
		desc: map[string]cache.OutputDescriptor{"out1a2b3c": {Ref: "out1a2b3c"}},
	}, t.TempDir())
	_, err := s.GetInvocation(context.Background(), connect.NewRequest(&viewerv1.GetInvocationRequest{Name: "out1a2b3c"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

// streamClient mounts the real ViewerService Connect handler over an httptest server and returns a
// client for it. connect.ServerStream has no injectable test sink, so a streaming RPC is exercised
// end to end, the same shape the status service's stream test uses. poll is tightened so a test
// does not wait on the production cadence.
func streamClient(t *testing.T, runsDir string) viewerv1alpha1connect.ViewerServiceClient { //nolint:ireturn // the generated client type is an interface; the implementation is chosen at runtime
	t.Helper()
	s := NewService(&storedOutputs{}, &storedRuns{}, runsDir)
	s.poll = 5 * time.Millisecond
	path, handler := viewerv1alpha1connect.NewViewerServiceHandler(s)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return viewerv1alpha1connect.NewViewerServiceClient(srv.Client(), srv.URL)
}

// TestStreamEventsReplaysThenTails is the RPC's whole contract in one run: what the journal already
// held arrives first, what the run appends next arrives as it lands, and the terminal event ends the
// stream instead of leaving the caller polling an idle journal forever.
func TestStreamEventsReplaysThenTails(t *testing.T) {
	dir := runsWith(t, at(1, journal.Output{Text: "compiling"}))
	client := streamClient(t, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := client.StreamEvents(ctx, connect.NewRequest(&viewerv1.StreamEventsRequest{Parent: testInv}))
	require.NoError(t, err)

	require.True(t, stream.Receive())
	assert.Equal(t, "compiling", stream.Msg().GetEvent().GetText())

	appendRecords(t, dir,
		at(2, journal.Output{Text: "linking"}),
		at(3, journal.Finished{Status: journal.StatusPass}),
	)
	require.True(t, stream.Receive())
	assert.Equal(t, "linking", stream.Msg().GetEvent().GetText())
	require.True(t, stream.Receive(), "the finished event is delivered, not swallowed")

	assert.False(t, stream.Receive(), "a finished run ends the stream")
	require.NoError(t, stream.Err())
	require.NoError(t, stream.Close())
}

// TestStreamEventsFilterNarrows checks the filter is applied before each send, and (the part worth
// pinning) that the terminal event is read from the UNFILTERED batch: a filter excluding it must
// still end the stream.
func TestStreamEventsFilterNarrows(t *testing.T) {
	dir := runsWith(t,
		at(1, journal.Exec{Command: "go build"}),
		at(2, journal.Output{Text: "compiling"}),
		at(3, journal.Finished{Status: journal.StatusPass}),
	)
	client := streamClient(t, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := client.StreamEvents(ctx, connect.NewRequest(&viewerv1.StreamEventsRequest{
		Parent: testInv,
		Filter: &viewerv1.EventQuery{Kinds: []string{string(journal.KindOutput)}},
	}))
	require.NoError(t, err)

	require.True(t, stream.Receive())
	assert.Equal(t, "compiling", stream.Msg().GetEvent().GetText())
	assert.False(t, stream.Receive(), "only the matching event streams, and the run still ends it")
	require.NoError(t, stream.Err())
}

// TestStreamEventsResumesFromSince covers reconnecting: filter.time.since trims the replay to what
// the caller has not seen. The boundary is inclusive, so the event resumed FROM arrives again;
// at-least-once is the honest guarantee for a millisecond cursor several events can share.
func TestStreamEventsResumesFromSince(t *testing.T) {
	dir := runsWith(t,
		at(1, journal.Output{Text: "compiling"}),
		at(2, journal.Output{Text: "linking"}),
		at(3, journal.Finished{Status: journal.StatusPass}),
	)
	client := streamClient(t, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := client.StreamEvents(ctx, connect.NewRequest(&viewerv1.StreamEventsRequest{
		Parent: testInv,
		Filter: &viewerv1.EventQuery{Time: &queryv1.TimeRange{Since: timestamppb.New(time.UnixMilli(2))}},
	}))
	require.NoError(t, err)

	require.True(t, stream.Receive())
	assert.Equal(t, "linking", stream.Msg().GetEvent().GetText(), "the pre-cursor event is not replayed")
	require.True(t, stream.Receive())
	assert.False(t, stream.Receive())
	require.NoError(t, stream.Err())
}

// TestStreamEventsEndsOnClientCancel checks a caller walking away from an unfinished run ends the
// RPC rather than leaving the server polling a journal nobody reads.
func TestStreamEventsEndsOnClientCancel(t *testing.T) {
	client := streamClient(t, runsWith(t, at(1, journal.Output{Text: "compiling"})))

	ctx, cancel := context.WithCancel(context.Background())
	stream, err := client.StreamEvents(ctx, connect.NewRequest(&viewerv1.StreamEventsRequest{Parent: testInv}))
	require.NoError(t, err)
	require.True(t, stream.Receive())

	cancel()
	assert.False(t, stream.Receive())
	assert.Equal(t, connect.CodeCanceled, connect.CodeOf(stream.Err()))
}

// TestStreamEventsUnknownInvocation checks an invocation with no journal is NotFound at the first
// read rather than an empty stream, which a caller cannot tell from a run that has yet to emit.
func TestStreamEventsUnknownInvocation(t *testing.T) {
	client := streamClient(t, t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := client.StreamEvents(ctx, connect.NewRequest(&viewerv1.StreamEventsRequest{Parent: "invmissing"}))
	require.NoError(t, err)
	require.False(t, stream.Receive())
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(stream.Err()))
}

// TestListEventsRejectsBadPageToken checks an unparseable token errors rather than restarting at
// page one, which a caller would read as the end of the list.
func TestListEventsRejectsBadPageToken(t *testing.T) {
	s := NewService(&storedOutputs{}, &storedRuns{}, runsWith(t, at(1, journal.Output{})))
	_, err := s.ListEvents(context.Background(), connect.NewRequest(&viewerv1.ListEventsRequest{
		Parent: testInv, PageToken: "not-a-number",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}
