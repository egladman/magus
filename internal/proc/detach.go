package proc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/proc/spawn"
)

// DetachedChildEnv returns this process's environment with the variables a detached broker
// or server must not inherit removed.
//
// MAGUS_PROC_SOCKET: a child inheriting it believes it is already adopted, binds no
// socket, and reports the parent's, leaving a server `server stop` cannot find.
// MAGUS_PROC_TOKEN goes with it: it is the secret for that socket and no other.
//
// The invocation ancestry and recursion depth, because A BACKGROUND PROCESS DESCENDS FROM
// NOBODY: the same rule submitJob already applies to a job's context. A run starts the
// broker, and a command may start the server. Without this the child's environment
// permanently records that one run's ancestry, and every workspace the server serves would
// read those refs as its own. Claims belonging to an invocation that ended hours ago
// would be excused from the budget, and a run with no ancestry of its own would be judged
// a nested magus that had lost it.
func DetachedChildEnv() []string {
	drop := []string{SocketEnv + "=", TokenEnv + "=", spawn.AncestorsEnvVar + "=", "MAGUS_LEVEL=", spawn.TreeMarkEnv + "="}
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if slices.ContainsFunc(drop, func(p string) bool { return strings.HasPrefix(kv, p) }) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// DetachedCommand builds the re-exec command with Path set to exe verbatim, skipping
// exec.Command's resolution. exe is the running binary's own path, so PATH and PATHEXT have
// no say in which binary it is. On Windows their say is fatal: given an absolute path with
// no extension, exec.Command demands a PATHEXT sibling on disk, and setup-magus installs an
// extensionless magus, so the binary could not re-exec itself ("executable file not found
// in %PATH%") and no broker ever started.
func DetachedCommand(exe string, args []string) *exec.Cmd {
	return &exec.Cmd{Path: exe, Args: append([]string{exe}, args...)}
}

// SpawnDetached re-execs this binary with args, detached, with its stdio appended to
// logPath, and returns its pid. The child is fully detached (its own session on unix) and
// Release()d so this process never waits on it. Its argv is its role: `broker`, or
// `server --foreground`.
func SpawnDetached(args []string, logPath string) (pid int, err error) {
	exe, err := os.Executable()
	if err != nil {
		// argv[0] may be a bare name, which DetachedCommand will not search for; resolve it the
		// way exec.Command used to.
		exe = os.Args[0]
		if lp, lerr := exec.LookPath(exe); lerr == nil {
			exe = lp
		}
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return 0, fmt.Errorf("create log directory for %s: %w", logPath, err)
	}
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, fmt.Errorf("open log %s: %w", logPath, err)
	}
	defer func() { _ = logf.Close() }()

	cmd := DetachedCommand(exe, args)
	cmd.Env = DetachedChildEnv()
	cmd.Stdin = nil
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = DetachedSysProcAttr()
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start %s: %w", strings.Join(args, " "), err)
	}
	pid = cmd.Process.Pid
	_ = cmd.Process.Release() // detach: the child outlives us, so never wait/reap it
	return pid, nil
}

// NewDetachedLog creates an empty log for one detached run under dir and returns its path.
// The name leads with the start time, so a directory listing sorts them.
func NewDetachedLog(dir string, now time.Time) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, now.UTC().Format("20060102T150405Z")+"-*.log")
	if err != nil {
		return "", fmt.Errorf("create a log in %s: %w", dir, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("create %s: %w", f.Name(), err)
	}
	return filepath.Clean(f.Name()), nil
}
