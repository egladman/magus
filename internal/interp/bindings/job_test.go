package bindings

import (
	"os"
	"os/exec"
	"strings"
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

	require.NoError(t, sess.Exec(ctx, `
import "magus";

export fun abandon() > str !> any {
    magus\job.put("optional-exit", opts: {"criteria": "exercise omitted result"});
    final row = magus\job.exit("optional-exit");
    return row.state;
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

// magus\job.put is a door that writes a checkpoint, so it resolves one in the repository
// before writing it: a made-up revision raises, a real abbreviated one is stored in full.
func TestJobPutResolvesItsCheckpointThroughBuzzScript(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "seed")
	head := git("rev-parse", "HEAD")

	workspace := &jobNamespaceWorkspace{cacheDir: t.TempDir(), root: root}
	ctx := types.WithWorkspace(t.Context(), workspace)
	sess := buzz.NewSession(ctx)
	t.Cleanup(func() { _ = sess.Close() })
	RegisterModules(ctx, sess)
	RegisterMagusNamespace(ctx, sess)
	require.NoError(t, sess.Exec(ctx, `
import "magus";

export fun put(id: str, checkpoint: str) > str !> any {
    final row = magus\job.put(id, opts: {"criteria": "hold a checkpoint", "checkpoint": checkpoint});
    return row.checkpoint;
}
`))
	put, ok := sess.Exports()["put"]
	require.True(t, ok, "exported Buzz entrypoint is missing")

	const madeUp = "34b41fd546ba8fa9c71a6a64e4ea0f3e93d5a7bc"
	_, err := sess.CallValue(ctx, put, []vm.Value{vm.StrValue("made-up"), vm.StrValue(madeUp)})
	require.ErrorContains(t, err, madeUp)
	require.ErrorContains(t, err, "`magus vcs checkpoint -o name`")

	got, err := sess.CallValue(ctx, put, []vm.Value{vm.StrValue("real"), vm.StrValue(head[:9])})
	require.NoError(t, err)
	assert.Equal(t, head, got.AsString())
}
