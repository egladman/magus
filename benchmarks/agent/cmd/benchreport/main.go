// Command benchreport turns an agent benchmark results tree into a report in
// three stages, each reading the previous one's file so a scored run is
// analyzed as often as wanted without re-running an agent:
//
//	benchreport extract <results> -o metrics.jsonl [-p pricing.json]
//	benchreport analyze <metrics.jsonl> -o analysis.json [--seed N]
//	benchreport report <analysis.json> -o report.md
//	benchreport makefixture <dir> -model <alias>
//
// makefixture writes a synthetic results tree under <dir>/results, the
// pipeline's own control. A failure in extract exits 2, in the other stages 1.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/egladman/magus/benchmarks/agent/internal/bench"
)

const usage = `usage:
  benchreport extract <results> -o metrics.jsonl [-p pricing.json]
  benchreport analyze <metrics.jsonl> -o analysis.json [--seed N]
  benchreport report <analysis.json> -o report.md
  benchreport makefixture <dir> -model <alias>
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	stage := os.Args[1]
	var err error
	switch stage {
	case "extract":
		err = extract(os.Args[2:])
	case "analyze":
		err = analyze(os.Args[2:])
	case "report":
		err = report(os.Args[2:])
	case "makefixture":
		err = makefixture(os.Args[2:])
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", stage, err)
		if stage == "extract" {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

// parse takes the one positional argument first, then the flags after it.
func parse(fs *flag.FlagSet, args []string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("want a positional argument first, then the flags")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return "", err
	}
	if fs.NArg() != 0 {
		return "", fmt.Errorf("want exactly one positional argument, got %d", 1+fs.NArg())
	}
	return args[0], nil
}

// summaryLine is one line per run, for the operator watching extraction.
func summaryLine(record bench.RunRecord) string {
	if c := record.Control; c != nil {
		check := "n/a"
		if c.CheckExit != nil {
			check = strconv.FormatInt(*c.CheckExit, 10)
		}
		return fmt.Sprintf("%s: %s control, check=%s", c.RunID, c.Control, check)
	}
	r := record.Scored
	outcome := "NOCHECK"
	if r.Success != nil {
		outcome = "FAIL"
		if *r.Success {
			outcome = "PASS"
		}
	}
	guard := "guard=none"
	if g := r.GuardEvents; g != nil {
		guard = fmt.Sprintf("guard=%d/%d/%d", g.Denials, g.Advisories, g.SkillLoads)
	}
	return fmt.Sprintf("%-34s %-8s %-10s %-7s tok=%-9d $%.4f turns=%-3d tools=%-3d %s",
		r.RunID, r.Arm, r.Task, outcome, r.Tokens.TotalBilled, r.Dollars, r.Turns, r.ToolCalls, guard)
}

func extract(args []string) error {
	fs := flag.NewFlagSet("extract", flag.ContinueOnError)
	out := fs.String("o", "", "metrics.jsonl to write")
	pricingFile := fs.String("p", "benchmarks/agent/pricing.json", "pricing table")
	results, err := parse(fs, args)
	if err != nil {
		return err
	}
	if *out == "" {
		return errors.New("-o is required")
	}
	pricing, err := bench.LoadPricing(*pricingFile)
	if err != nil {
		return err
	}
	records, err := bench.Extract(results, pricing)
	if err != nil {
		return err
	}
	lines, err := bench.RecordsJSONL(records)
	if err != nil {
		return err
	}
	controls := 0
	for _, record := range records {
		if record.Control != nil {
			controls++
		}
	}
	if err := os.WriteFile(*out, lines, 0o644); err != nil {
		return err
	}
	for _, record := range records {
		fmt.Println(summaryLine(record))
	}
	fmt.Printf("wrote %d runs to %s (%d of them controls)\n", len(records), *out, controls)
	return nil
}

func analyze(args []string) error {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	out := fs.String("o", "", "analysis.json to write")
	seed := fs.Int64("seed", 20260902, "bootstrap seed")
	metrics, err := parse(fs, args)
	if err != nil {
		return err
	}
	if *out == "" {
		return errors.New("-o is required")
	}
	records, err := bench.LoadRecords(metrics)
	if err != nil {
		return err
	}
	a, err := bench.Analyze(records, *seed)
	if err != nil {
		return err
	}
	raw, err := a.JSON()
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, raw, 0o644); err != nil {
		return err
	}
	fmt.Printf("analyzed %d runs, %d tasks, %d arms -> %s\n", a.Runs, len(a.Tasks), len(a.Arms), *out)
	return nil
}

func report(args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	out := fs.String("o", "", "report.md to write")
	file, err := parse(fs, args)
	if err != nil {
		return err
	}
	if *out == "" {
		return errors.New("-o is required")
	}
	a, err := bench.LoadAnalysis(file)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, []byte(bench.Report(a)), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", *out)
	return nil
}

func makefixture(args []string) error {
	fs := flag.NewFlagSet("makefixture", flag.ContinueOnError)
	model := fs.String("model", "", "model alias every synthetic run reports; must be in the pricing table")
	root, err := parse(fs, args)
	if err != nil {
		return err
	}
	if *model == "" {
		return errors.New("-model is required")
	}
	if err := bench.WriteFixture(root, *model); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", filepath.Join(root, "results"))
	return nil
}
