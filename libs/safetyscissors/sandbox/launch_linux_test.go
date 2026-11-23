package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"golang.org/x/sys/unix"

	"github.com/egladman/magus/libs/safetyscissors/sandbox/filesystem"
)

// The test binary doubles as the launcher and as the confined child, so no test
// confines the process running the tests. helperEnv picks the child's job.
const (
	helperEnv     = "SAFETYSCISSORS_SANDBOX_TEST_HELPER"
	helperPathEnv = "SAFETYSCISSORS_SANDBOX_TEST_PATH"
)

// launchFailedExit is the launcher's exit status when Relaunch could not confine
// itself or exec the command, as a caller's main exits.
const launchFailedExit = 126

// Helper exit codes. Anything else is a helper bug.
const (
	exitDenied     = 3
	exitReadFailed = 4
	exitFDLeaked   = 5
	exitFDMissing  = 6
)

func TestMain(m *testing.M) {
	if handled, err := Relaunch(os.Args); handled {
		os.Stderr.WriteString("confined launch failed: " + err.Error() + "\n")
		os.Exit(launchFailedExit)
	}
	switch os.Getenv(helperEnv) {
	case "read":
		os.Exit(helperRead(os.Getenv(helperPathEnv)))
	case "bind":
		os.Exit(helperBind(os.Getenv(helperPathEnv)))
	case "fd":
		// The launcher closed its ruleset before exec. A Go runtime may reuse fd 3
		// before TestMain runs, but it must not inherit the Landlock ruleset itself.
		if target, err := os.Readlink("/proc/self/fd/3"); err == nil && strings.Contains(target, "landlock") {
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

// helperBind binds a unix socket at path, reaching one longer than sun_path through
// its directory's /proc/self/fd entry.
func helperBind(path string) int {
	dirFD, err := unix.Open(filepath.Dir(path), unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
		return exitReadFailed
	}
	defer unix.Close(dirFD)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: fmt.Sprintf("/proc/self/fd/%d/%s", dirFD, filepath.Base(path)), Net: "unix"})
	if err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
		if errors.Is(err, fs.ErrPermission) {
			return exitDenied
		}
		return exitReadFailed
	}
	_ = ln.Close()
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

// confining is a policy over rules that confines wherever the kernel has landlock,
// whatever its ABI, so the tests run on kernels below RequiredABI too.
func confining(rules ...filesystem.Rule) *Policy {
	return &Policy{rules: filesystem.Ruleset{Rules: rules}, mode: BestEffort, launcher: defaultLauncherName}
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
	cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%s", helperEnv, mode), fmt.Sprintf("%s=%s", helperPathEnv, path))
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), string(out)
	}
	require.NoError(t, err, string(out))
	return 0, string(out)
}

func (s *LaunchSuite) TestCommandConfinesTheChild() {
	t := s.T()
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
	p := confining(rules...)

	code, out := confinedHelper(t, p, "read", filepath.Join(inside, "f"))
	assert.Equal(t, 0, code, "a file inside the rules is readable: %s", out)

	code, out = confinedHelper(t, p, "read", filepath.Join(outside, "f"))
	assert.Equal(t, exitDenied, code, "a file outside the rules is denied: %s", out)

	// The test process itself stays unconfined.
	_, err := os.ReadFile(filepath.Join(outside, "f"))
	assert.NoError(t, err)
}

func (s *LaunchSuite) TestCommandDoesNotLeakRulesetToTheChild() {
	t := s.T()
	requireLandlock(t)

	p := confining(testRules(t)...)
	code, out := confinedHelper(t, p, "fd", "")
	assert.Equal(t, 0, code, "the ruleset descriptor is not inherited by the exec: %s", out)
}

// A /proc/self grant is the child's own entry, not the entry of the process that built
// the policy.
func (s *LaunchSuite) TestCommandGrantsTheChildItsOwnProcEntries() {
	t := s.T()
	requireLandlock(t)

	own := filesystem.Rule{Path: filesystem.ResolveRulePath("/proc/self/status"), Read: true}
	p := confining(append(testRules(t), own)...)

	code, out := confinedHelper(t, p, "read", "/proc/self/status")
	assert.Equal(t, 0, code, "the child reads its own status: %s", out)
	code, out = confinedHelper(t, p, "read", own.Path)
	assert.Equal(t, exitDenied, code, "and not the status of the process that built the policy: %s", out)
}

// A read grant on all of /proc leaves the environment of a process outside the child's
// landlock domain closed: the kernel checks ptrace access on it, and landlock refuses
// that across domains. magusfile.buzz grants its test target /proc on this.
func (s *LaunchSuite) TestCommandProcGrantKeepsAnOutsideProcessesEnvironClosed() {
	t := s.T()
	requireLandlock(t)

	p := confining(append(testRules(t), filesystem.Rule{Path: "/proc", Read: true})...)
	code, out := confinedHelper(t, p, "read", "/proc/self/environ")
	assert.Equal(t, 0, code, "the child reads its own environment: %s", out)
	code, out = confinedHelper(t, p, "read", fmt.Sprintf("/proc/%d/environ", os.Getpid()))
	assert.Equal(t, exitDenied, code, "and not the unconfined test process's: %s", out)
}

// A socket path past sun_path is bound through /proc/self/fd/<fd>, and landlock judges
// that bind on the directory the link resolves to: a granted one allows it and an
// ungranted one refuses it, with no /proc grant either way.
func (s *LaunchSuite) TestCommandJudgesALongSocketBindOnItsRealDirectory() {
	t := s.T()
	requireLandlock(t)

	long := func() string {
		dir := filesystem.ResolveRulePath(t.TempDir())
		for len(dir) < 200 {
			dir = filepath.Join(dir, strings.Repeat("d", 50))
		}
		require.NoError(t, os.MkdirAll(dir, 0o700))
		return filepath.Join(dir, "magus-99999-0123abcd.sock")
	}
	granted, ungranted := long(), long()
	grant := filesystem.Rule{Path: filepath.Dir(granted), Read: true, Write: true}
	p := confining(append(testRules(t), grant)...)

	code, out := confinedHelper(t, p, "bind", granted)
	assert.Equal(t, 0, code, "a bind in a granted directory: %s", out)
	code, out = confinedHelper(t, p, "bind", ungranted)
	assert.Equal(t, exitDenied, code, "a bind in an ungranted one: %s", out)
}

// A caller's own ExtraFiles, a jobserver pipe say, reach the command after the
// ruleset's slot, where LauncherFiles says they will.
func (s *LaunchSuite) TestCommandPassesAppendedFilesAfterTheRuleset() {
	t := s.T()
	requireLandlock(t)

	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	exe, err := os.Executable()
	require.NoError(t, err)
	cmd, err := Command(context.Background(), confining(testRules(t)...), exe)
	require.NoError(t, err)
	cmd.ExtraFiles = append(cmd.ExtraFiles, r)

	code, out := runHelper(t, cmd, "fd", "appended")
	assert.Equal(t, 0, code, "the ruleset is absent and the appended file is at 3+LauncherFiles: %s", out)
}

func (s *LaunchSuite) TestCommandRefusesWithoutItsRuleset() {
	t := s.T()
	requireLandlock(t)

	readable := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(readable, "f"), []byte("x"), 0o600))
	rules := append(testRules(t), filesystem.Rule{Path: filesystem.ResolveRulePath(readable), Read: true})
	p := confining(rules...)

	exe, err := os.Executable()
	require.NoError(t, err)
	cmd, err := Command(context.Background(), p, exe)
	require.NoError(t, err)
	cmd.ExtraFiles = nil

	code, out := runHelper(t, cmd, "read", filepath.Join(readable, "f"))
	assert.Equal(t, launchFailedExit, code, "the launcher must not run the command unconfined: %s", out)
	assert.Contains(t, out, "landlock_restrict_self")
}

// A rule the building process resolved onto its own /proc entries is the child's to
// grant itself; left in the ruleset it would hand the child the builder's descriptors.
func (s *LaunchSuite) TestProcSelfRulesMoveToTheLauncher() {
	t := s.T()
	rules := []filesystem.Rule{
		{Path: "/proc/42/fd", Read: true, Write: true},
		{Path: "/proc/42", Read: true},
		{Path: "/proc/420/status", Read: true},
		{Path: "/usr", Read: true, Exec: true},
	}
	self, rest := procSelfRules(rules, 42)
	assert.Equal(t, []filesystem.Rule{
		{Path: "/proc/self/fd", Read: true, Write: true},
		{Path: "/proc/self", Read: true},
	}, self)
	assert.Equal(t, rules[2:], rest, "another pid's entries are not this process's")

	got, err := decodeRules(encodeRules(self))
	require.NoError(t, err)
	assert.Equal(t, self, got)
	empty, err := decodeRules(encodeRules(nil))
	require.NoError(t, err)
	assert.Empty(t, empty)
	_, err = decodeRules("rw:relative")
	assert.Error(t, err)
}

// A child Command starts on Linux runs through the launcher under its ruleset.
type LaunchSuite struct{ suite.Suite }

func TestLaunchSuite(t *testing.T) { suite.Run(t, new(LaunchSuite)) }

// The launcher's rule encoding carries any path back unchanged.
type RuleEncodingSuite struct{ suite.Suite }

func TestRuleEncodingSuite(t *testing.T) { suite.Run(t, new(RuleEncodingSuite)) }

func (s *RuleEncodingSuite) roundTrip(rules []filesystem.Rule) {
	got, err := decodeRules(encodeRules(rules))
	s.Require().NoError(err)
	s.Equal(rules, got)
}

func (s *RuleEncodingSuite) TestAPathHoldingAnInjectedRuleStaysOneRule() {
	s.roundTrip([]filesystem.Rule{{Path: "/ws/a,rwx:/", Read: true}})
}

func (s *RuleEncodingSuite) TestAPathHoldingABackslashRoundTrips() {
	s.roundTrip([]filesystem.Rule{{Path: `/ws/a\,b\`, Read: true, Write: true}})
}

func (s *RuleEncodingSuite) TestAPathHoldingAColonRoundTrips() {
	s.roundTrip([]filesystem.Rule{{Path: "/ws/a:b", Exec: true}, {Path: "/usr", Read: true}})
}

func (s *RuleEncodingSuite) TestADanglingEscapeIsMalformed() {
	_, err := decodeRules(`r--:/ws/a\`)
	s.Error(err)
}

func (s *RuleEncodingSuite) TestAnUnknownEscapeIsMalformed() {
	_, err := decodeRules(`r--:/ws/\a`)
	s.Error(err)
}
