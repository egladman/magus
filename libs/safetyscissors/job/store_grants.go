package job

import (
	"context"
	"errors"
	"slices"
)

// ConsumeGrant spends a live grant once across concurrent callers.
func (s *Store) ConsumeGrant(ctx context.Context, row Job, rule string) (bool, error) {
	if err := s.needWriter("ConsumeGrant"); err != nil {
		return false, err
	}
	if s.Actor().Bound() {
		return false, s.wrap(&GrantRefusedError{Lease: s.Actor().Lease})
	}
	tries, err := s.Attempts(ctx)
	if err != nil {
		return false, err
	}
	log, err := s.grantsLog()
	if err != nil {
		return false, err
	}
	var spent bool
	var decodeErr error
	err = log.Update(ctx, func(records []grantRecord) []grantRecord {
		for _, r := range slices.Backward(records) {
			if r.Job != row.ID || r.Rule != rule {
				continue
			}
			g, err := r.grant(s.creds)
			decodeErr = err
			if err != nil || !g.Lowers(row, NewAttempts(tries).LiveN(row.ID), s.Now().Unix()) {
				return nil
			}
			g.Revoked = true
			spent = true
			return []grantRecord{recordGrant(g)}
		}
		return nil
	})
	if err != nil || decodeErr != nil {
		return false, s.wrap(errors.Join(err, decodeErr))
	}
	return spent, nil
}
