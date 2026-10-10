// Command judge-docs judges prose against the prose rules and writes a JSON
// array of findings on stdout.
//
// With no flag, or -kind doc-comment, it reads a JSON array of symbol records
// on stdin and judges each one's doc, reporting in input order and then the
// order [prose.Judge] reports them. With -kind reference it judges the
// Markdown files its arguments name, in argument order; -kind guide judges
// them as procedural pages, held to the guide rules as well; -kind
// agent-instructions judges them as instructions an agent loads as written,
// and -kind agent-instructions-template as template bodies that render into a
// short and a full form, each finding at its source line. With -kind
// change-description it reads a pull request on stdin, the title on the first
// line and the description after it, and with -kind review-reply a review
// comment or a reply in a thread on stdin. A finding from text names its file,
// or the kind it read from stdin, and its line as source, `path:line`, the way
// a symbol's index position reads.
//
// Each finding carries its rule's decision, advise or deny, its PRS code and
// the page that documents the rule. -decisions names a JSON table that sets
// rules off, advise or deny, for every file or for the files a glob matches;
// house style runs only where it names it. -only takes comma-separated rule
// names. A rule, a decision or a glob the judge cannot use is an error.
// -thread-length N tells the reply rules how many replies the author already
// posted in the thread. -catalog writes every rule as the docs render it.
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

	"github.com/egladman/magus/libs/conventions/prose"
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

// run returns the process exit code: 0 once the findings are written, 1 when
// the flags, the decisions or the input cannot be used. A finding is not a
// failure: the caller decides what one costs.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("judge-docs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	kindFlag := fs.String("kind", "", "judge `reference`, guide, agent-instructions or agent-instructions-template "+
		"files named as arguments, a change-description or review-reply on stdin, or doc-comment symbols on stdin")
	decisionsPath := fs.String("decisions", "", "read the decisions table from this `file`, or - for stdin")
	only := fs.String("only", "", "judge by these comma-separated `rules` alone")
	threadLength := fs.Int("thread-length", 0, "the `count` of replies the author already posted in the thread")
	catalog := fs.Bool("catalog", false, "write every rule as its reference page shows it, and judge nothing")

	if err := fs.Parse(args); err != nil {
		return 1
	}

	fail := func(err error) int {
		fmt.Fprintf(stderr, "judge-docs: %v\n", err)

		return 1
	}

	if *catalog {
		if err := json.NewEncoder(stdout).Encode(prose.Catalog()); err != nil {
			return fail(fmt.Errorf("write the catalog: %w", err))
		}

		return 0
	}

	kind := prose.Kind(*kindFlag)
	if kind == "" {
		kind = prose.KindDocComment
	}

	t, err := readTable(*decisionsPath, kind, stdin, fs.Args())
	if err != nil {
		return fail(err)
	}

	onlyRules, err := onlyList(*only, t)
	if err != nil {
		return fail(err)
	}

	opts := []prose.Option{prose.WithOnly(onlyRules...), prose.WithThreadLength(*threadLength)}

	out, err := judge(kind, fs.Args(), stdin, t, opts)
	if err != nil {
		return fail(err)
	}

	if err := json.NewEncoder(stdout).Encode(out); err != nil {
		return fail(fmt.Errorf("write findings: %w", err))
	}

	return 0
}

// readsFiles reports whether kind is judged from files named as arguments
// rather than from stdin.
func readsFiles(kind prose.Kind) bool {
	switch kind {
	case prose.KindReference, prose.KindGuide, prose.KindAgentInstructions, prose.KindAgentInstructionsTemplate:
		return true
	default:
		return false
	}
}

// onlyList reads -only. A rule the judge does not know is an error, and so is
// one t leaves off for every file, since naming it selects nothing.
func onlyList(list string, t table) ([]prose.Rule, error) {
	if list == "" {
		return nil, nil
	}

	house := map[prose.Rule]bool{}
	for _, doc := range prose.Catalog() {
		house[doc.Name] = doc.House
	}

	var out []prose.Rule

	for name := range strings.SplitSeq(list, ",") {
		r := prose.Rule(strings.TrimSpace(name))

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

func judge(kind prose.Kind, paths []string, stdin io.Reader, t table, opts []prose.Option) ([]finding, error) {
	switch {
	case kind == prose.KindDocComment:
		if len(paths) > 0 {
			return nil, errors.New("symbols are read from stdin; a path needs -kind reference")
		}

		return judgeSymbols(stdin, append(opts, prose.WithDecisions(t.rules)))
	case readsFiles(kind):
		return judgeFiles(paths, kind, t, opts)
	case kind == prose.KindChangeDescription || kind == prose.KindReviewReply:
		if len(paths) > 0 {
			return nil, fmt.Errorf("a %s is read from stdin, not from a path", kind)
		}

		text, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("read the %s: %w", kind, err)
		}

		return textFindings(string(kind), string(text), kind, append(opts, prose.WithDecisions(t.rules))), nil
	default:
		return nil, fmt.Errorf("unknown kind %q: want doc-comment, reference, guide, agent-instructions, "+
			"agent-instructions-template, change-description or review-reply", kind)
	}
}

func judgeFiles(paths []string, kind prose.Kind, t table, opts []prose.Option) ([]finding, error) {
	out := []finding{}

	for _, p := range paths {
		text, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}

		fileOpts := append(slices.Clone(opts), prose.WithDecisions(t.decisionsAt(p)))
		out = append(out, textFindings(p, string(text), kind, fileOpts)...)
	}

	return out, nil
}

func textFindings(name, text string, kind prose.Kind, opts []prose.Option) []finding {
	out := []finding{}

	for _, f := range prose.JudgeText(text, kind, opts...) {
		out = append(out, toFinding(name, name+":"+strconv.Itoa(f.Line), kind, f))
	}

	return out
}

func toFinding(node, source string, kind prose.Kind, f prose.Finding) finding {
	return finding{
		Node: node, Source: source, Kind: string(kind), Rule: string(f.Rule), Code: string(f.Code),
		Decision: string(f.Decision), Message: f.Message, Match: f.Match, URL: f.URL,
	}
}

func judgeSymbols(stdin io.Reader, opts []prose.Option) ([]finding, error) {
	records, err := decode(stdin)
	if err != nil {
		return nil, err
	}

	out := []finding{}

	for _, r := range records {
		symbol := prose.Symbol{
			Name:     r.Name,
			Owner:    r.Owner,
			Callable: r.Kind == "function" || r.Kind == "method",
			Doc:      r.Doc,
		}

		for _, f := range prose.Judge(symbol, opts...) {
			out = append(out, toFinding(r.Node, r.Source, prose.KindDocComment, f))
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
	rules map[prose.Rule]prose.Decision
	paths []scoped
}

type scoped struct {
	glob  string
	rules map[prose.Rule]prose.Decision
}

// decisionsAt returns the decisions for the file at p: t.rules, overridden by
// each glob that matches p in turn, so the last match wins.
func (t table) decisionsAt(p string) map[prose.Rule]prose.Decision {
	out := maps.Clone(t.rules)
	if out == nil {
		out = map[prose.Rule]prose.Decision{}
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
func (t table) turnsOn(r prose.Rule, house bool) bool {
	d, set := t.rules[r]
	if (set && d != prose.DecisionOff) || (!set && !house) {
		return true
	}

	for _, s := range t.paths {
		if d, ok := s.rules[r]; ok && d != prose.DecisionOff {
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
func readTable(p string, kind prose.Kind, stdin io.Reader, args []string) (table, error) {
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
func ruleDecisions(in map[string]string) (map[prose.Rule]prose.Decision, error) {
	known := prose.Rules()
	out := make(map[prose.Rule]prose.Decision, len(in))

	for _, name := range slices.Sorted(maps.Keys(in)) {
		r, d := prose.Rule(name), prose.Decision(in[name])
		if !slices.Contains(known, r) {
			return nil, fmt.Errorf("unknown rule %q", name)
		}

		switch d {
		case prose.DecisionOff, prose.DecisionAdvise, prose.DecisionDeny:
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
