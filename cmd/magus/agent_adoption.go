package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/egladman/magus/internal/hint"
	json "github.com/egladman/magus/internal/json"
)

// adoptionReport measures how often agents reached for the knowledge graph versus a raw text
// search, over a corpus of shell commands. It turns magus's own doctrine - query before
// grepping - into a number a user can watch move as the graph gets easier to reach.
//
// magus does the ANALYSIS; it never reads a specific agent host's logs, because it is
// host-agnostic (a conventions test forbids naming one). The caller extracts the commands from
// wherever their host records them - the help prints the recipe - and feeds them in.
type adoptionReport struct {
	Total          int            `json:"total"`
	GraphVerbs     int            `json:"graph_verbs"`      // magus query/refs/explain/path/graph
	TextSearches   int            `json:"text_searches"`    // grep/rg/ag
	SearchOfSource int            `json:"search_of_source"` // repo-wide search over code: a refs/query candidate
	SearchOfProse  int            `json:"search_of_prose"`  // a search naming a .md: a docsection candidate
	FileReads      int            `json:"file_reads"`       // cat/head/tail/sed -n a file (the Read tool is better)
	MagusRuns      int            `json:"magus_runs"`       // magus run/affected/ci/... (not a graph verb)
	Other          int            `json:"other"`
	TopSymbolGreps []patternCount `json:"top_symbol_greps"` // repo-wide greps whose pattern is a bare identifier
}

// patternCount's JSON fields are additive-only: pattern and count keep their
// names and meanings for existing consumers.
type patternCount struct {
	Pattern string `json:"pattern"`
	Count   int    `json:"count"`
	Run     string `json:"run"` // the graph command to try for this pattern
}

// analyzeAdoption classifies each command line by its dominant intent and tallies the report.
// A line is counted once, by priority (graph beats search beats read), so the graph-to-search
// ratio is not inflated by a line that does several things.
func analyzeAdoption(lines []string) adoptionReport {
	var r adoptionReport
	symbols := map[string]int{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		r.Total++
		switch cat, pat := classifyCommandLine(line); cat {
		case "graph":
			r.GraphVerbs++
		case "search-source":
			r.TextSearches++
			r.SearchOfSource++
			if pat != "" {
				symbols[pat]++
			}
		case "search-prose":
			r.TextSearches++
			r.SearchOfProse++
		case "search-other":
			r.TextSearches++
		case "read":
			r.FileReads++
		case "magus":
			r.MagusRuns++
		default:
			r.Other++
		}
	}
	r.TopSymbolGreps = topPatterns(symbols, 15)
	for i, p := range r.TopSymbolGreps {
		r.TopSymbolGreps[i].Run = adoptionRun(p.Pattern)
	}
	return r
}

// adoptionRun renders the graph command for one top pattern through the same
// translator the live guard suggests with: an MGS code or a mgs_* op routes to
// `magus query`, which refs (compiled-language symbols only) would miss. The
// refs fallback covers a translator abstention, which looksLikeSymbol should
// have ruled out already.
func adoptionRun(pattern string) string {
	if s := adoptionHints.Suggest(hint.Invocation{Name: "rg", Args: []string{pattern}}); len(s) > 0 {
		return s[0].Run
	}
	return "magus refs " + pattern
}

// adoptionHints classifies commands through the same translator the live
// guard suggests with, so the report and the guard agree on what a search is.
// Bare on purpose: adoption reads any host's history, and the corpus may come
// from a different repo than the cwd, so scoping suggestions to the local
// workspace's projects would be dishonest.
var adoptionHints = hint.NewGraph()

// The report's category NAMES are frozen, and hint's taxonomy is richer than
// they are: hint calls a non-recursive grep and bat reads. These sets pin the
// mapping back onto them - the grep family stays a search however it was
// pointed, and only the read tools the report has always counted land in
// file_reads. Counts are not bit-comparable across versions: routing through
// hint re-baselines a few edge shapes (a search narrowed by a flag glob, an
// in-place sed).
var (
	adoptionGrepFamily = map[string]bool{"grep": true, "egrep": true, "fgrep": true}
	adoptionReadTools  = map[string]bool{"cat": true, "head": true, "tail": true, "less": true, "more": true, "sed": true}
)

// classifyCommandLine reuses the guard's own command parser, so the report agrees with what the
// live guard sees. It returns the dominant category and, for a repo-wide source search, the bare
// identifier that would route to `magus refs`.
func classifyCommandLine(line string) (category, symbol string) {
	cmds, _ := parseGuardCommands(line)
	var sawGraph, sawSearch, sawRead, sawMagus bool
	var srcSearch, proseSearch bool
	var symbolPat string
	for _, c := range cmds {
		// magus-verb detection stays local: hint models the tools magus
		// replaces, never magus itself.
		if c.Name == "magus" || c.Name == "./magus" {
			if len(c.Args) > 0 && slices.Contains([]string{"query", "refs", "explain", "path", "graph"}, c.Args[0]) {
				sawGraph = true
			} else {
				sawMagus = true
			}
			continue
		}
		inv := hint.Invocation{Name: c.Name, Args: c.Args}
		switch adoptionHints.Classify(inv) {
		case hint.ClassSearchProse:
			sawSearch, proseSearch = true, true
		case hint.ClassSearchSource:
			sawSearch, srcSearch = true, true
			if symbolPat == "" {
				if pats := adoptionHints.Patterns(inv); len(pats) > 0 && looksLikeSymbol(pats[0]) {
					symbolPat = pats[0]
				}
			}
		case hint.ClassRead:
			switch {
			case adoptionGrepFamily[c.Name]:
				sawSearch = true
			case adoptionReadTools[c.Name]:
				sawRead = true
			}
		}
	}
	switch {
	case sawGraph:
		return "graph", ""
	case sawSearch && proseSearch:
		return "search-prose", ""
	case sawSearch && srcSearch:
		return "search-source", symbolPat
	case sawSearch:
		return "search-other", ""
	case sawRead:
		return "read", ""
	case sawMagus:
		return "magus", ""
	default:
		return "other", ""
	}
}

// looksLikeSymbol is stricter than hint's identifier shape: for the report's "try refs" list it
// requires an uppercase letter, because a code identifier almost always carries one
// (parseQuery, HandleFoo) while the false positives are all-lowercase words and path fragments
// (test, node_modules, gen) that refs would never resolve. The guard's live nudge stays broad
// because it hedges; a report that names a pattern a symbol should be more sure.
func looksLikeSymbol(p string) bool {
	return hint.IsIdentifier(p) && strings.ContainsAny(p, "ABCDEFGHIJKLMNOPQRSTUVWXYZ")
}

func topPatterns(counts map[string]int, n int) []patternCount {
	out := make([]patternCount, 0, len(counts))
	for p, c := range counts {
		out = append(out, patternCount{Pattern: p, Count: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Pattern < out[j].Pattern
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// agentAdoptionCmd implements `magus agent adoption`: read shell commands (stdin or --commands),
// report how often the graph was used versus grep.
func agentAdoptionCmd(args []string) error {
	fset := flag.NewFlagSet("agent adoption", flag.ContinueOnError)
	bindDisplayFlags(fset)
	var commandsFile string
	fset.StringVar(&commandsFile, "commands", "", "file of shell commands, one per line (default: stdin)")
	fset.Usage = func() { agentAdoptionUsage(os.Stderr) }
	if err := fset.Parse(args); err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	var src io.Reader = os.Stdin
	if commandsFile != "" {
		f, err := os.Open(commandsFile)
		if err != nil {
			return fmt.Errorf("agent adoption: %w", err)
		}
		defer f.Close()
		src = f
	} else if stdinIsTerminal() {
		agentAdoptionUsage(os.Stderr)
		return usagef("magus agent adoption: reads commands from stdin or --commands <file>")
	}

	var lines []string
	sc := bufio.NewScanner(src)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("agent adoption: read commands: %w", err)
	}
	report := analyzeAdoption(lines)

	if opts.Format == outputJSON {
		b, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, string(b))
		return nil
	}
	writeAdoptionTable(os.Stdout, report)
	return nil
}

func writeAdoptionTable(w io.Writer, r adoptionReport) {
	fmt.Fprintf(w, "agent adoption over %s commands:\n\n", humanCount(r.Total))
	fmt.Fprintf(w, "  graph verbs (query/refs/explain/path/graph)   %8s\n", humanCount(r.GraphVerbs))
	fmt.Fprintf(w, "  text searches (grep/rg/ag)                    %8s\n", humanCount(r.TextSearches))
	fmt.Fprintf(w, "  graph : search ratio                          %8s\n\n", ratioString(r.GraphVerbs, r.TextSearches))
	fmt.Fprintf(w, "  repo-wide search over source (refs/query)     %8s\n", humanCount(r.SearchOfSource))
	fmt.Fprintf(w, "  search over prose .md (docsection)            %8s\n", humanCount(r.SearchOfProse))
	fmt.Fprintf(w, "  file reads via shell (cat/head/tail/sed)      %8s\n", humanCount(r.FileReads))
	if len(r.TopSymbolGreps) > 0 {
		fmt.Fprintf(w, "\ntop repo-wide patterns that look like symbols, with the graph query to try:\n")
		for _, p := range r.TopSymbolGreps {
			fmt.Fprintf(w, "  %6s  %s  ->  %s\n", humanCount(p.Count), p.Pattern, p.Run)
		}
	}
}

// ratioString renders graph:search as 1:N (or N:1), the shape that reads at a glance.
func ratioString(graph, search int) string {
	switch {
	case graph == 0 && search == 0:
		return "n/a"
	case graph == 0:
		return fmt.Sprintf("0 : %d", search)
	case search == 0:
		return fmt.Sprintf("%d : 0", graph)
	case search >= graph:
		return fmt.Sprintf("1 : %d", (search+graph/2)/graph)
	default:
		return fmt.Sprintf("%d : 1", (graph+search/2)/search)
	}
}

func humanCount(n int) string {
	s := fmt.Sprintf("%d", n)
	if n < 1000 {
		return s
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	return strings.Join(append([]string{s}, parts...), ",")
}

func agentAdoptionUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus agent adoption [--commands <file>] [-o json]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Reports how often agents used the knowledge graph versus a raw text search,")
	fmt.Fprintln(w, "over a corpus of shell commands (one per line, from stdin or --commands).")
	fmt.Fprintln(w, "magus stays host-agnostic: it analyzes commands, never a host's session logs.")
	fmt.Fprintln(w, "Extract the shell commands your agent host recorded - one per line - and pipe")
	fmt.Fprintln(w, "them in. The magus documentation carries the per-host extraction recipe.")
}
