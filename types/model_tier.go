package types

import (
	"context"

	"github.com/egladman/magus/types/agent"
)

// JobKind is what a job's work is judged by, derived from its row.
type JobKind string

// A job is judged by a target check, symbol goals, path goals, a script check, or,
// with none of those, by reading its prose.
const (
	JobKindCheck  JobKind = "check"
	JobKindSymbol JobKind = "symbol"
	JobKindPaths  JobKind = "paths"
	JobKindScript JobKind = "script"
	JobKindProse  JobKind = "prose"
)

// ModelTierMatrix chooses the model tier of one job from its typed request.
// magus\modelTier registers it. A nil answer leaves the default to decide.
// The job store is read-only while it runs, and it runs once, at the fork.
//
// An error means the matrix itself failed (it raised, or returned a choice the
// declaration cannot use), never that it declined: declining is a nil answer.
type ModelTierMatrix func(ctx context.Context, req ModelTierRequest) (*agent.JobModelTier, error)

// ModelTierRequest is what a matrix function reads to choose a job's tier: the row's
// typed declaration plus the workspace's recorded outcomes. ParentRank and DeclaredRank
// are nil when the parent or the row carries no tier.
type ModelTierRequest struct {
	ID, Parent, Instructions             string
	ReadOnly                             bool
	Check                                *LeaseCheck
	Goals                                []Goal
	Kind                                 JobKind
	WritePaths, WriteProjects, ReadPaths []string
	DependsOn                            []string
	WriteFiles                           int
	ParentRank, DeclaredRank             *int
	Model                                agent.Model
	History                              []ModelTierRecord
}

// ModelTierRecord is the recorded outcome of jobs at one tier and job kind. PassLower is
// the Wilson lower bound of the pass rate; the per-job figures are means. FirstAttempts
// counts the endings of a job's first attempt and FirstPass those that passed, with
// FirstPassLower their Wilson lower bound; NotRetryable counts the endings classified as
// needing a change before another attempt could help. MeanCostUSD and MedianTokens are nil,
// unknown, unless every counted outcome carries the figure.
type ModelTierRecord struct {
	Rank            int      `json:"rank"              yaml:"rank"`
	ModelTier       string   `json:"model_tier"        yaml:"model_tier"`
	Kind            JobKind  `json:"kind"              yaml:"kind"`
	Jobs            int      `json:"jobs"              yaml:"jobs"`
	Pass            int      `json:"pass"              yaml:"pass"`
	Fail            int      `json:"fail"              yaml:"fail"`
	NoReturn        int      `json:"no_return"         yaml:"no_return"`
	PassLower       float64  `json:"pass_lower"        yaml:"pass_lower"`
	ChecksPerJob    float64  `json:"checks_per_job"    yaml:"checks_per_job"`
	ForksPerID      float64  `json:"forks_per_id"      yaml:"forks_per_id"`
	ReworkPerJob    float64  `json:"rework_per_job"    yaml:"rework_per_job"`
	WideningsPerJob float64  `json:"widenings_per_job" yaml:"widenings_per_job"`
	MeanCostUSD     *float64 `json:"mean_cost_usd"     yaml:"mean_cost_usd"`
	MedianTokens    *int64   `json:"median_tokens"     yaml:"median_tokens"`
	FirstAttempts   int      `json:"first_attempts"    yaml:"first_attempts"`
	FirstPass       int      `json:"first_pass"        yaml:"first_pass"`
	FirstPassLower  float64  `json:"first_pass_lower"  yaml:"first_pass_lower"`
	NotRetryable    int      `json:"not_retryable"     yaml:"not_retryable"`
}

// JobOutcome is one job's terminal record, kept after the row expires. Model is the id
// the host reported; Site is the file:line of the declaration or matrix that chose the
// tier; Ended is Unix milliseconds. Attempt numbers the attempt that ended, from 1, and
// Retry is what must change before another attempt could help, empty when repeating may.
// Tokens and CostUSD are the context the holder's calls read and its cache-read price, a
// floor; nil when a call reported no context size or the price would be a guess, since an
// unreported figure is unknown, not zero.
//
// Parent and ParentAttempt name the attempt of the parent the job was forked under, so
// siblings of one fan-out group together and a re-forked parent's children do not join
// them. Projects are the projects the write paths reach, nil when the job had none or
// they could not be resolved. Unattributed counts writes into the job's paths from
// outside it. Work is nil when the holder's trail could not be read back to the attempt's
// start or holds no call of it: unknown, never an idle holder.
type JobOutcome struct {
	Job          string                `json:"job"                     yaml:"job"`
	ModelTier    string                `json:"model_tier,omitempty"    yaml:"model_tier,omitempty"`
	Kind         JobKind               `json:"kind"                    yaml:"kind"`
	Skills       agent.SkillVariant    `json:"skills,omitempty"        yaml:"skills,omitempty"`
	Model        string                `json:"model,omitempty"         yaml:"model,omitempty"`
	Source       agent.ModelTierSource `json:"source,omitempty"        yaml:"source,omitempty"`
	Reason       string                `json:"reason,omitempty"        yaml:"reason,omitempty"`
	Site         string                `json:"site,omitempty"          yaml:"site,omitempty"`
	WriteFiles   int                   `json:"write_files"             yaml:"write_files"`
	State        JobState              `json:"state"                   yaml:"state"`
	ChecksRun    int                   `json:"checks_run"              yaml:"checks_run"`
	Tokens       *int64                `json:"usage_tokens,omitzero"   yaml:"usage_tokens,omitempty"`
	CostUSD      *float64              `json:"usage_cost_usd,omitzero" yaml:"usage_cost_usd,omitempty"`
	DurationMs   int64                 `json:"duration_ms"             yaml:"duration_ms"`
	Ended        int64                 `json:"ended"                   yaml:"ended"`
	ReworkRounds int                   `json:"rework_rounds,omitempty" yaml:"rework_rounds,omitempty"`
	Widenings    int                   `json:"widenings,omitempty"     yaml:"widenings,omitempty"`
	Attempt      int                   `json:"attempt,omitzero"        yaml:"attempt,omitempty"`
	Retry        RetryChange           `json:"retry,omitempty"         yaml:"retry,omitempty"`
	Parent       string                `json:"parent,omitempty"        yaml:"parent,omitempty"`
	// ParentAttempt is 0 when the parent row was gone by the time the job ended.
	ParentAttempt int      `json:"parent_attempt,omitzero" yaml:"parent_attempt,omitempty"`
	Projects      []string `json:"projects,omitempty"      yaml:"projects,omitempty"`
	Unattributed  int      `json:"unattributed,omitzero"   yaml:"unattributed,omitempty"`
	Work          *JobWork `json:"work,omitzero"           yaml:"work,omitempty"`
}
