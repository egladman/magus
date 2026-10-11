package main

import (
	"context"
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
	Regenerated []job.RegeneratedOutput
	Unrun       []job.UnrunGate
	Overrun     *job.ForegroundOverrun
}

// jobNotes is rowNotes for each writing row, keyed by job id, over one workspace load. A
// workspace that will not load yields nothing: the listing states a fact, and it must
// never be the reason a fork or an apply fails.
func jobNotes(ctx context.Context, root string, rows []types.Job) map[string]rowNotes {
	var m *magus.Magus
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
		}
		if notes := loadNotes(ctx, m, row); len(notes.Regenerated) > 0 || len(notes.Unrun) > 0 || notes.Overrun != nil {
			out[row.ID] = notes
		}
	}
	return out
}

func loadNotes(ctx context.Context, m *magus.Magus, row types.Job) rowNotes {
	regenerated := job.RegeneratedOutside(m.Root(), row, m.All(), job.VCSIgnored(ctx, m.Root()))
	return rowNotes{
		Regenerated: regenerated,
		Unrun:       job.UnrunGates(m.Root(), row, m.All(), regenerated),
		Overrun:     checkOverrun(ctx, m, row),
	}
}

// checkOverrun is row's check when its last real run here took longer than a wired host
// lets a foreground call wait. The session store is read first, and the harnesses loaded
// only for a check that has a recorded run.
func checkOverrun(ctx context.Context, m *magus.Magus, row types.Job) *job.ForegroundOverrun {
	dir, err := sessions.Dir(m.Root())
	if err != nil {
		return nil
	}
	lastRun := func(project, target string) (time.Duration, bool) {
		return sessions.LastRealDuration(dir, project, target)
	}
	return job.CheckOverrun(row, lastRun, func() (time.Duration, bool) {
		wired := m.Harnesses()
		ctx := agent.ContextWithWiredHarnesses(ctx, wired)
		var loaded []agent.HarnessDescriptor
		for _, id := range wired {
			if d, _, err := agent.LoadHarness(ctx, m.Root(), id); err == nil {
				loaded = append(loaded, d)
			}
		}
		return agent.ForegroundMax(loaded)
	})
}

// forkOutput is `job fork`'s structured output: the row as stored, plus three keys. The
// embedded row flattens, so the wire shape is the job's own with regenerated_outside,
// unrun_gates and foreground_overrun added.
type forkOutput struct {
	types.Job `yaml:",inline"`
	// RegeneratedOutside is computed when the fork renders and never stored.
	RegeneratedOutside []job.RegeneratedOutput `json:"regenerated_outside,omitempty" yaml:"regenerated_outside,omitempty"`
	// UnrunGates is computed beside it: ci targets over the write paths that neither the
	// check nor a goal runs.
	UnrunGates []job.UnrunGate `json:"unrun_gates,omitempty" yaml:"unrun_gates,omitempty"`
	// ForegroundOverrun is the check, when its last real run took longer than a wired host
	// lets a foreground call wait.
	ForegroundOverrun *job.ForegroundOverrun `json:"foreground_overrun,omitempty" yaml:"foreground_overrun,omitempty"`
}
