package main

import (
	"context"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

// rowNotes is what the workspace says about a job's write paths that the row cannot: the
// outputs regenerated outside them and the ci targets over them that nothing the job is
// graded on runs.
type rowNotes struct {
	Regenerated []job.RegeneratedOutput
	Unrun       []job.UnrunGate
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
		if notes := loadNotes(ctx, m, row); len(notes.Regenerated) > 0 || len(notes.Unrun) > 0 {
			out[row.ID] = notes
		}
	}
	return out
}

func loadNotes(ctx context.Context, m *magus.Magus, row types.Job) rowNotes {
	regenerated := job.RegeneratedOutside(m.Root(), row, m.All(), job.VCSIgnored(ctx, m.Root()))
	return rowNotes{Regenerated: regenerated, Unrun: job.UnrunGates(m.Root(), row, m.All(), regenerated)}
}

// forkOutput is `job fork`'s structured output: the row as stored, plus two keys. The
// embedded row flattens, so the wire shape is the job's own with regenerated_outside and
// unrun_gates added.
type forkOutput struct {
	types.Job `yaml:",inline"`
	// RegeneratedOutside is computed when the fork renders and never stored.
	RegeneratedOutside []job.RegeneratedOutput `json:"regenerated_outside,omitempty" yaml:"regenerated_outside,omitempty"`
	// UnrunGates is computed beside it: ci targets over the write paths that neither the
	// check nor a goal runs.
	UnrunGates []job.UnrunGate `json:"unrun_gates,omitempty" yaml:"unrun_gates,omitempty"`
}
