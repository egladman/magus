package bindings

import (
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jobNamespaceWorkspace is the smallest workspace the job namespace needs: job rows
// are repository-scoped, while the cache directory is only retained for the legacy
// adoption path and resolving a returned attempt.
type jobNamespaceWorkspace struct {
	types.WorkspaceRepository
	cacheDir string
	root     string
}

func (w *jobNamespaceWorkspace) CacheDir() string { return w.cacheDir }
func (w *jobNamespaceWorkspace) Root() string     { return w.root }

// TestJobExitWithoutResultThroughBuzzScript exercises the actual parser -> VM ->
// direct-binding path. Buzz materializes an omitted optional map as {}, so testing the
// Go callable directly would miss the only call shape that matters to a script author.
func TestJobExitWithoutResultThroughBuzzScript(t *testing.T) {
	testkit.Isolate(t)
	workspace := &jobNamespaceWorkspace{cacheDir: t.TempDir(), root: t.TempDir()}
	ctx := types.WithWorkspace(t.Context(), workspace)
	sess := buzz.NewSession(ctx)
	t.Cleanup(func() { _ = sess.Close() })
	RegisterModules(ctx, sess)
	RegisterMagusNamespace(ctx, sess)
	DeclareMagusTypes(sess)

	require.NoError(t, sess.Exec(ctx, `
import "magus";

export fun abandon() > str !> any {
    magus\job.put("optional-exit", opts: {"criteria": "exercise omitted result", "read_only": true});
    final row = magus\job.exit("optional-exit");
    return row.state.value;
}

export fun waitOnNoReturn() > void !> any {
    magus\job.wait("optional-exit");
}
`))
	fn, ok := sess.Exports()["abandon"]
	require.True(t, ok, "exported Buzz entrypoint is missing")

	got, err := sess.CallValue(ctx, fn, nil)
	require.NoError(t, err)
	assert.Equal(t, string(types.StateNoReturn), got.AsString())

	wait, ok := sess.Exports()["waitOnNoReturn"]
	require.True(t, ok, "exported Buzz wait entrypoint is missing")
	_, err = sess.CallValue(ctx, wait, nil)
	require.ErrorContains(t, err, "has filed no result")
}

// trail.read and job.put still take maps, decoded at the binding: an unknown key or a
// value of the wrong type raises there, and a put's mistakes come back joined in one raise.
func TestOptionMapsStayStrictThroughBuzzScript(t *testing.T) {
	testkit.Isolate(t)
	workspace := &jobNamespaceWorkspace{cacheDir: t.TempDir(), root: t.TempDir()}
	ctx := types.WithWorkspace(t.Context(), workspace)
	sess := buzz.NewSession(ctx)
	t.Cleanup(func() { _ = sess.Close() })
	RegisterModules(ctx, sess)
	RegisterMagusNamespace(ctx, sess)
	DeclareMagusTypes(sess)
	require.NoError(t, sess.Exec(ctx, `
import "magus";

export fun trailTypo() > void !> any { magus\trail.read({"sinse": "6h"}); }
export fun trailNumber() > void !> any { magus\trail.read({"session": 3}); }
export fun putMistakes() > void !> any { magus\job.put("w", opts: {"passsed": true, "state": "passed"}); }
`))
	for name, want := range map[string][]string{
		"trailTypo":   {`trail.read: unknown option "sinse"`},
		"trailNumber": {`trail.read: opts.session is int64, want a string`},
		"putMistakes": {"no field of a job row is named passsed", "state must be one of"},
	} {
		fn, ok := sess.Exports()[name]
		require.True(t, ok, "exported Buzz entrypoint %s is missing", name)
		_, err := sess.CallValue(ctx, fn, nil)
		for _, w := range want {
			require.ErrorContains(t, err, w, name)
		}
	}
}

func TestDecodeJobResultUsesTheVersionedStrictDecoder(t *testing.T) {
	t.Parallel()

	result, err := decodeJobResult(map[string]any{
		"schema_version": types.JobResultSchemaVersion,
		"changed_paths":  []any{"internal/job/lifecycle.go"},
		"validation": map[string]any{
			"command":    "magus run test .",
			"output_ref": "out123",
		},
		"unresolved_risks": []any{},
	})
	require.NoError(t, err)
	assert.Equal(t, types.JobResultSchemaVersion, result.Version)
	assert.Equal(t, "out123", result.Validation.OutputRef)

	_, err = decodeJobResult(map[string]any{
		"schema_version":   types.JobResultSchemaVersion,
		"changed_paths":    []any{},
		"validation":       map[string]any{},
		"unresolved_risks": []any{},
		"claimed_pass":     true,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "claimed_pass", "a Buzz map cannot smuggle a field the verifier ignores")
}

// magus\job.put is a door that writes a checkpoint, so it resolves one in the repository
// before writing it: a made-up revision raises, a real abbreviated one is stored in full.
func TestJobPutResolvesItsCheckpointThroughBuzzScript(t *testing.T) {
	testkit.Isolate(t)
	repo := testkit.NewRepository(t, nil)
	root, head := repo.Root(), repo.Head()

	workspace := &jobNamespaceWorkspace{cacheDir: t.TempDir(), root: root}
	ctx := types.WithWorkspace(t.Context(), workspace)
	sess := buzz.NewSession(ctx)
	t.Cleanup(func() { _ = sess.Close() })
	RegisterModules(ctx, sess)
	RegisterMagusNamespace(ctx, sess)
	DeclareMagusTypes(sess)
	require.NoError(t, sess.Exec(ctx, `
import "magus";

export fun put(id: str, checkpoint: str) > str !> any {
    final row = magus\job.put(id, opts: {"criteria": "hold a checkpoint", "checkpoint": checkpoint, "read_only": true});
    return row.checkpoint;
}
`))
	put, ok := sess.Exports()["put"]
	require.True(t, ok, "exported Buzz entrypoint is missing")

	const madeUp = "34b41fd546ba8fa9c71a6a64e4ea0f3e93d5a7bc"
	_, err := sess.CallValue(ctx, put, []vm.Value{vm.StringValue("made-up"), vm.StringValue(madeUp)})
	require.ErrorContains(t, err, madeUp)
	require.ErrorContains(t, err, "`magus vcs checkpoint -o name`")

	got, err := sess.CallValue(ctx, put, []vm.Value{vm.StringValue("real"), vm.StringValue(head[:9])})
	require.NoError(t, err)
	assert.Equal(t, head, got.AsString())
}
