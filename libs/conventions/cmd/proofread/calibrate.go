package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"text/tabwriter"

	"github.com/egladman/magus/libs/conventions/proofread"
)

// runCalibrate replays the labeled cases and prints each rule's precision and
// recall, or with -gates each rule's shipped default against what that
// precision supports. With -sample it instead judges one field of each JSON-lines record
// as -kind and prints each rule's firing rate per value of -group.
func runCalibrate(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("proofread calibrate", flag.ContinueOnError)
	fset.SetOutput(stderr)
	casesDir := fset.String("cases", "", "read labeled case files from this `dir` instead of the ones proofread carries")
	format := fset.String("format", "table", "write `table` or json")
	sample := fset.String("sample", "", "judge the records of this JSON-lines `file`, or - for stdin")
	kind := fset.String("kind", "", "with -sample, judge each text as this `kind`")
	field := fset.String("field", "body", "with -sample, the comma-separated `fields` joined by newlines into each text")
	group := fset.String("group", "", "with -sample, the comma-separated `fields` whose values group the rates")
	gates := fset.Bool("gates", false, "hold each rule's shipped default against the precision its cases support, and exit 1 when one outruns it")

	if err := fset.Parse(args); err != nil {
		return 1
	}

	if fset.NArg() > 0 {
		return failure(stderr, fmt.Errorf("calibrate takes no operand, got %q", fset.Arg(0)))
	}

	if *format != "table" && *format != "json" {
		return failure(stderr, fmt.Errorf("-format %q is neither table nor json", *format))
	}

	if *sample == "" {
		if *kind != "" || *group != "" {
			return failure(stderr, errors.New("-kind and -group need -sample"))
		}

		return replayCases(*casesDir, *format, *gates, stdout, stderr)
	}

	if *casesDir != "" || *gates {
		return failure(stderr, errors.New("-cases and -gates replay the labeled cases and cannot be combined with -sample"))
	}

	k, err := sampleKind(*kind)
	if err != nil {
		return failure(stderr, err)
	}

	in := stdin

	if *sample != "-" {
		f, err := os.Open(*sample)
		if err != nil {
			return failure(stderr, err)
		}
		defer f.Close()

		in = f
	}

	rates, err := tallySample(in, k, splitFields(*field), splitFields(*group))
	if err != nil {
		return failure(stderr, err)
	}

	if err := writeRates(stdout, *format, rates); err != nil {
		return failure(stderr, err)
	}

	return 0
}

// replayCases prints the scores of the cases in dir, or with gates each
// rule's gate, exiting 1 when a rule ships a decision its cases do not
// support.
func replayCases(dir, format string, gates bool, stdout, stderr io.Writer) int {
	fsys := proofread.LabeledCases()
	if dir != "" {
		fsys = os.DirFS(dir)
	}

	cases, err := proofread.LoadCases(fsys)
	if err != nil {
		return failure(stderr, fmt.Errorf("load the cases: %w", err))
	}

	if len(cases) == 0 {
		return failure(stderr, fmt.Errorf("no case files in %s", dirName(dir)))
	}

	scores := proofread.Calibrate(cases)

	if !gates {
		if err := writeScores(stdout, format, scores); err != nil {
			return failure(stderr, err)
		}

		return 0
	}

	all := proofread.Gates(scores)
	if err := writeGates(stdout, format, all); err != nil {
		return failure(stderr, err)
	}

	for _, g := range all {
		if !g.Pass {
			return exitFindings
		}
	}

	return 0
}

func dirName(dir string) string {
	if dir == "" {
		return "the cases proofread carries"
	}

	return dir
}

// sampleKind is the -kind a sample is judged as: any kind proofread judges
// from one text.
func sampleKind(name string) (proofread.Kind, error) {
	if name == "" {
		return "", errors.New("-sample needs -kind")
	}

	for _, s := range subcommands {
		if s.kind != "" && s.name == name {
			if s.kind == proofread.KindAgentInstructionsTemplate {
				return "", fmt.Errorf("-kind %s renders a template first and cannot be sampled", name)
			}

			return s.kind, nil
		}
	}

	return "", fmt.Errorf("unknown kind %q", name)
}

func splitFields(list string) []string {
	var out []string

	for f := range strings.SplitSeq(list, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}

	return out
}

// tallySample judges each record of in, one JSON object per line, on all
// CPUs. A record with no text in fields is skipped.
func tallySample(in io.Reader, kind proofread.Kind, fields, groupBy []string) ([]proofread.GroupRates, error) {
	if len(fields) == 0 {
		return nil, errors.New("-field names no field")
	}

	tally := proofread.NewTally(kind)
	type job struct{ group, text string }

	jobs := make(chan job, 256)

	var wg sync.WaitGroup

	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			for j := range jobs {
				tally.Add(j.group, j.text)
			}
		})
	}

	err := readRecords(in, func(rec map[string]any) {
		text := joinText(rec, fields)
		if text == "" {
			return
		}

		jobs <- job{groupName(rec, groupBy), text}
	})

	close(jobs)
	wg.Wait()

	if err != nil {
		return nil, err
	}

	return tally.Rates(), nil
}

func readRecords(in io.Reader, each func(map[string]any)) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)

	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}

		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return fmt.Errorf("sample line %d: %w", n, err)
		}

		each(rec)
	}

	if err := sc.Err(); err != nil {
		return fmt.Errorf("read the sample: %w", err)
	}

	return nil
}

// joinText joins the non-empty strings rec holds for fields with newlines.
func joinText(rec map[string]any, fields []string) string {
	var parts []string

	for _, f := range fields {
		if v, ok := rec[f].(string); ok && v != "" {
			parts = append(parts, v)
		}
	}

	return strings.Join(parts, "\n")
}

// groupName joins the values rec holds for fields with slashes, "(none)" for
// one it lacks, or is "all" when fields is empty.
func groupName(rec map[string]any, fields []string) string {
	if len(fields) == 0 {
		return "all"
	}

	parts := make([]string, len(fields))

	for i, f := range fields {
		switch v := rec[f].(type) {
		case nil:
			parts[i] = "(none)"
		default:
			parts[i] = fmt.Sprint(v)
		}
	}

	return strings.Join(parts, "/")
}

func writeScores(w io.Writer, format string, scores []proofread.Score) error {
	if format == "json" {
		return writeJSON(w, scores)
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RULE\tCASES\tTP\tFP\tFN\tTN\tPRECISION\tLOWER\tRECALL\tHELD_OUT\tHELD_OUT_PRECISION\tDISAGREE")

	for _, s := range scores {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%d\t%s\t%s\t%s\t%d\t%s\t%d\n",
			s.Rule, s.Cases, s.TP, s.FP, s.FN, s.TN, percent(s.Precision), percent(s.Lower), percent(s.Recall),
			s.HeldOut, percent(s.HeldOutPrecision), s.Disagree)
	}

	return tw.Flush()
}

// writeGates prints each rule's gate, then, as a table, a count of the rules
// that fail and the deny conditions no replay measures.
func writeGates(w io.Writer, format string, gates []proofread.Gate) error {
	if format == "json" {
		return writeJSON(w, gates)
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RULE\tDIMENSION\tSHIPPED\tSUPPORTS\tFIRINGS\tWRONG\tPRECISION\tLOWER\tHELD_OUT_PRECISION\tGATE\tNEEDS")

	failed := 0

	for _, g := range gates {
		verdict := "pass"
		if !g.Pass {
			verdict = "fail"
			failed++
		}

		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\t%s\t%s\t%s\n",
			g.Rule, g.Dimension, g.Shipped, g.Supports, g.Firings, g.Wrong, percent(g.Precision), percent(g.Lower),
			percent(g.HeldOutPrecision), verdict, needs(g))
	}

	if err := tw.Flush(); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "\n%d of %d rules ship a decision their labeled firings do not support. "+
		"NEEDS is the labeled firings still to add at the rule's current precision.\n", failed, len(gates)); err != nil {
		return err
	}

	for _, g := range gates {
		if len(g.Unmeasured) > 0 {
			_, err := fmt.Fprintf(w, "Not measured here, and also needed for deny: %s.\n", strings.Join(g.Unmeasured, " and "))

			return err
		}
	}

	return nil
}

func needs(g proofread.Gate) string {
	switch {
	case g.Pass:
		return "-"
	case g.Needs == nil:
		return "unreachable"
	default:
		return fmt.Sprintf("+%d", *g.Needs)
	}
}

func percent(p *float64) string {
	if p == nil {
		return "-"
	}

	return fmt.Sprintf("%.1f%%", 100**p)
}

func writeRates(w io.Writer, format string, groups []proofread.GroupRates) error {
	if format == "json" {
		return writeJSON(w, groups)
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "GROUP\tTEXTS\tRULE\tDECISION\tFINDINGS\tPER_1000\tTEXTS_HIT\tSHARE\tTEXTS_DENIED")

	for _, g := range groups {
		for _, r := range g.Rules {
			share := r.Share
			fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%d\t%.1f\t%d\t%s\t%d\n",
				g.Group, g.Texts, r.Rule, r.Decision, r.Findings, r.PerThousand, r.Texts, percent(&share), r.DeniedTexts)
		}
	}

	return tw.Flush()
}

func writeJSON(w io.Writer, v any) error {
	if err := json.NewEncoder(w).Encode(v); err != nil {
		return fmt.Errorf("write the output: %w", err)
	}

	return nil
}
