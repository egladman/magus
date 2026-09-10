// Command benchreport turns an agent benchmark results tree into a report in
// three stages, each reading the previous one's file so a scored run is
// analyzed as often as wanted without re-running an agent:
//
//	benchreport extract <results> -o metrics.jsonl [-p pricing.json]
//	benchreport analyze <metrics.jsonl> -o analysis.json [--seed N]
//	benchreport report <analysis.json> -o report.md
//	benchreport makefixture <dir>
//
// makefixture writes a synthetic results tree under <dir>/results, the
// pipeline's own control. A failure in extract exits 2, in the other stages 1.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/egladman/magus/benchmarks/agent/internal/bench"
)

const usage = `usage:
  benchreport extract <results> -o metrics.jsonl [-p pricing.json]
  benchreport analyze <metrics.jsonl> -o analysis.json [--seed N]
  benchreport report <analysis.json> -o report.md
  benchreport makefixture <dir>
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

// parse accepts the flags before or after the one positional argument, the
// way the argparse commands did.
func parse(fs *flag.FlagSet, args []string) (string, error) {
	var positional, flags []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positional = append(positional, arg)
			continue
		}
		flags = append(flags, arg)
		if !strings.Contains(arg, "=") && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	if err := fs.Parse(flags); err != nil {
		return "", err
	}
	if len(positional) != 1 {
		return "", fmt.Errorf("want exactly one positional argument, got %d", len(positional))
	}
	return positional[0], nil
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
		return fmt.Errorf("-o is required")
	}
	pricing, err := bench.LoadPricing(*pricingFile)
	if err != nil {
		return err
	}
	records, err := bench.ExtractAll(results, pricing)
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
		fmt.Println(bench.SummaryLine(record))
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
		return fmt.Errorf("-o is required")
	}
	records, err := bench.LoadRecords(metrics)
	if err != nil {
		return err
	}
	a, err := bench.Analyze(records, *seed)
	if err != nil {
		return err
	}
	raw, err := bench.AnalysisJSON(a)
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
		return fmt.Errorf("-o is required")
	}
	a, err := bench.LoadAnalysis(file)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, []byte(bench.Render(a)), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", *out)
	return nil
}

func makefixture(args []string) error {
	fs := flag.NewFlagSet("makefixture", flag.ContinueOnError)
	root, err := parse(fs, args)
	if err != nil {
		return err
	}
	results, err := bench.BuildFixture(root)
	if err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", results)
	return nil
}
