package job

import (
	"context"
	"io"
	"slices"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
)

// maxFixRounds bounds `--fix`. An edit can bring problems of its own (an added write path
// regenerates another output), so each round validates again, and a record whose edits
// never settle stops here with what is left.
const maxFixRounds = 5

// FixFork makes every edit the problems of row's fork carry, validating again after each
// round, until a round applies none or maxFixRounds have run. merge builds the fork's merge
// from a record. With refresh, a checkpoint naming a revision the repository does not hold
// is replaced by this checkout's HEAD revision: only a fork into a checkout of its own may
// start somewhere else than it was told, and one naming a real revision starts there. Nothing is written; the result is the record and the
// edits that changed it, in order.
func FixFork(ctx context.Context, store *Store, row types.Declaration, merge func(types.Declaration) func(*types.Job), limits config.Jobs, read SymbolReader, refresh bool) (types.Declaration, []types.JobEdit, error) {
	quiet := quietDry(store)
	var applied []types.JobEdit
	for range maxFixRounds {
		rows, err := quiet.List()
		if err != nil {
			return row, applied, err
		}
		v, _ := validateFork(ctx, quiet, rows, row.ID, merge(row), limits, read)
		edits := problemEdits(v.Problems)
		if refresh && unheldCheckpoint(ctx, quiet, row.Checkpoint) && slices.ContainsFunc(v.Problems, func(p types.JobProblem) bool { return p.Code == types.JobCheckpointUnplaceable }) {
			if token, err := quiet.CheckoutToken(ctx); err == nil {
				rev, _ := types.ParseCheckpointToken(token)
				edits = append(edits, types.JobEdit{RefreshCheckpoint: rev})
			}
		}
		made := applyEdits(&row, edits)
		if len(made) == 0 {
			break
		}
		applied = append(applied, made...)
	}
	return row, applied, nil
}

// unheldCheckpoint reports whether token names a revision the repository does not hold.
func unheldCheckpoint(ctx context.Context, store *Store, token string) bool {
	rev, _ := types.ParseCheckpointToken(token)
	_, _, err := store.fullRevision(ctx, rev)
	return err != nil
}

// FixApply is [FixFork] for the records `magus job apply` takes, each validated by a dry
// [Apply] against the plan as the records before it leave it. It stops early, with the
// records as they stand, on a refusal that is no record's problem (an authorization), which
// the apply that follows reports. The edits are keyed by record id.
func FixApply(ctx context.Context, store *Store, records []types.Declaration, limits config.Jobs, read SymbolReader, checkpoint func() string) ([]types.Declaration, map[string][]types.JobEdit) {
	quiet := quietDry(store)
	ids := make([]string, len(records))
	for i, rec := range records {
		ids[i] = rec.ID
	}
	applied := map[string][]types.JobEdit{}
	for range maxFixRounds {
		_, err := Apply(ctx, quiet, records, limits, read, checkpoint, true)
		validations, ok := ApplyValidations(err, ids)
		if err == nil || !ok {
			break
		}
		changed := false
		for _, v := range validations {
			i := slices.Index(ids, v.Job.ID)
			if made := applyEdits(&records[i], problemEdits(v.Problems)); len(made) > 0 {
				applied[v.Job.ID] = append(applied[v.Job.ID], made...)
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return records, applied
}

// ApplyEdits makes each edit on row and returns the ones that changed it.
func applyEdits(row *types.Declaration, edits []types.JobEdit) []types.JobEdit {
	var made []types.JobEdit
	for _, e := range edits {
		if e.Apply(row) {
			made = append(made, e)
		}
	}
	return made
}

// problemEdits is the Edit of each problem of ps that carries one.
func problemEdits(ps types.JobProblems) []types.JobEdit {
	var edits []types.JobEdit
	for _, p := range ps {
		if p.Edit != nil {
			edits = append(edits, *p.Edit)
		}
	}
	return edits
}

// quietDry is store's dry view with its notices discarded: a fix round validates the same
// record several times, and the fork that follows says each notice once.
func quietDry(store *Store) *Store {
	quiet := store.dry()
	quiet.notices = io.Discard
	return quiet
}
