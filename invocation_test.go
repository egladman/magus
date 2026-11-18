package magus

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/egladman/magus/internal/proc/spawn"
	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/libs/testkit/register"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBeginInvocationWritesLifecycle(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.Magusfile("")
	root := w.Root()
	m, err := Inspect(context.Background(), root, WithSpawnRule(tier.Ceiling))
	require.NoError(t, err)
	workspace := m.(*Magus)

	ctx, finish := workspace.BeginInvocation(context.Background(), Command{
		Arguments: []string{"test"},
		Cwd:       root,
		Trigger:   string(journal.TriggerRun),
	}, "v0.test")
	id := recorder.InvocationIDFromContext(ctx)
	require.NotEmpty(t, id)
	finish(errors.New("failed"))

	var kinds []journal.Kind
	var started journal.Started
	var finished journal.Finished
	for r, err := range journal.Records(t.Context(), workspace.runsDir(), id) {
		require.NoError(t, err)
		assert.Equal(t, id, r.Invocation)
		kinds = append(kinds, r.Kind())
		if b, ok := r.Started(); ok {
			started = b
		}
		if b, ok := r.Finished(); ok {
			finished = b
		}
	}
	assert.Equal(t, []journal.Kind{journal.KindStarted, journal.KindFinished}, kinds)
	assert.Equal(t, journal.Started{
		Command: journal.Command{Arguments: []string{"test"}, Cwd: root, Trigger: journal.TriggerRun},
		Version: "v0.test",
	}, started)
	assert.Equal(t, journal.Finished{Status: journal.StatusFail}, finished)
}

// attributeRun is what keeps a run identifiable when the caller is not the CLI. The two
// halves are asserted separately because they fail differently: an id with no ancestor
// entry still logs, and still lets a descendant take a second machine claim.
func TestAttributeRunNamesAnAnonymousRun(t *testing.T) {
	ctx := attributeRun(context.Background())

	id := recorder.InvocationIDFromContext(ctx)
	require.NotEmpty(t, id, "a run reached the engine with no invocation identity")
	assert.True(t, types.HasInvocationAncestor(ctx, os.Getpid(), id),
		"the run is not its own ancestor, so a descendant cannot recognize the resources it holds")
}

// A library caller inside a magus process tree carries its ancestry in the environment and
// nowhere else. Stamping only self leaves a one-element list, which reads as "this run
// minted everything it knows" and strips to empty at the consumer, so the machine budget
// refuses against a parent claim it can no longer excuse and the project lock can no longer
// report MGS3007. Both fallbacks fire only on an empty ctx, so the adopt must come first.
func TestAttributeRunAdoptsAncestryFromTheEnvironment(t *testing.T) {
	parent := "4242:invparent"
	t.Setenv(spawn.AncestorsEnvVar, parent)

	ctx := attributeRun(context.Background())

	refs := types.InvocationAncestorsFromContext(ctx)
	require.Len(t, refs, 2, "the parent's ancestry was dropped, so this run reads as its own root")
	assert.Equal(t, parent, refs[0], "the adopted ancestor must stay ahead of self")
	assert.True(t, types.HasInvocationAncestor(ctx, os.Getpid(), recorder.InvocationIDFromContext(ctx)))
}

// An identity already on the context is the CLI's or the server's, and taking a second one
// would orphan the first: the journal file, the pool entry and the locks are all keyed on
// it, and a descendant comparing ancestry would stop recognizing its parent.
func TestAttributeRunKeepsAnIdentityItWasGiven(t *testing.T) {
	given := recorder.NewInvocationID()
	ctx := attributeRun(recorder.WithInvocationID(context.Background(), given))

	assert.Equal(t, given, recorder.InvocationIDFromContext(ctx))
}

// The identity has to reach the WORK, not merely the context runResolved holds: a
// dispatched step recognizes an ancestor's machine claim by reading the ancestry off the
// context it is invoked with. Driven through the public Run, because the three tests
// above call attributeRun themselves and so stay green when nothing calls it.
func TestLibraryRunStampsTheIdentityTheWorkReads(t *testing.T) {
	var invoked context.Context
	spellName := register.Spell(t, "zzz-invocation-identity-spell",
		spells.WithTargets("build"),
		spells.WithInvoker(func(ctx context.Context, _ spells.InvokeRequest) (any, error) {
			invoked = ctx
			return nil, nil
		}),
	)

	w := testkit.NewWorkspace(t)
	w.Magusfile("")
	root := w.Root()

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(spellName))
	m, err := Open(context.Background(), root, WithWorkspaceRegistry(reg), WithSpawnRule(tier.Ceiling))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	require.NoError(t, m.Run(context.Background(), []types.Target{{Path: ".", Name: "build"}}), "Run")
	require.NotNil(t, invoked, "the target never ran, so this proves nothing about its context")

	id := recorder.InvocationIDFromContext(invoked)
	require.NotEmpty(t, id, "a library run reached the work anonymous: nothing it dispatches can be attributed")
	assert.True(t, types.HasInvocationAncestor(invoked, os.Getpid(), id),
		"the work does not carry this run as an ancestor, so a dispatch takes a second machine claim against the reservation the parent already holds")
}

// covInspect opens a throwaway single-project workspace with no cache, which is
// all the journal accessors need: they read the output store straight off the
// resolved cache dir.
func covInspect(t *testing.T) *Magus {
	t.Helper()
	root := writeWorkspace(t, map[string]string{"magusfile.buzz": ""})
	ws, err := Inspect(context.Background(), root, WithSpawnRule(tier.Ceiling))
	require.NoError(t, err)
	return ws.(*Magus)
}

// TestInvocationRoundTripsThroughTheJournal is the read side of folding a run's
// identity into its event stream: there is no separate metadata file, so the
// header a caller reads back has to be reconstructed from the two lifecycle
// events BeginInvocation brackets the run with.
func TestInvocationRoundTripsThroughTheJournal(t *testing.T) {
	m := covInspect(t)
	cmd := Command{Arguments: []string{"run", "build", "api"}, Cwd: m.Root(), Trigger: string(journal.TriggerRun)}

	ctx, finish := m.BeginInvocation(context.Background(), cmd, "v0.cov")
	id := recorder.InvocationIDFromContext(ctx)
	require.NotEmpty(t, id)
	finish(nil)

	inv, err := m.InvocationByID(id)
	require.NoError(t, err)
	// The clock fields are taken from inv and checked below; finish(nil) is a passing run.
	assert.Equal(t, Invocation{
		ID:           id,
		Command:      Command{Arguments: []string{"run", "build", "api"}, Cwd: m.Root(), Trigger: string(journal.TriggerRun)},
		StartedMs:    inv.StartedMs,
		FinishedMs:   inv.FinishedMs,
		Status:       string(journal.StatusPass),
		MagusVersion: "v0.cov",
	}, inv)
	assert.Positive(t, inv.StartedMs)
	assert.GreaterOrEqual(t, inv.FinishedMs, inv.StartedMs)

	header, events, err := m.InvocationEventsByID(id)
	require.NoError(t, err)
	assert.Equal(t, inv, header, "both accessors reconstruct the same header")

	require.Len(t, events, 2)
	assert.Equal(t, Event{
		Timestamp:    events[0].Timestamp, // wall clock
		Invocation:   id,
		Kind:         string(journal.KindStarted),
		Command:      &Command{Arguments: []string{"run", "build", "api"}, Cwd: m.Root(), Trigger: string(journal.TriggerRun)},
		MagusVersion: "v0.cov",
	}, events[0])

	// The lineage rides the started event alone, so the finished event has no Command.
	assert.Equal(t, Event{
		Timestamp:  events[1].Timestamp, // wall clock
		Invocation: id,
		Kind:       string(journal.KindFinished),
		Status:     string(journal.StatusPass),
	}, events[1])
}

// TestBeginInvocationReusesAnIDFromTheContext: the server mints the id before
// adopting a call, so its pool entry can deep-link to this run's live log. Minting
// a second one here would break that link.
func TestBeginInvocationReusesAnIDFromTheContext(t *testing.T) {
	m := covInspect(t)
	preset := recorder.NewInvocationID()

	ctx, finish := m.BeginInvocation(recorder.WithInvocationID(context.Background(), preset),
		Command{Arguments: []string{"run"}, Trigger: string(journal.TriggerDirect)}, "v0.cov")
	assert.Equal(t, preset, recorder.InvocationIDFromContext(ctx))
	finish(nil)

	inv, err := m.InvocationByID(preset)
	require.NoError(t, err)
	assert.Equal(t, preset, inv.ID)
}

// TestNewInvocationCarriesEveryField guards the caller-facing projection: a field
// dropped here vanishes from the viewer and from `magus query output <ref> --meta`,
// so the two would disagree about one run.
func TestNewInvocationCarriesEveryField(t *testing.T) {
	got := newInvocation(journal.Invocation{
		ID: "inv1",
		Command: journal.Command{
			Arguments: []string{"run", "build"},
			Cwd:       "/w",
			Trigger:   journal.TriggerAffected,
		},
		Started: time.UnixMilli(1700000000000), Finished: time.UnixMilli(1700000001000),
		Status: journal.StatusFail, Version: "v0.9.0",
	})

	assert.Equal(t, Invocation{
		ID:        "inv1",
		Command:   Command{Arguments: []string{"run", "build"}, Cwd: "/w", Trigger: string(journal.TriggerAffected)},
		StartedMs: 1700000000000, FinishedMs: 1700000001000,
		Status: string(journal.StatusFail), MagusVersion: "v0.9.0",
	}, got)

	assert.Equal(t, Invocation{}, newInvocation(journal.Invocation{}))
}

func TestNewEventCarriesEveryField(t *testing.T) {
	got, err := newEvent(journal.NewRecord(time.UnixMilli(1700000000000), "inv1", journal.Run{
		Project: "api", Target: "build:rw", Level: "info",
		Status: journal.StatusCached, Ref: "outdeadbe", Duration: 42 * time.Millisecond, Error: "done",
	}))
	require.NoError(t, err)

	assert.Equal(t, Event{
		Timestamp: 1700000000000, Invocation: "inv1", Project: "api", Target: "build:rw",
		Kind: string(journal.KindRun), Level: "info",
		Status: string(journal.StatusCached), Ref: "outdeadbe", Duration: 42 * time.Millisecond, Text: "done",
	}, got)
}

// TestEventJSONKeepsTheWireForm: older readers (the CI jq step, saved query output)
// depend on the keys ts, inv and duration_ms.
func TestEventJSONKeepsTheWireForm(t *testing.T) {
	const line = `{"ts":1700000000000,"inv":"inv1","project":"api","target":"build:rw","kind":"result","status":"cached","ref":"outdeadbe","text":"done","duration_ms":42}`

	var got Event
	require.NoError(t, codec.Unmarshal([]byte(line), &got))
	assert.Equal(t, Event{
		Timestamp: 1700000000000, Invocation: "inv1", Project: "api", Target: "build:rw",
		Kind: string(journal.KindRun), Status: string(journal.StatusCached), Ref: "outdeadbe",
		Duration: 42 * time.Millisecond, Text: "done",
	}, got)

	out, err := codec.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, line, string(out))

	out, err = codec.Marshal(Event{Kind: string(journal.KindOutput)})
	require.NoError(t, err)
	assert.Equal(t, `{"ts":0,"kind":"output","duration_ms":0}`, string(out), "json/v2 omitempty keeps a zero duration_ms, as the journal writes it")
}

// TestNewEventCopiesTheCommand: the projection must not alias the journal's own
// Command, or a caller mutating what it read would reach back into the stream.
func TestNewEventCopiesTheCommand(t *testing.T) {
	src := journal.NewRecord(time.Time{}, "", journal.Started{
		Command: journal.Command{Arguments: []string{"run"}, Cwd: "/w", Trigger: journal.TriggerWatch},
	})

	got, err := newEvent(src)
	require.NoError(t, err)
	require.NotNil(t, got.Command)
	assert.Equal(t, Command{Arguments: []string{"run"}, Cwd: "/w", Trigger: string(journal.TriggerWatch)}, *got.Command)

	got.Command.Cwd = "/scribbled"
	started, _ := src.Started()
	assert.Equal(t, "/w", started.Command.Cwd)

	output, err := newEvent(journal.NewRecord(time.Time{}, "", journal.Output{}))
	require.NoError(t, err)
	assert.Nil(t, output.Command, "only the started event carries the lineage")
}
