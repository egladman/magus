package bench

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/egladman/magus/benchmarks/agent/internal/pricing"
	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/exit"
)

// extractFailure is a failure in the extract stage, which exits 2.
type extractFailure struct{ error }

func (e extractFailure) Unwrap() error { return e.error }
func (extractFailure) ExitCode() int   { return 2 }

// ReportCommand declares benchreport, with fresh flag targets per call.
func ReportCommand() command.Command {
	return command.Command{
		Name:  "benchreport",
		Short: "Turn an agent benchmark results tree into a report.",
		Long: "Three stages, each reading the previous one's file so a scored run is analyzed as " +
			"often as wanted without re-running an agent. makefixture writes a synthetic results " +
			"tree under <dir>/results, the pipeline's own control. A failure in extract exits 2, " +
			"in the other stages 1.",
		Usage: "benchreport <stage> [flags] <path>",
		Children: []command.Command{
			extractCommand(),
			analyzeCommand(),
			reportCommand(),
			makefixtureCommand(),
		},
	}
}

// stage declares one stage that takes a single positional argument and the
// flags given.
func stage(name, short, usage, operand string, flags []command.Flag, run func(inv command.Invocation, arg string) error) command.Command {
	return command.Command{
		Name:     name,
		Short:    short,
		Usage:    fmt.Sprintf("benchreport %s %s", name, usage),
		Operands: []command.Operand{{Name: operand}},
		Flags:    flags,
		Run: func(_ context.Context, inv command.Invocation) error {
			if len(inv.Args) != 1 {
				return exit.Usagef("want exactly one positional argument, got %d", len(inv.Args))
			}
			return run(inv, inv.Args[0])
		},
	}
}

func outFlag(doc string, target *string) command.Flag {
	return command.Flag{Name: "o", Kind: command.String, Doc: doc, Target: target}
}

func extractCommand() command.Command {
	var out, pricingFile string
	flags := []command.Flag{
		outFlag("metrics.jsonl to write", &out),
		{Name: "p", Kind: command.String, Doc: "price table to use instead of the one compiled in", Target: &pricingFile},
	}
	return stage("extract", "Read a results tree into metrics.jsonl.",
		"-o metrics.jsonl [-p pricing.json] <results>", "results", flags,
		func(inv command.Invocation, results string) error {
			if err := runExtract(inv, results, out, pricingFile); err != nil {
				return extractFailure{err}
			}
			return nil
		})
}

func analyzeCommand() command.Command {
	var out string
	var seed int
	flags := []command.Flag{
		outFlag("analysis.json to write", &out),
		{Name: "seed", Kind: command.Int, Default: 20260902, Doc: "seed for the sampled randomization tests", Target: &seed},
	}
	return stage("analyze", "Test the metrics for differences between arms.",
		"-o analysis.json [--seed N] <metrics.jsonl>", "metrics.jsonl", flags,
		func(inv command.Invocation, metrics string) error {
			return runAnalyze(inv, metrics, out, uint64(seed))
		})
}

func reportCommand() command.Command {
	var out string
	return stage("report", "Render the analysis as Markdown.",
		"-o report.md <analysis.json>", "analysis.json",
		[]command.Flag{outFlag("report.md to write", &out)},
		func(inv command.Invocation, analysis string) error {
			return runReport(inv, analysis, out)
		})
}

func makefixtureCommand() command.Command {
	var model string
	flags := []command.Flag{
		{Name: "model", Kind: command.String, Doc: "model alias every synthetic run reports; must be in the pricing table", Target: &model},
	}
	return stage("makefixture", "Write a synthetic results tree, the pipeline's own control.",
		"-model <alias> <dir>", "dir", flags,
		func(inv command.Invocation, dir string) error {
			return runMakefixture(inv, dir, model)
		})
}

// summaryLine is one line per run, for the operator watching extraction.
func summaryLine(record RunRecord) string {
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

func runExtract(inv command.Invocation, results, out, pricingFile string) error {
	if out == "" {
		return errors.New("-o is required")
	}
	// The table is compiled in, so a report never depends on the working
	// directory. -p prices an old tree against the table current when it ran.
	table, err := pricing.Default()
	if err != nil {
		return err
	}
	if pricingFile != "" {
		if table, err = pricing.Load(pricingFile); err != nil {
			return err
		}
	}
	records, err := Extract(results, table)
	if err != nil {
		return err
	}
	lines, err := RecordsJSONL(records)
	if err != nil {
		return err
	}
	controls := 0
	for _, record := range records {
		if record.Control != nil {
			controls++
		}
	}
	if err := os.WriteFile(out, lines, 0o644); err != nil {
		return err
	}
	for _, record := range records {
		fmt.Fprintln(inv.Stdout, summaryLine(record))
	}
	fmt.Fprintf(inv.Stdout, "wrote %d runs to %s (%d of them controls)\n", len(records), out, controls)
	return nil
}

func runAnalyze(inv command.Invocation, metrics, out string, seed uint64) error {
	if out == "" {
		return errors.New("-o is required")
	}
	records, err := LoadRecords(metrics)
	if err != nil {
		return err
	}
	a, err := Analyze(records, seed)
	if err != nil {
		return err
	}
	raw, err := a.JSON()
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, raw, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(inv.Stdout, "analyzed %d runs, %d tasks, %d arms -> %s\n", a.Runs, len(a.Tasks), len(a.Arms), out)
	return nil
}

func runReport(inv command.Invocation, file, out string) error {
	if out == "" {
		return errors.New("-o is required")
	}
	a, err := LoadAnalysis(file)
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, []byte(Report(a)), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(inv.Stdout, "wrote %s\n", out)
	return nil
}

func runMakefixture(inv command.Invocation, root, model string) error {
	if model == "" {
		return errors.New("-model is required")
	}
	if err := WriteFixture(root, model); err != nil {
		return err
	}
	fmt.Fprintf(inv.Stdout, "wrote %s\n", filepath.Join(root, "results"))
	return nil
}
