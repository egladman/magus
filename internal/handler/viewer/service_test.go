package viewer

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/journal"
	viewerv1 "github.com/egladman/magus/proto/gen/go/magus/viewer/v1alpha1"
)

// fakeRuns is the runSource half of the handler's contract, filled in per test. It is a struct of
// stored answers rather than a mock: every RPC here reads, so what a test needs to say is what the
// store would have returned.
type fakeRuns struct {
	events []journal.Event
	header journal.Invocation
	err    error
	logs   []cache.RunLog
	desc   map[string]cache.OutputDescriptor
}

func (f *fakeRuns) ListRunLogs(int) []cache.RunLog { return f.logs }

func (f *fakeRuns) InvocationEventsByID(string) (journal.Invocation, []journal.Event, error) {
	if f.err != nil {
		return journal.Invocation{}, nil, f.err
	}
	return f.header, f.events, nil
}

func (f *fakeRuns) DescriptorByRef(ref string) (cache.OutputDescriptor, error) {
	d, ok := f.desc[ref]
	if !ok {
		return cache.OutputDescriptor{}, errors.New("no such ref")
	}
	return d, nil
}

// fakeOutputs is the outputSource half. The ListEvents/GetInvocation tests never reach it.
type fakeOutputs struct {
	descs []cache.OutputDescriptor
	body  []byte
}

func (f *fakeOutputs) ListDescriptors() []cache.OutputDescriptor { return f.descs }

func (f *fakeOutputs) ByRef(string) ([]byte, cache.OutputDescriptor, error) {
	return f.body, cache.OutputDescriptor{}, nil
}

// TestListEventsUnfiltered checks the whole journal comes back when no filter is set, in stored
// order, with the next token empty because one page held everything.
func TestListEventsUnfiltered(t *testing.T) {
	runs := &fakeRuns{events: []journal.Event{
		{Ts: 1, Project: "web", Kind: journal.KindOutput, Text: "compiling"},
		{Ts: 2, Project: "api", Kind: journal.KindOutput, Text: "linking"},
		{Ts: 3, Project: "web", Kind: journal.KindResult, Status: journal.StatusFail},
	}}
	s := NewService(&fakeOutputs{}, runs)

	resp, err := s.ListEvents(context.Background(), connect.NewRequest(&viewerv1.ListEventsRequest{
		Parent: "inv1a2b3c",
	}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.GetEvents(), 3)
	assert.Equal(t, "compiling", resp.Msg.GetEvents()[0].GetText())
	assert.Empty(t, resp.Msg.GetNextPageToken())
}

// TestListEventsFilterNarrows is the regression for the filter being declared on the request and
// silently ignored: a filtered call returned the whole journal.
func TestListEventsFilterNarrows(t *testing.T) {
	runs := &fakeRuns{events: []journal.Event{
		{Ts: 1, Project: "web", Kind: journal.KindOutput, Text: "compiling"},
		{Ts: 2, Project: "api", Kind: journal.KindOutput, Text: "linking"},
		{Ts: 3, Project: "web", Kind: journal.KindResult, Status: journal.StatusFail},
	}}
	s := NewService(&fakeOutputs{}, runs)

	resp, err := s.ListEvents(context.Background(), connect.NewRequest(&viewerv1.ListEventsRequest{
		Parent: "inv1a2b3c",
		Filter: &viewerv1.EventQuery{Projects: []string{"web"}, Kinds: []string{journal.KindOutput}},
	}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.GetEvents(), 1)
	assert.Equal(t, "compiling", resp.Msg.GetEvents()[0].GetText())
}

// TestListEventsFiltersBeforePaging pins the ORDER of the two operations. The one matching event
// sits past the first page, so cutting the page first would answer "none" - a false absence a
// caller cannot tell from an empty journal.
func TestListEventsFiltersBeforePaging(t *testing.T) {
	events := make([]journal.Event, 0, 51)
	for i := range 50 {
		events = append(events, journal.Event{Ts: int64(i), Project: "noise", Kind: journal.KindOutput})
	}
	events = append(events, journal.Event{Ts: 99, Project: "web", Kind: journal.KindOutput, Text: "needle"})
	s := NewService(&fakeOutputs{}, &fakeRuns{events: events})

	resp, err := s.ListEvents(context.Background(), connect.NewRequest(&viewerv1.ListEventsRequest{
		Parent:   "inv1a2b3c",
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
	events := make([]journal.Event, 0, 6)
	for i := range 6 {
		p := "web"
		if i%2 == 1 {
			p = "api"
		}
		events = append(events, journal.Event{Ts: int64(i), Project: p, Kind: journal.KindOutput})
	}
	s := NewService(&fakeOutputs{}, &fakeRuns{events: events})
	filter := &viewerv1.EventQuery{Projects: []string{"web"}}

	first, err := s.ListEvents(context.Background(), connect.NewRequest(&viewerv1.ListEventsRequest{
		Parent: "inv1a2b3c", PageSize: 2, Filter: filter,
	}))
	require.NoError(t, err)
	require.Len(t, first.Msg.GetEvents(), 2)
	require.Equal(t, "2", first.Msg.GetNextPageToken())

	second, err := s.ListEvents(context.Background(), connect.NewRequest(&viewerv1.ListEventsRequest{
		Parent: "inv1a2b3c", PageSize: 2, PageToken: first.Msg.GetNextPageToken(), Filter: filter,
	}))
	require.NoError(t, err)
	require.Len(t, second.Msg.GetEvents(), 1, "three web events in all, two already served")
	assert.Empty(t, second.Msg.GetNextPageToken())
}

// TestGetInvocationResolvesRefAndID covers both spellings of a run's resource name: an invocation
// id used as-is, and an output ref resolved through the descriptor to the run that produced it.
func TestGetInvocationResolvesRefAndID(t *testing.T) {
	runs := &fakeRuns{
		header: journal.Invocation{
			ID:           "inv1a2b3c",
			Command:      journal.Command{Arguments: []string{"magus", "run", "build"}, Trigger: "run"},
			StartedMs:    1700,
			FinishedMs:   1900,
			Status:       journal.StatusPass,
			MagusVersion: "0.1.0",
		},
		desc: map[string]cache.OutputDescriptor{"out1a2b3c": {Ref: "out1a2b3c", Inv: "inv1a2b3c"}},
	}
	s := NewService(&fakeOutputs{}, runs)

	byID, err := s.GetInvocation(context.Background(), connect.NewRequest(&viewerv1.GetInvocationRequest{Name: "inv1a2b3c"}))
	require.NoError(t, err)
	assert.Equal(t, "inv1a2b3c", byID.Msg.GetId())
	assert.Equal(t, []string{"magus", "run", "build"}, byID.Msg.GetCommand().GetArguments())
	assert.Equal(t, viewerv1.Status_STATUS_PASS, byID.Msg.GetStatus())
	assert.Equal(t, int64(1700), byID.Msg.GetStartTime().AsTime().UnixMilli())

	byRef, err := s.GetInvocation(context.Background(), connect.NewRequest(&viewerv1.GetInvocationRequest{Name: "out1a2b3c"}))
	require.NoError(t, err)
	assert.Equal(t, "inv1a2b3c", byRef.Msg.GetId())
}

// TestGetInvocationRejectsPreJournalRef pins the deliberate NotFound for an output whose run
// predates journalling: an empty journal would read as "this run did nothing".
func TestGetInvocationRejectsPreJournalRef(t *testing.T) {
	s := NewService(&fakeOutputs{}, &fakeRuns{
		desc: map[string]cache.OutputDescriptor{"out1a2b3c": {Ref: "out1a2b3c"}},
	})
	_, err := s.GetInvocation(context.Background(), connect.NewRequest(&viewerv1.GetInvocationRequest{Name: "out1a2b3c"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

// TestStreamEventsIsUnimplemented pins the CURRENT answer, not a desired one. The RPC is declared
// and deliberately not served - live events ride the SSE route in live.go - and whether it gets an
// implementation or gets removed is undecided. Either way the code must be a deliberate
// Unimplemented rather than a handler that hangs or panics.
func TestStreamEventsIsUnimplemented(t *testing.T) {
	s := NewService(&fakeOutputs{}, &fakeRuns{})
	err := s.StreamEvents(context.Background(), connect.NewRequest(&viewerv1.StreamEventsRequest{Parent: "inv1a2b3c"}), nil)
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
}

// TestListEventsRejectsBadPageToken checks an unparseable token errors rather than restarting at
// page one, which a caller would read as the end of the list.
func TestListEventsRejectsBadPageToken(t *testing.T) {
	s := NewService(&fakeOutputs{}, &fakeRuns{events: []journal.Event{{Ts: 1, Kind: journal.KindOutput}}})
	_, err := s.ListEvents(context.Background(), connect.NewRequest(&viewerv1.ListEventsRequest{
		Parent: "inv1a2b3c", PageToken: "not-a-number",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}
