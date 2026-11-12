package job

import (
	"context"
	"fmt"
	"os"
	"slices"

	"github.com/egladman/magus/types"
)

// forkAuthority is the Authority a fork by actor records (ADR 0045). The
// transport named actor, so no declared parent or checkpoint decides it. A
// worker's child takes its parent's, and an unbound fork the root's head.
func forkAuthority(actor Actor, rows []types.Job, rootHead func() string) string {
	if !actor.Bound() {
		return rootHead()
	}
	if i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == actor.Lease }); i >= 0 {
		return rows[i].Authority
	}
	return ""
}

// noteNoAuthority says a fork stored row with no authority. The guard refuses
// its holder's work, and the forker hears it before the worker does.
func (s *Store) noteNoAuthority(row types.Job) {
	if row.Authority != "" {
		return
	}
	out := s.notices
	if out == nil {
		out = os.Stderr
	}
	cause := fmt.Sprintf("the version control at %q names no committed revision", s.root)
	if actor := s.Actor(); actor.Bound() {
		cause = fmt.Sprintf("its forker's row %s records none", actor.Lease)
	}
	fmt.Fprintf(out, "job: %s records no authority, so the guard refuses its holder's work: %s\n", row.ID, cause)
}

// committedRevision is the full revision of the store's checkout HEAD, or ""
// where no checkout or version control answers.
func (s *Store) committedRevision(ctx context.Context) string {
	if s.root == "" {
		return ""
	}
	driver, _ := resolveDriver(ctx, s.root)
	if driver == nil {
		return ""
	}
	head, err := driver.FindCommit(ctx, s.root, "")
	if err != nil {
		return ""
	}
	return head.ID
}

// AdvanceAuthority moves row id's Authority to the store checkout's HEAD and
// returns the row. Only an unbound caller, the root, may: a bound one gets a
// [*RefusedError], since a worker moving its own authority approves its own
// work. It fails on an undeclared row and where version control names no HEAD.
func (s *Store) AdvanceAuthority(ctx context.Context, id string) (types.Job, error) {
	head := s.committedRevision(ctx)
	if head == "" {
		return types.Job{}, fmt.Errorf("job: %s's authority cannot advance: the version control at %q names no committed revision", id, s.root)
	}
	return s.mutate(ctx, id, asAdvance, func(cur *types.Job, exists bool, _ int64) error {
		if !exists {
			return fmt.Errorf("job: %s is not declared, so it has no authority to advance", id)
		}
		cur.Authority = head
		return nil
	})
}
