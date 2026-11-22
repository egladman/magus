package job

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// Attempt is one try of a Job: what the guard saw start it, how its
// end was observed, and what it left. A fork of the ended job starts
// the next try. The store keeps them in attempts.jsonl (ADR 0044).
type Attempt struct {
	// Job is the job this is a try of; N numbers its tries from 1.
	Job    string        `json:"job"    yaml:"job"`
	N      int           `json:"n"      yaml:"n"`
	Status AttemptStatus `json:"status" yaml:"status"`
}

// AttemptStatus is what the store's caller observed of one try. The store
// records what it was told and starts nothing.
type AttemptStatus struct {
	// Started is the unix second the guard let a spawn or continuation
	// naming the job through, zero when it saw none.
	Started int64 `json:"started,omitzero" yaml:"started,omitempty"`
	// Ended is the unix second the end was observed, and End how.
	// A started try with neither is "started, end unobserved".
	Ended int64      `json:"ended,omitzero" yaml:"ended,omitempty"`
	End   AttemptEnd `json:"end,omitempty"  yaml:"end,omitempty"`
	// Result is what the holder filed when it exited.
	Result *Result `json:"result,omitzero" yaml:"result,omitempty"`
	// The members below are written when a fork of the ended job starts
	// the next try. State is what the job ended as, "" until then.
	State              State                `json:"state,omitempty"               yaml:"state,omitempty"`
	ObservedGeneration int                  `json:"observed_generation,omitzero"  yaml:"observed_generation,omitempty"`
	EndReason          string               `json:"end_reason,omitempty"          yaml:"end_reason,omitempty"`
	ModelTier          string               `json:"model_tier,omitempty"          yaml:"model_tier,omitempty"`
	Checkpoint         string               `json:"checkpoint,omitempty"          yaml:"checkpoint,omitempty"`
	ReportedBase       string               `json:"reported_base,omitempty"       yaml:"reported_base,omitempty"`
	ReworkRounds       int                  `json:"rework_rounds,omitzero"        yaml:"rework_rounds,omitempty"`
	Widenings          int                  `json:"widenings,omitzero"            yaml:"widenings,omitempty"`
	Unmet              []VerificationStatus `json:"unmet,omitempty"               yaml:"unmet,omitempty"`
	Violations         []string             `json:"violations,omitempty"          yaml:"violations,omitempty"`
	// FailingRefs are the output refs of the try's failed runs.
	FailingRefs []string `json:"failing_refs,omitempty" yaml:"failing_refs,omitempty"`
	// DenialsKnown is false once the trail rotated past the try, so
	// empty Denials then means unknown rather than none.
	Denials      []Denial     `json:"denials,omitempty"       yaml:"denials,omitempty"`
	DenialsKnown bool         `json:"denials_known,omitzero"  yaml:"denials_known,omitempty"`
	Retry        RetryVerdict `json:"retry,omitzero"          yaml:"retry,omitempty"`
}

// AttemptEnd is how the end of a try was observed.
type AttemptEnd string

const (
	// AttemptEndExit is the holder's own exit ending the job.
	AttemptEndExit AttemptEnd = "exit"
	// AttemptEndHandback is the worker's handback tool call.
	AttemptEndHandback AttemptEnd = "handback"
	// AttemptEndStop is the host's subagent stop event, standing.
	AttemptEndStop AttemptEnd = "stop"
	// AttemptEndDeadline is the job's deadline passing first. It is
	// read, never written: see [Attempt.Bound].
	AttemptEndDeadline AttemptEnd = "deadline"
	// AttemptEndJob is the job ending with no end of the try observed,
	// written when a fork starts the next try.
	AttemptEndJob AttemptEnd = "job"
)

// Running reports a try the guard saw start whose end nobody observed.
func (a Attempt) Running() bool {
	return a.Status.Started != 0 && a.Status.Ended == 0
}

// Superseded reports a try a fork of its ended job followed.
func (a Attempt) Superseded() bool { return a.Status.State != "" }

// Bound is a read at now under its job's deadline, zero for none: a
// try still running past it reads as ended there.
func (a Attempt) Bound(deadline, now int64) Attempt {
	if a.Running() && deadline != 0 && now >= deadline {
		a.Status.Ended, a.Status.End = deadline, AttemptEndDeadline
	}
	return a
}

// Clone returns a copy of a that shares no slice or pointer with it.
func (a Attempt) Clone() Attempt {
	s := &a.Status
	if s.Result != nil {
		r := *s.Result
		r.Schema = s.Result.Schema.clone()
		r.ChangedPaths = slices.Clone(r.ChangedPaths)
		r.Descendants = slices.Clone(r.Descendants)
		r.UnresolvedRisks = slices.Clone(r.UnresolvedRisks)
		r.FollowUps = slices.Clone(r.FollowUps)
		r.VerificationEvidence = slices.Clone(r.VerificationEvidence)
		s.Result = &r
	}
	s.Unmet = cloneVerificationStatuses(s.Unmet)
	s.Violations = slices.Clone(s.Violations)
	s.FailingRefs = slices.Clone(s.FailingRefs)
	s.Denials = slices.Clone(s.Denials)
	return a
}

// Attempts are a store's attempts by job, each job's in order of N.
// A job with none reads as on its first try, nothing observed.
type Attempts map[string][]Attempt

// NewAttempts groups all by job.
func NewAttempts(all []Attempt) Attempts {
	out := Attempts{}
	for _, a := range all {
		out[a.Job] = append(out[a.Job], a)
	}
	for _, tries := range out {
		slices.SortStableFunc(tries, func(a, b Attempt) int { return a.N - b.N })
	}
	return out
}

// LiveN numbers job's try in progress: its newest try no fork
// superseded, else one past the newest that was, else 1.
func (as Attempts) LiveN(job string) int {
	n := 1
	for _, a := range as[job] {
		if a.Superseded() {
			n = max(n, a.N+1)
		} else {
			n = max(n, a.N)
		}
	}
	return n
}

// Live is job's try in progress, with only Job and N set when the
// store holds nothing of it yet.
func (as Attempts) Live(job string) Attempt {
	n := as.LiveN(job)
	for _, a := range as[job] {
		if a.N == n {
			return a
		}
	}
	return Attempt{Job: job, N: n}
}

// Prior are job's tries a fork superseded, oldest first.
func (as Attempts) Prior(job string) []Attempt {
	var out []Attempt
	for _, a := range as[job] {
		if a.Superseded() {
			out = append(out, a)
		}
	}
	return out
}

// Result is what job's try in progress filed, nil for nothing.
func (as Attempts) Result(job string) *Result {
	return as.Live(job).Status.Result
}

// RetryChange names what must change before repeating a failed action can help.
type RetryChange string

const (
	// RetryAsIs is a failure that repeating unchanged may clear.
	RetryAsIs RetryChange = ""
	// RetryAfterConfig needs a declaration or configuration edit first.
	RetryAfterConfig RetryChange = "config"
	// RetryAfterWidening needs the job's write paths widened first.
	RetryAfterWidening RetryChange = "widening"
	// RetryAfterPerson needs a person to decide or act first.
	RetryAfterPerson RetryChange = "person"
	// RetryAfterUpgrade needs a different runner or tool version first.
	RetryAfterUpgrade RetryChange = "upgrade"
)

// RetryVerdict is a classification of whether a failed action may be repeated. Change
// and Reason are set only when Retryable is false; Code names the diagnostic whose entry
// in the caller's non-retryable table decided it.
type RetryVerdict struct {
	Retryable bool        `json:"retryable"        yaml:"retryable"`
	Change    RetryChange `json:"change,omitempty" yaml:"change,omitempty"`
	Reason    string      `json:"reason,omitempty" yaml:"reason,omitempty"`
	Code      Code        `json:"code,omitempty"   yaml:"code,omitempty"`
}

// InvalidAttemptError is an attempt [Store.RecordAttempt] refused. Nothing was written.
type InvalidAttemptError struct {
	Job string
	N   int
	// Reason says what the attempt got wrong, such as "n 3 is not the next try, 2".
	Reason string
}

func (e *InvalidAttemptError) Error() string {
	return fmt.Sprintf("job: attempt %d of %s was not recorded: %s", e.N, e.Job, e.Reason)
}

// RecordAttempt records a try of a job that the caller started and ended itself, for a
// host that runs its own attempts rather than forking the row again for each: a fork's
// fold never records such a try, so this call is the only way it is stored.
//
// The row a.Job must exist, or the error wraps [ErrUnknownJob]. a.N must be one past the
// job's newest stored try, 1 for its first. a.Status.Started must be set and no later
// than a.Status.Ended, and a.Status.End one of [AttemptEndExit], [AttemptEndHandback],
// [AttemptEndStop] or [AttemptEndJob]; [AttemptEndDeadline] is read, never written. Any
// other attempt is an [*InvalidAttemptError]. The rest of a.Status, ModelTier,
// FailingRefs and Retry among it, is stored as given.
//
// The check and the append hold the store's row and attempts locks, so two hosts
// recording the same N cannot both succeed. Like every write it needs a store opened
// with [WithActor] and [WithNotices].
func (s *Store) RecordAttempt(ctx context.Context, a Attempt) error {
	if err := s.needWriter("RecordAttempt"); err != nil {
		return err
	}
	if strings.TrimSpace(a.Job) == "" {
		return ErrNoID
	}
	if reason := attemptTimesInvalid(a.Status); reason != "" {
		return s.wrap(&InvalidAttemptError{Job: a.Job, N: a.N, Reason: reason})
	}
	log, err := s.attemptsLog()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.locked(ctx, func() error {
		ledger, err := s.readFile(ctx, false)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(ledger.rows.Jobs, func(row Job) bool { return row.ID == a.Job }) {
			return s.wrap(fmt.Errorf("%w: %q", ErrUnknownJob, a.Job))
		}
		var refused error
		err = log.Update(ctx, func(recs []attemptRecord) []attemptRecord {
			next := 1
			for _, r := range recs {
				if r.Spec.Job != a.Job {
					continue
				}
				if refused = s.refuseAttemptVersion(r); refused != nil {
					return nil
				}
				next = max(next, r.Spec.N+1)
			}
			if a.N != next {
				refused = s.wrap(&InvalidAttemptError{Job: a.Job, N: a.N, Reason: fmt.Sprintf("n %d is not the next try, %d", a.N, next)})
				return nil
			}
			return []attemptRecord{recordAttempt(a)}
		})
		if err != nil {
			return fmt.Errorf("job: %s: %w", attemptsFileName, s.wrap(err))
		}
		return refused
	})
}

// attemptTimesInvalid says what is wrong with an ended try's times and end, "" for nothing.
func attemptTimesInvalid(st AttemptStatus) string {
	switch {
	case st.Started <= 0:
		return "it has no start time"
	case st.Ended < st.Started:
		return fmt.Sprintf("it ended at %d, before it started at %d", st.Ended, st.Started)
	}
	switch st.End {
	case AttemptEndExit, AttemptEndHandback, AttemptEndStop, AttemptEndJob:
		return ""
	case AttemptEndDeadline:
		return "end deadline is read, never written"
	}
	return fmt.Sprintf("end %q is not one a try records", st.End)
}
