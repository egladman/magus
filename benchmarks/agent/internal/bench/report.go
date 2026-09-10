package bench

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

type headline struct {
	name, label string
	digits      int
}

// Metrics given their own delta table. The rest stay in analysis.json rather
// than padding the report with fourteen tables nobody reads.
var headlineMetrics = []headline{
	{"total_billed_tokens", "total billed tokens", 1},
	{"dollars", "dollars", 4},
	{"wall_ms", "wall clock (ms)", 1},
	{"turns", "turns", 1},
	{"tool_calls", "tool calls", 1},
	{"file_reads", "file reads", 1},
	{"re_read_rate", "re-read rate", 4},
	{"tool_result_bytes", "tool result bytes", 1},
}

// Below this the paired deltas are directional at best; Terminal-Bench runs 5.
const minReps = 5

var deltaHeader = []string{"task", "pairs", "median delta", "mean delta", "95% CI", "relative", "p (Holm)", "verdict"}

func numFloat(value *float64, digits int) string {
	if value == nil {
		return "n/a"
	}
	return strconv.FormatFloat(*value, 'f', digits, 64)
}

// num prints a float to the given digits and an int as it is, which is how
// the Python's f-string told the two apart.
func num(value *Number, digits int) string {
	if value == nil {
		return "n/a"
	}
	if i, ok := value.Int64(); ok {
		return strconv.FormatInt(i, 10)
	}
	f := value.Float64()
	return numFloat(&f, digits)
}

func usd(value *float64) string { return "$" + numFloat(value, 4) }

func pct(value *float64) string {
	if value == nil {
		return "n/a"
	}
	return strconv.FormatFloat(100.0**value, 'f', 0, 64) + "%"
}

func interval(bounds [2]*float64, digits int) string {
	if bounds[0] == nil || bounds[1] == nil {
		return "n/a"
	}
	return "[" + numFloat(bounds[0], digits) + ", " + numFloat(bounds[1], digits) + "]"
}

func table(header []string, rows [][]string) []string {
	dashes := make([]string, len(header))
	for i := range dashes {
		dashes[i] = "---"
	}
	lines := make([]string, 0, 2+len(rows))
	lines = append(lines, "| "+strings.Join(header, " | ")+" |", "| "+strings.Join(dashes, " | ")+" |")
	for _, row := range rows {
		lines = append(lines, "| "+strings.Join(row, " | ")+" |")
	}
	return lines
}

func section(title, blurb string, body []string) []string {
	lines := []string{"## " + title, "", blurb, ""}
	lines = append(lines, body...)
	return append(lines, "")
}

func controlsSection(a *Analysis) []string {
	rows := make([][]string, 0, len(a.Tasks))
	for _, task := range a.Tasks {
		cell := a.Controls[task]
		verdict := "NO"
		if cell.Discriminates {
			verdict = "yes"
		} else if cell.Golden.N == 0 || cell.Null.N == 0 {
			verdict = "UNVERIFIED (control missing)"
		}
		rows = append(rows, []string{
			task,
			fmt.Sprintf("%d/%d", cell.Golden.Passes, cell.Golden.N),
			fmt.Sprintf("%d/%d", cell.Null.Passes, cell.Null.N),
			verdict,
		})
	}
	return section(
		"Controls",
		"Whether each task's check can tell a solution from its absence: the golden "+
			"control applies the known solution and must pass, the null control touches "+
			"nothing and must fail. A pass rate below is only worth reading where both hold.",
		table([]string{"task", "golden (pass/n)", "null (pass/n)", "checks discriminate"}, rows),
	)
}

func headlineSection(a *Analysis) []string {
	rows := make([][]string, 0, len(a.Arms))
	for _, arm := range a.Arms {
		summary := a.ArmSummary[arm]
		cost := "infinite (no passes)"
		if !summary.CostOfPassUSD.Infinite {
			cost = usd(summary.CostOfPassUSD.Value)
		}
		rows = append(rows, []string{
			arm,
			strconv.FormatInt(summary.N, 10),
			strconv.FormatInt(summary.Successes, 10),
			pct(&summary.PassRate),
			interval(summary.PassRateCI, 3),
			usd(summary.Dollars.Mean),
			usdNumber(summary.Dollars.Median),
			cost,
		})
	}
	return section(
		"Headline: cost-of-pass",
		"Expected dollars per correct solution (mean dollars / pass rate).",
		table([]string{"arm", "n", "passes", "pass rate", "Wilson 95%", "mean $", "median $", "cost-of-pass"}, rows),
	)
}

func usdNumber(value *Number) string { return "$" + num(value, 4) }

func paretoSection(a *Analysis) []string {
	rows := make([][]string, 0, len(a.Cells))
	for _, key := range sortedKeys(a.Cells) {
		arm, task, _ := strings.Cut(key, "/")
		cell := a.Cells[key]
		rows = append(rows, []string{
			arm,
			task,
			strconv.FormatInt(cell.N, 10),
			pct(cell.PassAt1),
			num(cell.Metrics["total_billed_tokens"].Median, 0),
			num(cell.MetricsSuccessOnly["total_billed_tokens"].Median, 0),
			usdNumber(cell.Metrics["dollars"].Median),
		})
	}
	return section(
		"Correctness against median tokens",
		"The cost-accuracy frontier in text: pass rate beside the token spend it cost.",
		table([]string{"arm", "task", "n", "pass@1", "median tokens", "median tokens (passes only)", "median $"}, rows),
	)
}

func deltasSection(a *Analysis) []string {
	lines := []string{
		"## Paired deltas, full minus rampant",
		"",
		fmt.Sprintf("Rep i of one arm is paired with rep i of the other. CI is a seeded "+
			"%d-sample bootstrap of the mean paired delta; p is "+
			"Holm-adjusted across tasks. A verdict needs the CI to exclude zero AND the delta "+
			"to reach %s%% of the rampant median.",
			a.BootstrapIters, strconv.FormatFloat(100.0*a.MinRelativeDelta, 'f', 0, 64)),
		"",
	}
	for _, m := range headlineMetrics {
		perTask := a.Paired[m.name]
		if len(perTask) == 0 {
			continue
		}
		lines = append(lines, "### "+m.label, "")
		rows := make([][]string, 0, len(a.Tasks))
		for _, task := range a.Tasks {
			delta, ok := perTask[task]
			if !ok {
				continue
			}
			rows = append(rows, []string{
				task,
				strconv.FormatInt(delta.NPairs, 10),
				num(delta.DeltaMedian, m.digits),
				numFloat(delta.DeltaMean, m.digits),
				interval([2]*float64{delta.CILow, delta.CIHigh}, m.digits),
				pct(delta.Relative),
				numFloat(delta.PHolm, 3),
				delta.Verdict,
			})
		}
		lines = append(lines, table(deltaHeader, rows)...)
		lines = append(lines, "")
	}
	return lines
}

func passRatesSection(a *Analysis) []string {
	rows := make([][]string, 0, len(a.Cells))
	for _, key := range sortedKeys(a.Cells) {
		arm, task, _ := strings.Cut(key, "/")
		cell := a.Cells[key]
		rows = append(rows, []string{
			arm,
			task,
			strconv.FormatInt(cell.K, 10),
			pct(cell.PassAt1),
			interval(cell.PassAt1CI, 3),
			numFloat(&cell.PassPowK, 0),
		})
	}
	return section(
		"pass@1 and pass^k",
		"pass@1 is capability; pass^k (all k reps succeed) is reliability.",
		table([]string{"arm", "task", "k", "pass@1", "Wilson 95%", "pass^k"}, rows),
	)
}

func caveatsSection(a *Analysis) []string {
	lines := []string{"## Caveats", ""}
	for _, line := range caveatLines(a) {
		lines = append(lines, "- "+line)
	}
	return append(lines, "")
}

// caveatLines is one caveat per fact in the data that limits what the tables
// above can claim.
func caveatLines(a *Analysis) []string {
	quality := a.DataQuality
	keys := sortedKeys(a.Cells)
	var lines []string
	reps := make([]string, 0, len(keys))
	var small []string
	for _, key := range keys {
		reps = append(reps, fmt.Sprintf("%s n=%d", key, a.Cells[key].N))
		if a.Cells[key].N < minReps {
			small = append(small, key)
		}
	}
	lines = append(lines, "Reps per cell: "+strings.Join(reps, ", ")+".")
	if len(small) > 0 {
		lines = append(lines, fmt.Sprintf("Under the %d-rep protocol: %s. Treat those deltas as directional.", minReps, strings.Join(small, ", ")))
	}
	for _, gap := range quality.IncompletePairs {
		lines = append(lines, fmt.Sprintf("Unpaired: %s rep %d is missing arm(s) %s, so it contributes to no delta.",
			gap.Task, gap.Rep, strings.Join(gap.MissingArms, ", ")))
	}
	if n := len(quality.RunsWithoutCheck); n > 0 {
		lines = append(lines, fmt.Sprintf("Unknown outcome: %d run(s) carry no acceptance check and are excluded from pass rates (%s).",
			n, strings.Join(quality.RunsWithoutCheck, ", ")))
	}
	if n := len(quality.RunsWithoutGuardEvents); n > 0 {
		lines = append(lines, fmt.Sprintf("No activity trail for %d run(s) (%s); their guard_events are null, not zero.",
			n, strings.Join(quality.RunsWithoutGuardEvents, ", ")))
	}
	if n := len(quality.RunsWithAssumedCacheTTL); n > 0 {
		lines = append(lines, fmt.Sprintf("Cache-write TTL was not reported for %d run(s) (%s); those writes are priced at the 5-minute rate, so their dollars are a floor.",
			n, strings.Join(quality.RunsWithAssumedCacheTTL, ", ")))
	}
	if len(quality.RunsWithInvariantViolations) > 0 {
		lines = append(lines, fmt.Sprintf("Invariant violation (test file deleted) in %s.", strings.Join(quality.RunsWithInvariantViolations, ", ")))
	}
	if len(a.Models) > 1 {
		lines = append(lines, fmt.Sprintf("More than one model appears across runs (%s); the arm is no longer the only variable.", strings.Join(a.Models, ", ")))
	}
	if ratio := quality.TableToBilledRatioMedian; ratio != nil && !(0.9 <= *ratio && *ratio <= 1.1) {
		lines = append(lines, fmt.Sprintf("Dollars are the host's billed cost; the pricing table would have said %sx that, so the table is wrong for this model and only backs runs with no result record.",
			strconv.FormatFloat(*ratio, 'f', 2, 64)))
	}
	if n := len(quality.RunsWithoutBilledCost); n > 0 {
		lines = append(lines, fmt.Sprintf("No billed cost for %d run(s); their dollars come from the pricing table.", n))
	}
	var unverified []string
	for _, task := range a.Tasks {
		if cell, ok := a.Controls[task]; !ok || !cell.Discriminates {
			unverified = append(unverified, task)
		}
	}
	if len(unverified) > 0 {
		lines = append(lines, fmt.Sprintf("Checks not shown to discriminate for %s (see Controls); pass rates there are not evidence.", strings.Join(unverified, ", ")))
	}
	return lines
}

// Render is the whole of report.md: every table and every caveat is generated
// from the analysis, so a report can never claim more than the data behind it.
func Render(a *Analysis) string {
	models := strings.Join(a.Models, " and ")
	if models == "" {
		models = "unrecorded"
	}
	head := []string{
		"# Harness-effectiveness benchmark",
		"",
		fmt.Sprintf("%d runs, %d task(s), arms %s, model(s) %s, bootstrap seed %d.",
			a.Runs, len(a.Tasks), strings.Join(a.Arms, " and "), models, a.Seed),
		"",
	}
	lines := slices.Concat(head, controlsSection(a), headlineSection(a), paretoSection(a),
		deltasSection(a), passRatesSection(a), caveatsSection(a))
	return strings.Join(lines, "\n") + "\n"
}
