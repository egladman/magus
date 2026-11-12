package job

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/store/migrate"
	"github.com/egladman/magus/types"
)

// attemptsFileName sits beside jobsFileName: an attempt lives and dies
// with the row it is a try of.
const attemptsFileName = "attempts.jsonl"

// attemptRecord is one line of attempts.jsonl, the persistence type of
// [types.Attempt] (ADR 0030). The last line per job and number is the
// attempt; a Dropped line ends it with its job's row.
type attemptRecord struct {
	APIVersion types.APIVersion    `json:"api_version"`
	Job        string              `json:"job"`
	N          int                 `json:"n"`
	Status     attemptStatusRecord `json:"status"`
	Dropped    bool                `json:"dropped,omitempty"`
}

// attemptStatusRecord is the stored status. Its own type, so the record
// can leave the domain shape without the domain type moving.
type attemptStatusRecord types.AttemptStatus

func (r attemptRecord) key() string { return r.Job + "\x00" + strconv.Itoa(r.N) }

// recordAttempt is a as the line attempts.jsonl stores.
func recordAttempt(a types.Attempt) attemptRecord {
	a = a.Clone()
	return attemptRecord{APIVersion: types.JobAPIVersion, Job: a.Job, N: a.N, Status: attemptStatusRecord(a.Status)}
}

// attempt is r as the domain type.
func (r attemptRecord) attempt() types.Attempt {
	return types.Attempt{Job: r.Job, N: r.N, Status: types.AttemptStatus(r.Status)}.Clone()
}

func (s *Store) attemptsLog() (file.JSONL[attemptRecord], error) {
	if s.err != nil {
		return file.JSONL[attemptRecord]{}, s.err
	}
	return file.JSONL[attemptRecord]{
		Path: filepath.Join(filepath.Dir(s.path), attemptsFileName),
		Perm: 0o600,
		Key:  attemptRecord.key,
		Live: func(r attemptRecord) bool { return !r.Dropped },
	}, nil
}

// refuseAttemptVersion refuses a line another version of magus wrote,
// which this one cannot tell the meaning of.
func refuseAttemptVersion(r attemptRecord) error {
	if r.APIVersion == types.JobAPIVersion {
		return nil
	}
	return types.DiagnosticErrorf(types.StoreRecordsNewer, "job: %s: attempt %d of %s is %s, and this magus reads %s: update magus, or %s",
		attemptsFileName, r.N, r.Job, r.APIVersion, types.JobAPIVersion, staleStoreFix)
}

// Attempts is every try the store holds, in the order their last lines
// were written, as stored: [types.Attempt.Bound] reads one under its
// job's deadline. An absent file holds none.
func (s *Store) Attempts() ([]types.Attempt, error) {
	log, err := s.attemptsLog()
	if err != nil {
		return nil, err
	}
	recs, err := log.Load()
	if err != nil {
		return nil, fmt.Errorf("job: %w", err)
	}
	out := make([]types.Attempt, 0, len(recs))
	for _, r := range recs {
		if err := refuseAttemptVersion(r); err != nil {
			return nil, err
		}
		out = append(out, r.attempt())
	}
	return out, nil
}

// BoundAttempts is attempts each read at now under its job's deadline in
// rows. A try whose job rows does not hold is left out.
func BoundAttempts(rows []types.Job, attempts []types.Attempt, now int64) []types.Attempt {
	deadline := make(map[string]int64, len(rows))
	for _, r := range rows {
		deadline[r.ID] = r.Deadline
	}
	out := make([]types.Attempt, 0, len(attempts))
	for _, a := range attempts {
		if d, ok := deadline[a.Job]; ok {
			out = append(out, a.Bound(d, now))
		}
	}
	return out
}

// changeLiveAttempt shows change job id's try in progress under the
// attempts lock, and appends it when change reports it changed.
func (s *Store) changeLiveAttempt(ctx context.Context, id string, change func(*types.Attempt) bool) (types.Attempt, error) {
	log, err := s.attemptsLog()
	if err != nil {
		return types.Attempt{}, err
	}
	var live types.Attempt
	var failed error
	err = log.Update(ctx, func(recs []attemptRecord) []attemptRecord {
		all := make([]types.Attempt, 0, len(recs))
		for _, r := range recs {
			if failed = refuseAttemptVersion(r); failed != nil {
				return nil
			}
			all = append(all, r.attempt())
		}
		live = types.NewJobAttempts(all).Live(id)
		if !change(&live) {
			return nil
		}
		return []attemptRecord{recordAttempt(live)}
	})
	if err == nil {
		err = failed
	}
	if err != nil {
		return types.Attempt{}, fmt.Errorf("job: %s: %w", attemptsFileName, err)
	}
	return live, nil
}

// StartAttempt records that the guard let a spawn or continuation naming
// live job id through at now. A try already running stays as it was,
// so a hook fired twice records one start; an ended one starts again,
// which is a holder sent back with more work.
func (s *Store) StartAttempt(ctx context.Context, id string, now time.Time) (types.Attempt, error) {
	rows, err := s.unswept()
	if err != nil {
		return types.Attempt{}, err
	}
	i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id })
	if i < 0 || !rows[i].State.Live() {
		return types.Attempt{}, fmt.Errorf("%w: %q is not a live job, so no try of it starts", ErrUnknownJob, id)
	}
	return s.changeLiveAttempt(ctx, id, func(a *types.Attempt) bool {
		if a.Running() {
			return false
		}
		a.Status.Started, a.Status.Ended, a.Status.End = now.Unix(), 0, ""
		return true
	})
}

// EndAttempt records that the end of job id's try in progress was
// observed at now, as end. The first end observed stands.
func (s *Store) EndAttempt(ctx context.Context, id string, end types.AttemptEnd, now time.Time) error {
	_, err := s.changeLiveAttempt(ctx, id, func(a *types.Attempt) bool {
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
func (s *Store) EndCallerAttempt(ctx context.Context, c Caller, end types.AttemptEnd, now time.Time) error {
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
func (s *Store) fileResult(ctx context.Context, id string, result *types.JobResult, now int64) error {
	_, err := s.changeLiveAttempt(ctx, id, func(a *types.Attempt) bool {
		a.Status.Result = result
		if a.Status.Ended == 0 {
			a.Status.Ended, a.Status.End = now, types.AttemptEndExit
		}
		return true
	})
	return err
}

// appendAttempts appends attempts as their records.
func (s *Store) appendAttempts(ctx context.Context, attempts ...types.Attempt) error {
	if len(attempts) == 0 {
		return nil
	}
	log, err := s.attemptsLog()
	if err != nil {
		return err
	}
	return log.Update(ctx, func([]attemptRecord) []attemptRecord {
		out := make([]attemptRecord, len(attempts))
		for i, a := range attempts {
			out[i] = recordAttempt(a)
		}
		return out
	})
}

// dropAttempts ends every try of the jobs gone names, whose rows a write
// dropped, so an id declared again starts on its first try.
func (s *Store) dropAttempts(ctx context.Context, gone map[string]bool) error {
	if len(gone) == 0 {
		return nil
	}
	log, err := s.attemptsLog()
	if err != nil {
		return err
	}
	return log.Update(ctx, func(recs []attemptRecord) []attemptRecord {
		var out []attemptRecord
		for _, r := range recs {
			if gone[r.Job] {
				r.Dropped = true
				out = append(out, r)
			}
		}
		return out
	})
}

// archiveAttempts writes the tries of rows to path beside the rows'
// archive, so a cleared or deleted job keeps its results there too.
func (s *Store) archiveAttempts(path string, rows []types.Job) error {
	all, err := s.Attempts()
	if err != nil {
		return err
	}
	named := map[string]bool{}
	for _, r := range rows {
		named[r.ID] = true
	}
	var lines [][]byte
	for _, a := range all {
		if !named[a.Job] {
			continue
		}
		line, err := json.Marshal(recordAttempt(a))
		if err != nil {
			return err
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return nil
	}
	return file.RewriteLines(path, lines, 0o600)
}

// storedTries is what an older jobs.jsonl row kept of its tries before
// attempts.jsonl held them.
type storedTries struct {
	ID       string           `json:"id"`
	Attempts int              `json:"attempts"`
	Result   *types.JobResult `json:"result"`
	Prior    []struct {
		types.AttemptStatus
		N               int      `json:"n"`
		UnresolvedRisks []string `json:"unresolved_risks"`
	} `json:"prior"`
}

// attempts is t as the records that replace it. A prior try's risks
// were all it kept of its result, so they become that result.
func (t storedTries) attempts() []types.Attempt {
	var out []types.Attempt
	for _, p := range t.Prior {
		a := types.Attempt{Job: t.ID, N: p.N, Status: p.AttemptStatus}
		if a.Status.Ended != 0 {
			a.Status.End = types.AttemptEndJob
		}
		if len(p.UnresolvedRisks) > 0 {
			a.Status.Result = &types.JobResult{Job: t.ID, UnresolvedRisks: p.UnresolvedRisks}
		}
		out = append(out, a)
	}
	if t.Result != nil {
		out = append(out, types.Attempt{Job: t.ID, N: max(t.Attempts, 1), Status: types.AttemptStatus{Result: t.Result}})
	}
	return out
}

// moveTries writes the tries the rows of jobs.jsonl still carry to
// attempts.jsonl, read from each row's last line. A run cut short runs
// again and appends the same lines, which the log reads once.
func (s *Store) moveTries(ctx context.Context) error {
	read, err := file.ReadLines(s.path)
	if err != nil {
		return err
	}
	byID := map[string]storedTries{}
	var order []string
	for _, line := range read.Lines {
		var t storedTries
		if err := json.Unmarshal(line.Text, &t); err != nil {
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
	var moved []types.Attempt
	for _, id := range order {
		moved = append(moved, byID[id].attempts()...)
	}
	return s.appendAttempts(ctx, moved...)
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
