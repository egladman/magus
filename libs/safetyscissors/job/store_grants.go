package job

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/egladman/magus/libs/codec"
)

func (s *Store) grantsLog() (codec.JSONL[grantRecord], error) {
	path, err := s.Path()
	if err != nil {
		return codec.JSONL[grantRecord]{}, err
	}
	return codec.JSONL[grantRecord]{
		Path:   filepath.Join(filepath.Dir(path), grantsFileName),
		Perm:   0o600,
		Key:    func(r grantRecord) string { return Grant{Job: r.Job, Rule: r.Rule}.Key() },
		OnDrop: s.dropped,
	}, nil
}

// grantRecord is a [Grant] as grants.jsonl stores it. It refuses a member this build does
// not declare, because the compaction after an older build's read would drop it, and a
// dropped member of a grant can widen what the grant lowers.
type grantRecord struct {
	Job     string       `json:"job"`
	Rule    string       `json:"rule"`
	By      originRecord `json:"by"`
	At      int64        `json:"at"`
	Until   int64        `json:"until,omitzero"`
	Attempt int          `json:"attempt,omitzero"`
	Revoked bool         `json:"revoked,omitzero"`
}

func (r *grantRecord) UnmarshalJSON(data []byte) error {
	type plain grantRecord
	return readNested(data, (*plain)(r), "grantRecord")
}

func recordGrant(g Grant) grantRecord {
	return grantRecord{Job: g.Job, Rule: g.Rule, By: recordOrigin(g.By), At: g.At, Until: g.Until, Attempt: g.Attempt, Revoked: g.Revoked}
}

func (r grantRecord) grant(creds wireEnum[CredentialKind]) (Grant, error) {
	var bad wireReads
	g := Grant{Job: r.Job, Rule: r.Rule, By: r.By.origin(&bad, creds), At: r.At, Until: r.Until, Attempt: r.Attempt, Revoked: r.Revoked}
	if err := bad.refusal(); err != nil {
		return Grant{}, fmt.Errorf("job: grant of %s for %s: %w", r.Rule, r.Job, err)
	}
	return g, nil
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
	g := Grant{Job: id, Rule: rule, By: s.callerOrigin(ctx), At: s.Now().Unix()}
	if err := fill(rows[i], &g); err != nil {
		return Grant{}, err
	}
	log, err := s.grantsLog()
	if err != nil {
		return Grant{}, err
	}
	return g, s.wrap(log.Update(ctx, func([]grantRecord) []grantRecord { return []grantRecord{recordGrant(g)} }))
}

// Grants is every job and rule's last line, revocations included, in the order they were
// last written.
func (s *Store) Grants(ctx context.Context) ([]Grant, error) {
	log, err := s.grantsLog()
	if err != nil {
		return nil, err
	}
	records, err := log.Load()
	if err != nil {
		return nil, s.wrap(err)
	}
	grants := make([]Grant, 0, len(records))
	for _, r := range records {
		g, err := r.grant(s.creds)
		if err != nil {
			return nil, s.wrap(err)
		}
		grants = append(grants, g)
	}
	return grants, nil
}
