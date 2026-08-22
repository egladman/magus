package magus

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/journal"
)

// covInspect opens a throwaway single-project workspace with no cache, which is
// all the journal accessors need: they read the output store straight off the
// resolved cache dir.
func covInspect(t *testing.T) *Magus {
	t.Helper()
	root := writeWorkspace(t, map[string]string{"magusfile.buzz": ""})
	ws, err := Inspect(context.Background(), root)
	require.NoError(t, err)
	return ws.(*Magus)
}

// TestInvocationRoundTripsThroughTheJournal is the read side of folding a run's
// identity into its event stream: there is no separate metadata file, so the
// header a caller reads back has to be reconstructed from the two lifecycle
// events BeginInvocation brackets the run with.
func TestInvocationRoundTripsThroughTheJournal(t *testing.T) {
	m := covInspect(t)
	cmd := journal.Command{Arguments: []string{"run", "build", "api"}, Cwd: m.Root(), Trigger: journal.TriggerRun}

	ctx, finish := m.BeginInvocation(context.Background(), cmd, "v0.cov")
	id := journal.InvocationIDFromContext(ctx)
	require.NotEmpty(t, id)
	finish(nil)

	inv, err := m.InvocationByID(id)
	require.NoError(t, err)
	assert.Equal(t, id, inv.ID)
	assert.Equal(t, Command{Arguments: []string{"run", "build", "api"}, Cwd: m.Root(), Trigger: journal.TriggerRun}, inv.Command)
	assert.Equal(t, "v0.cov", inv.MagusVersion)
	assert.Equal(t, journal.StatusPass, inv.Status, "finish(nil) is a passing run")
	assert.Positive(t, inv.StartedMs)
	assert.GreaterOrEqual(t, inv.FinishedMs, inv.StartedMs)

	header, events, err := m.InvocationEventsByID(id)
	require.NoError(t, err)
	assert.Equal(t, inv, header, "both accessors reconstruct the same header")

	require.Len(t, events, 2)
	assert.Equal(t, journal.KindStarted, events[0].Kind)
	assert.Equal(t, id, events[0].Inv)
	assert.Equal(t, "v0.cov", events[0].MagusVersion)
	require.NotNil(t, events[0].Command)
	assert.Equal(t, []string{"run", "build", "api"}, events[0].Command.Arguments)

	assert.Equal(t, journal.KindFinished, events[1].Kind)
	assert.Equal(t, journal.StatusPass, events[1].Status)
	assert.Nil(t, events[1].Command, "the lineage rides the started event alone")
}

// TestBeginInvocationReusesAnIDFromTheContext: the daemon mints the id before
// adopting a call, so its pool entry can deep-link to this run's live log. Minting
// a second one here would break that link.
func TestBeginInvocationReusesAnIDFromTheContext(t *testing.T) {
	m := covInspect(t)
	preset := journal.NewInvocationID()

	ctx, finish := m.BeginInvocation(journal.WithInvocationID(context.Background(), preset),
		journal.Command{Arguments: []string{"run"}, Trigger: journal.TriggerDirect}, "v0.cov")
	assert.Equal(t, preset, journal.InvocationIDFromContext(ctx))
	finish(nil)

	inv, err := m.InvocationByID(preset)
	require.NoError(t, err)
	assert.Equal(t, preset, inv.ID)
}

// TestNewInvocationCarriesEveryField guards the caller-facing projection: a field
// dropped here vanishes from the viewer and from `magus query output <ref> --meta`,
// and the JSON tags match journal.Invocation's for wire compatibility.
func TestNewInvocationCarriesEveryField(t *testing.T) {
	got := newInvocation(journal.Invocation{
		ID: "inv1",
		Command: journal.Command{
			Arguments: []string{"run", "build"},
			Cwd:       "/w",
			Trigger:   journal.TriggerAffected,
		},
		StartedMs: 1700000000000, FinishedMs: 1700000001000,
		Status: journal.StatusFail, MagusVersion: "v0.9.0",
	})

	assert.Equal(t, Invocation{
		ID:        "inv1",
		Command:   Command{Arguments: []string{"run", "build"}, Cwd: "/w", Trigger: journal.TriggerAffected},
		StartedMs: 1700000000000, FinishedMs: 1700000001000,
		Status: journal.StatusFail, MagusVersion: "v0.9.0",
	}, got)

	assert.Equal(t, Invocation{}, newInvocation(journal.Invocation{}))
}

func TestNewEventCarriesEveryField(t *testing.T) {
	got := newEvent(journal.Event{
		Ts: 1700000000000, Inv: "inv1", Project: "api", Target: "build:rw",
		Kind: journal.KindResult, Stream: "stdout", Level: "info",
		Status: journal.StatusCached, Ref: "outdeadbe", DurMs: 42, Text: "done",
		MagusVersion: "v0.9.0",
	})

	assert.Equal(t, Event{
		Ts: 1700000000000, Inv: "inv1", Project: "api", Target: "build:rw",
		Kind: journal.KindResult, Stream: "stdout", Level: "info",
		Status: journal.StatusCached, Ref: "outdeadbe", DurMs: 42, Text: "done",
		MagusVersion: "v0.9.0",
	}, got)
}

// TestNewEventCopiesTheCommand: the projection must not alias the journal's own
// Command, or a caller mutating what it read would reach back into the stream.
func TestNewEventCopiesTheCommand(t *testing.T) {
	src := journal.Event{
		Kind:    journal.KindStarted,
		Command: &journal.Command{Arguments: []string{"run"}, Cwd: "/w", Trigger: journal.TriggerWatch},
	}

	got := newEvent(src)
	require.NotNil(t, got.Command)
	assert.Equal(t, Command{Arguments: []string{"run"}, Cwd: "/w", Trigger: journal.TriggerWatch}, *got.Command)

	got.Command.Cwd = "/scribbled"
	assert.Equal(t, "/w", src.Command.Cwd)

	assert.Nil(t, newEvent(journal.Event{Kind: journal.KindOutput}).Command,
		"only the started event carries the lineage")
}

// TestNewEvents keeps nil distinct from empty: a stream that was never read and
// one that held no events are different answers.
func TestNewEvents(t *testing.T) {
	assert.Nil(t, newEvents(nil))
	assert.Empty(t, newEvents([]journal.Event{}))
	assert.Equal(t, []Event{
		{Kind: journal.KindStarted},
		{Kind: journal.KindFinished, Status: journal.StatusPass},
	}, newEvents([]journal.Event{
		{Kind: journal.KindStarted},
		{Kind: journal.KindFinished, Status: journal.StatusPass},
	}))
}
