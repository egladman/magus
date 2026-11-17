package job

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/egladman/magus/libs/codec"
)

func (s *Store) grantsLog() (codec.JSONL[Grant], error) {
	path, err := s.Path()
	if err != nil {
		return codec.JSONL[Grant]{}, err
	}
	return codec.JSONL[Grant]{
		Path:   filepath.Join(filepath.Dir(path), grantsFileName),
		Perm:   0o600,
		Key:    Grant.Key,
		OnDrop: s.dropped,
	}, nil
}

// Grant records a person's approval lowering rule to advice for job id's calls until the
// unix second until, or for the job's live attempt when until is zero. A later grant for the
// same job and rule replaces it. The caller has checked rule is grantable; the store
// refuses a lease holder, an unknown job and one that already ended.
func (s *Store) Grant(ctx context.Context, id, rule string, until int64) (Grant, error) {
	return s.appendGrant(ctx, id, rule, func(row Job, g *Grant) error {
		if row.State.Terminal() {
			return fmt.Errorf("job: %s already ended %s, so there is nothing left to grant", id, row.State)
		}
		tries, err := s.Attempts(ctx)
		if err != nil {
			return err
		}
		g.Until, g.Attempt = until, NewAttempts(tries).LiveN(id)
		return nil
	})
}

// Revoke ends job id's grant of rule, recording who ended it. Revoking a grant that is not
// there still writes the line.
func (s *Store) Revoke(ctx context.Context, id, rule string) (Grant, error) {
	return s.appendGrant(ctx, id, rule, func(_ Job, g *Grant) error {
		g.Revoked = true
		return nil
	})
}

// GrantRefusedError is a grant or revocation made under a lease: a grant is a person's
// keystroke, and a process acting under a lease is a worker.
type GrantRefusedError struct {
	Lease string
}

func (e *GrantRefusedError) Error() string {
	return fmt.Sprintf("job: a grant is a person's keystroke, and this process acts under lease %q: a person grants it at their own terminal", e.Lease)
}

func (s *Store) appendGrant(ctx context.Context, id, rule string, fill func(Job, *Grant) error) (Grant, error) {
	if err := s.needWriter("Grant"); err != nil {
		return Grant{}, err
	}
	if actor := s.Actor(); actor.Bound() {
		return Grant{}, s.wrap(&GrantRefusedError{Lease: actor.Lease})
	}
	rows, err := s.List(ctx)
	if err != nil {
		return Grant{}, err
	}
	i := slices.IndexFunc(rows, func(r Job) bool { return r.ID == id })
	if i < 0 {
		return Grant{}, s.wrap(fmt.Errorf("%w: %q", ErrUnknownJob, id))
	}
	g := Grant{Job: id, Rule: rule, By: s.originOf(ctx), At: s.Now().Unix()}
	if err := fill(rows[i], &g); err != nil {
		return Grant{}, err
	}
	log, err := s.grantsLog()
	if err != nil {
		return Grant{}, err
	}
	return g, s.wrap(log.Update(ctx, func([]Grant) []Grant { return []Grant{g} }))
}

// Grants is every job and rule's last line, revocations included, in the order they were
// last written.
func (s *Store) Grants(ctx context.Context) ([]Grant, error) {
	log, err := s.grantsLog()
	if err != nil {
		return nil, err
	}
	grants, err := log.Load()
	return grants, s.wrap(err)
}
