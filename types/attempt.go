package types

import "slices"

// Attempt is one try of a Job: what the guard saw start it, how its
// end was observed, and what it left. A fork of the ended job starts
// the next try. The store keeps them in attempts.jsonl (ADR 0044).
type Attempt struct {
	// Job is the job this is a try of; N numbers its tries from 1.
	Job    string        `json:"job"    yaml:"job"`
	N      int           `json:"n"      yaml:"n"`
	Status AttemptStatus `json:"status" yaml:"status"`
}

// AttemptStatus is what magus observed of one try. Magus records what
// it saw and starts nothing.
type AttemptStatus struct {
	// Started is the unix second the guard let a spawn or continuation
	// naming the job through, zero when it saw none.
	Started int64 `json:"started,omitzero" yaml:"started,omitempty"`
	// Ended is the unix second the end was observed, and End how.
	// A started try with neither is "started, end unobserved".
	Ended int64      `json:"ended,omitzero" yaml:"ended,omitempty"`
	End   AttemptEnd `json:"end,omitempty"  yaml:"end,omitempty"`
	// Result is what the holder filed with `magus job exit`.
	Result *JobResult `json:"result,omitempty" yaml:"result,omitempty"`
	// The members below are written when a fork of the ended job starts
	// the next try. State is what the job ended as, "" until then.
	State              JobState             `json:"state,omitempty"               yaml:"state,omitempty"`
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
	Denials      []JobDenial  `json:"denials,omitempty"       yaml:"denials,omitempty"`
	DenialsKnown bool         `json:"denials_known,omitzero"  yaml:"denials_known,omitempty"`
	Retry        RetryVerdict `json:"retry,omitzero"          yaml:"retry,omitempty"`
}

// AttemptEnd is how the end of a try was observed.
type AttemptEnd string

const (
	// AttemptEndExit is `magus job exit` ending the job.
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
		r.Schema = s.Result.clone()
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

// JobAttempts are a store's attempts by job, each job's in order of N.
// A job with none reads as on its first try, nothing observed.
type JobAttempts map[string][]Attempt

// NewJobAttempts groups all by job.
func NewJobAttempts(all []Attempt) JobAttempts {
	out := JobAttempts{}
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
func (j JobAttempts) LiveN(job string) int {
	n := 1
	for _, a := range j[job] {
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
func (j JobAttempts) Live(job string) Attempt {
	n := j.LiveN(job)
	for _, a := range j[job] {
		if a.N == n {
			return a
		}
	}
	return Attempt{Job: job, N: n}
}

// Prior are job's tries a fork superseded, oldest first.
func (j JobAttempts) Prior(job string) []Attempt {
	var out []Attempt
	for _, a := range j[job] {
		if a.Superseded() {
			out = append(out, a)
		}
	}
	return out
}

// Result is what job's try in progress filed, nil for nothing.
func (j JobAttempts) Result(job string) *JobResult {
	return j.Live(job).Status.Result
}
