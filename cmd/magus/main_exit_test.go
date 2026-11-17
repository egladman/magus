package main

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

// TestMachineBusyRidesTheExitCodeSeam pins that a machine-budget refusal needs no
// branch of its own in mapExitCode. The local path and the server now ask the error the
// same question, so the refusal must answer it rather than be recognised by type or by
// diagnostic code, which is what lets one seam serve both this and a contended lock.
func TestMachineBusyRidesTheExitCodeSeam(t *testing.T) {
	// The refusal is built inside the cache, so stand in for it with an error carrying
	// the same two properties the real one does (machine_test.go pins that it does).
	busy := machineRefusal{types.DiagnosticErrorf(types.MachineBudgetExhausted, "the machine is full")}

	assert.Equal(t, cache.ExitCodeMachineBusy, exit.Code(busy), "the server reads the code off a forwarded refusal")
	assert.Equal(t, cache.ExitCodeMachineBusy, mapExitCode(busy), "and the local path must agree")
	assert.Equal(t, cache.ExitCodeMachineBusy, mapExitCode(fmt.Errorf("run: %w", busy)),
		"through every layer between the step and main")

	// A run where real targets ALSO failed is a broken build, not a scheduling problem:
	// a Reported failure is matched first and keeps 1, so a peer being busy cannot
	// rewrite the verdict on work that actually ran.
	assert.Equal(t, 1, mapExitCode(errors.Join(exit.Reported(exit.Failure), busy)))
}

type machineRefusal struct{ error }

func (machineRefusal) ExitCode() int { return cache.ExitCodeMachineBusy }

// TestExitCodeOfPrintsWhatNoOneElseDid pins which of the two exit-75 refusals mapExitCode
// prints. A contended workspace lock is refused before any step exists, so this line is
// the only place it is ever said. A machine refusal is an ExitError the cache already
// logged beside the step it refused (cache's TestRunAllReportsAMachineRefusal), so
// printing it here too would say it twice.
func TestExitCodeOfPrintsWhatNoOneElseDid(t *testing.T) {
	var logged bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	lock := lockRefusal{errors.New("magus: project . is locked by another magus process (held by pid 41221 magus run test in /elsewhere); not waiting")}
	assert.Equal(t, 75, mapExitCode(fmt.Errorf("run: %w", lock)))
	assert.Contains(t, logged.String(), "held by pid 41221 magus run test in /elsewhere")

	logged.Reset()
	refused := types.ExitError{Code: cache.ExitCodeMachineBusy,
		Err: types.DiagnosticErrorf(types.MachineBudgetExhausted, "not starting (root) test")}
	assert.Equal(t, cache.ExitCodeMachineBusy, mapExitCode(fmt.Errorf("run: %w", refused)))
	assert.Empty(t, logged.String())
}

// lockRefusal has the shape of the root package's unexported lockContendedError:
// a plain error that states 75 and is not an ExitError.
type lockRefusal struct{ error }

func (lockRefusal) ExitCode() int      { return 75 }
func (e machineRefusal) Unwrap() error { return e.error }
