package job

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/egladman/magus/internal/store/migrate"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
)

// envelopeAttempts rewrites each line of attempts.jsonl an older magus
// wrote as an attempt record ([job.MarshalAttempt]). A line already one is kept, so a run cut
// short runs again to the same file.
func (s *Store) envelopeAttempts(ctx context.Context) error {
	path, err := s.AttemptsPath()
	if err != nil {
		return err
	}
	return codec.WithLock(ctx, path+".lock", codec.LockWait, func() error {
		read, err := codec.ReadLines(path)
		if err != nil || len(read.Lines) == 0 {
			return err
		}
		lines := make([][]byte, 0, len(read.Lines))
		for _, line := range read.Lines {
			var old struct {
				APIVersion types.APIVersion  `json:"api_version"`
				Job        string            `json:"job"`
				N          int               `json:"n"`
				Status     job.AttemptStatus `json:"status"`
				Dropped    bool              `json:"dropped"`
			}
			if err := codec.Unmarshal(line.Text, &old); err != nil {
				return types.WrapDiagnostic(types.StoreLineUndecodable, err, "%s:%d: %v", path, line.N, err)
			}
			if old.APIVersion == "" {
				lines = append(lines, line.Text)
				continue
			}
			text, err := job.MarshalAttempt(job.Attempt{Job: old.Job, N: old.N, Status: old.Status}, old.Dropped)
			if err != nil {
				return err
			}
			lines = append(lines, text)
		}
		return codec.RewriteLines(path, lines, 0o600)
	})
}

// regroupAttempts respells each line of attempts.jsonl stamped with the batch.magus group
// in the job group ([regroup]). A line already regrouped is kept, so a run cut short runs
// again to the same file.
func (s *Store) regroupAttempts(ctx context.Context) error {
	path, err := s.AttemptsPath()
	if err != nil {
		return err
	}
	return codec.WithLock(ctx, path+".lock", codec.LockWait, func() error {
		read, err := codec.ReadLines(path)
		if err != nil || len(read.Lines) == 0 {
			return err
		}
		lines := make([][]byte, 0, len(read.Lines))
		for _, line := range read.Lines {
			var e migrate.Entry
			if err := codec.Unmarshal(line.Text, &e); err != nil {
				return types.WrapDiagnostic(types.StoreLineUndecodable, err, "%s:%d: %v", path, line.N, err)
			}
			if e, err = regroup(e); err != nil {
				return fmt.Errorf("%s:%d: %w", path, line.N, err)
			}
			text, err := codec.Marshal(e)
			if err != nil {
				return err
			}
			lines = append(lines, text)
		}
		return codec.RewriteLines(path, lines, 0o600)
	})
}

// BoundAttempts is attempts each read at now under its job's deadline in
// rows. A try whose job rows does not hold is left out.
func BoundAttempts(rows []job.Job, attempts []job.Attempt, now int64) []job.Attempt {
	deadline := make(map[string]int64, len(rows))
	for _, r := range rows {
		deadline[r.ID] = r.Deadline
	}
	out := make([]job.Attempt, 0, len(attempts))
	for _, a := range attempts {
		if d, ok := deadline[a.Job]; ok {
			out = append(out, a.Bound(d, now))
		}
	}
	return out
}

// StartAttempt records that the guard let a spawn or continuation naming
// live job id through at now. A try already running stays as it was,
// so a hook fired twice records one start; an ended one starts again,
// which is a holder sent back with more work.
func (s *Store) StartAttempt(ctx context.Context, id string, now time.Time) (job.Attempt, error) {
	rows, err := s.Unswept(ctx)
	if err != nil {
		return job.Attempt{}, err
	}
	i := slices.IndexFunc(rows, func(r job.Job) bool { return r.ID == id })
	if i < 0 || !rows[i].State.Live() {
		return job.Attempt{}, fmt.Errorf("%w: %q is not a live job, so no try of it starts", job.ErrUnknownJob, id)
	}
	return s.ChangeLiveAttempt(ctx, id, func(a *job.Attempt) bool {
		if a.Running() {
			return false
		}
		a.Status.Started, a.Status.Ended, a.Status.End = now.Unix(), 0, ""
		return true
	})
}

// EndAttempt records that the end of job id's try in progress was
// observed at now, as end. The first end observed stands.
func (s *Store) EndAttempt(ctx context.Context, id string, end job.AttemptEnd, now time.Time) error {
	_, err := s.ChangeLiveAttempt(ctx, id, func(a *job.Attempt) bool {
		if a.Status.Ended != 0 {
			return false
		}
		a.Status.Ended, a.Status.End = now.Unix(), end
		return true
	})
	return err
}

// EndCallerAttempt is [Store.EndAttempt] for the job c is bound to. A
// caller bound to none ends nothing.
func (s *Store) EndCallerAttempt(ctx context.Context, c Caller, end job.AttemptEnd, now time.Time) error {
	if !c.Identified() {
		return nil
	}
	b := readBinding(s.record(c))
	if b.Job == "" || b.Gone {
		return nil
	}
	return s.EndAttempt(ctx, b.Job, end, now)
}

// fileResult puts result on job id's try in progress and ends it by
// exit, as `magus job exit` does.
func (s *Store) fileResult(ctx context.Context, id string, result *job.Result, now int64) error {
	_, err := s.ChangeLiveAttempt(ctx, id, func(a *job.Attempt) bool {
		a.Status.Result = result
		if a.Status.Ended == 0 {
			a.Status.Ended, a.Status.End = now, job.AttemptEndExit
		}
		return true
	})
	return err
}

// storedTries is what an older jobs.jsonl row kept of its tries before
// attempts.jsonl held them.
type storedTries struct {
	ID       string      `json:"id"`
	Attempts int         `json:"attempts"`
	Result   *job.Result `json:"result"`
	Prior    []struct {
		job.AttemptStatus
		N               int      `json:"n"`
		UnresolvedRisks []string `json:"unresolved_risks"`
	} `json:"prior"`
}

// attempts is t as the records that replace it. A prior try's risks
// were all it kept of its result, so they become that result.
func (t storedTries) attempts() []job.Attempt {
	var out []job.Attempt
	for _, p := range t.Prior {
		a := job.Attempt{Job: t.ID, N: p.N, Status: p.AttemptStatus}
		if a.Status.Ended != 0 {
			a.Status.End = job.AttemptEndJob
		}
		if len(p.UnresolvedRisks) > 0 {
			a.Status.Result = &job.Result{Job: t.ID, UnresolvedRisks: p.UnresolvedRisks}
		}
		out = append(out, a)
	}
	if t.Result != nil {
		out = append(out, job.Attempt{Job: t.ID, N: max(t.Attempts, 1), Status: job.AttemptStatus{Result: t.Result}})
	}
	return out
}

// moveTries writes the tries the rows of jobs.jsonl still carry to
// attempts.jsonl, read from each row's last line. A run cut short runs
// again and appends the same lines, which the log reads once.
func (s *Store) moveTries(ctx context.Context) error {
	read, err := codec.ReadLines(s.path)
	if err != nil {
		return err
	}
	byID := map[string]storedTries{}
	var order []string
	for _, line := range read.Lines {
		var t storedTries
		if err := codec.Unmarshal(line.Text, &t); err != nil {
			return types.WrapDiagnostic(types.StoreLineUndecodable, err, "%s:%d: %v", s.path, line.N, err)
		}
		if t.ID == "" {
			continue
		}
		if _, ok := byID[t.ID]; !ok {
			order = append(order, t.ID)
		}
		byID[t.ID] = t
	}
	var moved []job.Attempt
	for _, id := range order {
		moved = append(moved, byID[id].attempts()...)
	}
	return s.AppendAttempts(ctx, moved...)
}

// triesMoved names the migration [Store.migrate] runs [Store.moveTries]
// before.
const triesMoved = "result, prior and attempts move to attempts.jsonl"

// dropTries takes the tries off a row once [Store.moveTries] wrote them.
func dropTries(e migrate.Entry) (migrate.Entry, error) {
	delete(e, "result")
	delete(e, "prior")
	delete(e, "attempts")
	return e, nil
}
