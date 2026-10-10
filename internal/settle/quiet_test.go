//go:build unix

package settle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"testing"

	"github.com/egladman/magus/internal/journal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// terminal points fds 1 and 2 at a file for the duration of fn and returns what reached
// them: what a person running the hook would have seen.
func terminal(t *testing.T, fn func()) string {
	t.Helper()
	path := t.TempDir() + "/terminal"
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()
	saved := [3]int{-1, -1, -1}
	for _, std := range []int{1, 2} {
		saved[std], err = unix.Dup(std)
		require.NoError(t, err)
		require.NoError(t, unix.Dup2(int(f.Fd()), std))
	}
	fn()
	require.NoError(t, restore(saved))
	seen, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(seen)
}

// emitResult feeds one step result into the handler observe threaded onto ctx, the way the
// cache reports a step to a run's invocation log.
func emitResult(ctx context.Context, e journal.Event) {
	e.Kind = journal.KindResult
	journal.Emit(journal.WithLogger(ctx, journal.NewLogger(ctx.Value(observedKey{}).(slog.Handler))), e)
}

type observedKey struct{}

func observe(ctx context.Context, h slog.Handler) context.Context {
	return context.WithValue(ctx, observedKey{}, h)
}

// TestSettleQuietlyPrintsNothingWhenTheRunPasses: a regeneration's own log, its slog
// lines and a generator child's output all stay off the terminal, so the hook's one line
// is all a person sees.
func TestSettleQuietlyPrintsNothingWhenTheRunPasses(t *testing.T) {
	run := Quietly(func(ctx context.Context, inv []string) error {
		fmt.Println("[pass] magus generate (ran, 2.1s)")
		slog.Info("wrote internal/gen/mocks/store.go")
		emitResult(ctx, journal.Event{Project: ".", Target: "generate:rw", Status: journal.StatusPass, Ref: "out1"})
		return exec.Command("sh", "-c", "echo 'INF mockery generating mocks'; echo 'wrote x' >&2").Run()
	}, observe)
	var err error
	seen := terminal(t, func() { err = run(context.Background(), []string{"generate:rw", "."}) })
	require.NoError(t, err)
	assert.Empty(t, seen)
}

// TestSettleQuietlyNamesTheFailingStep: a failed step becomes the error, with the first
// line of its cause and the command that reads its output, and nothing else reaches the
// terminal.
func TestSettleQuietlyNamesTheFailingStep(t *testing.T) {
	run := Quietly(func(ctx context.Context, inv []string) error {
		fmt.Println("[fail] magus generate (ran, 1s)")
		emitResult(ctx, journal.Event{Project: "docs", Target: "generate:rw", Status: journal.StatusPass, Ref: "out1"})
		emitResult(ctx, journal.Event{Project: ".", Target: "generate:rw", Status: journal.StatusFail, Ref: "out2", Text: "mockery exited 1\n  at line 4"})
		emitResult(ctx, journal.Event{Project: "api", Target: "generate:rw", Status: journal.StatusFail, Ref: "out3", Text: "later"})
		return errors.New("already reported")
	}, observe)
	var err error
	seen := terminal(t, func() { err = run(context.Background(), []string{"generate:rw", "."}) })
	assert.Empty(t, seen)
	var failure *StepFailure
	require.ErrorAs(t, err, &failure)
	assert.Equal(t, "generate:rw failed in .: mockery exited 1, read it with `magus query output out2`", err.Error())
}

// TestSettleQuietlyReplaysAFailureWithNoStep: a run that failed before any step did has
// no output ref to point at, so what it printed is the only account of why, and it is
// replayed.
func TestSettleQuietlyReplaysAFailureWithNoStep(t *testing.T) {
	run := Quietly(func(ctx context.Context, inv []string) error {
		fmt.Fprintln(os.Stderr, "magus: no project here serves generate")
		return errors.New("already reported")
	}, observe)
	var err error
	seen := terminal(t, func() { err = run(context.Background(), []string{"generate:rw", "."}) })
	require.EqualError(t, err, "already reported")
	assert.Equal(t, "magus: no project here serves generate\n", seen)
}
