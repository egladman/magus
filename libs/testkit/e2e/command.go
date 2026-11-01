package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/proc/spawn"
)

// A killed group's stragglers get this long to let go of the output pipes.
const pipeGrace = 10 * time.Second

var errNoTimeout = errors.New("e2e: Command.Timeout must be positive")

// Command is one run of an external program in its own process group, so
// a timeout takes down what it started too. Env is the whole environment;
// a nil Env inherits this process's. A Timeout not above zero is an error.
type Command struct {
	Path    string
	Args    []string
	Dir     string
	Env     []string
	Timeout time.Duration
}

// Result is what a Command left behind. Err wraps the *exec.ExitError of
// a program that exited nonzero.
type Result struct {
	Stdout   string
	Stderr   string
	Err      error
	Elapsed  time.Duration
	TimedOut bool
}

// Run runs the command to its end with no stdin.
func (c Command) Run() Result {
	if c.Timeout <= 0 {
		return Result{Err: errNoTimeout}
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.Timeout)
	defer cancel()
	cmd := c.prepare(ctx)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	return c.result(ctx, start, stdout.String(), stderr.String(), err)
}

// Converse runs the command with stdin open, for a program that reads
// answers while it prints. Stdin closes when talk returns at the latest,
// and whatever talk left unread still lands in Result.Stdout.
func (c Command) Converse(talk func(stdin io.WriteCloser, stdout io.Reader)) Result {
	if c.Timeout <= 0 {
		return Result{Err: errNoTimeout}
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.Timeout)
	defer cancel()
	cmd := c.prepare(ctx)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return Result{Err: err}
	}
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return Result{Err: err}
	}
	var stdout, stderr bytes.Buffer
	cmd.Stderr = &stderr
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return Result{Err: err}
	}
	// WaitDelay acts only in Wait, which a straggler on stdout would block.
	stop := context.AfterFunc(ctx, func() {
		_ = pipe.Close()
		_ = stdin.Close()
	})
	defer stop()
	seen := io.TeeReader(pipe, &stdout)
	talk(stdin, seen)
	_ = stdin.Close()
	_, _ = io.Copy(io.Discard, seen)
	err = cmd.Wait()
	return c.result(ctx, start, stdout.String(), stderr.String(), err)
}

func (c Command) prepare(ctx context.Context) *exec.Cmd {
	cmd := exec.CommandContext(ctx, c.Path, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = c.Env
	spawn.SetupProcessGroup(cmd)
	cmd.Cancel = func() error {
		spawn.KillGroup(cmd)
		return nil
	}
	cmd.WaitDelay = pipeGrace
	return cmd
}

func (c Command) result(ctx context.Context, start time.Time, stdout, stderr string, err error) Result {
	r := Result{Stdout: stdout, Stderr: stderr, Err: err, Elapsed: time.Since(start)}
	if ctx.Err() != nil {
		r.TimedOut = true
		r.Err = fmt.Errorf("timed out after %s: %w", c.Timeout, err)
	}
	return r
}

// Scrub returns environ without each entry starting with a drop prefix,
// except an entry starting with a keep prefix. A prefix ending in = names
// one variable exactly.
func Scrub(environ, drop, keep []string) []string {
	var out []string
	for _, kv := range environ {
		starts := func(prefix string) bool { return strings.HasPrefix(kv, prefix) }
		if slices.ContainsFunc(keep, starts) || !slices.ContainsFunc(drop, starts) {
			out = append(out, kv)
		}
	}
	return out
}
