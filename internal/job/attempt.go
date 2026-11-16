package job

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/store/migrate"
	"github.com/egladman/magus/libs/apiversion"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
)

// attemptsFileName sits beside jobsFileName: an attempt lives and dies
// with the row it is a try of.
const attemptsFileName = "attempts.jsonl"

// attemptRecord is one line of attempts.jsonl, the persistence type of
// [job.Attempt] (ADR 0030). The last line per job and number is the
// attempt; a dropped line ends it with its job's row.
type attemptRecord struct {
	apiversion.TypeMeta
	Spec   attemptSpec         `json:"spec"`
	Status attemptStatusRecord `json:"status"`
}

// attemptSpec names the try: an attempt has no name of its own.
type attemptSpec struct {
	Job string `json:"job"`
	N   int    `json:"n"`
}

// attemptStatusRecord is [job.AttemptStatus] under camelCase keys.
// JobState is what the job ended as, never the try's lifecycle.
type attemptStatusRecord struct {
	Started            int64                `json:"started,omitzero"`
	Ended              int64                `json:"ended,omitzero"`
	End                string               `json:"end,omitempty"`
	Result             *resultRecord        `json:"result,omitempty"`
	JobState           string               `json:"jobState,omitempty"`
	ObservedGeneration int                  `json:"observedGeneration,omitzero"`
	EndReason          string               `json:"endReason,omitempty"`
	ModelTier          string               `json:"modelTier,omitempty"`
	Checkpoint         string               `json:"checkpoint,omitempty"`
	ReportedBase       string               `json:"reportedBase,omitempty"`
	ReworkRounds       int                  `json:"reworkRounds,omitzero"`
	Widenings          int                  `json:"widenings,omitzero"`
	Unmet              []verificationRecord `json:"unmet,omitempty"`
	Violations         []string             `json:"violations,omitempty"`
	FailingRefs        []string             `json:"failingRefs,omitempty"`
	Denials            []denialRecord       `json:"denials,omitempty"`
	DenialsKnown       bool                 `json:"denialsKnown,omitzero"`
	Retry              retryRecord          `json:"retry,omitzero"`
	Dropped            bool                 `json:"dropped,omitzero"`
}

type resultRecord struct {
	Job                  string                 `json:"job,omitempty"`
	ChangedPaths         []string               `json:"changedPaths"`
	Validation           resultValidationRecord `json:"validation,omitzero"`
	VerificationEvidence []evidenceRecord       `json:"verificationEvidence,omitempty"`
	Descendants          []string               `json:"descendants,omitempty"`
	UnresolvedRisks      []string               `json:"unresolvedRisks"`
	FollowUps            []string               `json:"followUps,omitempty"`
	ObservedGeneration   int                    `json:"observedGeneration,omitzero"`
}

type resultValidationRecord struct {
	Command   string `json:"command"`
	OutputRef string `json:"outputRef"`
	Owed      string `json:"owed,omitempty"`
	Unrun     string `json:"unrun,omitempty"`
}

type evidenceRecord struct {
	GateID    string `json:"gateId"`
	OutputRef string `json:"outputRef"`
	Owed      string `json:"owed,omitempty"`
	Unrun     string `json:"unrun,omitempty"`
}

type denialRecord struct {
	Rule       string `json:"rule"`
	Reason     string `json:"reason,omitempty"`
	Action     string `json:"action,omitempty"`
	VerdictRef string `json:"verdictRef,omitempty"`
	Ts         int64  `json:"ts"`
}

type retryRecord struct {
	Retryable bool                 `json:"retryable"`
	Change    string               `json:"change,omitempty"`
	Reason    string               `json:"reason,omitempty"`
	Code      types.DiagnosticCode `json:"code,omitempty"`
}

var (
	wireAttemptEnd  = newWireEnum(job.AttemptEndExit, job.AttemptEndHandback, job.AttemptEndStop, job.AttemptEndDeadline, job.AttemptEndJob)
	wireJobState    = newWireEnum(job.States()...)
	wireRetryChange = newWireEnum(job.RetryAfterConfig, job.RetryAfterWidening, job.RetryAfterPerson, job.RetryAfterUpgrade)
)

func (r attemptRecord) key() string { return fmt.Sprintf("%s\x00%d", r.Spec.Job, r.Spec.N) }

// recordAttempt is a as the line attempts.jsonl stores.
func recordAttempt(a job.Attempt) attemptRecord {
	s := a.Status
	st := attemptStatusRecord{
		Started: s.Started, Ended: s.Ended, End: wireAttemptEnd.spell(s.End), Result: recordResult(s.Result),
		JobState: wireJobState.spell(s.State), ObservedGeneration: s.ObservedGeneration, EndReason: s.EndReason,
		ModelTier: s.ModelTier, Checkpoint: s.Checkpoint, ReportedBase: s.ReportedBase,
		ReworkRounds: s.ReworkRounds, Widenings: s.Widenings, Unmet: recordVerifications(s.Unmet),
		Violations: slices.Clone(s.Violations), FailingRefs: slices.Clone(s.FailingRefs), DenialsKnown: s.DenialsKnown,
		Retry: retryRecord{Retryable: s.Retry.Retryable, Change: wireRetryChange.spell(s.Retry.Change), Reason: s.Retry.Reason, Code: types.DiagnosticCode(s.Retry.Code)},
	}
	for _, d := range s.Denials {
		st.Denials = append(st.Denials, denialRecord(d))
	}
	return attemptRecord{TypeMeta: attemptType, Spec: attemptSpec{Job: a.Job, N: a.N}, Status: st}
}

// attempt is r as the domain type, refusing a stored spelling no enum lists.
func (r attemptRecord) attempt() (job.Attempt, error) {
	st := r.Status
	var bad wireReads
	s := job.AttemptStatus{
		Started: st.Started, Ended: st.Ended, End: wireAttemptEnd.read(st.End, &bad), Result: st.Result.result(),
		State: wireJobState.read(st.JobState, &bad), ObservedGeneration: st.ObservedGeneration, EndReason: st.EndReason,
		ModelTier: st.ModelTier, Checkpoint: st.Checkpoint, ReportedBase: st.ReportedBase,
		ReworkRounds: st.ReworkRounds, Widenings: st.Widenings, Unmet: verifications(st.Unmet),
		Violations: st.Violations, FailingRefs: st.FailingRefs, DenialsKnown: st.DenialsKnown,
		Retry: job.RetryVerdict{Retryable: st.Retry.Retryable, Change: wireRetryChange.read(st.Retry.Change, &bad), Reason: st.Retry.Reason, Code: job.Code(st.Retry.Code)},
	}
	if err := bad.refusal(); err != nil {
		return job.Attempt{}, fmt.Errorf("attempt %d of job %s: %w", r.Spec.N, r.Spec.Job, err)
	}
	for _, d := range st.Denials {
		s.Denials = append(s.Denials, job.Denial(d))
	}
	return job.Attempt{Job: r.Spec.Job, N: r.Spec.N, Status: s}.Clone(), nil
}

func recordResult(r *job.Result) *resultRecord {
	if r == nil {
		return nil
	}
	out := &resultRecord{
		Job: r.Job, ChangedPaths: slices.Clone(r.ChangedPaths), Validation: resultValidationRecord(r.Validation),
		Descendants: slices.Clone(r.Descendants), UnresolvedRisks: slices.Clone(r.UnresolvedRisks),
		FollowUps: slices.Clone(r.FollowUps), ObservedGeneration: r.ObservedGeneration,
	}
	for _, e := range r.VerificationEvidence {
		out.VerificationEvidence = append(out.VerificationEvidence, evidenceRecord(e))
	}
	return out
}

// result is r as the result a holder filed. Its version was the input's,
// checked when it was filed, and the record's own stands for it here.
func (r *resultRecord) result() *job.Result {
	if r == nil {
		return nil
	}
	out := &job.Result{
		Job: r.Job, ChangedPaths: r.ChangedPaths, Validation: job.ResultValidation(r.Validation),
		Descendants: r.Descendants, UnresolvedRisks: r.UnresolvedRisks, FollowUps: r.FollowUps,
		ObservedGeneration: r.ObservedGeneration,
	}
	for _, e := range r.VerificationEvidence {
		out.VerificationEvidence = append(out.VerificationEvidence, job.VerificationEvidence(e))
	}
	return out
}

func (s *Store) attemptsLog() (codec.JSONL[attemptRecord], error) {
	if s.err != nil {
		return codec.JSONL[attemptRecord]{}, s.err
	}
	return codec.JSONL[attemptRecord]{
		Log:  slog.With(attr.Component("magus")),
		Path: filepath.Join(filepath.Dir(s.path), attemptsFileName),
		Perm: 0o600,
		Key:  attemptRecord.key,
		Live: func(r attemptRecord) bool { return !r.Status.Dropped },
	}, nil
}

// refuseAttemptVersion refuses a line another version of magus wrote,
// which this one cannot tell the meaning of.
func refuseAttemptVersion(r attemptRecord) error {
	if r.TypeMeta == attemptType {
		return nil
	}
	return types.DiagnosticErrorf(types.StoreRecordsNewer, "%s: attempt %d of %s is %s %s, and this magus reads %s %s: update magus, or %s",
		attemptsFileName, r.Spec.N, r.Spec.Job, r.APIVersion, r.Kind, attemptType.APIVersion, attemptType.Kind, staleStoreFix(attemptsFileName))
}

// envelopeAttempts rewrites each line of attempts.jsonl an older magus
// wrote as an [attemptRecord]. A line already one is kept, so a run cut
// short runs again to the same file.
func (s *Store) envelopeAttempts() error {
	log, err := s.attemptsLog()
	if err != nil {
		return err
	}
	return file.WithLock(context.Background(), log.Path+".lock", file.LockWait, func() error {
		read, err := codec.ReadLines(log.Path)
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
				return types.WrapDiagnostic(types.StoreLineUndecodable, err, "%s:%d: %v", log.Path, line.N, err)
			}
			if old.APIVersion == "" {
				lines = append(lines, line.Text)
				continue
			}
			r := recordAttempt(job.Attempt{Job: old.Job, N: old.N, Status: old.Status})
			r.Status.Dropped = old.Dropped
			text, err := codec.Marshal(r)
			if err != nil {
				return err
			}
			lines = append(lines, text)
		}
		return codec.RewriteLines(log.Path, lines, log.Perm)
	})
}

// Attempts is every try the store holds, in the order their last lines
// were written, as stored: [job.Attempt.Bound] reads one under its
// job's deadline. An absent file holds none.
func (s *Store) Attempts() ([]job.Attempt, error) {
	log, err := s.attemptsLog()
	if err != nil {
		return nil, err
	}
	recs, err := log.Load()
	if err != nil {
		return nil, fmt.Errorf("job: %w", err)
	}
	out := make([]job.Attempt, 0, len(recs))
	for _, r := range recs {
		if err := refuseAttemptVersion(r); err != nil {
			return nil, err
		}
		a, err := r.attempt()
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", attemptsFileName, err)
		}
		out = append(out, a)
	}
	return out, nil
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

// changeLiveAttempt shows change job id's try in progress under the
// attempts lock, and appends it when change reports it changed.
func (s *Store) changeLiveAttempt(ctx context.Context, id string, change func(*job.Attempt) bool) (job.Attempt, error) {
	log, err := s.attemptsLog()
	if err != nil {
		return job.Attempt{}, err
	}
	var live job.Attempt
	var failed error
	err = log.Update(ctx, func(recs []attemptRecord) []attemptRecord {
		all := make([]job.Attempt, 0, len(recs))
		for _, r := range recs {
			if failed = refuseAttemptVersion(r); failed != nil {
				return nil
			}
			var a job.Attempt
			if a, failed = r.attempt(); failed != nil {
				return nil
			}
			all = append(all, a)
		}
		live = job.NewAttempts(all).Live(id)
		if !change(&live) {
			return nil
		}
		return []attemptRecord{recordAttempt(live)}
	})
	if err == nil {
		err = failed
	}
	if err != nil {
		return job.Attempt{}, fmt.Errorf("job: %s: %w", attemptsFileName, err)
	}
	return live, nil
}

// StartAttempt records that the guard let a spawn or continuation naming
// live job id through at now. A try already running stays as it was,
// so a hook fired twice records one start; an ended one starts again,
// which is a holder sent back with more work.
func (s *Store) StartAttempt(ctx context.Context, id string, now time.Time) (job.Attempt, error) {
	rows, err := s.unswept()
	if err != nil {
		return job.Attempt{}, err
	}
	i := slices.IndexFunc(rows, func(r job.Job) bool { return r.ID == id })
	if i < 0 || !rows[i].State.Live() {
		return job.Attempt{}, fmt.Errorf("%w: %q is not a live job, so no try of it starts", ErrUnknownJob, id)
	}
	return s.changeLiveAttempt(ctx, id, func(a *job.Attempt) bool {
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
	_, err := s.changeLiveAttempt(ctx, id, func(a *job.Attempt) bool {
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
	_, err := s.changeLiveAttempt(ctx, id, func(a *job.Attempt) bool {
		a.Status.Result = result
		if a.Status.Ended == 0 {
			a.Status.Ended, a.Status.End = now, job.AttemptEndExit
		}
		return true
	})
	return err
}

// appendAttempts appends attempts as their records.
func (s *Store) appendAttempts(ctx context.Context, attempts ...job.Attempt) error {
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
			if gone[r.Spec.Job] {
				r.Status.Dropped = true
				out = append(out, r)
			}
		}
		return out
	})
}

// archiveAttempts writes the tries of rows to path beside the rows'
// archive, so a cleared or deleted job keeps its results there too.
func (s *Store) archiveAttempts(path string, rows []job.Job) error {
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
		line, err := codec.Marshal(recordAttempt(a))
		if err != nil {
			return err
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return nil
	}
	return codec.RewriteLines(path, lines, 0o600)
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
