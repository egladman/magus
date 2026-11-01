// Package bench turns an agent benchmark results tree into a report in three
// stages: Extract (runs to metrics), Analyze (metrics to statistics) and
// Report (statistics to markdown). Each stage only reads artifacts, so a
// scored run is analyzed as often as wanted without re-running an agent. The
// record types here are the file formats: metrics.jsonl carries one ScoredRun
// or controlRun per line, analysis.json one Analysis.
package bench

// The two setups the paired comparison names. A record's arm stays a plain
// string: the runner accepts arms outside this pair (_selftest), and cells
// and pass rates cover whatever ran.
const (
	armFull    = "full"
	armRampant = "rampant"
)

// Control kinds a meta.json may carry.
const (
	controlGolden = "golden"
	controlNull   = "null"
)

// Verdicts a comparison can earn. A metric outside the headline family is
// described, never judged: judging it would widen the family Holm corrects for.
const (
	verdictLower        = "lower under full"
	verdictHigher       = "higher under full"
	verdictInconclusive = "inconclusive"
	verdictDescriptive  = "descriptive"
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

// controlRun is a golden or null control: identity, kind and check verdict,
// no agent.
type controlRun struct {
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
// zero. Dollars is the token totals priced from the checked-in table, always;
// ReportedCostUSD is the host's own estimate, recorded for comparison and never
// substituted for it. Control is always nil: every row carries
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
	WallMs               *float64            `json:"wall_ms"`
	TimeToFirstEditMs    *float64            `json:"time_to_first_edit_ms"`
	TimeToDoneMs         *float64            `json:"time_to_done_ms"`
	// The meta.json passthrough: written as null when the runner recorded
	// nothing, and a row may omit the key entirely, so these are the fields a
	// reader does not require (the bench tag is what LoadRecords consults).
	Effort       *string  `json:"effort"        bench:"optional"`
	MaxTurns     *float64 `json:"max_turns"     bench:"optional"`
	BudgetUSD    *float64 `json:"budget_usd"    bench:"optional"`
	MagusBinary  *string  `json:"magus_binary"  bench:"optional"`
	MagusVersion *string  `json:"magus_version" bench:"optional"`
	FixtureSHA   *string  `json:"fixture_sha"   bench:"optional"`
	Started      *string  `json:"started"       bench:"optional"`
	Ended        *string  `json:"ended"         bench:"optional"`
	ExitReason   *string  `json:"exit_reason"   bench:"optional"`
	Control      *string  `json:"control"       bench:"optional"`
	// Answer is set only for an interrogation task, whose grader writes answer.json.
	Answer *answerCounts `json:"answer" bench:"optional"`
}

// answerCounts grade an interrogation answer against its key: TP key items
// named, FP items named that the key lacks, FN key items missed.
type answerCounts struct {
	TP int64 `json:"tp"`
	FP int64 `json:"fp"`
	FN int64 `json:"fn"`
}

// AnswerScore is precision, recall and F1 micro-averaged from the summed counts
// of Runs graded answers. Precision is nil when nothing was named, recall when
// the key is empty, F1 when either is.
type AnswerScore struct {
	Runs      int64    `json:"runs"`
	TP        int64    `json:"tp"`
	FP        int64    `json:"fp"`
	FN        int64    `json:"fn"`
	Precision *float64 `json:"precision"`
	Recall    *float64 `json:"recall"`
	F1        *float64 `json:"f1"`
}

// RunRecord is one line of metrics.jsonl: exactly one of the two is set.
type RunRecord struct {
	Scored  *ScoredRun
	Control *controlRun
}

// Spread is median and IQR beside the mean; a mean alone hides the spread
// that matters.
type Spread struct {
	N      int64       `json:"n"`
	Median *float64    `json:"median"`
	IQR    [2]*float64 `json:"iqr"`
	Mean   *float64    `json:"mean"`
}

// CellStats are the pass rates and metric spreads of one (arm, task) cell.
// PassAtK[k-1] and PassPowK[k-1] are the unbiased pass@k and pass^k estimates
// for k = 1 up to the graded run count (Chen et al. 2021; Yao et al., tau-bench).
type CellStats struct {
	N                  int64             `json:"n"`
	Successes          int64             `json:"successes"`
	UnknownOutcomes    int64             `json:"unknown_outcomes"`
	PassAt1            *float64          `json:"pass_at_1"`
	PassAt1CI          [2]*float64       `json:"pass_at_1_ci"`
	PassAtK            []float64         `json:"pass_at_k"`
	PassPowK           []float64         `json:"pass_pow_k"`
	Answers            *AnswerScore      `json:"answers"`
	Metrics            map[string]Spread `json:"metrics"`
	MetricsSuccessOnly map[string]Spread `json:"metrics_success_only"`
}

// CostOfPass is expected dollars per correct solution; infinite at a zero
// pass rate.
type CostOfPass struct {
	Value    *float64 `json:"value"`
	Infinite bool     `json:"infinite"`
}

// ArmSummary is one arm over every task it ran. The task is the unit: PassAt1 is
// the macro mean of per-task pass rates and its CI a t interval over those
// rates, never a Wilson interval pooling trials of different tasks. PassAtK and
// PassPowK average the per-task estimates at a k every task reached, k = 1..K.
// CostOfPassUSD is the dollars of every graded run over the passes among them.
type ArmSummary struct {
	N             int64        `json:"n"`
	Graded        int64        `json:"graded"`
	Successes     int64        `json:"successes"`
	Tasks         int64        `json:"tasks"`
	PassAt1       *float64     `json:"pass_at_1"`
	PassAt1CI     [2]*float64  `json:"pass_at_1_ci"`
	K             int64        `json:"k"`
	PassAtK       []float64    `json:"pass_at_k"`
	PassPowK      []float64    `json:"pass_pow_k"`
	Answers       *AnswerScore `json:"answers"`
	Dollars       Spread       `json:"dollars"`
	CostOfPassUSD CostOfPass   `json:"cost_of_pass_usd"`
}

// TaskDelta is one task's per-task means under each arm.
type TaskDelta struct {
	Task     string  `json:"task"`
	Treated  float64 `json:"treated"`
	Baseline float64 `json:"baseline"`
	Delta    float64 `json:"delta"`
}

// Comparison is full minus rampant for one metric, paired by task: each task's
// mean under one arm against its mean under the other, since reps are
// independent draws and rep i of one arm shares nothing with rep i of the other.
// P is the two-sided sign-flip p, exact up to 16 tasks; MinAttainableP is the
// floor that test has at this many tasks. CI is the paired t interval, and
// MinDetectable the difference this many tasks detects at 80% power.
type Comparison struct {
	Metric         string      `json:"metric"`
	Family         bool        `json:"family"`
	Tasks          int64       `json:"tasks"`
	Deltas         []TaskDelta `json:"deltas"`
	DeltaMean      *float64    `json:"delta_mean"`
	BaselineMean   *float64    `json:"baseline_mean"`
	Relative       *float64    `json:"relative"`
	CILow          *float64    `json:"ci_low"`
	CIHigh         *float64    `json:"ci_high"`
	P              *float64    `json:"p"`
	MinAttainableP *float64    `json:"min_attainable_p"`
	PHolm          *float64    `json:"p_holm"`
	EffectSize     *float64    `json:"effect_size_dz"`
	MinDetectable  *float64    `json:"min_detectable"`
	Verdict        string      `json:"verdict"`
	Reason         string      `json:"reason"`
}

// ControlCount is how many controls of one kind a task ran and how many passed.
type ControlCount struct {
	N      int64    `json:"n"`
	Passes int64    `json:"passes"`
	RunIDs []string `json:"run_ids"`
}

// controlCell says whether one task's check discriminates: golden must pass,
// null must fail.
type controlCell struct {
	Golden        ControlCount `json:"golden"`
	Null          ControlCount `json:"null"`
	Discriminates bool         `json:"discriminates"`
}

// unpairedTask is a task one arm ran and another did not.
type unpairedTask struct {
	Task        string   `json:"task"`
	MissingArms []string `json:"missing_arms"`
}

// DataQuality holds the facts the report's caveats are generated from, not
// prose about them.
type DataQuality struct {
	TableToBilledRatioMedian    *float64       `json:"table_to_billed_ratio_median"`
	RunsWithoutBilledCost       []string       `json:"runs_without_billed_cost"`
	RunsWithoutGuardEvents      []string       `json:"runs_without_guard_events"`
	RunsWithoutCheck            []string       `json:"runs_without_check"`
	RunsWithAssumedCacheTTL     []string       `json:"runs_with_assumed_cache_ttl"`
	RunsWithInvariantViolations []string       `json:"runs_with_invariant_violations"`
	UnpairedTasks               []unpairedTask `json:"unpaired_tasks"`
}

// Analysis is analysis.json. Cells are keyed arm/task, Comparisons by metric.
// Refusal, when set, names the field besides the arm that differs across runs;
// Comparisons is then empty, since a difference could be either factor's.
type Analysis struct {
	// Platform is the OS, architecture and toolchain the statistics were
	// computed on, as GOOS/GOARCH/go-version.
	//
	// Load-bearing, not provenance decoration: quartile interpolation is
	// `a*b + c*d`, which arm64 contracts into an FMA and amd64 does not, so two
	// machines can report the same figure to different last bits. A number is
	// only reproducible against the platform that produced it, so every report
	// names it.
	Platform             string                 `json:"platform"`
	Seed                 uint64                 `json:"seed"`
	RandomizationSamples int64                  `json:"randomization_samples"`
	Alpha                float64                `json:"alpha"`
	Power                float64                `json:"power"`
	MinRelativeDelta     float64                `json:"min_relative_delta"`
	Runs                 int64                  `json:"runs"`
	Arms                 []string               `json:"arms"`
	Tasks                []string               `json:"tasks"`
	Models               []string               `json:"models"`
	Controls             map[string]controlCell `json:"controls"`
	Cells                map[string]CellStats   `json:"cells"`
	ArmSummaries         map[string]ArmSummary  `json:"arm_summary"`
	Refusal              string                 `json:"refusal"`
	Comparisons          map[string]Comparison  `json:"comparisons"`
	DataQuality          DataQuality            `json:"data_quality"`
}
