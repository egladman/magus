package sandbox

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/egladman/magus/internal/sandbox/filesystem"
)

// The test binary doubles as the launcher and as the confined child, so no test
// confines the process running the tests. helperEnv picks the child's job.
const (
	helperEnv     = "MAGUS_SANDBOX_TEST_HELPER"
	helperPathEnv = "MAGUS_SANDBOX_TEST_PATH"
)

// Helper exit codes. Anything else is a helper bug.
const (
	exitDenied     = 3
	exitReadFailed = 4
	exitFDLeaked   = 5
	exitFDMissing  = 6
)

func TestMain(m *testing.M) {
	MaybeLaunch()
	switch os.Getenv(helperEnv) {
	case "read":
		os.Exit(helperRead(os.Getenv(helperPathEnv)))
	case "fd":
		// Before anything else opens a descriptor that could land on 3.
		if _, err := unix.FcntlInt(3, unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
			os.Exit(exitFDLeaked)
		}
		if os.Getenv(helperPathEnv) == "appended" {
			if _, err := unix.FcntlInt(3+LauncherFiles, unix.F_GETFD, 0); err != nil {
				os.Exit(exitFDMissing)
			}
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func helperRead(path string) int {
	if _, err := os.ReadFile(path); err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
		if errors.Is(err, fs.ErrPermission) {
			return exitDenied
		}
		return exitReadFailed
	}
	return 0
}

// testRules grants the test binary exec and the library dirs a cgo-linked test
// binary's ELF interpreter lives under; landlock checks EXECUTE on the interpreter.
func testRules(t *testing.T) []filesystem.Rule {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	rules := []filesystem.Rule{{Path: filesystem.ResolveRulePath(exe), Read: true, Exec: true}}
	for _, dir := range []string{"/lib", "/lib64", "/usr/lib", "/usr/lib64"} {
		rules = append(rules, filesystem.Rule{Path: filesystem.ResolveRulePath(dir), Read: true, Exec: true})
	}
	return rules
}

// confinedHelper runs the test binary under Command as helper mode with path.
func confinedHelper(t *testing.T, p *Policy, mode, path string) (int, string) {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	cmd, err := Command(context.Background(), p, exe)
	require.NoError(t, err)
	return runHelper(t, cmd, mode, path)
}

func runHelper(t *testing.T, cmd *exec.Cmd, mode, path string) (int, string) {
	t.Helper()
	cmd.Env = append(os.Environ(), helperEnv+"="+mode, helperPathEnv+"="+path)
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), string(out)
	}
	require.NoError(t, err, string(out))
	return 0, string(out)
}

func TestCommandConfinesTheChild(t *testing.T) {
	requireLandlock(t)

	inside := t.TempDir()
	outside := t.TempDir()
	for _, dir := range []string{inside, outside} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o600))
	}
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(outside, link))

	rules := append(testRules(t),
		filesystem.Rule{Path: filesystem.ResolveRulePath(inside), Read: true},
		// A rule path that is a symlink grants nothing, not its target.
		filesystem.Rule{Path: link, Read: true},
	)
	p := &Policy{FS: filesystem.Ruleset{Rules: rules}}

	code, out := confinedHelper(t, p, "read", filepath.Join(inside, "f"))
	assert.Equal(t, 0, code, "a file inside the rules is readable: %s", out)

	code, out = confinedHelper(t, p, "read", filepath.Join(outside, "f"))
	assert.Equal(t, exitDenied, code, "a file outside the rules is denied: %s", out)

	// The test process itself stays unconfined.
	_, err := os.ReadFile(filepath.Join(outside, "f"))
	assert.NoError(t, err)
}

func TestCommandLeavesNoRulesetInTheChild(t *testing.T) {
	requireLandlock(t)

	p := &Policy{FS: filesystem.Ruleset{Rules: testRules(t)}}
	code, out := confinedHelper(t, p, "fd", "")
	assert.Equal(t, 0, code, "descriptor 3 is closed before the exec: %s", out)
}

// A /proc/self grant is the child's own entry, not the entry of the magus that built
// the policy.
func TestCommandGrantsTheChildItsOwnProcEntries(t *testing.T) {
	requireLandlock(t)

	own := filesystem.Rule{Path: filesystem.ResolveRulePath("/proc/self/status"), Read: true}
	p := &Policy{FS: filesystem.Ruleset{Rules: append(testRules(t), own)}}

	code, out := confinedHelper(t, p, "read", "/proc/self/status")
	assert.Equal(t, 0, code, "the child reads its own status: %s", out)
	code, out = confinedHelper(t, p, "read", own.Path)
	assert.Equal(t, exitDenied, code, "and not the status of the magus that built the policy: %s", out)
}

// A read grant on all of /proc leaves the environment of a process outside the child's
// landlock domain closed: the kernel checks ptrace access on it, and landlock refuses
// that across domains. magusfile.buzz grants its test target /proc on this.
func TestCommandProcGrantKeepsAnOutsideProcessesEnvironClosed(t *testing.T) {
	requireLandlock(t)

	p := &Policy{FS: filesystem.Ruleset{Rules: append(testRules(t), filesystem.Rule{Path: "/proc", Read: true})}}
	code, out := confinedHelper(t, p, "read", "/proc/self/environ")
	assert.Equal(t, 0, code, "the child reads its own environment: %s", out)
	code, out = confinedHelper(t, p, "read", "/proc/"+strconv.Itoa(os.Getpid())+"/environ")
	assert.Equal(t, exitDenied, code, "and not the unconfined test process's: %s", out)
}

// A caller's own ExtraFiles, a jobserver pipe say, reach the command after the
// ruleset's slot, where LauncherFiles says they will.
func TestCommandPassesAppendedFilesAfterTheRuleset(t *testing.T) {
	requireLandlock(t)

	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	exe, err := os.Executable()
	require.NoError(t, err)
	cmd, err := Command(context.Background(), &Policy{FS: filesystem.Ruleset{Rules: testRules(t)}}, exe)
	require.NoError(t, err)
	cmd.ExtraFiles = append(cmd.ExtraFiles, r)

	code, out := runHelper(t, cmd, "fd", "appended")
	assert.Equal(t, 0, code, "descriptor 3 closed, the appended file at 3+LauncherFiles: %s", out)
}

func TestCommandRefusesWithoutItsRuleset(t *testing.T) {
	requireLandlock(t)

	readable := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(readable, "f"), []byte("x"), 0o600))
	rules := append(testRules(t), filesystem.Rule{Path: filesystem.ResolveRulePath(readable), Read: true})
	p := &Policy{FS: filesystem.Ruleset{Rules: rules}}

	exe, err := os.Executable()
	require.NoError(t, err)
	cmd, err := Command(context.Background(), p, exe)
	require.NoError(t, err)
	cmd.ExtraFiles = nil

	code, out := runHelper(t, cmd, "read", filepath.Join(readable, "f"))
	assert.Equal(t, launchFailedExit, code, "the launcher must not run the command unconfined: %s", out)
	assert.Contains(t, out, "landlock_restrict_self")
}

func TestCommandNilPolicyIsPlain(t *testing.T) {
	cmd, err := Command(context.Background(), nil, "true")
	require.NoError(t, err)
	assert.Equal(t, []string{"true"}, cmd.Args)
	assert.Empty(t, cmd.ExtraFiles)
}
