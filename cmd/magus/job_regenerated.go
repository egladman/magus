package main

import (
	"context"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

// jobRegenerated is job.RegeneratedOutside for each writing row, keyed by job id, over one
// workspace load. A workspace that will not load yields nothing: the listing states a fact,
// and it must never be the reason a fork or an apply fails.
func jobRegenerated(ctx context.Context, root string, rows []types.Job) map[string][]job.RegeneratedOutput {
	var m *magus.Magus
	out := map[string][]job.RegeneratedOutput{}
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
		if found := regeneratedIn(ctx, m, row); len(found) > 0 {
			out[row.ID] = found
		}
	}
	return out
}

func regeneratedIn(ctx context.Context, m *magus.Magus, row types.Job) []job.RegeneratedOutput {
	return job.RegeneratedOutside(m.Root(), row, m.All(), job.VCSIgnored(ctx, m.Root()))
}

// forkOutput is `job fork`'s structured output: the row as stored, plus one key. The
// embedded row flattens, so the wire shape is the job's own with regenerated_outside added.
type forkOutput struct {
	types.Job `yaml:",inline"`
	// RegeneratedOutside is computed when the fork renders and never stored.
	RegeneratedOutside []job.RegeneratedOutput `json:"regenerated_outside,omitempty" yaml:"regenerated_outside,omitempty"`
}
