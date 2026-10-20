package rankeval

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"
)

// Replay calls replay for every judgment, at most workers at a time, and returns the
// runs in judgments' order, so a report reads the same however they were scheduled.
// replay must be safe for concurrent use. The first error cancels ctx, starts no
// further judgment, and is returned naming its judgment.
func Replay(ctx context.Context, judgments []Judgment, workers int, replay func(context.Context, Judgment) (Run, error)) ([]Run, error) {
	runs := make([]Run, len(judgments))
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(max(workers, 1))
	for i, j := range judgments {
		if ctx.Err() != nil {
			break
		}
		g.Go(func() error {
			run, err := replay(ctx, j)
			if err != nil {
				return fmt.Errorf("judgment %s: %w", j.ID, err)
			}
			runs[i] = run
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return runs, nil
}
