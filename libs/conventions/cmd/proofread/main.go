// Command proofread judges written text against the proofread rules and
// writes a JSON array of findings on stdout.
//
// Its first argument names what to do. A kind judges text of that kind:
// doc-comment reads a JSON array of symbol records on stdin and judges each
// one's doc, reporting in input order and then the order [proofread.Judge]
// reports them. reference judges the Markdown files its arguments name, in
// argument order; guide judges them as procedural pages, held to the guide
// rules as well; agent-instructions judges them as instructions an agent
// loads as written, and agent-instructions-template as template bodies that
// render into a short and a full form, each finding at its source line.
// change-description reads a pull request on stdin, the title on the first
// line and the description after it, and review-reply a review comment or a
// reply in a thread on stdin. A finding from text names its file, or the kind
// it read from stdin, and its line as source, `path:line`, the way a symbol's
// index position reads. rules writes every rule as the docs render it, and
// explain prints one rule, named by its name or its code.
//
// Each finding carries its rule's decision, advise or deny, its PRF code and
// the page that documents the rule. -decisions names a JSON table that sets
// rules off, advise or deny, for every file or for the files a glob matches;
// house style runs only where it names it. -only takes comma-separated rule
// names. A rule, a decision or a glob proofread cannot use is an error.
// -thread-length N tells the reply rules how many replies the author already
// posted in the thread. The flags follow the kind.
//
// This repository's lint rules and its pull request guard and CI step run it.
// The magus module never imports libs/conventions, so the rules stay this
// repository's policy.
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

	"github.com/egladman/magus/libs/conventions/proofread"
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
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// subcommand is one word the first argument may be. A kind judges text of
// that kind; rules and explain read the catalog.
type subcommand struct {
	name    string
	kind    proofread.Kind
	operand string
	summary string
}

var subcommands = []subcommand{
	{"doc-comment", proofread.KindDocComment, "", "judge the doc of each symbol in a JSON array on stdin"},
	{"reference", proofread.KindReference, "FILE...", "judge Markdown pages a reader looks things up in"},
	{"guide", proofread.KindGuide, "FILE...", "judge procedural pages, held to the guide rules too"},
	{"agent-instructions", proofread.KindAgentInstructions, "FILE...", "judge instructions an agent loads as written"},
	{"agent-instructions-template", proofread.KindAgentInstructionsTemplate, "FILE...", "judge template bodies that render into a short and a full form"},
	{"change-description", proofread.KindChangeDescription, "", "judge a pull request on stdin, its title on the first line"},
	{"review-reply", proofread.KindReviewReply, "", "judge a review comment or a reply on stdin"},
	{"rules", "", "", "write every rule as the docs render it, as JSON"},
	{"explain", "", "RULE|CODE", "print what a rule catches, why, its default decisions and its page"},
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

	return b.String()
}

// run returns the process exit code: 0 once the output is written, 1 when
// the subcommand, the flags, the decisions or the input cannot be used. A
// finding is not a failure: the caller decides what one costs.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage())

		return 1
	}

	name, rest := args[0], args[1:]

	switch name {
	case "rules":
		return runRules(rest, stdout, stderr)
	case "explain":
		return runExplain(rest, stdout, stderr)
	}

	for _, s := range subcommands {
		if s.kind != "" && s.name == name {
			return runKind(s.kind, rest, stdin, stdout, stderr)
		}
	}

	fmt.Fprintf(stderr, "proofread: unknown subcommand %q\n\n%s", name, usage())

	return 1
}

func failure(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "proofread: %v\n", err)

	return 1
}

func runKind(kind proofread.Kind, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("proofread "+string(kind), flag.ContinueOnError)
	fs.SetOutput(stderr)
	decisionsPath := fs.String("decisions", "", "read the decisions table from this `file`, or - for stdin")
	only := fs.String("only", "", "judge by these comma-separated `rules` alone")
	threadLength := fs.Int("thread-length", 0, "the `count` of replies the author already posted in the thread")

	if err := fs.Parse(args); err != nil {
		return 1
	}

	t, err := readTable(*decisionsPath, kind, stdin, fs.Args())
	if err != nil {
		return failure(stderr, err)
	}

	onlyRules, err := onlyList(*only, t)
	if err != nil {
		return failure(stderr, err)
	}

	opts := []proofread.Option{proofread.WithOnly(onlyRules...), proofread.WithThreadLength(*threadLength)}

	out, err := judge(kind, fs.Args(), stdin, t, opts)
	if err != nil {
		return failure(stderr, err)
	}

	if err := json.NewEncoder(stdout).Encode(out); err != nil {
		return failure(stderr, fmt.Errorf("write findings: %w", err))
	}

	return 0
}

func runRules(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		return failure(stderr, errors.New("rules takes no arguments"))
	}

	if err := json.NewEncoder(stdout).Encode(proofread.Catalog()); err != nil {
		return failure(stderr, fmt.Errorf("write the rules: %w", err))
	}

	return 0
}

func runExplain(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return failure(stderr, errors.New("explain takes one rule name or code"))
	}

	for _, doc := range proofread.Catalog() {
		if strings.EqualFold(string(doc.Name), args[0]) || strings.EqualFold(string(doc.Code), args[0]) {
			writeExplanation(stdout, doc)

			return 0
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
	switch kind {
	case proofread.KindReference, proofread.KindGuide, proofread.KindAgentInstructions, proofread.KindAgentInstructionsTemplate:
		return true
	default:
		return false
	}
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
	switch {
	case kind == proofread.KindDocComment:
		if len(paths) > 0 {
			return nil, errors.New("symbols are read from stdin; a path needs the reference subcommand")
		}

		return judgeSymbols(stdin, append(opts, proofread.WithDecisions(t.rules)))
	case readsFiles(kind):
		return judgeFiles(paths, kind, t, opts)
	case kind == proofread.KindChangeDescription || kind == proofread.KindReviewReply:
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
		out = append(out, textFindings(p, string(text), kind, fileOpts)...)
	}

	return out, nil
}

func textFindings(name, text string, kind proofread.Kind, opts []proofread.Option) []finding {
	out := []finding{}

	for _, f := range proofread.JudgeText(text, kind, opts...) {
		out = append(out, toFinding(name, name+":"+strconv.Itoa(f.Line), kind, f))
	}

	return out
}

func toFinding(node, source string, kind proofread.Kind, f proofread.Finding) finding {
	return finding{
		Node: node, Source: source, Kind: string(kind), Rule: string(f.Rule), Code: string(f.Code),
		Decision: string(f.Decision), Message: f.Message, Match: f.Match, URL: f.URL,
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
		if !readsFiles(kind) {
			return table{}, fmt.Errorf("-decisions -: the %s itself is read from stdin; name the table's file", kind)
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
