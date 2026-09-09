package magus

import (
	"bytes"
	"context"
	"errors"
	json "github.com/egladman/magus/internal/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/journal"
	procrun "github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBeginInvocationWritesLifecycle(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), nil, 0o644))
	m, err := Inspect(context.Background(), root)
	require.NoError(t, err)
	workspace := m.(*Magus)

	ctx, finish := workspace.BeginInvocation(context.Background(), journal.Command{
		Arguments: []string{"test"},
		Cwd:       root,
		Trigger:   journal.TriggerRun,
	}, "v0.test")
	id := journal.InvocationIDFromContext(ctx)
	require.NotEmpty(t, id)
	finish(errors.New("failed"))

	body, err := os.ReadFile(filepath.Join(workspace.CacheDir(), "runs", id+".jsonl"))
	require.NoError(t, err)
	decoder := json.NewDecoder(bytes.NewReader(body))
	var events []journal.Event
	for {
		var event journal.Event
		err := decoder.Decode(&event)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		event.Ts = 0
		event.Inv = ""
		events = append(events, event)
	}
	assert.Equal(t, []journal.Event{
		{Kind: journal.KindStarted, Command: &journal.Command{Arguments: []string{"test"}, Cwd: root, Trigger: journal.TriggerRun}, MagusVersion: "v0.test"},
		{Kind: journal.KindFinished, Status: journal.StatusFail},
	}, events)
}

// attributeRun is what keeps a run identifiable when the caller is not the CLI. The two
// halves are asserted separately because they fail differently: an id with no ancestor
// entry still logs, and still lets a descendant take a second machine claim.
func TestAttributeRunNamesAnAnonymousRun(t *testing.T) {
	ctx := attributeRun(context.Background())

	id := journal.InvocationIDFromContext(ctx)
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
	t.Setenv(procrun.AncestorsEnvVar, parent)

	ctx := attributeRun(context.Background())

	refs := types.InvocationAncestorsFromContext(ctx)
	require.Len(t, refs, 2, "the parent's ancestry was dropped, so this run reads as its own root")
	assert.Equal(t, parent, refs[0], "the adopted ancestor must stay ahead of self")
	assert.True(t, types.HasInvocationAncestor(ctx, os.Getpid(), journal.InvocationIDFromContext(ctx)))
}

// An identity already on the context is the CLI's or the daemon's, and taking a second one
// would orphan the first: the journal file, the pool entry and the locks are all keyed on
// it, and a descendant comparing ancestry would stop recognizing its parent.
func TestAttributeRunKeepsAnIdentityItWasGiven(t *testing.T) {
	given := journal.NewInvocationID()
	ctx := attributeRun(journal.WithInvocationID(context.Background(), given))

	assert.Equal(t, given, journal.InvocationIDFromContext(ctx))
}

// The identity has to reach the WORK, not merely the context runResolved holds: a
// dispatched step recognizes an ancestor's machine claim by reading the ancestry off the
// context it is invoked with. Driven through the public Run, because the three tests
// above call attributeRun themselves and so stay green when nothing calls it.
func TestLibraryRunStampsTheIdentityTheWorkReads(t *testing.T) {
	const spellName = "zzz-invocation-identity-spell"
	var invoked context.Context
	spell := spells.NewSpell(spellName,
		spells.WithTargets("build"),
		spells.WithInvoker(func(ctx context.Context, _ spells.InvokeRequest) (any, error) {
			invoked = ctx
			return nil, nil
		}),
	)
	project.DefaultSpellRegistry().RegisterSpell(spell)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), nil, 0o644))

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(spellName))
	m, err := Open(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	require.NoError(t, m.Run(context.Background(), []types.Target{{Path: ".", Name: "build"}}), "Run")
	require.NotNil(t, invoked, "the target never ran, so this proves nothing about its context")

	id := journal.InvocationIDFromContext(invoked)
	require.NotEmpty(t, id, "a library run reached the work anonymous: nothing it dispatches can be attributed")
	assert.True(t, types.HasInvocationAncestor(invoked, os.Getpid(), id),
		"the work does not carry this run as an ancestor, so a dispatch takes a second machine claim against the reservation the parent already holds")
}
