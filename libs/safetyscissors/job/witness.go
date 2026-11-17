package job

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// CheckCommand is the command goal, a check goal of row, is run by: the argv a caller's
// exec.Cmd must carry exactly, run in dir. A narrowed run (`-run TestOne`) is another
// command, so it never witnesses the goal. See [WithCheckCommand].
type CheckCommand func(row Job, goal Goal) (argv []string, dir string, err error)

// WitnessRefusal names why [Store.Witness] or [Witness.Close] refused a command.
type WitnessRefusal string

const (
	// RefusalNoCommand is a Witness call with no exec.Cmd.
	RefusalNoCommand WitnessRefusal = "no-command"
	// RefusalStarted is a command that started before the witness attached.
	RefusalStarted WitnessRefusal = "started"
	// RefusalOtherCheck is a command whose argv or directory is not the goal's check's.
	RefusalOtherCheck WitnessRefusal = "other-check"
	// RefusalNeverWaited is a Close before the caller waited on the command.
	RefusalNeverWaited WitnessRefusal = "never-waited"
	// RefusalOtherProcess is an exit status that is not the wait of the process the
	// command started: one built by hand, or taken from another command.
	RefusalOtherProcess WitnessRefusal = "other-process"
	// RefusalRedirected is output sent past the witness's digest after it attached.
	RefusalRedirected WitnessRefusal = "redirected"
	// RefusalClosed is a second Close.
	RefusalClosed WitnessRefusal = "closed"
)

// WitnessRefusedError is a command [Store.Witness] would not watch or [Witness.Close]
// would not record. Detail adds what the refusal compared, when it compared anything.
type WitnessRefusedError struct {
	Job, Goal string
	Refusal   WitnessRefusal
	Detail    string
}

func (e *WitnessRefusedError) Error() string {
	why := map[WitnessRefusal]string{
		RefusalNoCommand:    "Witness needs the caller's exec.Cmd",
		RefusalStarted:      "the command already started, so its start was not witnessed",
		RefusalOtherCheck:   "the command's run would grade another check",
		RefusalNeverWaited:  "the command was never waited on, so there is no exit status to witness",
		RefusalOtherProcess: "the exit status is not the wait of the process this command started",
		RefusalRedirected:   "the output was redirected after the witness attached, so its digest is not the run's output",
		RefusalClosed:       "the witness is already closed",
	}[e.Refusal]
	if why == "" {
		why = string(e.Refusal)
	}
	if e.Detail != "" {
		why += ": " + e.Detail
	}
	return fmt.Sprintf("job: %s goal %q: %s", e.Job, e.Goal, why)
}

// Witness watches one run of a check goal's check that the caller starts and waits on.
// Get one from [Store.Witness] before the command starts, and call Close after cmd.Wait
// (or cmd.Run) returns. A Witness is used by one goroutine; Close is safe to call again
// and refuses.
type Witness struct {
	s       *Store
	row     Job
	goal    Goal
	argv    []string
	dir     string
	cmd     *exec.Cmd
	out     *digestWriter
	stdout  io.Writer
	stderr  io.Writer
	tree    string
	started time.Time

	mu     sync.Mutex
	closed bool
}

// Witness attaches to cmd, which must not have started and must carry exactly the argv
// and directory [WithCheckCommand] names for goal goal of job id. It wraps cmd.Stdout and
// cmd.Stderr so every byte the process writes passes through the store's digest as well
// as wherever the caller sent it, and digests the tree before the run reads it.
//
// Witness never starts cmd: the caller runs it however it likes, and [Witness.Close]
// reads the outcome back from cmd. It needs [WithTree] and [WithCheckCommand]; each
// refusal is a [*GradingUnconfiguredError], a [*GoalError], a [*WitnessRefusedError] or
// a [*TreeError].
func (s *Store) Witness(ctx context.Context, id, goal string, cmd *exec.Cmd) (*Witness, error) {
	if err := s.needTree("Witness"); err != nil {
		return nil, err
	}
	if s.command == nil {
		return nil, s.wrap(&GradingUnconfiguredError{Op: "Witness", Option: "WithCheckCommand"})
	}
	refuse := func(r WitnessRefusal, detail string) error {
		return s.wrap(&WitnessRefusedError{Job: id, Goal: goal, Refusal: r, Detail: detail})
	}
	switch {
	case cmd == nil:
		return nil, refuse(RefusalNoCommand, "")
	case cmd.Process != nil || cmd.ProcessState != nil:
		return nil, refuse(RefusalStarted, "")
	}
	row, g, err := s.checkGoal(id, goal)
	if err != nil {
		return nil, err
	}
	argv, dir, err := s.command(row, g)
	if err != nil {
		return nil, fmt.Errorf("job: %s goal %q: the check's command: %w", id, goal, err)
	}
	if !slices.Equal(cmd.Args, argv) || filepath.Clean(cmd.Dir) != filepath.Clean(dir) {
		return nil, refuse(RefusalOtherCheck, fmt.Sprintf("the command runs %q in %s and the check is %q in %s", cmd.Args, cmd.Dir, argv, dir))
	}
	tree, err := s.digestTree(ctx, "before the run")
	if err != nil {
		return nil, err
	}
	out := &digestWriter{h: sha256.New()}
	w := &Witness{s: s, row: row, goal: g, argv: slices.Clone(argv), dir: dir, cmd: cmd, out: out, tree: tree, started: s.Now()}
	w.stdout, w.stderr = tee(cmd.Stdout, out), tee(cmd.Stderr, out)
	cmd.Stdout, cmd.Stderr = w.stdout, w.stderr
	return w, nil
}

// Close records the run the command finished and returns its ref. It refuses a command
// never waited on, an exit status that is not the wait of the process the command
// started, output redirected away from the witness after it attached, and a second Close,
// each a [*WitnessRefusedError]; a refused Witness is spent. A run the tree moved under is
// recorded, and grades as not verified, since no one tree is what it read.
func (w *Witness) Close(ctx context.Context) (RunRef, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	refuse := func(r WitnessRefusal) error {
		return w.s.wrap(&WitnessRefusedError{Job: w.row.ID, Goal: w.goal.ID, Refusal: r})
	}
	if w.closed {
		return "", refuse(RefusalClosed)
	}
	w.closed = true
	cmd, ps := w.cmd, w.cmd.ProcessState
	switch {
	case cmd.Process == nil || ps == nil:
		return "", refuse(RefusalNeverWaited)
	// Only a real wait sets a ProcessState's pid: one built by hand reads 0, and one taken
	// from another Cmd names another process.
	case ps.Pid() == 0 || ps.Pid() != cmd.Process.Pid:
		return "", refuse(RefusalOtherProcess)
	case cmd.Stdout != w.stdout || cmd.Stderr != w.stderr:
		return "", refuse(RefusalRedirected)
	}
	after, err := w.s.digestTree(ctx, "after the run")
	if err != nil {
		return "", err
	}
	return w.s.record(ctx, run{
		job: w.row.ID, goal: w.goal.ID, basis: BasisWitnessed, check: w.goal.Check, argv: w.argv, dir: w.dir,
		tree: w.tree, exit: ps.ExitCode(), output: w.out.sum(), started: w.started, ended: w.s.Now(), moved: after != w.tree,
	})
}

func tee(caller, out io.Writer) io.Writer {
	if caller == nil {
		return out
	}
	return io.MultiWriter(caller, out)
}

// digestWriter is shared by stdout and stderr, which exec copies on two goroutines.
type digestWriter struct {
	mu sync.Mutex
	h  hash.Hash
}

func (d *digestWriter) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.h.Write(p)
}

func (d *digestWriter) sum() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return "sha256:" + hex.EncodeToString(d.h.Sum(nil))
}
