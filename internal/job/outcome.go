package job

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/ci/volatility"
	"github.com/egladman/magus/internal/config"
	configgen "github.com/egladman/magus/internal/config/gen"
	"github.com/egladman/magus/types"
)

// newJobOutcome is row's terminal record, kept in the history file after the row expires.
// ended is Unix milliseconds.
//
// The row carries no token count or cost, so those stay zero until a host reports them
// onto it. ChecksRun counts the runs the row kept evidence of: its own check's and one per
// goal. Retry classifies the attempt from the row alone: an outcome is recorded under the
// store's lock, where the trail's denials are not read.
func newJobOutcome(row types.Job, ended int64) types.JobOutcome {
	out := types.JobOutcome{
		Job:          row.ID,
		Kind:         modelTierKind(row),
		Model:        row.Model,
		WriteFiles:   writeFileCount(row.WritePaths),
		State:        row.State,
		ChecksRun:    len(row.GateAttempts),
		Ended:        ended,
		ReworkRounds: row.ReworkRounds,
		Widenings:    row.Widenings,
		Attempt:      max(row.Attempts, 1),
		Retry:        foldAttempt(row, nil, false, ended/1000).Retry.Change,
	}
	if row.Attempt != nil {
		out.ChecksRun++
	}
	if t := row.ModelTier; t != nil {
		out.ModelTier, out.Skills, out.Source, out.Reason, out.Site = t.Name, t.Skills, t.Source, t.Reason, t.Site
		out.Model = cmp.Or(t.Model, row.Model)
	}
	if row.Created > 0 {
		out.DurationMs = max(ended-row.Created*1000, 0)
	}
	return out
}

// ModelTierRecords folds outcomes into one record per (tier, job kind), ordered by rank
// then name then kind. A tier set does not declare, because the declaration changed since
// the job ran, keeps its record at rank -1 and sorts last; a nil set ranks every tier -1.
// An outcome that ran at no tier, or whose state is not terminal, is not counted.
//
// PassLower is the Wilson lower bound of the pass rate, the same bound volatility scores
// flakiness with. ForksPerID is jobs per distinct job id: above 1, a person re-forked an id
// after it failed. The other per-job figures are means, except MedianTokens.
func ModelTierRecords(outcomes []types.JobOutcome, set *types.ModelTierSet) []types.ModelTierRecord {
	type key struct {
		tier string
		kind types.JobKind
	}
	type fold struct {
		rec                    types.ModelTierRecord
		ids                    map[string]struct{}
		checks, rework, widens int
		cost                   float64
		tokens                 []int64
	}
	groups := map[key]*fold{}
	for _, o := range outcomes {
		if o.ModelTier == "" || !o.State.Terminal() {
			continue
		}
		k := key{o.ModelTier, o.Kind}
		f := groups[k]
		if f == nil {
			f = &fold{rec: types.ModelTierRecord{Rank: -1, ModelTier: o.ModelTier, Kind: o.Kind}, ids: map[string]struct{}{}}
			if set != nil {
				if t, err := set.Parse(o.ModelTier); err == nil {
					f.rec.Rank = t.Rank()
				}
			}
			groups[k] = f
		}
		f.rec.Jobs++
		switch o.State {
		case types.StatePass:
			f.rec.Pass++
		case types.StateFail:
			f.rec.Fail++
		default:
			f.rec.NoReturn++
		}
		// An outcome recorded before attempts were numbered was a first attempt.
		if o.Attempt <= 1 {
			f.rec.FirstAttempts++
			if o.State == types.StatePass {
				f.rec.FirstPass++
			}
		}
		if o.Retry != types.RetryAsIs {
			f.rec.NotRetryable++
		}
		f.ids[o.Job] = struct{}{}
		f.checks += o.ChecksRun
		f.rework += o.ReworkRounds
		f.widens += o.Widenings
		f.cost += o.CostUSD
		f.tokens = append(f.tokens, o.ContextTokens)
	}
	out := make([]types.ModelTierRecord, 0, len(groups))
	for _, f := range groups {
		n := float64(f.rec.Jobs)
		f.rec.PassLower = volatility.WilsonLower(f.rec.Pass, f.rec.Jobs)
		f.rec.FirstPassLower = volatility.WilsonLower(f.rec.FirstPass, f.rec.FirstAttempts)
		f.rec.ChecksPerJob = float64(f.checks) / n
		f.rec.ForksPerID = n / float64(len(f.ids))
		f.rec.ReworkPerJob = float64(f.rework) / n
		f.rec.WideningsPerJob = float64(f.widens) / n
		f.rec.MeanCostUSD = f.cost / n
		f.rec.MedianTokens = median(f.tokens)
		out = append(out, f.rec)
	}
	declaredFirst := func(rank int) int {
		if rank < 0 {
			return math.MaxInt
		}
		return rank
	}
	slices.SortFunc(out, func(a, b types.ModelTierRecord) int {
		return cmp.Or(
			cmp.Compare(declaredFirst(a.Rank), declaredFirst(b.Rank)),
			cmp.Compare(a.ModelTier, b.ModelTier),
			cmp.Compare(a.Kind, b.Kind),
		)
	})
	return out
}

func median(xs []int64) int64 {
	if len(xs) == 0 {
		return 0
	}
	slices.Sort(xs)
	mid := len(xs) / 2
	if len(xs)%2 == 1 {
		return xs[mid]
	}
	return (xs[mid-1] + xs[mid]) / 2
}

// historyPath is the history file in effect for the workspace: the user-global, workspace
// and environment tiers merged, as every command that writes the history resolves it.
func (s *Store) historyPath() (string, error) {
	cfg, err := config.LoadWithRoot("", s.root)
	if err == nil {
		err = configgen.ApplyEnv(&cfg, os.Getenv)
	}
	if err != nil {
		return "", fmt.Errorf("job: read history_path for the recorded job outcomes: %w", err)
	}
	return cfg.HistoryPath, nil
}

// endings are the rows one write ends, held until the write lands so their outcomes are
// recorded together. Every door that ends a row (a write, the sweep, Prune, EndSession)
// holds them here, so an outcome is recorded the same way whoever ended the job.
type endings struct {
	path string
	rows []types.Job
}

// hold keeps row's outcome, row having just ended. The history file is resolved here,
// inside the write, so a config that does not read fails the write rather than losing the
// outcome after it. A dry run writes nothing, a store with no checkout has no workspace to
// name, and a row that ran at no tier has nothing the lens could rank, so none holds one.
func (e *endings) hold(s *Store, row types.Job) error {
	if s.dryRun || s.root == "" || row.ModelTier == nil {
		return nil
	}
	if e.path == "" {
		path, err := s.historyPath()
		if err != nil || path == "" {
			return err
		}
		e.path = path
	}
	e.rows = append(e.rows, row.Clone())
	return nil
}

// record appends the held outcomes to the history file through [forecast.UpdateHistory],
// which rewrites the file under its cross-process lock without erasing what another
// workspace recorded meanwhile. ended is Unix milliseconds.
func (e endings) record(ctx context.Context, ended int64) error {
	if len(e.rows) == 0 {
		return nil
	}
	ids := make([]string, len(e.rows))
	for i, row := range e.rows {
		ids[i] = row.ID
	}
	if err := forecast.UpdateHistory(ctx, e.path, func(h *forecast.History) error {
		for _, row := range e.rows {
			h.RecordJob(newJobOutcome(row, ended))
		}
		return nil
	}); err != nil {
		return fmt.Errorf("job: record the outcome of %s: %w", strings.Join(ids, ", "), err)
	}
	return nil
}

// assignModelTierWithHistory is [Store.assignModelTier] with the recorded outcomes in the
// matrix's request. They are read only when a matrix runs, since nothing else reads them.
func (s *Store) assignModelTierWithHistory(ctx context.Context, row types.Job, plan []types.Job) (types.JobModelTier, error) {
	if s.modelTierMatrix == nil {
		return s.assignModelTier(ctx, row, plan)
	}
	matrix := s.modelTierMatrix
	withHistory := &Store{modelTiers: s.modelTiers, owners: s.owners}
	withHistory.modelTierMatrix = func(ctx context.Context, req types.ModelTierRequest) (*types.JobModelTier, error) {
		records, err := s.tierHistory(ctx)
		if err != nil {
			return nil, err
		}
		req.History = records
		return matrix(ctx, req)
	}
	return withHistory.assignModelTier(ctx, row, plan)
}

// tierHistory is the model-tier records of the outcomes the workspace's history file holds,
// none for a store with no checkout.
func (s *Store) tierHistory(ctx context.Context) ([]types.ModelTierRecord, error) {
	if s.root == "" {
		return nil, nil
	}
	path, err := s.historyPath()
	if err != nil {
		return nil, err
	}
	var h forecast.History
	if err := h.Load(ctx, path); err != nil {
		return nil, fmt.Errorf("job: %w", err)
	}
	return ModelTierRecords(h.Jobs, s.modelTiers), nil
}

// RecordRework counts one more rework round on job id: its holder handed back and was sent
// more work. An observation, like [Store.RecordUnattributedWrite]: the guard noting what a
// message did is not the row's holder declaring anything. A missing row is not an error,
// since the job may have been removed between the message and this call.
func (s *Store) RecordRework(ctx context.Context, id string) error {
	_, err := s.mutate(ctx, id, asObservation, func(cur *types.Job, exists bool, _ int64) error {
		if !exists {
			return fmt.Errorf("job: no such lease %q: %w", id, errNoSuchJob)
		}
		cur.ReworkRounds++
		return nil
	})
	if errors.Is(err, errNoSuchJob) {
		return nil
	}
	return err
}
