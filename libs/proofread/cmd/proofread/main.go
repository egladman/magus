package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/libs/proofread"
)

// record is one symbol as `magus\symbols()` describes it.
type record struct {
	Node     string `json:"node"`
	Source   string `json:"source"`
	Language string `json:"language"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Owner    string `json:"owner"`
	Doc      string `json:"doc"`
}

// finding is one rule's verdict, carrying the judged text's identity so a
// caller can point at it. Its fields are the contract any other judge's
// findings meet.
type finding struct {
	Node     string `json:"node"`
	Source   string `json:"source"`
	Kind     string `json:"kind"`
	Rule     string `json:"rule"`
	Code     string `json:"code"`
	Decision string `json:"decision"`
	Message  string `json:"message"`
	Match    string `json:"match"`
	URL      string `json:"url"`
	// Line, Column, EndLine and EndColumn place Match in its file, 1-based,
	// the end just past it; each is omitted when it is not known.
	Line         int      `json:"line,omitempty"`
	Column       int      `json:"column,omitempty"`
	EndLine      int      `json:"end_line,omitempty"`
	EndColumn    int      `json:"end_column,omitempty"`
	Replacements []string `json:"replacements,omitempty"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// subcommand is one word the first argument may be. A kind judges text of
// that kind; rules and explain read the catalog.
type subcommand struct {
	name    string
	kind    proofread.Kind
	input   input
	operand string
	summary string
}

// input is where a subcommand's text comes from.
type input int

const (
	inputNone    input = iota // not a kind: it reads no text to judge
	inputSymbols              // a JSON array of symbol records on stdin
	inputFiles                // the files named as arguments
	inputStdin                // one text on stdin
)

// kindCommand returns the subcommand that judges kind.
func kindCommand(kind proofread.Kind) (subcommand, bool) {
	if kind == "" {
		return subcommand{}, false
	}

	i := slices.IndexFunc(subcommands, func(s subcommand) bool { return s.kind == kind })
	if i < 0 {
		return subcommand{}, false
	}

	return subcommands[i], true
}

var subcommands = []subcommand{
	{"doc-comment", proofread.KindDocComment, inputSymbols, "", "judge the doc of each symbol in a JSON array on stdin"},
	{"reference", proofread.KindReference, inputFiles, "FILE...", "judge Markdown pages a reader looks things up in"},
	{"guide", proofread.KindGuide, inputFiles, "FILE...", "judge procedural pages, held to the guide rules too"},
	{"agent-brief", proofread.KindAgentBrief, inputFiles, "FILE...", "judge hand-written instructions an agent loads as written"},
	{"agent-compact", proofread.KindAgentCompact, inputFiles, "FILE...", "judge grammar-free text only an agent reads"},
	{"skill-template", proofread.KindSkillTemplate, inputFiles, "FILE...", "judge skill template bodies that render into a short and a full form"},
	{"change-description", proofread.KindChangeDescription, inputStdin, "", "judge a pull request on stdin, its title on the first line"},
	{"review-summary", proofread.KindReviewSummary, inputStdin, "", "judge a review's body on stdin, its verdict in -event"},
	{"review-inline", proofread.KindReviewInline, inputStdin, "", "judge a review comment on a line of the diff, on stdin"},
	{"review-reply", proofread.KindReviewReply, inputStdin, "", "judge a reply in a review thread or a pull request comment, on stdin"},
	{"diagnostic", proofread.KindDiagnostic, inputStdin, "", "judge one diagnostic a program prints, on stdin"},
	{"commit-message", proofread.KindCommitMessage, inputStdin, "[FILE]", "judge a commit message from FILE or stdin, its subject on the first line"},
	{"cli-help", proofread.KindCLIHelp, inputStdin, "", "judge a command's or a flag's help text on stdin"},
	{"issue", proofread.KindIssue, inputStdin, "", "judge an issue on stdin, its title on the first line"},
	{"release-notes", proofread.KindReleaseNotes, inputFiles, "FILE...", "judge the notes a release ships with"},
	{"changelog", proofread.KindChangelog, inputFiles, "FILE...", "judge a changelog or its fragments, in the Keep a Changelog shape"},
	{"agent-reply", proofread.KindAgentReply, inputStdin, "", "judge what a coding agent writes back to its person, on stdin"},
	{"tool-description", proofread.KindToolDescription, inputStdin, "", "judge a tool's or a skill's description, on stdin"},
	{"stylesheet", proofread.KindStylesheet, inputStdin, "[FILE...]", "judge the comments of stylesheets, or of one on stdin"},
	{"rules", "", inputNone, "", "write every rule as the docs render it, as JSON"},
	{"calibrate", "", inputNone, "", "replay the labeled cases and print each rule's precision and recall"},
	{"stats", "", inputNone, "", "print each rule's recorded outcomes and not-useful rate"},
	{"explain", "", inputNone, "RULE|CODE", "print what a rule catches, why, its default decisions and its page"},
	{"voice build", "", inputNone, "[FILE...]", "measure your own texts into the voice file -voice reads"},
	{"voice eval", "", inputNone, "", "score which shape features tell -owner's JSON-lines texts from -other's, per kind; -pool N measures chunks of N+ words, -seed S shuffles them"},
}

// usage lists every subcommand and the flags a kind takes.
func usage() string {
	var b strings.Builder

	b.WriteString("usage: proofread <subcommand> [flags] [operand]\n\nsubcommands:\n")

	for _, s := range subcommands {
		fmt.Fprintf(&b, "  %-28s %-10s %s\n", s.name, s.operand, s.summary)
	}

	b.WriteString("\nflags, after a kind:\n")
	b.WriteString("  -decisions FILE    read the decisions table from FILE, or - for stdin\n")
	b.WriteString("  -only RULES        judge by these comma-separated rules alone\n")
	b.WriteString("  -thread-length N   the count of replies the author already posted in the thread\n")
	b.WriteString("  -event EVENT       the verdict a review summary is posted with: APPROVE, REQUEST_CHANGES or COMMENT\n")
	b.WriteString("  -anchored          with review-reply, judge as review-inline\n")
	b.WriteString("  -voice FILE        judge with the voice file proofread voice build wrote\n")
	b.WriteString("  -format FORMAT     write findings as json (default), sarif, rdjson or text\n")
	b.WriteString("  -fail-on DECISION  exit 1 when a finding at or above deny, advise or never remains (default never)\n")
	b.WriteString("  -record            keep this run's findings on this machine, to count what later runs fix\n")
	b.WriteString("  -baseline FILE     write only the findings past the counts FILE holds\n")
	b.WriteString("  -prune             with -baseline, rewrite FILE to this run's counts and write no findings\n")
	b.WriteString("  -metrics           write each text's readability metrics instead of its findings\n")
	b.WriteString("\nvoice build flags: -kind, -field, -kind-field, -o; voice eval flags: -owner, -other, -kind, -field, -kind-field, -format, -pool, -seed\n")
	b.WriteString("\nA text silences a finding with \"<!-- proofread off RULE: REASON -->\" in Markdown or \"proofread:ignore RULE REASON\" in a doc comment.\n")
	b.WriteString("Findings are a JSON array on stdout. Exit codes: 0 judged with no finding at the -fail-on bar, 1 a finding at it, 2 unusable subcommand, flags, decisions or input.\n")

	return b.String()
}

// The process exit codes: 0 for a run with no finding at the -fail-on bar, 1
// for one with a finding at it, 2 for a run that could not judge.
const (
	exitOK       = 0
	exitFindings = 1
	exitError    = 2
)

// run returns the process exit code. A finding fails the run only at the
// -fail-on bar; the caller otherwise decides what one costs.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage())

		return exitError
	}

	name, rest := args[0], args[1:]

	switch name {
	case "rules":
		return runRules(rest, stdout, stderr)
	case "explain":
		return runExplain(rest, stdout, stderr)
	case "calibrate":
		return runCalibrate(rest, stdin, stdout, stderr)
	case "stats":
		return runStats(rest, stdout, stderr)
	case "voice":
		return runVoice(rest, stdin, stdout, stderr)
	}

	if kind, ok := proofread.ParseKind(name); ok { // compat: see proofread's kindAliases
		if s, ok := kindCommand(kind); ok {
			return runKind(s.kind, rest, stdin, stdout, stderr)
		}
	}

	fmt.Fprintf(stderr, "proofread: unknown subcommand %q\n\n%s", name, usage())

	return exitError
}

func failure(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "proofread: %v\n", err)

	return exitError
}

// reachesBar reports whether a finding in out is at or above the failOn
// decision: deny, advise or never.
func reachesBar(failOn string, out []finding) bool {
	for _, f := range out {
		switch failOn {
		case "deny":
			if f.Decision == string(proofread.DecisionDeny) {
				return true
			}
		case "advise":
			return true
		}
	}

	return false
}

func runKind(kind proofread.Kind, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("proofread "+string(kind), flag.ContinueOnError)
	fs.SetOutput(stderr)
	decisionsPath := fs.String("decisions", "", "read the decisions table from this `file`, or - for stdin")
	only := fs.String("only", "", "judge by these comma-separated `rules` alone")
	threadLength := fs.Int("thread-length", 0, "the `count` of replies the author already posted in the thread")
	event := fs.String("event", "", "the `verdict` a review summary is posted with: APPROVE, REQUEST_CHANGES or COMMENT")
	anchored := fs.Bool("anchored", false, "with review-reply, judge as review-inline")
	voicePath := fs.String("voice", "", "judge with the voice `file` proofread voice build wrote")
	format := fs.String("format", "json", "write findings as `json`, sarif, rdjson or text")
	failOn := fs.String("fail-on", "never", "exit 1 when a finding at or above this `decision` remains: deny, advise or never")
	record := fs.Bool("record", false, "keep this run's findings on this machine to count what later runs fix")
	baselinePath := fs.String("baseline", "", "report only findings past the counts this baseline `file` holds")
	prune := fs.Bool("prune", false, "with -baseline, rewrite the file to the counts this run found")
	metrics := fs.Bool("metrics", false, "write each text's readability metrics instead of its findings")

	if err := fs.Parse(args); err != nil {
		return exitError
	}

	// compat(until: no caller passes -anchored, the guard and the docs/guides
	// proofread-reply scripts included): before review-inline was a kind, an inline
	// comment was a review-reply with this flag.
	if *anchored && kind == proofread.KindReviewReply {
		kind = proofread.KindReviewInline
	}

	if *metrics {
		return runMetrics(kind, fs.Args(), stdin, stdout, stderr)
	}

	if !slices.Contains([]string{"", "APPROVE", "REQUEST_CHANGES", "COMMENT"}, *event) {
		return failure(stderr, fmt.Errorf("unknown -event %q: want APPROVE, REQUEST_CHANGES or COMMENT", *event))
	}

	if !slices.Contains([]string{"deny", "advise", "never"}, *failOn) {
		return failure(stderr, fmt.Errorf("unknown -fail-on %q: want deny, advise or never", *failOn))
	}

	if *record && !fromFiles(kind, fs.Args()) {
		return failure(stderr, errors.New("-record needs a file to judge, since an outcome is kept per path"))
	}

	t, err := readTable(*decisionsPath, kind, stdin, fs.Args())
	if err != nil {
		return failure(stderr, err)
	}

	onlyRules, err := onlyList(*only, t)
	if err != nil {
		return failure(stderr, err)
	}

	opts := []proofread.Option{
		proofread.WithOnly(onlyRules...), proofread.WithThreadLength(*threadLength), proofread.WithEvent(proofread.Event(*event)),
	}

	if *voicePath != "" {
		v, err := readVoiceFile(*voicePath)
		if err != nil {
			return failure(stderr, err)
		}

		opts = append(opts, proofread.WithVoice(v))
	}

	out, err := judge(kind, fs.Args(), stdin, t, opts)
	if err != nil {
		return failure(stderr, err)
	}

	if *record {
		if err := recordRun(kind, fs.Args(), t, onlyRules, opts, out); err != nil {
			return failure(stderr, err)
		}
	}

	out, err = applyBaseline(*baselinePath, *prune, out)
	if err != nil {
		return failure(stderr, err)
	}

	if err := writeFindings(stdout, *format, out); err != nil {
		return failure(stderr, err)
	}

	if *format == "text" {
		fmt.Fprint(stderr, textSummary(out))
	}

	if reachesBar(*failOn, out) {
		return exitFindings
	}

	return exitOK
}

func runRules(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		return failure(stderr, errors.New("rules takes no arguments"))
	}

	if err := json.NewEncoder(stdout).Encode(proofread.Catalog()); err != nil {
		return failure(stderr, fmt.Errorf("write the rules: %w", err))
	}

	return exitOK
}

func runExplain(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return failure(stderr, errors.New("explain takes one rule name or code"))
	}

	for _, doc := range proofread.Catalog() {
		if strings.EqualFold(string(doc.Name), args[0]) || strings.EqualFold(string(doc.Code), args[0]) {
			writeExplanation(stdout, doc)

			return exitOK
		}
	}

	return failure(stderr, fmt.Errorf("unknown rule or code %q", args[0]))
}

func writeExplanation(w io.Writer, doc proofread.RuleDoc) {
	fmt.Fprintf(w, "%s (%s)\n\nCatches: %s\n", doc.Name, doc.Code, doc.Catches)

	if doc.Why != "" {
		fmt.Fprintf(w, "Why: %s\n", doc.Why)
	}

	if doc.House {
		fmt.Fprintln(w, "House style: runs only where a decisions table names it.")
	}

	fmt.Fprintln(w, "\nDefault decisions:")

	for _, k := range doc.Kinds {
		fmt.Fprintf(w, "  %-28s %s\n", k, doc.Decisions[k])
	}

	fmt.Fprintf(w, "\nPage: %s\n", doc.URL())
}

// readsFiles reports whether kind is judged from files named as arguments
// rather than from stdin.
func readsFiles(kind proofread.Kind) bool {
	s, ok := kindCommand(kind)

	return ok && s.input == inputFiles
}

// onlyList reads -only. A rule proofread does not know is an error, and so is
// one t leaves off for every file, since naming it selects nothing.
func onlyList(list string, t table) ([]proofread.Rule, error) {
	if list == "" {
		return nil, nil
	}

	house := map[proofread.Rule]bool{}
	for _, doc := range proofread.Catalog() {
		house[doc.Name] = doc.House
	}

	var out []proofread.Rule

	for name := range strings.SplitSeq(list, ",") {
		r := proofread.Rule(strings.TrimSpace(name))

		isHouse, known := house[r]
		if !known {
			return nil, fmt.Errorf("unknown rule %q", r)
		}

		if !t.turnsOn(r, isHouse) {
			return nil, fmt.Errorf("-only names %q, which is off: set it to advise or deny in -decisions", r)
		}

		out = append(out, r)
	}

	return out, nil
}

func judge(kind proofread.Kind, paths []string, stdin io.Reader, t table, opts []proofread.Option) ([]finding, error) {
	cmd, _ := kindCommand(kind)

	switch {
	case cmd.input == inputSymbols:
		if len(paths) > 0 {
			return nil, errors.New("a path needs the reference subcommand, since symbols are read from stdin")
		}

		return judgeSymbols(stdin, append(opts, proofread.WithDecisions(t.rules)))
	case kind == proofread.KindCommitMessage && len(paths) > 1:
		return nil, errors.New("commit-message takes one message file, or reads stdin")
	case fromFiles(kind, paths):
		return judgeFiles(paths, kind, t, opts)
	case cmd.input == inputStdin:
		if len(paths) > 0 {
			return nil, fmt.Errorf("a %s is read from stdin, not from a path", kind)
		}

		text, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("read the %s: %w", kind, err)
		}

		return textFindings(string(kind), string(text), kind, append(opts, proofread.WithDecisions(t.rules))), nil
	default:
		return nil, fmt.Errorf("no judge for kind %q", kind)
	}
}

func judgeFiles(paths []string, kind proofread.Kind, t table, opts []proofread.Option) ([]finding, error) {
	out := []finding{}

	for _, p := range paths {
		text, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}

		fileOpts := append(slices.Clone(opts), proofread.WithDecisions(t.decisionsAt(p)))
		out = append(out, fileFindings(p, string(text), kind, fileOpts)...)
	}

	return out, nil
}

// fileFindings judges the text of the file p with opts already carrying the
// decisions that apply to it.
func fileFindings(p, text string, kind proofread.Kind, opts []proofread.Option) []finding {
	if kind == proofread.KindCommitMessage {
		return commitMessageFindings(p, text, opts)
	}

	return textFindings(p, text, kind, opts)
}

func textFindings(name, text string, kind proofread.Kind, opts []proofread.Option) []finding {
	out := []finding{}

	for _, f := range proofread.JudgeText(text, kind, opts...) {
		out = append(out, toFinding(name, sourceAt(name, f.Line), kind, f))
	}

	return out
}

// sourceAt is "name:line", or name alone for a finding about the whole text,
// which has no line.
func sourceAt(name string, line int) string {
	if line == 0 {
		return name
	}

	return name + ":" + strconv.Itoa(line)
}

func toFinding(node, source string, kind proofread.Kind, f proofread.Finding) finding {
	return finding{
		Node: node, Source: source, Kind: string(kind), Rule: string(f.Rule), Code: string(f.Code),
		Decision: string(f.Decision), Message: f.Message, Match: f.Match, URL: f.URL,
		Line: f.Line, Column: f.Column, EndLine: f.EndLine, EndColumn: f.EndColumn, Replacements: f.Replacements,
	}
}

func judgeSymbols(stdin io.Reader, opts []proofread.Option) ([]finding, error) {
	records, err := decode(stdin)
	if err != nil {
		return nil, err
	}

	out := []finding{}

	for _, r := range records {
		symbol := proofread.Symbol{
			Name:     r.Name,
			Owner:    r.Owner,
			Callable: r.Kind == "function" || r.Kind == "method",
			Doc:      r.Doc,
		}

		for _, f := range proofread.Judge(symbol, opts...) {
			out = append(out, toFinding(r.Node, r.Source, proofread.KindDocComment, f))
		}
	}

	return out, nil
}

func decode(stdin io.Reader) ([]record, error) {
	dec := json.NewDecoder(stdin)
	dec.DisallowUnknownFields()

	var records []record
	if err := dec.Decode(&records); err != nil {
		return nil, fmt.Errorf("read symbols: %w", err)
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("read symbols: data after the array")
	}

	return records, nil
}

// table is a decisions file: decisions for every file, then decisions for the
// files each glob matches, in the order the file lists them.
type table struct {
	rules map[proofread.Rule]proofread.Decision
	paths []scoped
}

type scoped struct {
	glob  string
	rules map[proofread.Rule]proofread.Decision
}

// decisionsAt returns the decisions for the file at p: t.rules, overridden by
// each glob that matches p in turn, so the last match wins.
func (t table) decisionsAt(p string) map[proofread.Rule]proofread.Decision {
	out := maps.Clone(t.rules)
	if out == nil {
		out = map[proofread.Rule]proofread.Decision{}
	}

	for _, s := range t.paths {
		if globMatch(s.glob, argumentPath(p)) {
			maps.Copy(out, s.rules)
		}
	}

	return out
}

// turnsOn reports whether t runs rule r for some file: by its default, unless
// t.rules sets it off, or by a glob's entry.
func (t table) turnsOn(r proofread.Rule, house bool) bool {
	d, set := t.rules[r]
	if (set && d != proofread.DecisionOff) || (!set && !house) {
		return true
	}

	for _, s := range t.paths {
		if d, ok := s.rules[r]; ok && d != proofread.DecisionOff {
			return true
		}
	}

	return false
}

// argumentPath is a file argument as a glob reads it: slash-separated, with
// any leading "./" dropped. A glob matches the argument as given, so a
// caller passes workspace-relative paths to match workspace-relative globs.
func argumentPath(p string) string { return strings.TrimPrefix(filepath.ToSlash(p), "./") }

// readTable reads the decisions file at p, or stdin for "-", and checks it
// against the rules and against args. An empty p is an empty table.
func readTable(p string, kind proofread.Kind, stdin io.Reader, args []string) (table, error) {
	if p == "" {
		return table{}, nil
	}

	var data []byte

	var err error

	if p == "-" {
		if !fromFiles(kind, args) {
			return table{}, fmt.Errorf("-decisions -: name the table's file, since the %s itself is read from stdin", kind)
		}

		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(p)
	}

	if err != nil {
		return table{}, fmt.Errorf("read decisions %s: %w", p, err)
	}

	t, err := parseTable(data)
	if err != nil {
		return table{}, fmt.Errorf("decisions %s: %w", p, err)
	}

	for _, s := range t.paths {
		if !slices.ContainsFunc(args, func(a string) bool { return globMatch(s.glob, argumentPath(a)) }) {
			return table{}, fmt.Errorf("decisions %s: path %q matches no file argument", p, s.glob)
		}
	}

	return t, nil
}

// parseTable reads {"rules": {rule: decision}, "paths": {glob: {rule:
// decision}}}, keeping the globs in the order the file lists them.
func parseTable(data []byte) (table, error) {
	var raw struct {
		Rules map[string]string `json:"rules"`
		Paths json.RawMessage   `json:"paths"`
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&raw); err != nil {
		return table{}, err
	}

	rules, err := ruleDecisions(raw.Rules)
	if err != nil {
		return table{}, err
	}

	t := table{rules: rules}

	if len(raw.Paths) == 0 || string(raw.Paths) == "null" {
		return t, nil
	}

	pd := json.NewDecoder(bytes.NewReader(raw.Paths))
	if tok, err := pd.Token(); err != nil || tok != json.Delim('{') {
		return table{}, errors.New("paths: want an object of globs")
	}

	for pd.More() {
		tok, err := pd.Token()
		if err != nil {
			return table{}, fmt.Errorf("paths: %w", err)
		}

		glob, _ := tok.(string)
		if err := checkGlob(glob); err != nil {
			return table{}, err
		}

		var entry map[string]string
		if err := pd.Decode(&entry); err != nil {
			return table{}, fmt.Errorf("path %q: %w", glob, err)
		}

		scopedRules, err := ruleDecisions(entry)
		if err != nil {
			return table{}, fmt.Errorf("path %q: %w", glob, err)
		}

		t.paths = append(t.paths, scoped{glob: glob, rules: scopedRules})
	}

	return t, nil
}

// ruleDecisions checks each name against the rules and each value against the
// three decisions.
func ruleDecisions(in map[string]string) (map[proofread.Rule]proofread.Decision, error) {
	known := proofread.Rules()
	out := make(map[proofread.Rule]proofread.Decision, len(in))

	for _, name := range slices.Sorted(maps.Keys(in)) {
		r, d := proofread.Rule(name), proofread.Decision(in[name])
		if !slices.Contains(known, r) {
			return nil, fmt.Errorf("unknown rule %q", name)
		}

		switch d {
		case proofread.DecisionOff, proofread.DecisionAdvise, proofread.DecisionDeny:
		default:
			return nil, fmt.Errorf("rule %q: unknown decision %q: want off, advise or deny", name, in[name])
		}

		out[r] = d
	}

	return out, nil
}

// checkGlob refuses an empty glob and one path.Match cannot read.
func checkGlob(glob string) error {
	if glob == "" {
		return errors.New("paths: an empty glob")
	}

	for seg := range strings.SplitSeq(glob, "/") {
		if _, err := path.Match(seg, ""); err != nil {
			return fmt.Errorf("path %q: %w", glob, err)
		}
	}

	return nil
}

// globMatch matches name against glob a segment at a time with path.Match,
// where a "**" segment matches any number of segments, none included.
func globMatch(glob, name string) bool {
	return matchSegments(strings.Split(glob, "/"), strings.Split(name, "/"))
}

func matchSegments(glob, name []string) bool {
	for len(glob) > 0 {
		if glob[0] == "**" {
			for i := range len(name) + 1 {
				if matchSegments(glob[1:], name[i:]) {
					return true
				}
			}

			return false
		}

		if len(name) == 0 {
			return false
		}

		if ok, _ := path.Match(glob[0], name[0]); !ok {
			return false
		}

		glob, name = glob[1:], name[1:]
	}

	return len(name) == 0
}
