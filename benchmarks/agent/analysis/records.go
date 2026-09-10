// Package analysis turns an agent benchmark results tree into a report in
// three stages: Extract (runs to metrics), Analyze (metrics to statistics)
// and Render (statistics to markdown). Each stage only reads artifacts, so a
// scored run is analyzed as often as wanted without re-running an agent. The
// record types here are the file formats: metrics.jsonl carries one ScoredRun
// or ControlRun per line, analysis.json one Analysis.
package analysis

import "github.com/egladman/magus/benchmarks/agent/analysis/pycompat"

// Number is a JSON number that remembers whether it was an int or a float.
type Number = pycompat.Number

// The two recipes the paired comparison names. A record's arm stays a plain
// string: the runner accepts arms outside this pair (_selftest), and cells
// and pass rates cover whatever ran.
const (
	ArmFull    = "full"
	ArmRampant = "rampant"
)

// Control kinds a meta.json may carry.
const (
	ControlGolden = "golden"
	ControlNull   = "null"
)

// Verdicts a paired delta can earn.
const (
	VerdictLower        = "lower under full"
	VerdictHigher       = "higher under full"
	VerdictInconclusive = "inconclusive"
)

// TokenCounts are the billed tokens of one run. TotalBilled is a token count,
// not a price.
type TokenCounts struct {
	Input       int64 `json:"input"`
	Output      int64 `json:"output"`
	CacheRead   int64 `json:"cache_read"`
	CacheWrite  int64 `json:"cache_write"`
	TotalBilled int64 `json:"total_billed"`
}

// GuardEvents is guard activity read from the magus trail. A run carries nil
// instead when no trail was captured; zero means the guard was present and
// silent, and the two must never be conflated.
type GuardEvents struct {
	Denials    int64 `json:"denials"`
	Advisories int64 `json:"advisories"`
	SkillLoads int64 `json:"skill_loads"`
}

// InvariantViolations lists what a run's diff did that no task allows.
type InvariantViolations struct {
	TestsDeleted []string `json:"tests_deleted"`
}

// ControlRun is a golden or null control: identity, kind and check verdict,
// no agent.
type ControlRun struct {
	RunID      string  `json:"run_id"`
	Arm        string  `json:"arm"`
	Task       string  `json:"task"`
	Rep        int64   `json:"rep"`
	Model      string  `json:"model"`
	Control    string  `json:"control"`
	ExitReason *string `json:"exit_reason"`
	CheckExit  *int64  `json:"check_exit"`
	Success    *bool   `json:"success"`
}

// ScoredRun is one agent run measured from its artifacts. Success is nil when
// no check ran and GuardEvents is nil when no trail was captured; neither is a
// zero. Dollars is the host's billed cost when the transcript recorded one and
// the priced table total otherwise. Control is always nil: every row carries
// the key so a reader tells the two record kinds apart by it.
type ScoredRun struct {
	RunID                string              `json:"run_id"`
	Arm                  string              `json:"arm"`
	Task                 string              `json:"task"`
	Rep                  int64               `json:"rep"`
	Model                string              `json:"model"`
	Tokens               TokenCounts         `json:"tokens"`
	Dollars              float64             `json:"dollars"`
	TableDollarsUSD      float64             `json:"table_dollars_usd"`
	ReportedCostUSD      *float64            `json:"reported_cost_usd"`
	CacheWriteTTLAssumed bool                `json:"cache_write_ttl_assumed"`
	Turns                int64               `json:"turns"`
	ToolCalls            int64               `json:"tool_calls"`
	ToolCallsByName      map[string]int64    `json:"tool_calls_by_name"`
	FileReads            int64               `json:"file_reads"`
	DistinctFilesRead    int64               `json:"distinct_files_read"`
	ReReadRate           float64             `json:"re_read_rate"`
	ToolResultBytes      int64               `json:"tool_result_bytes"`
	GuardEvents          *GuardEvents        `json:"guard_events"`
	CheckExit            *int64              `json:"check_exit"`
	Success              *bool               `json:"success"`
	InvariantViolations  InvariantViolations `json:"invariant_violations"`
	WallMs               *Number             `json:"wall_ms"`
	TimeToFirstEditMs    *Number             `json:"time_to_first_edit_ms"`
	TimeToDoneMs         *Number             `json:"time_to_done_ms"`
	Effort               *string             `json:"effort"`
	MaxTurns             *Number             `json:"max_turns"`
	BudgetUSD            *Number             `json:"budget_usd"`
	MagusBinary          *string             `json:"magus_binary"`
	MagusVersion         *string             `json:"magus_version"`
	FixtureSha           *string             `json:"fixture_sha"`
	Started              *string             `json:"started"`
	Ended                *string             `json:"ended"`
	ExitReason           *string             `json:"exit_reason"`
	Control              *string             `json:"control"`
}

// RunRecord is one line of metrics.jsonl: exactly one of the two is set.
type RunRecord struct {
	Scored  *ScoredRun
	Control *ControlRun
}

// Spread is median and IQR beside the mean; a mean alone hides the spread
// that matters. Median and the IQR bounds keep the int form of a count metric
// when they fall on a value rather than between two.
type Spread struct {
	N      int64      `json:"n"`
	Median *Number    `json:"median"`
	IQR    [2]*Number `json:"iqr"`
	Mean   *float64   `json:"mean"`
}

// CellStats are the pass rates and metric spreads of one (arm, task) cell.
type CellStats struct {
	N                  int64             `json:"n"`
	Successes          int64             `json:"successes"`
	UnknownOutcomes    int64             `json:"unknown_outcomes"`
	PassAt1            *float64          `json:"pass_at_1"`
	PassAt1CI          [2]*float64       `json:"pass_at_1_ci"`
	K                  int64             `json:"k"`
	PassPowK           float64           `json:"pass_pow_k"`
	Metrics            map[string]Spread `json:"metrics"`
	MetricsSuccessOnly map[string]Spread `json:"metrics_success_only"`
}

// CostOfPass is expected dollars per correct solution; infinite at a zero
// pass rate.
type CostOfPass struct {
	Value    *float64 `json:"value"`
	Infinite bool     `json:"infinite"`
}

// ArmSummary is one arm's pass rate and cost-of-pass over every task.
type ArmSummary struct {
	N             int64       `json:"n"`
	Successes     int64       `json:"successes"`
	PassRate      float64     `json:"pass_rate"`
	PassRateCI    [2]*float64 `json:"pass_rate_ci"`
	Dollars       Spread      `json:"dollars"`
	CostOfPassUSD CostOfPass  `json:"cost_of_pass_usd"`
}

// PairedDelta is treatment minus baseline for one metric on one task, over
// matched reps.
type PairedDelta struct {
	NPairs         int64    `json:"n_pairs"`
	DeltaMean      *float64 `json:"delta_mean"`
	DeltaMedian    *Number  `json:"delta_median"`
	CILow          *float64 `json:"ci_low"`
	CIHigh         *float64 `json:"ci_high"`
	P              *float64 `json:"p"`
	BaselineMedian *Number  `json:"baseline_median"`
	Relative       *float64 `json:"relative"`
	CIExcludesZero bool     `json:"ci_excludes_zero"`
	Verdict        string   `json:"verdict"`
	PHolm          *float64 `json:"p_holm"`
}

// ControlCount is how many controls of one kind a task ran and how many passed.
type ControlCount struct {
	N      int64    `json:"n"`
	Passes int64    `json:"passes"`
	RunIDs []string `json:"run_ids"`
}

// ControlCell says whether one task's check discriminates: golden must pass,
// null must fail.
type ControlCell struct {
	Golden        ControlCount `json:"golden"`
	Null          ControlCount `json:"null"`
	Discriminates bool         `json:"discriminates"`
}

// UnpairedRep is a rep one arm ran and another did not.
type UnpairedRep struct {
	Task        string   `json:"task"`
	Rep         int64    `json:"rep"`
	MissingArms []string `json:"missing_arms"`
}

// DataQuality holds the facts the report's caveats are generated from, not
// prose about them.
type DataQuality struct {
	TableToBilledRatioMedian    *float64      `json:"table_to_billed_ratio_median"`
	RunsWithoutBilledCost       []string      `json:"runs_without_billed_cost"`
	RunsWithoutGuardEvents      []string      `json:"runs_without_guard_events"`
	RunsWithoutCheck            []string      `json:"runs_without_check"`
	RunsWithAssumedCacheTTL     []string      `json:"runs_with_assumed_cache_ttl"`
	RunsWithInvariantViolations []string      `json:"runs_with_invariant_violations"`
	IncompletePairs             []UnpairedRep `json:"incomplete_pairs"`
}

// Analysis is analysis.json. Cells are keyed arm/task; Paired is keyed
// metric, then task.
type Analysis struct {
	Seed             int64                             `json:"seed"`
	BootstrapIters   int64                             `json:"bootstrap_iters"`
	MinRelativeDelta float64                           `json:"min_relative_delta"`
	Runs             int64                             `json:"runs"`
	Arms             []string                          `json:"arms"`
	Tasks            []string                          `json:"tasks"`
	Models           []string                          `json:"models"`
	Controls         map[string]ControlCell            `json:"controls"`
	Cells            map[string]CellStats              `json:"cells"`
	ArmSummary       map[string]ArmSummary             `json:"arm_summary"`
	Paired           map[string]map[string]PairedDelta `json:"paired"`
	DataQuality      DataQuality                       `json:"data_quality"`
}
