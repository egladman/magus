package bench

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"maps"
	"math"
	"os"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/embed/rankeval"
)

// randomizationSamples makes the sign-flip test exact up to 16 tasks, where
// 2^16 patterns fit; past that its Monte Carlo error at p = 0.05 is about 0.0007.
const randomizationSamples = 100_000

const (
	alpha = 0.05
	power = 0.8
)

// minRelativeDelta is the relative size a comparison must reach before it earns
// a verdict, on top of its Holm-adjusted p clearing alpha. A significant 2
// percent difference is not a finding.
const minRelativeDelta = 0.10

// passAt1Metric names the pass@1 comparison, which reads a per-task pass rate
// rather than one value per run.
const passAt1Metric = "pass_at_1"

// metric is one per-run value every cell describes and every comparison pairs
// by task. A headline metric gets a row in the report's comparison table; a
// family metric is one of the claims Holm corrects across, and the only kind
// that earns a verdict.
type metric struct {
	name     string
	label    string
	digits   int
	headline bool
	family   bool
	read     func(*ScoredRun) *float64
}

func intMetric(read func(*ScoredRun) int64) func(*ScoredRun) *float64 {
	return func(r *ScoredRun) *float64 {
		n := float64(read(r))
		return &n
	}
}

func floatMetric(read func(*ScoredRun) float64) func(*ScoredRun) *float64 {
	return func(r *ScoredRun) *float64 {
		n := read(r)
		return &n
	}
}

var metrics = []metric{
	{name: "total_billed_tokens", label: "total billed tokens", digits: 1, headline: true, family: true,
		read: intMetric(func(r *ScoredRun) int64 { return r.Tokens.TotalBilled })},
	{name: "input_tokens", read: intMetric(func(r *ScoredRun) int64 { return r.Tokens.Input })},
	{name: "output_tokens", read: intMetric(func(r *ScoredRun) int64 { return r.Tokens.Output })},
	{name: "cache_read_tokens", read: intMetric(func(r *ScoredRun) int64 { return r.Tokens.CacheRead })},
	{name: "cache_write_tokens", read: intMetric(func(r *ScoredRun) int64 { return r.Tokens.CacheWrite })},
	{name: "dollars", label: "dollars", digits: 4, headline: true, family: true,
		read: floatMetric(func(r *ScoredRun) float64 { return r.Dollars })},
	{name: "wall_ms", label: "wall clock (ms)", digits: 1, headline: true, family: true,
		read: func(r *ScoredRun) *float64 { return r.WallMs }},
	{name: "time_to_first_edit_ms", read: func(r *ScoredRun) *float64 { return r.TimeToFirstEditMs }},
	{name: "time_to_done_ms", read: func(r *ScoredRun) *float64 { return r.TimeToDoneMs }},
	{name: "turns", label: "turns", digits: 1, headline: true,
		read: intMetric(func(r *ScoredRun) int64 { return r.Turns })},
	{name: "tool_calls", label: "tool calls", digits: 1, headline: true,
		read: intMetric(func(r *ScoredRun) int64 { return r.ToolCalls })},
	{name: "file_reads", label: "file reads", digits: 1, headline: true,
		read: intMetric(func(r *ScoredRun) int64 { return r.FileReads })},
	{name: "re_read_rate", label: "re-read rate", digits: 4, headline: true,
		read: floatMetric(func(r *ScoredRun) float64 { return r.ReReadRate })},
	{name: "tool_result_bytes", label: "tool result bytes", digits: 1, headline: true,
		read: intMetric(func(r *ScoredRun) int64 { return r.ToolResultBytes })},
}

// passAt1Row is pass@1's place among the comparisons: first, and in the family.
var passAt1Row = metric{name: passAt1Metric, label: "pass@1", digits: 3, headline: true, family: true}

// comparedMetrics is every comparison in report order.
func comparedMetrics() []metric { return append([]metric{passAt1Row}, metrics...) }

// wilsonInterval is the cell's Wilson score interval; both bounds are nil when
// nothing was graded.
func wilsonInterval(successes, graded int) [2]*float64 {
	if graded <= 0 {
		return [2]*float64{}
	}
	low, high := rankeval.WilsonInterval(successes, graded)
	return [2]*float64{&low, &high}
}

// passAtK is the unbiased pass@k of c passes in n graded runs,
// 1 - C(n-c, k)/C(n, k), as the product of (n-c-i)/(n-i) over i < k so no
// binomial is formed.
func passAtK(n, c, k int) float64 {
	miss := 1.0
	for i := range k {
		miss *= float64(n-c-i) / float64(n-i)
		if miss <= 0 {
			return 1
		}
	}
	return 1 - miss
}

// passPowK is tau-bench's pass^k, C(c, k)/C(n, k): the chance k runs drawn
// without replacement all pass. It is not pass@1^k unless every task's rate is
// the same.
func passPowK(n, c, k int) float64 {
	all := 1.0
	for i := range k {
		all *= float64(c-i) / float64(n-i)
		if all <= 0 {
			return 0
		}
	}
	return all
}

// median is statistics.median over a sorted list: the middle value as it is,
// or the true-division mean of the middle two.
func median(sorted []float64) float64 {
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// fmean is the arithmetic mean: one pass, then one division.
func fmean(values []float64) float64 {
	var total float64
	for _, v := range values {
		total += v
	}
	return total / float64(len(values))
}

// sampleSD is the n-1 standard deviation, in two passes so a large mean does not
// cancel the variance.
func sampleSD(values []float64) float64 {
	mean := fmean(values)
	var ss float64
	for _, v := range values {
		ss += (v - mean) * (v - mean)
	}
	return math.Sqrt(ss / float64(len(values)-1))
}

// quartiles is statistics.quantiles(n=4, method="inclusive") over a sorted
// list of at least two values: linear interpolation between neighbors,
// which always yields floats.
func quartiles(sorted []float64) [2]*float64 {
	m := int64(len(sorted) - 1)
	var out [2]*float64
	for k, i := range []int64{1, 3} {
		j, delta := (i*m)/4, (i*m)%4
		// `a*b + c*d`, which the compiler may contract into an FMA: arm64 does,
		// amd64 does not, so this quartile's last bits depend on the machine
		// (MEASURED 2026-09-20: 0.217875 against 0.21787500000000004). That is
		// accepted rather than fought, which is why Analysis records Platform
		// and every report names it.
		q := (sorted[j]*float64(4-delta) + sorted[j+1]*float64(delta)) / 4
		out[k] = &q
	}
	return out
}

// describe is the Spread of values, nil values dropped first.
func describe(values []*float64) Spread {
	var clean []float64
	for _, v := range values {
		if v != nil {
			clean = append(clean, *v)
		}
	}
	if len(clean) == 0 {
		return Spread{}
	}
	sort.Float64s(clean)
	var iqr [2]*float64
	if len(clean) == 1 {
		q := clean[0]
		iqr = [2]*float64{&q, &q}
	} else {
		iqr = quartiles(clean)
	}
	med := median(clean)
	mean := fmean(clean)
	return Spread{N: int64(len(clean)), Median: &med, IQR: iqr, Mean: &mean}
}

// LoadRecords reads metrics.jsonl; a line is a control when its control key
// is set, a scored run otherwise, and each must carry every field the
// extractor writes for its kind.
func LoadRecords(file string) ([]RunRecord, error) {
	fh, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	var records []RunRecord
	number := 0
	err = jsonlLines(fh, func(line []byte) error {
		number++
		if len(bytes.TrimSpace(line)) == 0 {
			return nil
		}
		record, err := runFromJSON(line)
		if err != nil {
			return fmt.Errorf("%s line %d: not a metrics record: %w", file, number, err)
		}
		records = append(records, record)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("%s is empty", file)
	}
	return records, nil
}

// RecordsJSONL is metrics.jsonl for records, one record per line, the inverse
// of LoadRecords so the file is assembled in one place.
func RecordsJSONL(records []RunRecord) ([]byte, error) {
	var lines []byte
	for _, record := range records {
		line, err := record.JSON()
		if err != nil {
			return nil, err
		}
		lines = append(append(lines, line...), '\n')
	}
	return lines, nil
}

// The keys a metrics row must carry are the struct's own json tags, read
// once, so a field added to the record cannot go unrequired by an oversight;
// a field tagged bench:"optional" is the exception, declared where it lives.
var (
	scoredFields  = requiredJSONFields(reflect.TypeFor[ScoredRun]())
	controlFields = requiredJSONFields(reflect.TypeFor[ControlRun]())
)

func requiredJSONFields(t reflect.Type) []string {
	names := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		field := t.Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" || field.Tag.Get("bench") == "optional" {
			continue
		}
		names = append(names, name)
	}
	return names
}

func runFromJSON(line []byte) (RunRecord, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		return RunRecord{}, err
	}
	required, isControl := scoredFields, false
	if kind, ok := raw["control"]; ok {
		var control *string
		if err := json.Unmarshal(kind, &control); err != nil {
			return RunRecord{}, err
		}
		if control != nil && *control != "" {
			required, isControl = controlFields, true
		}
	}
	for _, name := range required {
		if _, ok := raw[name]; !ok {
			return RunRecord{}, fmt.Errorf("missing %q", name)
		}
	}
	if isControl {
		var c ControlRun
		if err := json.Unmarshal(line, &c); err != nil {
			return RunRecord{}, err
		}
		if c.Control != controlGolden && c.Control != controlNull {
			return RunRecord{}, fmt.Errorf("control %q is not golden or null", c.Control)
		}
		return RunRecord{Control: &c}, nil
	}
	var s ScoredRun
	if err := json.Unmarshal(line, &s); err != nil {
		return RunRecord{}, err
	}
	return RunRecord{Scored: &s}, nil
}

func countPasses(successes []*bool) int64 {
	var n int64
	for _, s := range successes {
		if s != nil && *s {
			n++
		}
	}
	return n
}

// graded counts the runs that carry a check, and the passes among them. A run
// with no check is unknown, not failed, and stays out of every pass rate.
func graded(runs []*ScoredRun) (n, passes int) {
	for _, run := range runs {
		if run.Success == nil {
			continue
		}
		n++
		if *run.Success {
			passes++
		}
	}
	return n, passes
}

// answerScore sums the answer counts of the runs that carry one; nil when none do.
func answerScore(runs []*ScoredRun) *AnswerScore {
	var s AnswerScore
	for _, run := range runs {
		if run.Answer == nil {
			continue
		}
		s.Runs++
		s.TP += run.Answer.TP
		s.FP += run.Answer.FP
		s.FN += run.Answer.FN
	}
	if s.Runs == 0 {
		return nil
	}
	if named := s.TP + s.FP; named > 0 {
		v := float64(s.TP) / float64(named)
		s.Precision = &v
	}
	if key := s.TP + s.FN; key > 0 {
		v := float64(s.TP) / float64(key)
		s.Recall = &v
	}
	if s.Precision != nil && s.Recall != nil {
		f1 := 0.0
		if sum := *s.Precision + *s.Recall; sum > 0 {
			f1 = 2 * *s.Precision * *s.Recall / sum
		}
		s.F1 = &f1
	}
	return &s
}

// cellStats computes pass rates and metric spreads for one (arm, task) cell.
func cellStats(runs []*ScoredRun) CellStats {
	n, passes := graded(runs)
	var successful []*ScoredRun
	for _, run := range runs {
		if run.Success != nil && *run.Success {
			successful = append(successful, run)
		}
	}
	cell := CellStats{
		N: int64(len(runs)), Successes: int64(passes), UnknownOutcomes: int64(len(runs) - n),
		PassAt1CI: wilsonInterval(passes, n), PassAtK: []float64{}, PassPowK: []float64{},
		Answers: answerScore(runs), Metrics: spreads(runs), MetricsSuccessOnly: spreads(successful),
	}
	if n > 0 {
		p := float64(passes) / float64(n)
		cell.PassAt1 = &p
	}
	for k := 1; k <= n; k++ {
		cell.PassAtK = append(cell.PassAtK, passAtK(n, passes, k))
		cell.PassPowK = append(cell.PassPowK, passPowK(n, passes, k))
	}
	return cell
}

func spreads(runs []*ScoredRun) map[string]Spread {
	out := map[string]Spread{}
	for _, m := range metrics {
		values := make([]*float64, len(runs))
		for i, run := range runs {
			values[i] = m.read(run)
		}
		out[m.name] = describe(values)
	}
	return out
}

// armSummary is one arm over the tasks it graded, each task weighing the same
// whatever its rep count.
func armSummary(runs []*ScoredRun, cells map[string]CellStats) ArmSummary {
	n, passes := graded(runs)
	dollars := make([]*float64, len(runs))
	var spent []float64
	for i, run := range runs {
		d := run.Dollars
		dollars[i] = &d
		if run.Success != nil {
			spent = append(spent, d)
		}
	}
	// Summed in sorted order, so record order cannot move the last bits.
	sort.Float64s(spent)
	var gradedDollars float64
	for _, d := range spent {
		gradedDollars += d
	}
	summary := ArmSummary{
		N: int64(len(runs)), Graded: int64(n), Successes: int64(passes),
		PassAtK: []float64{}, PassPowK: []float64{},
		Answers: answerScore(runs), Dollars: describe(dollars), CostOfPassUSD: CostOfPass{Infinite: true},
	}
	if passes > 0 {
		v := gradedDollars / float64(passes)
		summary.CostOfPassUSD = CostOfPass{Value: &v}
	}
	var rates []float64
	k := math.MaxInt
	for _, key := range slices.Sorted(maps.Keys(cells)) {
		cell := cells[key]
		if cell.PassAt1 == nil {
			continue
		}
		rates = append(rates, *cell.PassAt1)
		k = min(k, len(cell.PassAtK))
	}
	summary.Tasks = int64(len(rates))
	if len(rates) == 0 {
		return summary
	}
	mean := fmean(rates)
	summary.PassAt1 = &mean
	if len(rates) >= 2 {
		_, lo, hi := rankeval.MeanInterval(rates)
		lo, hi = max(0, lo), min(1, hi)
		summary.PassAt1CI = [2]*float64{&lo, &hi}
	}
	summary.K = int64(k)
	for i := range k {
		var at, pow float64
		for _, key := range slices.Sorted(maps.Keys(cells)) {
			if cell := cells[key]; cell.PassAt1 != nil {
				at += cell.PassAtK[i]
				pow += cell.PassPowK[i]
			}
		}
		summary.PassAtK = append(summary.PassAtK, at/float64(len(rates)))
		summary.PassPowK = append(summary.PassPowK, pow/float64(len(rates)))
	}
	return summary
}

type cellKey struct{ arm, task string }

type cellRuns map[cellKey][]*ScoredRun

// taskValue is one task's value of m under arm: the mean over runs that carry
// it, or for pass@1 the pass rate over graded runs. nil when no run does.
func (c cellRuns) taskValue(arm, task string, m metric) *float64 {
	runs := c[cellKey{arm, task}]
	if m.name == passAt1Metric {
		n, passes := graded(runs)
		if n == 0 {
			return nil
		}
		v := float64(passes) / float64(n)
		return &v
	}
	var values []float64
	for _, run := range runs {
		if v := m.read(run); v != nil {
			values = append(values, *v)
		}
	}
	if len(values) == 0 {
		return nil
	}
	// Summed in sorted order, so record order cannot move the last bits.
	sort.Float64s(values)
	v := fmean(values)
	return &v
}

// metricSeed gives each comparison its own randomization stream, so one
// metric's p does not move when another is added.
func metricSeed(seed uint64, name string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name)) // hash.Hash.Write never returns an error
	return seed ^ h.Sum64()
}

// compare pairs m by task: each task's full value against its rampant value.
func compare(c cellRuns, tasks []string, m metric, seed uint64) Comparison {
	out := Comparison{Metric: m.name, Family: m.family, Deltas: []TaskDelta{}, Verdict: verdictInconclusive}
	var treated, baseline []float64
	for _, task := range tasks {
		after, before := c.taskValue(armFull, task, m), c.taskValue(armRampant, task, m)
		if after == nil || before == nil {
			continue
		}
		treated, baseline = append(treated, *after), append(baseline, *before)
		out.Deltas = append(out.Deltas, TaskDelta{Task: task, Treated: *after, Baseline: *before, Delta: *after - *before})
	}
	out.Tasks = int64(len(treated))
	if len(treated) == 0 {
		out.Reason = "no task ran under both arms"
		return out
	}
	mean, p := rankeval.PairedRandomization(treated, baseline, randomizationSamples, metricSeed(seed, m.name))
	minP := rankeval.MinimumAttainableP(len(treated))
	baseMean := fmean(baseline)
	out.DeltaMean, out.P, out.MinAttainableP, out.BaselineMean = &mean, &p, &minP, &baseMean
	if baseMean != 0 {
		rel := mean / baseMean
		out.Relative = &rel
	}
	if len(treated) >= 2 {
		_, lo, hi := rankeval.PairedTInterval(treated, baseline, 0.95)
		out.CILow, out.CIHigh = &lo, &hi
		deltas := make([]float64, len(treated))
		for i := range deltas {
			deltas[i] = treated[i] - baseline[i]
		}
		// A zero spread leaves d_z infinite and the power curve undefined; both
		// stay nil rather than print a number the data cannot support.
		if sd := sampleSD(deltas); sd > 0 {
			dz := rankeval.EffectSize(treated, baseline)
			mde := rankeval.MinimumDetectableEffect(len(treated), sd, alpha, power)
			out.EffectSize, out.MinDetectable = &dz, &mde
		}
	}
	return out
}

// judge sets a family comparison's verdict once Holm has adjusted its p, and
// says why when it stays inconclusive.
func judge(c *Comparison) {
	switch {
	case !c.Family:
		c.Verdict, c.Reason = verdictDescriptive, "outside the headline family, so Holm does not cover it"
	case c.P == nil:
	case *c.MinAttainableP > alpha:
		c.Reason = fmt.Sprintf("%d task(s): the smallest p an exact test can reach is %s, above %s",
			c.Tasks, formatP(*c.MinAttainableP), formatP(alpha))
	case *c.PHolm > alpha:
		c.Reason = fmt.Sprintf("p (Holm) %s is above %s", formatP(*c.PHolm), formatP(alpha))
	case c.Relative == nil || math.Abs(*c.Relative) < minRelativeDelta:
		c.Reason = fmt.Sprintf("the difference is under %s%% of the rampant mean",
			strconv.FormatFloat(100*minRelativeDelta, 'f', 0, 64))
	default:
		c.Verdict, c.Reason = verdictHigher, ""
		if *c.DeltaMean < 0 {
			c.Verdict = verdictLower
		}
	}
}

// formatP prints a p to three significant digits; an exact p never rounds to 0.
func formatP(p float64) string { return strconv.FormatFloat(p, 'g', 3, 64) }

// comparisons is every metric compared by task, p Holm-adjusted across the
// headline family only: adjusting across tasks or every metric would answer a
// question nobody asked.
func comparisons(c cellRuns, tasks []string, seed uint64) map[string]Comparison {
	out := map[string]Comparison{}
	var family []string
	var ps []float64
	for _, m := range comparedMetrics() {
		row := compare(c, tasks, m, seed)
		out[m.name] = row
		if m.family && row.P != nil {
			family = append(family, m.name)
			ps = append(ps, *row.P)
		}
	}
	for i, adjusted := range rankeval.Holm(ps) {
		row := out[family[i]]
		row.PHolm = &adjusted
		out[family[i]] = row
	}
	for name, row := range out {
		judge(&row)
		out[name] = row
	}
	return out
}

// comparability is every recorded field that must hold one value across the
// runs a comparison reads: the arm is the one factor it may vary.
var comparability = []struct {
	name string
	read func(*ScoredRun) string
}{
	{"model", func(r *ScoredRun) string { return r.Model }},
	{"effort", func(r *ScoredRun) string { return optional(r.Effort, strconv.Quote) }},
	{"max_turns", func(r *ScoredRun) string { return optional(r.MaxTurns, formatFloat) }},
	{"budget_usd", func(r *ScoredRun) string { return optional(r.BudgetUSD, formatFloat) }},
	{"magus_version", func(r *ScoredRun) string { return optional(r.MagusVersion, strconv.Quote) }},
	{"fixture_sha", func(r *ScoredRun) string { return optional(r.FixtureSHA, strconv.Quote) }},
}

func formatFloat(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

func optional[T any](v *T, format func(T) string) string {
	if v == nil {
		return "unrecorded"
	}
	return format(*v)
}

// refusal names the first comparability field the compared arms' runs disagree
// on, with the values seen; "" when they agree on all of them.
func refusal(runs []*ScoredRun) string {
	for _, field := range comparability {
		seen := map[string]bool{}
		for _, run := range runs {
			if run.Arm == armFull || run.Arm == armRampant {
				seen[field.read(run)] = true
			}
		}
		if len(seen) > 1 {
			return fmt.Sprintf("%s differs across the runs (%s); a comparison may vary only the arm",
				field.name, strings.Join(slices.Sorted(maps.Keys(seen)), ", "))
		}
	}
	return ""
}

func dataQuality(runs []*ScoredRun, c cellRuns, tasks, arms []string) DataQuality {
	unpaired := []UnpairedTask{} // serialized; see runIDs
	for _, task := range tasks {
		var missing []string
		for _, arm := range []string{armFull, armRampant} {
			if slices.Contains(arms, arm) && len(c[cellKey{arm, task}]) == 0 {
				missing = append(missing, arm)
			}
		}
		if len(missing) > 0 {
			unpaired = append(unpaired, UnpairedTask{Task: task, MissingArms: missing})
		}
	}
	// How far the table sits from what the host reported, over the runs that
	// carry both. This ratio is the detector, and reading it the other way round
	// is what cost the 2026-09-10 pilot its cost basis: a ratio CONSTANT across
	// a changing token mix can only come from a proportional rate vector, so it
	// is a HOST error. Only a ratio that MOVES with the mix could be a table
	// error, since a wrong rate on one category shows up as a different multiple
	// on every run.
	var ratios []float64
	for _, run := range runs {
		if run.ReportedCostUSD != nil && *run.ReportedCostUSD != 0 {
			ratios = append(ratios, run.TableDollarsUSD / *run.ReportedCostUSD)
		}
	}
	var ratioMedian *float64
	if len(ratios) > 0 {
		sort.Float64s(ratios)
		// Index n//2 is the high median, deliberately: an even count takes the
		// upper of the two middles rather than interpolating between them.
		v := ratios[len(ratios)/2]
		ratioMedian = &v
	}
	return DataQuality{
		TableToBilledRatioMedian: ratioMedian,
		RunsWithoutBilledCost: runIDs(runs, func(r *ScoredRun) bool {
			return r.ReportedCostUSD == nil || *r.ReportedCostUSD == 0
		}),
		RunsWithoutGuardEvents:      runIDs(runs, func(r *ScoredRun) bool { return r.GuardEvents == nil }),
		RunsWithoutCheck:            runIDs(runs, func(r *ScoredRun) bool { return r.Success == nil }),
		RunsWithAssumedCacheTTL:     runIDs(runs, func(r *ScoredRun) bool { return r.CacheWriteTTLAssumed }),
		RunsWithInvariantViolations: runIDs(runs, func(r *ScoredRun) bool { return len(r.InvariantViolations.TestsDeleted) > 0 }),
		UnpairedTasks:               unpaired,
	}
}

// runIDs starts from an empty slice rather than nil: these lists are serialized
// and iterated by readers of analysis.json, and the workspace codec writes a nil
// slice as null, which is a different shape to consume than an empty list.
func runIDs(runs []*ScoredRun, keep func(*ScoredRun) bool) []string {
	ids := []string{}
	for _, run := range runs {
		if keep(run) {
			ids = append(ids, run.RunID)
		}
	}
	slices.Sort(ids)
	return ids
}

// controlSummary says, per task, whether the checks discriminate: golden
// must pass, null must fail. A pass rate means nothing until this holds,
// because a check that accepts an untouched tree would grade every arm at
// 100%. A task with no control of one kind is reported as unverified rather
// than assumed.
func controlSummary(controls []*ControlRun, tasks []string) map[string]ControlCell {
	out := map[string]ControlCell{}
	for _, task := range tasks {
		golden := controlCount(controls, task, controlGolden)
		null := controlCount(controls, task, controlNull)
		goldenAllPass := golden.N > 0 && golden.Passes == golden.N
		nullAllFail := null.N > 0 && null.Passes == 0
		out[task] = ControlCell{Golden: golden, Null: null, Discriminates: goldenAllPass && nullAllFail}
	}
	return out
}

func controlCount(controls []*ControlRun, task, kind string) ControlCount {
	var successes []*bool
	ids := []string{} // serialized; see runIDs
	for _, run := range controls {
		if run.Task == task && run.Control == kind {
			successes = append(successes, run.Success)
			ids = append(ids, run.RunID)
		}
	}
	slices.Sort(ids)
	return ControlCount{N: int64(len(ids)), Passes: countPasses(successes), RunIDs: ids}
}

// Analyze turns the records into the statistics analysis.json carries. The
// seed drives every sampled randomization test, so the same records and seed
// give identical output, and record order does not matter.
func Analyze(records []RunRecord, seed uint64) (*Analysis, error) {
	var controls []*ControlRun
	var runs []*ScoredRun
	for _, record := range records {
		if record.Control != nil {
			controls = append(controls, record.Control)
		} else {
			runs = append(runs, record.Scored)
		}
	}
	if len(runs) == 0 {
		return nil, fmt.Errorf("no scored runs, only %d control(s)", len(controls))
	}
	// A rep that ran twice leaves two directories, since run ids carry a
	// timestamp, and would count twice in its cell, so the tree has to be
	// cleaned up before it is read.
	seen := map[cellKey]map[int64]string{}
	for _, run := range runs {
		key := cellKey{run.Arm, run.Task}
		if seen[key] == nil {
			seen[key] = map[int64]string{}
		}
		if other, dup := seen[key][run.Rep]; dup {
			return nil, fmt.Errorf("%s/%s rep %d ran twice (%s and %s), keep one run directory", run.Arm, run.Task, run.Rep, other, run.RunID)
		}
		seen[key][run.Rep] = run.RunID
	}
	armSet, taskSet, modelSet := map[string]bool{}, map[string]bool{}, map[string]bool{}
	c := cellRuns{}
	for _, run := range runs {
		armSet[run.Arm] = true
		taskSet[run.Task] = true
		if run.Model != "" {
			modelSet[run.Model] = true
		}
		key := cellKey{run.Arm, run.Task}
		c[key] = append(c[key], run)
	}
	arms, tasks := slices.Sorted(maps.Keys(armSet)), slices.Sorted(maps.Keys(taskSet))
	cells := map[string]CellStats{}
	armCells := map[string]map[string]CellStats{}
	for key, members := range c {
		cell := cellStats(members)
		cells[fmt.Sprintf("%s/%s", key.arm, key.task)] = cell
		if armCells[key.arm] == nil {
			armCells[key.arm] = map[string]CellStats{}
		}
		armCells[key.arm][key.task] = cell
	}
	summaries := map[string]ArmSummary{}
	for _, arm := range arms {
		var armRuns []*ScoredRun
		for _, run := range runs {
			if run.Arm == arm {
				armRuns = append(armRuns, run)
			}
		}
		summaries[arm] = armSummary(armRuns, armCells[arm])
	}
	refused := refusal(runs)
	compared := map[string]Comparison{}
	if refused == "" {
		compared = comparisons(c, tasks, seed)
	}
	return &Analysis{
		Platform:             fmt.Sprintf("%s/%s/%s", runtime.GOOS, runtime.GOARCH, runtime.Version()),
		Seed:                 seed,
		RandomizationSamples: randomizationSamples,
		Alpha:                alpha,
		Power:                power,
		MinRelativeDelta:     minRelativeDelta,
		Runs:                 int64(len(runs)),
		Arms:                 arms,
		Tasks:                tasks,
		Models:               slices.Sorted(maps.Keys(modelSet)),
		Controls:             controlSummary(controls, tasks),
		Cells:                cells,
		ArmSummaries:         summaries,
		Refusal:              refused,
		Comparisons:          compared,
		DataQuality:          dataQuality(runs, c, tasks, arms),
	}, nil
}

// JSON is analysis.json's bytes: two-space indent and a trailing newline.
func (a *Analysis) JSON() ([]byte, error) {
	raw, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// LoadAnalysis reads an analysis.json.
func LoadAnalysis(file string) (*Analysis, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var a Analysis
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("%s: not an analysis.json: %w", file, err)
	}
	return &a, nil
}
