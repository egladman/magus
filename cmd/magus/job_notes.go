package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/output"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
)

// forkDryRun reports what a fork would do and writes nothing: the validation as
// [types.JobValidation] for a structured format, or the rendered problems and record. It
// exits Failure while a problem remains, as the fork it stands for would refuse.
func forkDryRun(opts output.Options, v types.JobValidation) error {
	var err error
	switch opts.Format {
	case outputName:
		if len(v.Problems) == 0 {
			err = emitNames(os.Stdout, []string{v.Job.ID})
		}
	case outputText:
		job.RenderValidation(os.Stdout, v, true)
		fmt.Println("dry run: nothing written; rerun without --dry-run to fork it")
	default:
		err = emitFormatted(opts, v)
	}
	if err != nil {
		return err
	}
	if len(v.Problems) > 0 {
		return exit.Reported(exit.Failure)
	}
	return nil
}

// warnWorkspaceUnloaded names on stderr what a workspace that will not load left unchecked,
// so a row stored without those checks never reads as a clean one.
func warnWorkspaceUnloaded(ctx context.Context, err error) {
	slog.WarnContext(ctx, "the workspace did not load, so the outputs regenerated outside the write paths, the ci targets no goal runs and a check too long for the foreground were not listed",
		attr.Notice("unchecked"), attr.Error(err))
}

// forkOverrun is row's check when its last real run here took longer than a wired host
// lets a foreground call wait. A workspace that will not load is the error, which the
// fork lists among the checks that could not run rather than failing on: the note states a
// fact and must never be the reason a fork fails, nor read as a clean bill when it was
// never computed.
func forkOverrun(ctx context.Context, root string, row jobtypes.Job) (*job.ForegroundOverrun, error) {
	m, err := loadMagus(ctx, root)
	if err != nil {
		return nil, err
	}
	return job.CheckOverrun(m.Root(), row, hostForegroundMax(ctx, m)), nil
}

// collectNotes is row's notes over the already loaded workspace m.
func collectNotes(ctx context.Context, m *magus.Magus, row jobtypes.Job) job.RowNotes {
	return job.RowNotesWith(ctx, m.Root(), m.All(), row, hostForegroundMax(ctx, m))
}

// hostForegroundMax is the shortest foreground maximum among the harnesses m wires. The
// harness spells load on the first call and are kept for the rest, so a check with no
// recorded run costs none of it.
func hostForegroundMax(ctx context.Context, m *magus.Magus) func() (time.Duration, bool) {
	return sync.OnceValues(func() (time.Duration, bool) {
		wired, err := workspace.WiredHarnesses(m)
		if err != nil {
			slog.With(attr.Component("job fork")).WarnContext(ctx, "no host foreground limit is read", attr.Error(err))
			return 0, false
		}
		ctx := agent.ContextWithWiredHarnesses(ctx, wired)
		var loaded []agent.HarnessDescriptor
		for _, id := range wired {
			if d, _, err := agent.LoadHarness(ctx, m.Root(), id); err == nil {
				loaded = append(loaded, d)
			}
		}
		return agent.ShortestForegroundMax(loaded)
	})
}
