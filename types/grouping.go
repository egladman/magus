package types

// JobWork is what the guard observed a job's holder do in the attempt that ended, folded
// from the activity trail when the job ended, before the trail rotates it away. A call is
// one agent command recorded under the job's lease; a write is one of class file.write.
//
// The token figures are context sizes the host reported beside each call. ContextTokens
// and PeakTokens are nil unless every call carried one, since a sum over some calls would
// read as the job's, and SetupContext unless every call before the first write carried
// one. The Setup figures are nil when the holder never wrote, and SetupTokens also when
// the host reported no size for that first write.
type JobWork struct {
	// Calls counts every observed call and Writes the write-class ones among them.
	Calls  int `json:"calls"  yaml:"calls"`
	Writes int `json:"writes" yaml:"writes"`
	// SetupCalls are the calls before the first write, and SetupContext the context they
	// carried: what the holder spent loading before it changed anything.
	SetupCalls   int    `json:"setup_calls"             yaml:"setup_calls"`
	SetupContext *int64 `json:"setup_context,omitempty" yaml:"setup_context,omitempty"`
	// SetupMs is from the attempt's start to the first write; SetupTokens is the holder's
	// context size at that write.
	SetupMs       *int64 `json:"setup_ms,omitempty"       yaml:"setup_ms,omitempty"`
	SetupTokens   *int64 `json:"setup_tokens,omitempty"   yaml:"setup_tokens,omitempty"`
	ContextTokens *int64 `json:"context_tokens,omitempty" yaml:"context_tokens,omitempty"`
	PeakTokens    *int64 `json:"peak_tokens,omitempty"    yaml:"peak_tokens,omitempty"`
	// Denials counts the guard's denials of the holder in the attempt.
	Denials int `json:"denials" yaml:"denials"`
}

// GroupingShape is how one recorded job's work was grouped across workers.
type GroupingShape string

const (
	// GroupingChained is one job, with no recorded sibling, whose write paths reach two or
	// more projects: one worker carried the work across them.
	GroupingChained GroupingShape = "chained"
	// GroupingSplit is two or more jobs forked under one attempt of one parent: the work
	// was split across workers. The group, not the job, is the unit compared.
	GroupingSplit GroupingShape = "split"
	// GroupingSolo is one job, with no recorded sibling, whose write paths reach at most
	// one project.
	GroupingSolo GroupingShape = "solo"
)

// GroupingVerdict says whether a grouping row has the sample to report figures.
type GroupingVerdict string

const (
	// GroupingMeasured is a row with at least the minimum sample of groups, most of whose
	// jobs carry a work profile.
	GroupingMeasured GroupingVerdict = "measured"
	// GroupingUnknown is a row below that sample. Its figures are nil, not zero: too few
	// groups is no evidence about the shape either way.
	GroupingUnknown GroupingVerdict = "unknown"
)

// GroupingMinSample is the fewest groups of a shape the grouping lens reports figures for.
const GroupingMinSample = 5

// GroupingDefinition is the grouping lens's contract, stated where a reader meets it.
const GroupingDefinition = "Grouping reads job history, not git: each recorded job is chained (one worker " +
	"whose write paths reach two or more projects), split (two or more jobs forked under one attempt " +
	"of one parent) or solo. Per shape it sets the context a worker loaded before its first write and " +
	"the context, wall time, denials and unattributed writes per group beside the pass rate. It is " +
	"observational: jobs were not assigned to a shape at random, and bigger work is split more often, " +
	"so it reports what each shape cost here and never which to choose; a person writes that rule. " +
	"A shape with fewer than the minimum sample of groups, or with most trails rotated away, is unknown."

// GroupingRecord is the recorded outcome of one grouping shape. Jobs and Groups count what
// was classified as the shape (a chained or solo job is its own group), and Known the jobs
// carrying a work profile. Every other figure is nil while Verdict is unknown, and a
// figure only some jobs carry is taken over the jobs that do.
type GroupingRecord struct {
	Shape   GroupingShape   `json:"shape"   yaml:"shape"`
	Verdict GroupingVerdict `json:"verdict" yaml:"verdict"`
	Jobs    int             `json:"jobs"    yaml:"jobs"`
	Groups  int             `json:"groups"  yaml:"groups"`
	Known   int             `json:"known"   yaml:"known"`
	// PassLower is the Wilson lower bound of the jobs' pass rate.
	PassLower *float64 `json:"pass_lower" yaml:"pass_lower"`
	// SetupTokensMedian is the median context size at a job's first write, and SetupShare
	// the share of a job's context spent before it, both over the jobs that report them.
	SetupTokensMedian *int64   `json:"setup_tokens_median" yaml:"setup_tokens_median"`
	SetupShare        *float64 `json:"setup_share"         yaml:"setup_share"`
	// ContextPerGroup is the mean context a group's calls carried, over the groups every
	// job of which reports it; WallMsPerGroup is the mean span from a group's first start to
	// its last end.
	ContextPerGroup *float64 `json:"context_per_group"  yaml:"context_per_group"`
	WallMsPerGroup  *float64 `json:"wall_ms_per_group"  yaml:"wall_ms_per_group"`
	DenialsPerJob   *float64 `json:"denials_per_job"    yaml:"denials_per_job"`
	// UnattributedPerJob counts writes into a job's paths from outside it, the collisions
	// the store saw.
	UnattributedPerJob *float64 `json:"unattributed_per_job" yaml:"unattributed_per_job"`
	ReworkPerJob       *float64 `json:"rework_per_job"       yaml:"rework_per_job"`
	WideningsPerJob    *float64 `json:"widenings_per_job"    yaml:"widenings_per_job"`
}

// GroupingOutput is the grouping lens: one row per shape the recorded job outcomes cover,
// in chained, split, solo order. Unclassified counts the jobs with write paths whose
// projects could not be resolved when they ended, which no shape counts.
type GroupingOutput struct {
	Definition   string           `json:"definition"   yaml:"definition"`
	MinSample    int              `json:"min_sample"   yaml:"min_sample"`
	Unclassified int              `json:"unclassified" yaml:"unclassified"`
	Rows         []GroupingRecord `json:"rows"         yaml:"rows"`
}
