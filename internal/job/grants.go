package job

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// grantsFileName sits beside jobsFileName, so a grant lives and dies with the plan it
// names.
const grantsFileName = "grants.jsonl"

func (s *Store) grantsLog() (file.JSONL[types.JobGrant], error) {
	path, err := s.Path()
	if err != nil {
		return file.JSONL[types.JobGrant]{}, err
	}
	return file.JSONL[types.JobGrant]{
		Path: filepath.Join(filepath.Dir(path), grantsFileName),
		Key:  types.JobGrant.Key,
	}, nil
}

// Grant records a person's approval lowering rule to advice for job id's calls until the
// unix second until, or for the job's live attempt when until is zero. A later grant for the
// same job and rule replaces it. The caller has checked rule is grantable; the store
// refuses a lease holder, an unknown job and one that already ended.
func (s *Store) Grant(ctx context.Context, id, rule string, until int64) (types.JobGrant, error) {
	return s.appendGrant(ctx, id, rule, func(row types.Job, g *types.JobGrant) error {
		if row.State.Terminal() {
			return fmt.Errorf("job: %s already ended %s, so there is nothing left to grant", id, row.State)
		}
		g.Until, g.Attempt = until, row.Attempts
		return nil
	})
}

// Revoke ends job id's grant of rule, recording who ended it. Revoking a grant that is not
// there still writes the line.
func (s *Store) Revoke(ctx context.Context, id, rule string) (types.JobGrant, error) {
	return s.appendGrant(ctx, id, rule, func(_ types.Job, g *types.JobGrant) error {
		g.Revoked = true
		return nil
	})
}

func (s *Store) appendGrant(ctx context.Context, id, rule string, fill func(types.Job, *types.JobGrant) error) (types.JobGrant, error) {
	if actor := s.Actor(); actor.Bound() {
		return types.JobGrant{}, fmt.Errorf("job: a grant is a person's keystroke, and this process acts under lease %q; a person types `%s job grant` at their own terminal", actor.Lease, hint.BinaryName())
	}
	rows, err := s.List()
	if err != nil {
		return types.JobGrant{}, err
	}
	i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id })
	if i < 0 {
		return types.JobGrant{}, fmt.Errorf("%w: %q, `%s` lists the declared ids", ErrUnknownJob, id, hint.LsJobs)
	}
	g := types.JobGrant{Job: id, Rule: rule, By: trail.StampOrigin(ctx, types.Origin{}), At: time.Now().Unix()}
	if err := fill(rows[i], &g); err != nil {
		return types.JobGrant{}, err
	}
	log, err := s.grantsLog()
	if err != nil {
		return types.JobGrant{}, err
	}
	return g, log.Update(ctx, func([]types.JobGrant) []types.JobGrant { return []types.JobGrant{g} })
}

// Grants is every job and rule's last line, revocations included, in the order they were
// last written.
func (s *Store) Grants() ([]types.JobGrant, error) {
	log, err := s.grantsLog()
	if err != nil {
		return nil, err
	}
	return log.Load()
}

// LiveGrant is row's grant of rule while it still lowers the rule at now (see
// [types.JobGrant.LiveFor]).
func (s *Store) LiveGrant(row types.Job, rule string, now int64) (types.JobGrant, bool, error) {
	grants, err := s.Grants()
	if err != nil {
		return types.JobGrant{}, false, err
	}
	for _, g := range grants {
		if g.Rule == rule && g.LiveFor(row, now) {
			return g, true, nil
		}
	}
	return types.JobGrant{}, false, nil
}
