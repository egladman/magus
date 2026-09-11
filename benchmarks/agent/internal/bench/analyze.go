package bench

import (
	"bytes"
	"fmt"
	"maps"
	"math"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/egladman/magus/benchmarks/agent/internal/pycompat"
	"github.com/egladman/magus/internal/json"
)

// bootstrapIters is the resample count behind every paired CI.
const bootstrapIters = 10000

const z95 = 1.959963984540054

// minRelativeDelta is the relative size a paired delta must clear before it
// earns a verdict, on top of the CI excluding zero. A significant 2 percent
// difference is not a finding.
const minRelativeDelta = 0.10

// metric is one per-run value every cell describes and every pairing
// compares. A headline metric also gets its own delta table in the report,
// titled label and printed to digits; the rest stay in analysis.json rather
// than padding the report with fourteen tables nobody reads.
type metric struct {
	name     string
	label    string
	digits   int
	headline bool
	read     func(*ScoredRun) *pycompat.Number
}

func intMetric(read func(*ScoredRun) int64) func(*ScoredRun) *pycompat.Number {
	return func(r *ScoredRun) *pycompat.Number {
		n := pycompat.Int(read(r))
		return &n
	}
}

func floatMetric(read func(*ScoredRun) float64) func(*ScoredRun) *pycompat.Number {
	return func(r *ScoredRun) *pycompat.Number {
		n := pycompat.Float(read(r))
		return &n
	}
}

var metrics = []metric{
	{name: "total_billed_tokens", label: "total billed tokens", digits: 1, headline: true,
		read: intMetric(func(r *ScoredRun) int64 { return r.Tokens.TotalBilled })},
	{name: "input_tokens", read: intMetric(func(r *ScoredRun) int64 { return r.Tokens.Input })},
	{name: "output_tokens", read: intMetric(func(r *ScoredRun) int64 { return r.Tokens.Output })},
	{name: "cache_read_tokens", read: intMetric(func(r *ScoredRun) int64 { return r.Tokens.CacheRead })},
	{name: "cache_write_tokens", read: intMetric(func(r *ScoredRun) int64 { return r.Tokens.CacheWrite })},
	{name: "dollars", label: "dollars", digits: 4, headline: true,
		read: floatMetric(func(r *ScoredRun) float64 { return r.Dollars })},
	{name: "wall_ms", label: "wall clock (ms)", digits: 1, headline: true,
		read: func(r *ScoredRun) *pycompat.Number { return r.WallMs }},
	{name: "time_to_first_edit_ms", read: func(r *ScoredRun) *pycompat.Number { return r.TimeToFirstEditMs }},
	{name: "time_to_done_ms", read: func(r *ScoredRun) *pycompat.Number { return r.TimeToDoneMs }},
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

// wilsonInterval is the Wilson score interval for a binomial proportion;
// both bounds are nil when total is 0.
func wilsonInterval(successes, total int64) [2]*float64 {
	if total <= 0 {
		return [2]*float64{}
	}
	z := z95
	t := float64(total)
	p := float64(successes) / t
	denom := 1.0 + z*z/t
	center := (p + z*z/(2.0*t)) / denom
	half := z * math.Sqrt(p*(1.0-p)/t+z*z/(4.0*t*t)) / denom
	low := math.Max(0.0, center-half)
	high := math.Min(1.0, center+half)
	return [2]*float64{&low, &high}
}

// percentile takes the value at index `floor(q * (n - 1))` of an already sorted
// list, the index the Python's bootstrap used; it is not the nearest-rank
// definition, and the CIs pinned in testdata depend on this one.
func percentile(sorted []float64, q float64) *float64 {
	if len(sorted) == 0 {
		return nil
	}
	v := sorted[int(math.Floor(q*float64(len(sorted)-1)))]
	return &v
}

// bootstrapPaired bootstraps the mean of paired deltas; it returns the CI and
// a two-sided p value. The RNG is seeded from the metric and task name so
// each interval is independent of how many other cells are analyzed
// alongside it. The resampled mean accumulates left to right, as the Python
// did; a compensated sum here would move the CI bounds.
func bootstrapPaired(deltas []pycompat.Number, seedKey string) (ci [2]*float64, p *float64) {
	n := len(deltas)
	if n == 0 {
		return ci, nil
	}
	rng := pycompat.NewRandomString(seedKey)
	means := make([]float64, bootstrapIters)
	for i := range means {
		total := 0.0
		for range n {
			total += deltas[rng.RandRange(n)].Float64()
		}
		means[i] = total / float64(n)
	}
	sort.Float64s(means)
	var atOrBelow, atOrAbove int64
	for _, m := range means {
		if m <= 0.0 {
			atOrBelow++
		}
		if m >= 0.0 {
			atOrAbove++
		}
	}
	v := math.Min(1.0, 2.0*float64(min(atOrBelow, atOrAbove))/float64(bootstrapIters))
	v = math.Max(v, 1.0/float64(bootstrapIters))
	return [2]*float64{percentile(means, 0.025), percentile(means, 0.975)}, &v
}

// holm adjusts a {key: p} mapping across the family of tasks
// (Holm-Bonferroni); a nil p stays nil.
func holm(pvalues map[string]*float64) map[string]*float64 {
	type ranked struct {
		p   float64
		key string
	}
	var order []ranked
	adjusted := map[string]*float64{}
	for key, p := range pvalues {
		adjusted[key] = nil
		if p != nil {
			order = append(order, ranked{*p, key})
		}
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].p != order[j].p {
			return order[i].p < order[j].p
		}
		return order[i].key < order[j].key
	})
	m := len(order)
	running := 0.0
	for i, r := range order {
		running = math.Max(running, math.Min(1.0, float64(m-i)*r.p))
		v := running
		adjusted[r.key] = &v
	}
	return adjusted
}

func sortNumbers(values []pycompat.Number) {
	sort.SliceStable(values, func(i, j int) bool { return values[i].Less(values[j]) })
}

// median is statistics.median over a sorted list: the middle value as it is,
// or the true-division mean of the middle two.
func median(sorted []pycompat.Number) pycompat.Number {
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return pycompat.Float(sorted[n/2-1].Add(sorted[n/2]).TrueDiv(2))
}

// fmean is statistics.fmean: fsum, then one division.
func fmean(values []pycompat.Number) float64 {
	floats := make([]float64, len(values))
	for i, v := range values {
		floats[i] = v.Float64()
	}
	total, err := pycompat.FSum(floats)
	if err != nil {
		// FSum only fails on inf or nan, which parseNumber never admits.
		panic(err)
	}
	return total / float64(len(values))
}

// quartiles is statistics.quantiles(n=4, method="inclusive") over a sorted
// list of at least two values: linear interpolation between neighbors,
// which always yields floats.
func quartiles(sorted []pycompat.Number) [2]*pycompat.Number {
	m := int64(len(sorted) - 1)
	var out [2]*pycompat.Number
	for k, i := range []int64{1, 3} {
		j, delta := (i*m)/4, (i*m)%4
		q := pycompat.Float(sorted[j].MulInt(4 - delta).Add(sorted[j+1].MulInt(delta)).TrueDiv(4))
		out[k] = &q
	}
	return out
}

// describe is the Spread of values, nil values dropped first.
func describe(values []*pycompat.Number) Spread {
	var clean []pycompat.Number
	for _, v := range values {
		if v != nil {
			clean = append(clean, *v)
		}
	}
	if len(clean) == 0 {
		return Spread{}
	}
	sortNumbers(clean)
	var iqr [2]*pycompat.Number
	if len(clean) == 1 {
		q := clean[0]
		iqr = [2]*pycompat.Number{&q, &q}
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

// cellStats computes pass rates and metric spreads for one (arm, task) cell.
func cellStats(runs []*ScoredRun) CellStats {
	n := int64(len(runs))
	var successes, unknown int64
	var successful []*ScoredRun
	for _, run := range runs {
		switch {
		case run.Success == nil:
			unknown++
		case *run.Success:
			successes++
			successful = append(successful, run)
		}
	}
	// A run with no check is unknown, not failed, and the caveat says it is
	// excluded from the pass rate; graded is the denominator that makes that true.
	graded := n - unknown
	var passAt1 *float64
	if graded > 0 {
		p := float64(successes) / float64(graded)
		passAt1 = &p
	}
	passPowK := 0.0
	if graded > 0 && successes == graded {
		passPowK = 1.0
	}
	return CellStats{
		N: n, Successes: successes, UnknownOutcomes: unknown,
		PassAt1: passAt1, PassAt1CI: wilsonInterval(successes, graded), K: n, PassPowK: passPowK,
		Metrics: spreads(runs), MetricsSuccessOnly: spreads(successful),
	}
}

func spreads(runs []*ScoredRun) map[string]Spread {
	out := map[string]Spread{}
	for _, m := range metrics {
		values := make([]*pycompat.Number, len(runs))
		for i, run := range runs {
			values[i] = m.read(run)
		}
		out[m.name] = describe(values)
	}
	return out
}

// armSummary is the arm-level pass rate and cost-of-pass: expected dollars
// per correct solution.
func armSummary(runs []*ScoredRun) ArmSummary {
	n := int64(len(runs))
	var successes, graded int64
	dollars := make([]*pycompat.Number, len(runs))
	for i, run := range runs {
		d := pycompat.Float(run.Dollars)
		dollars[i] = &d
		if run.Success != nil {
			graded++
			if *run.Success {
				successes++
			}
		}
	}
	spread := describe(dollars)
	passRate := 0.0
	if graded > 0 {
		passRate = float64(successes) / float64(graded)
	}
	cost := CostOfPass{Infinite: true}
	if passRate > 0 && spread.Mean != nil {
		v := *spread.Mean / passRate
		cost = CostOfPass{Value: &v}
	}
	return ArmSummary{
		N: n, Successes: successes, PassRate: passRate, PassRateCI: wilsonInterval(successes, graded),
		Dollars: spread, CostOfPassUSD: cost,
	}
}

// pairedDelta bootstraps treated-minus-baseline over the reps both arms ran,
// for one metric.
func pairedDelta(treated, baseline map[int64]*ScoredRun, m metric, seedKey string) PairedDelta {
	var reps []int64
	for rep := range treated {
		if _, ok := baseline[rep]; ok {
			reps = append(reps, rep)
		}
	}
	slices.Sort(reps)
	var deltas, baseValues []pycompat.Number
	for _, rep := range reps {
		after, before := m.read(treated[rep]), m.read(baseline[rep])
		if after == nil || before == nil {
			continue
		}
		deltas = append(deltas, after.Sub(*before))
		baseValues = append(baseValues, *before)
	}
	ci, p := bootstrapPaired(deltas, seedKey)
	out := PairedDelta{NPairs: int64(len(deltas)), CILow: ci[0], CIHigh: ci[1], P: p, Verdict: verdictInconclusive}
	if len(deltas) == 0 {
		return out
	}
	sortNumbers(baseValues)
	baseMedian := median(baseValues)
	deltaMean := fmean(deltas)
	sortedDeltas := append([]pycompat.Number(nil), deltas...)
	sortNumbers(sortedDeltas)
	deltaMedian := median(sortedDeltas)
	out.DeltaMean, out.DeltaMedian, out.BaselineMedian = &deltaMean, &deltaMedian, &baseMedian
	if !baseMedian.IsZero() {
		rel := deltaMean / baseMedian.Float64()
		out.Relative = &rel
	}
	out.CIExcludesZero = ci[0] != nil && ci[1] != nil && (*ci[0] > 0 || *ci[1] < 0)
	if out.CIExcludesZero && out.Relative != nil && math.Abs(*out.Relative) >= minRelativeDelta {
		out.Verdict = verdictHigher
		if deltaMean < 0 {
			out.Verdict = verdictLower
		}
	}
	return out
}

type cellKey struct{ arm, task string }

type cellRuns map[cellKey][]*ScoredRun

func (c cellRuns) byRep(arm, task string) map[int64]*ScoredRun {
	out := map[int64]*ScoredRun{}
	for _, run := range c[cellKey{arm, task}] {
		out[run.Rep] = run
	}
	return out
}

// pairedDeltas is, per metric, per task, the full-minus-rampant delta, with
// p Holm-adjusted across tasks.
func pairedDeltas(c cellRuns, tasks []string, seed int64) map[string]map[string]PairedDelta {
	out := map[string]map[string]PairedDelta{}
	for _, m := range metrics {
		perTask := map[string]PairedDelta{}
		pvalues := map[string]*float64{}
		for _, task := range tasks {
			delta := pairedDelta(c.byRep(armFull, task), c.byRep(armRampant, task), m, fmt.Sprintf("%d|%s|%s", seed, m.name, task))
			perTask[task] = delta
			pvalues[task] = delta.P
		}
		adjusted := holm(pvalues)
		for task, delta := range perTask {
			delta.PHolm = adjusted[task]
			perTask[task] = delta
		}
		out[m.name] = perTask
	}
	return out
}

func dataQuality(runs []*ScoredRun, c cellRuns, tasks, arms []string) DataQuality {
	var unpaired []UnpairedRep
	for _, task := range tasks {
		repsByArm := map[string]map[int64]*ScoredRun{}
		union := map[int64]bool{}
		for _, arm := range arms {
			repsByArm[arm] = c.byRep(arm, task)
			for rep := range repsByArm[arm] {
				union[rep] = true
			}
		}
		for _, rep := range slices.Sorted(maps.Keys(union)) {
			var missing []string
			for _, arm := range arms {
				if _, ok := repsByArm[arm][rep]; !ok {
					missing = append(missing, arm)
				}
			}
			if len(missing) > 0 {
				unpaired = append(unpaired, UnpairedRep{Task: task, Rep: rep, MissingArms: missing})
			}
		}
	}
	// How far the pricing table sits from what the host billed, over the runs
	// that carry both. A constant ratio far from 1 is a table error; the report
	// says so rather than letting a floor pass for a bill.
	var ratios []pycompat.Number
	for _, run := range runs {
		if run.ReportedCostUSD != nil && *run.ReportedCostUSD != 0 {
			ratios = append(ratios, pycompat.Float(run.TableDollarsUSD / *run.ReportedCostUSD))
		}
	}
	var ratioMedian *float64
	if len(ratios) > 0 {
		sortNumbers(ratios)
		// Index n//2 is median_high, which the Python took; the byte pin depends on it.
		v := ratios[len(ratios)/2].Float64()
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
		UnpairedReps:                unpaired,
	}
}

func runIDs(runs []*ScoredRun, keep func(*ScoredRun) bool) []string {
	var ids []string
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
	var ids []string
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
// seed drives every bootstrap, so the same records and seed give identical
// output, and record order does not matter.
func Analyze(records []RunRecord, seed int64) (*Analysis, error) {
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
	// timestamp; pairing would take whichever came last while the cell counted
	// both, so the tree has to be cleaned up before it is read.
	seen := map[cellKey]map[int64]string{}
	for _, run := range runs {
		key := cellKey{run.Arm, run.Task}
		if seen[key] == nil {
			seen[key] = map[int64]string{}
		}
		if other, dup := seen[key][run.Rep]; dup {
			return nil, fmt.Errorf("%s/%s rep %d ran twice (%s and %s); keep one run directory", run.Arm, run.Task, run.Rep, other, run.RunID)
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
	for key, members := range c {
		cells[key.arm+"/"+key.task] = cellStats(members)
	}
	summaries := map[string]ArmSummary{}
	for _, arm := range arms {
		var armRuns []*ScoredRun
		for _, run := range runs {
			if run.Arm == arm {
				armRuns = append(armRuns, run)
			}
		}
		summaries[arm] = armSummary(armRuns)
	}
	return &Analysis{
		Seed:             seed,
		BootstrapIters:   bootstrapIters,
		MinRelativeDelta: minRelativeDelta,
		Runs:             int64(len(runs)),
		Arms:             arms,
		Tasks:            tasks,
		Models:           slices.Sorted(maps.Keys(modelSet)),
		Controls:         controlSummary(controls, tasks),
		Cells:            cells,
		ArmSummaries:     summaries,
		Paired:           pairedDeltas(c, tasks, seed),
		DataQuality:      dataQuality(runs, c, tasks, arms),
	}, nil
}

// JSON is analysis.json's bytes: sorted keys, two-space indent, and a
// trailing newline.
func (a *Analysis) JSON() ([]byte, error) {
	raw, err := pycompat.Marshal(a, 2)
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
