package run

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/journal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// TestExecEmitsExecEventWithinStep confirms Exec emits a KindExec event tagged with the
// step's project/target and the command line when run inside a captured step, and emits
// nothing when there is no step on ctx (an internal probe).
func TestExecEmitsExecEventWithinStep(t *testing.T) {
	if _, err := exec.LookPath("echo"); err != nil {
		t.Skip("'echo' not available")
	}

	// A Broadcaster is the public way to observe the captured stream; its retained
	// backlog holds every event emitted before we subscribe.
	rec := journal.NewBroadcaster()
	ctx := journal.WithStep(journal.WithLogger(context.Background(), journal.NewLogger(rec)), "web", "build")
	_, err := Exec(ctx, "echo", []string{"hello world"}, ExecOptions{Quiet: true})
	require.NoError(t, err)

	got, _, cancel := rec.Subscribe()
	defer cancel()
	require.Len(t, got, 1)
	assert.Equal(t, journal.KindExec, got[0].Kind)
	assert.Equal(t, "web", got[0].Project)
	assert.Equal(t, "build", got[0].Target)
	assert.Equal(t, `echo "hello world"`, got[0].Text, "args with spaces are quoted")

	// No step on ctx: no exec event (internal probes stay silent).
	noStep := journal.NewBroadcaster()
	noCtx := journal.WithLogger(context.Background(), journal.NewLogger(noStep))
	_, err = Exec(noCtx, "echo", []string{"hi"}, ExecOptions{Quiet: true})
	require.NoError(t, err)
	got2, _, cancel2 := noStep.Subscribe()
	defer cancel2()
	assert.Empty(t, got2)
}

// TestExecInjectsMAGUS pins that Exec exports MAGUS (the running binary's resolved
// path) into the subprocess environment, so a spell or recipe can re-invoke magus
// via "${MAGUS:-magus}" without relying on PATH (the GNU Make $(MAKE) convention).
// Here the "running binary" is the test executable.
func TestExecInjectsMAGUS(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("'sh' not available")
	}
	res, err := Exec(context.Background(), "sh", []string{"-c", `printf %s "$MAGUS"`}, ExecOptions{Capture: true})
	require.NoError(t, err)
	want, err := os.Executable()
	require.NoError(t, err)
	if resolved, err := filepath.EvalSymlinks(want); err == nil {
		want = resolved
	}
	assert.Equal(t, want, res.Stdout, "$MAGUS in subprocess")
}

// TestExecInjectsMagusLevel pins the GNU Make MAKELEVEL semantics: a subprocess
// sees MAGUS_LEVEL = this process's depth + 1, so the counter climbs by one per
// magus process (top-level, with MAGUS_LEVEL unset, is depth 0). Not parallel: it
// mutates the process env via t.Setenv.
func TestExecInjectsMagusLevel(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("'sh' not available")
	}
	level := func(t *testing.T) string {
		t.Helper()
		res, err := Exec(context.Background(), "sh", []string{"-c", `printf %s "$MAGUS_LEVEL"`}, ExecOptions{Capture: true})
		require.NoError(t, err)
		return res.Stdout
	}

	// Top level: MAGUS_LEVEL absent (empty means 0), so child runs at depth 1.
	t.Setenv("MAGUS_LEVEL", "")
	assert.Equal(t, "1", level(t), "MAGUS_LEVEL at top")
	// Nested: depth 2, so child runs at depth 3.
	t.Setenv("MAGUS_LEVEL", "2")
	assert.Equal(t, "3", level(t), "MAGUS_LEVEL when nested")
}

// TestCurrentLevel pins the contract startup relies on to decide whether to stand
// up its own daemon: absent/invalid means 0 (top-level, starts a server), > 0 means
// nested (must not, to keep one socket / one pool). Mutates env; not parallel.
func TestCurrentLevel(t *testing.T) {
	t.Setenv("MAGUS_LEVEL", "")
	assert.Equal(t, 0, CurrentLevel(), "top-level CurrentLevel")
	t.Setenv("MAGUS_LEVEL", "2")
	assert.Equal(t, 2, CurrentLevel(), "nested CurrentLevel")
	t.Setenv("MAGUS_LEVEL", "not-a-number")
	assert.Equal(t, 0, CurrentLevel(), "invalid CurrentLevel")
}

// TestExecWithholdsDaemonSocket pins the contract runMagus (std/magus.go) relies on: the
// daemon/pool pointer MAGUS_DAEMON_SOCKET is magus-internal and must NOT reach an op
// subprocess - even with the sandbox off (the default), where childEnv takes the raw process
// env. A leaked socket makes any program that links proc (magus's own test binaries) mistake
// itself for "already adopted under a parent magus". An explicit Env override still wins, which
// is how a legitimate nested magus re-injects it for forwarding. Mutates env; not parallel.
func TestExecWithholdsDaemonSocket(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("'sh' not available")
	}
	read := func(t *testing.T, opts ExecOptions) string {
		t.Helper()
		opts.Capture = true
		res, err := Exec(context.Background(), "sh", []string{"-c", `printf %s "$MAGUS_DAEMON_SOCKET"`}, opts)
		require.NoError(t, err)
		return res.Stdout
	}
	// Set in the parent, as startup does when it hosts its own pool: the child must not see it.
	t.Setenv("MAGUS_DAEMON_SOCKET", "unix:///tmp/magus-parent.sock")
	assert.Empty(t, read(t, ExecOptions{}), "daemon socket must be withheld from an op child")
	// A nested magus re-injects it as an Env override, which layers last and wins.
	assert.Equal(t, "unix:///tmp/child.sock",
		read(t, ExecOptions{Env: []string{"MAGUS_DAEMON_SOCKET=unix:///tmp/child.sock"}}),
		"an explicit override re-injects the socket for legitimate recursion")
}

// TestChildEnvReportsWithheldDaemonVars pins the breadcrumb contract: childEnv reports which
// daemon pointers it actually withheld from the child (present in the base, not re-added by an
// override), so Exec can log the answer to "the sandbox is off, why is my var missing?". Mutates
// env; not parallel.
func TestChildEnvReportsWithheldDaemonVars(t *testing.T) {
	t.Setenv("MAGUS_DAEMON_SOCKET", "unix:///tmp/p.sock")
	t.Setenv("MAGUS_DAEMON_ADDRESS", "unix:///tmp/p.sock")
	// Both present in the process env, no overrides: both are withheld from the child.
	_, withheld := childEnv(context.Background(), nil, nil)
	assert.ElementsMatch(t, DaemonForwardVars, withheld, "both daemon pointers withheld")
	// An override that re-adds one (a nested magus forwarding) means it is NOT withheld.
	_, withheld = childEnv(context.Background(), nil, []string{"MAGUS_DAEMON_SOCKET=unix:///tmp/child.sock"})
	assert.Equal(t, []string{"MAGUS_DAEMON_ADDRESS"}, withheld, "re-injected var is not reported withheld")
}

// TestChildEnvCarriesInvocationAncestry pins the ONLY carrier the ordinary nested case
// has. A nested magus normally runs as its own process (childEnv withholds the daemon
// socket), so if this variable does not reach it, the child cannot recognize a lock its own
// ancestor holds and the deadlock this machinery exists to refuse comes straight back.
//
// It also pins the drop: the value must come from ctx, never from this process's
// environment, because under the daemon the process env belongs to no invocation at all.
// Mutates env; not parallel.
func TestChildEnvCarriesInvocationAncestry(t *testing.T) {
	t.Setenv(AncestorsEnvVar, "9:inv-stale")

	ctx := types.AppendInvocationAncestor(context.Background(), 100, "inv-outer")
	ctx = types.AppendInvocationAncestor(ctx, 101, "inv-nested")
	env, _ := childEnv(ctx, nil, nil)

	var got []string
	for _, kv := range env {
		if name, value, _ := strings.Cut(kv, "="); name == AncestorsEnvVar {
			got = append(got, value)
		}
	}
	assert.Equal(t, []string{"100:inv-outer,101:inv-nested"}, got,
		"exactly one ancestry entry, oldest first, from ctx and not from the process env")

	// No ancestry on ctx: the inherited value must not be passed through either, or a child
	// inherits an ancestry that belongs to whoever started this process.
	env, _ = childEnv(context.Background(), nil, nil)
	for _, kv := range env {
		if name, _, _ := strings.Cut(kv, "="); name == AncestorsEnvVar {
			t.Errorf("child got %q with no ancestry on ctx; a stale inherited value leaked", kv)
		}
	}
}

func TestExecSuccess(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("'true' not available")
	}
	_, err := Exec(context.Background(), "true", nil, ExecOptions{Dir: t.TempDir(), Quiet: true})
	assert.NoError(t, err)
}

func TestExecFailure(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("false"); err != nil {
		t.Skip("'false' not available")
	}
	_, err := Exec(context.Background(), "false", nil, ExecOptions{Dir: t.TempDir(), Quiet: true})
	assert.Error(t, err, "want non-nil exit error")
}

func TestExecContextCancel(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("'sleep' not available")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Exec(ctx, "sleep", []string{"60"}, ExecOptions{Dir: t.TempDir(), Quiet: true})
	assert.Error(t, err, "a cancelled context should surface an error")
}
