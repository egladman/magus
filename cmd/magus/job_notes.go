package main

import (
	"context"
	"sync"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/sessions"
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

// jobNotes is rowNotes for each writing row, keyed by job id, over one workspace load. A
// workspace that will not load yields nothing: the listing states a fact, and it must
// never be the reason a fork or an apply fails.
func jobNotes(ctx context.Context, root string, rows []types.Job) map[string]rowNotes {
	var m *magus.Magus
	var foregroundMax func() (time.Duration, bool)
	out := map[string]rowNotes{}
	for _, row := range rows {
		if row.ReadOnly || len(row.WritePaths) == 0 {
			continue
		}
		if m == nil {
			loaded, err := loadMagus(ctx, root)
			if err != nil {
				return nil
			}
			m = loaded
			foregroundMax = hostForegroundMax(ctx, m)
		}
		if notes := rowNotesWith(ctx, m, row, foregroundMax); len(notes.RegeneratedOutside) > 0 || len(notes.UnrunGates) > 0 || notes.ForegroundOverrun != nil {
			out[row.ID] = notes
		}
	}
	return out
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
		wired := m.Harnesses()
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
// embedded row flattens, so the wire shape is the job's own with regenerated_outside,
// unrun_gates, deletion_candidates, foreground_overrun and claim_notes added.
type forkOutput struct {
	types.Job `yaml:",inline"`
	// RegeneratedOutside is computed when the fork renders and never stored.
	RegeneratedOutside []job.RegeneratedOutput `json:"regenerated_outside,omitempty" yaml:"regenerated_outside,omitempty"`
	// UnrunGates is computed beside it: ci targets over the write paths that neither the
	// check nor a goal runs.
	UnrunGates []job.UnrunGate `json:"unrun_gates,omitempty" yaml:"unrun_gates,omitempty"`
	// DeletionCandidates are the unreferenced symbols the write paths cover: findings to
	// finish or delete, never goals.
	DeletionCandidates []types.UnreferencedEntry `json:"deletion_candidates,omitempty" yaml:"deletion_candidates,omitempty"`
	// ForegroundOverrun is the check, when its last real run took longer than a wired host
	// lets a foreground call wait.
	ForegroundOverrun *job.ForegroundOverrun `json:"foreground_overrun,omitempty" yaml:"foreground_overrun,omitempty"`
	// ClaimNotes are job.ForkNotes for the row: a claim naming no declaration yet, and the
	// claim pair that would split a file a job in this checkout also writes.
	ClaimNotes []string `json:"claim_notes,omitempty" yaml:"claim_notes,omitempty"`
}
