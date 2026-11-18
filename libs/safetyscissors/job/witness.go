package job

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
)

// CheckCommand is the command postcondition, a check postcondition of row, is run by: the argv a caller's
// exec.Cmd must carry exactly, run in dir. A narrowed run (`-run TestOne`) is another
// command, so it never witnesses the postcondition. See [WithCheckCommand].
type CheckCommand func(row Job, postcondition Postcondition) (argv []string, dir string, err error)

// WitnessRefusal names why [Store.Witness] refused a command.
type WitnessRefusal string

const (
	// RefusalNoCommand is a Witness call with no exec.Cmd.
	RefusalNoCommand WitnessRefusal = "no-command"
	// RefusalStarted is a command that started before Witness ran it, or one carrying
	// the Process or ProcessState of another command.
	RefusalStarted WitnessRefusal = "started"
	// RefusalOtherCheck is a command whose argv or directory is not the postcondition's check's.
	RefusalOtherCheck WitnessRefusal = "other-check"
	// RefusalOtherProgram is a command whose Path is not the program its argv[0]
	// resolves to, such as /bin/true carrying the check's argv.
	RefusalOtherProgram WitnessRefusal = "other-program"
	// RefusalEnv is a command carrying an Env the check's command does not name.
	RefusalEnv WitnessRefusal = "env"
	// RefusalSysProcAttr is a command carrying a SysProcAttr the check's command does not
	// name.
	RefusalSysProcAttr WitnessRefusal = "sys-proc-attr"
	// RefusalExtraFiles is a command carrying ExtraFiles the check's command does not name.
	RefusalExtraFiles WitnessRefusal = "extra-files"
	// RefusalNotStarted is a command that would not start; Err is why.
	RefusalNotStarted WitnessRefusal = "not-started"
	// RefusalUnfinished is a run whose wait or output copy failed, so the digest is not
	// the whole run's output; Err is why.
	RefusalUnfinished WitnessRefusal = "unfinished"
)

// WitnessRefusedError is a command [Store.Witness] would not run or record. Detail adds
// what the refusal compared, when it compared anything, and Err the failure behind a
// [RefusalNotStarted] or [RefusalUnfinished].
type WitnessRefusedError struct {
	Job, Postcondition string
	Refusal            WitnessRefusal
	Detail             string
	Err                error
}

func (e *WitnessRefusedError) Error() string {
	why := witnessRefusalText(e.Refusal)
	if e.Detail != "" {
		why += ": " + e.Detail
	}
	if e.Err != nil {
		why += ": " + e.Err.Error()
	}
	return fmt.Sprintf("job: %s postcondition %q: %s", e.Job, e.Postcondition, why)
}

func (e *WitnessRefusedError) Unwrap() error { return e.Err }

func witnessRefusalText(r WitnessRefusal) string {
	switch r {
	case RefusalNoCommand:
		return "Witness needs the caller's exec.Cmd"
	case RefusalStarted:
		return "the command already started or carries another command's process, so its start would not be witnessed"
	case RefusalOtherCheck:
		return "the command's run would grade another check"
	case RefusalOtherProgram:
		return "the command runs a program its argv[0] does not name"
	case RefusalEnv:
		return "the command sets an environment the check's command does not name"
	case RefusalSysProcAttr:
		return "the command sets process attributes the check's command does not name"
	case RefusalExtraFiles:
		return "the command passes extra files the check's command does not name"
	case RefusalNotStarted:
		return "the command did not start"
	case RefusalUnfinished:
		return "the run did not finish cleanly, so its digest is not the whole output"
	}
	return string(r)
}

// Witness runs one check of postcondition postcondition of job id and records it. The caller builds cmd and
// may set its Stdout, Stderr, Stdin, Cancel and WaitDelay; Witness starts it and waits on
// it, so the exit status it records is the wait of the process it started and every byte
// the process writes passes through the store's digest as well as wherever the caller
// sent it. Bound the run with exec.CommandContext; ctx bounds the tree digests and the
// record.
//
// cmd must not have started, must carry exactly the argv and directory
// [WithCheckCommand] names, its Path must be the program argv[0] resolves to (through
// exec.LookPath when argv[0] is a bare name), and it must leave Env, SysProcAttr and
// ExtraFiles unset: the check's command names none, so a run under any of them is
// another check. A run the tree moved under is recorded, and grades as not verified,
// since no one tree is what it read.
//
// It needs [WithTree], [WithCheckSpec] and [WithCheckCommand]; each refusal is a
// [*GradingUnconfiguredError], a [*PostconditionError], a [*CheckRefusedError], a
// [*WitnessRefusedError] or a [*TreeError].
func (s *Store) Witness(ctx context.Context, id, postcondition string, cmd *exec.Cmd) (RunRef, error) {
	if err := s.needTree("Witness"); err != nil {
		return "", err
	}
	if err := s.needChecks("Witness"); err != nil {
		return "", err
	}
	if s.command == nil {
		return "", s.wrap(&GradingUnconfiguredError{Op: "Witness", Option: "WithCheckCommand"})
	}
	refuse := func(r WitnessRefusal, detail string, err error) error {
		return s.wrap(&WitnessRefusedError{Job: id, Postcondition: postcondition, Refusal: r, Detail: detail, Err: err})
	}
	if cmd == nil {
		return "", refuse(RefusalNoCommand, "", nil)
	}
	row, g, err := s.checkPostcondition(ctx, id, postcondition)
	if err != nil {
		return "", err
	}
	check, err := s.checks.runCheck(row.ID, postconditionCheckField(g.ID), g.Check)
	if err != nil {
		return "", s.wrap(err)
	}
	argv, dir, err := s.command(row, g)
	if err != nil {
		return "", fmt.Errorf("job: %s postcondition %q: the check's command: %w", id, postcondition, err)
	}
	if r, detail, err := foreignCommand(cmd, argv, dir); r != "" {
		return "", refuse(r, detail, err)
	}
	tree, err := s.digestTree(ctx, "before the run")
	if err != nil {
		return "", err
	}
	out := &digestWriter{h: sha256.New()}
	cmd.Stdout, cmd.Stderr = tee(cmd.Stdout, out), tee(cmd.Stderr, out)
	started := s.Now()
	if err := cmd.Start(); err != nil {
		return "", refuse(RefusalNotStarted, "", err)
	}
	exit, err := waitExitCode(cmd.Wait())
	if err != nil {
		return "", refuse(RefusalUnfinished, "", err)
	}
	ended := s.Now()
	after, err := s.digestTree(ctx, "after the run")
	if err != nil {
		return "", err
	}
	return s.record(ctx, run{
		job: row.ID, postcondition: g.ID, basis: BasisWitnessed, check: check, argv: slices.Clone(argv), dir: dir,
		tree: tree, exit: exit, output: out.sum(), started: started, ended: ended, moved: after != tree,
	})
}

// foreignCommand is the refusal cmd earns against the check's argv and dir, "" for none.
func foreignCommand(cmd *exec.Cmd, argv []string, dir string) (WitnessRefusal, string, error) {
	switch {
	case cmd.Process != nil || cmd.ProcessState != nil:
		return RefusalStarted, "", nil
	case len(argv) == 0:
		return RefusalOtherCheck, "the check's command names no program", nil
	case !slices.Equal(cmd.Args, argv) || filepath.Clean(cmd.Dir) != filepath.Clean(dir):
		return RefusalOtherCheck, fmt.Sprintf("the command runs %q in %s and the check is %q in %s", cmd.Args, cmd.Dir, argv, dir), nil
	case cmd.Env != nil:
		return RefusalEnv, "", nil
	case cmd.SysProcAttr != nil:
		return RefusalSysProcAttr, "", nil
	case len(cmd.ExtraFiles) > 0:
		return RefusalExtraFiles, "", nil
	}
	program, err := resolveProgram(argv[0])
	if err != nil {
		return RefusalNotStarted, "", err
	}
	if cmd.Path != program {
		return RefusalOtherProgram, fmt.Sprintf("Path is %s and argv[0] %q is %s", cmd.Path, argv[0], program), nil
	}
	return "", "", nil
}

// resolveProgram is the Path exec.Command gives a command named argv0.
func resolveProgram(argv0 string) (string, error) {
	if filepath.Base(argv0) != argv0 {
		return argv0, nil
	}
	return exec.LookPath(argv0)
}

// waitExitCode is the exit code of the wait whose error is err. An *exec.ExitError carries the
// state its own wait returned, which no write to cmd.ProcessState reaches; any other error
// is a failed wait or output copy.
func waitExitCode(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		return exit.ExitCode(), nil
	}
	return 0, err
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
