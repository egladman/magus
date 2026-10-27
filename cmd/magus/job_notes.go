package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/libs/cmdline/output"
	"github.com/egladman/magus/types"
)

// rowNotes is what the workspace says about a job's write paths that the row cannot: the
// outputs regenerated outside them, the ci targets over them that nothing the job is
// graded on runs, and a check too long to wait on in the foreground.
type rowNotes struct {
	RegeneratedOutside []job.RegeneratedOutput
	UnrunGates         []job.UnrunGate
	ForegroundOverrun  *job.ForegroundOverrun
}

// refuseFork refuses the fork with every problem of v listed in full.
func refuseFork(v types.JobValidation) error {
	var list strings.Builder
	job.RenderValidation(&list, v, false)
	return listedProblems{list: list.String(), err: v.Error()}
}

// judgedMerge is row's merge as validation reads it. A job that works in its own checkout
// shares none, which the standing placeholder says before the worktree exists.
func judgedMerge(row types.Declaration) func(*types.Job) {
	merge := job.Declare(row, globalCfg.Jobs.DefaultTimeout)
	if row.Checkout == "" {
		return merge
	}
	return func(u *types.Job) {
		merge(u)
		u.Worktree = &types.JobWorktree{Root: row.Checkout}
	}
}

// fixFork is [job.FixFork] over the merge a fork validates, noting each edit it made.
func fixFork(ctx context.Context, store *job.Store, row types.Declaration, read job.SymbolReader) (types.Declaration, error) {
	fixed, edits, err := job.FixFork(ctx, store, row, judgedMerge, globalCfg.Jobs, read)
	for _, e := range edits {
		slog.InfoContext(ctx, e.String(), attr.Notice("fixed"))
	}
	return fixed, err
}

// fixApply is [job.FixApply], noting each edit it made under its record's id.
func fixApply(ctx context.Context, store *job.Store, records []types.Declaration, read job.SymbolReader, checkpoint func() string) []types.Declaration {
	fixed, edits := job.FixApply(ctx, store, records, globalCfg.Jobs, read, checkpoint)
	for _, rec := range fixed {
		for _, e := range edits[rec.ID] {
			slog.InfoContext(ctx, fmt.Sprintf("%s: %s", rec.ID, e.String()), attr.Notice("fixed"))
		}
	}
	return fixed
}

// listedProblems is a refusal whose text is its problems as [job.RenderValidation] lists
// them, so the one error record carries the whole list. It unwraps to the problems.
type listedProblems struct {
	list string
	err  error
}

func (e listedProblems) Error() string { return strings.TrimSuffix(e.list, "\n") }
func (e listedProblems) Unwrap() error { return e.err }
func (listedProblems) ExitCode() int   { return exit.Failure }

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
func forkOverrun(ctx context.Context, root string, row types.Job) (*job.ForegroundOverrun, error) {
	m, err := loadMagus(ctx, root)
	if err != nil {
		return nil, err
	}
	return checkOverrun(m, row, hostForegroundMax(ctx, m)), nil
}

// collectNotes is row's notes over the already loaded workspace m.
func collectNotes(ctx context.Context, m *magus.Magus, row types.Job) rowNotes {
	return rowNotesWith(ctx, m, row, hostForegroundMax(ctx, m))
}

func rowNotesWith(ctx context.Context, m *magus.Magus, row types.Job, foregroundMax func() (time.Duration, bool)) rowNotes {
	regenerated := job.RegeneratedOutside(m.Root(), row, m.All(), job.VCSIgnored(ctx, m.Root()))
	return rowNotes{
		RegeneratedOutside: regenerated,
		UnrunGates:         job.UnrunGates(m.Root(), row, m.All(), regenerated),
		ForegroundOverrun:  checkOverrun(m, row, foregroundMax),
	}
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

// checkOverrun is row's check when its last real run here took longer than a wired host
// lets a foreground call wait. The session store is read first, and the harnesses are
// consulted only for a check that has a recorded run.
func checkOverrun(m *magus.Magus, row types.Job, foregroundMax func() (time.Duration, bool)) *job.ForegroundOverrun {
	dir, err := sessions.Dir(m.Root())
	if err != nil {
		return nil
	}
	lastRun := func(project, target string) (time.Duration, bool) {
		return sessions.LastRunDuration(dir, project, target)
	}
	overrun, ok := job.OverrunsForeground(row, lastRun, foregroundMax)
	if !ok {
		return nil
	}
	return &overrun
}

// forkOutput is `job fork`'s structured output: the row as stored, plus its notes. The
// embedded row flattens, so the wire shape is the job's own with deletion_candidates,
// referenced_outside, referenced_outside_gap, foreground_overrun and claim_notes added.
type forkOutput struct {
	types.Job `yaml:",inline"`
	// DeletionCandidates are the unreferenced symbols the write paths cover: findings to
	// finish or delete, never goals.
	DeletionCandidates []types.UnreferencedEntry `json:"deletion_candidates,omitempty" yaml:"deletion_candidates,omitempty"`
	// ReferencedOutside are the files outside the write paths that use what they define.
	ReferencedOutside []job.ReferencedFile `json:"referenced_outside,omitempty" yaml:"referenced_outside,omitempty"`
	// ReferencedOutsideGap says why ReferencedOutside is unknown; empty when a symbol index
	// answered.
	ReferencedOutsideGap string `json:"referenced_outside_gap,omitempty" yaml:"referenced_outside_gap,omitempty"`
	// ForegroundOverrun is the check, when its last real run took longer than a wired host
	// lets a foreground call wait.
	ForegroundOverrun *job.ForegroundOverrun `json:"foreground_overrun,omitempty" yaml:"foreground_overrun,omitempty"`
	// ClaimNotes are job.ForkNotes for the row: a claim naming no declaration yet, and the
	// claim pair that would split a file a job in this checkout also writes.
	ClaimNotes []string `json:"claim_notes,omitempty" yaml:"claim_notes,omitempty"`
}
