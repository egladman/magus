package job

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
)

// grantsFileName sits beside jobsFileName, so a grant lives and dies with the plan it
// names.
const grantsFileName = "grants.jsonl"

func (s *Store) grantsLog() (codec.JSONL[job.Grant], error) {
	path, err := s.Path()
	if err != nil {
		return codec.JSONL[job.Grant]{}, err
	}
	return codec.JSONL[job.Grant]{
		Log:  slog.With(attr.Component("magus")),
		Path: filepath.Join(filepath.Dir(path), grantsFileName),
		Key:  job.Grant.Key,
	}, nil
}

// Grant records a person's approval lowering rule to advice for job id's calls until the
// unix second until, or for the job's live attempt when until is zero. A later grant for the
// same job and rule replaces it. The caller has checked rule is grantable; the store
// refuses a lease holder, an unknown job and one that already ended.
func (s *Store) Grant(ctx context.Context, id, rule string, until int64) (job.Grant, error) {
	return s.appendGrant(ctx, id, rule, func(row job.Job, g *job.Grant) error {
		if row.State.Terminal() {
			return fmt.Errorf("job: %s already ended %s, so there is nothing left to grant", id, row.State)
		}
		tries, err := s.Attempts()
		if err != nil {
			return err
		}
		g.Until, g.Attempt = until, job.NewAttempts(tries).LiveN(id)
		return nil
	})
}

// Revoke ends job id's grant of rule, recording who ended it. Revoking a grant that is not
// there still writes the line.
func (s *Store) Revoke(ctx context.Context, id, rule string) (job.Grant, error) {
	return s.appendGrant(ctx, id, rule, func(_ job.Job, g *job.Grant) error {
		g.Revoked = true
		return nil
	})
}

func (s *Store) appendGrant(ctx context.Context, id, rule string, fill func(job.Job, *job.Grant) error) (job.Grant, error) {
	if actor := s.Actor(); actor.Bound() {
		return job.Grant{}, fmt.Errorf("job: a grant is a person's keystroke, and this process acts under lease %q: a person types `%s job grant` at their own terminal", actor.Lease, hint.BinaryName())
	}
	rows, err := s.List()
	if err != nil {
		return job.Grant{}, err
	}
	i := slices.IndexFunc(rows, func(r job.Job) bool { return r.ID == id })
	if i < 0 {
		return job.Grant{}, fmt.Errorf("%w: %q, `%s` lists the declared ids", ErrUnknownJob, id, hint.LsJobs)
	}
	g := job.Grant{Job: id, Rule: rule, By: trail.StampOrigin(ctx, job.Origin{}), At: time.Now().Unix()}
	if err := fill(rows[i], &g); err != nil {
		return job.Grant{}, err
	}
	log, err := s.grantsLog()
	if err != nil {
		return job.Grant{}, err
	}
	return g, log.Update(ctx, func([]job.Grant) []job.Grant { return []job.Grant{g} })
}

// Grants is every job and rule's last line, revocations included, in the order they were
// last written.
func (s *Store) Grants() ([]job.Grant, error) {
	log, err := s.grantsLog()
	if err != nil {
		return nil, err
	}
	return log.Load()
}

// LiveGrant is row's grant of rule while it still lowers the rule at now (see
// [job.Grant.Lowers]).
func (s *Store) LiveGrant(row job.Job, rule string, now int64) (job.Grant, bool, error) {
	grants, err := s.Grants()
	if err != nil {
		return job.Grant{}, false, err
	}
	tries, err := s.Attempts()
	if err != nil {
		return job.Grant{}, false, err
	}
	attempt := job.NewAttempts(tries).LiveN(row.ID)
	for _, g := range grants {
		if g.Rule == rule && g.Lowers(row, attempt, now) {
			return g, true, nil
		}
	}
	return job.Grant{}, false, nil
}
