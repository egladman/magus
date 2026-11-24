package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/diagnostics"
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
	// Weight is what the finding costs its text's grade, 0 on a deny finding.
	Weight float64 `json:"weight"`
	// WeightSet marks a Weight the kind's declaration set, which no labeling
	// round measured.
	WeightSet bool `json:"weight_set,omitempty"`
	// Line, Column, EndLine and EndColumn place Match in its file, 1-based,
	// the end just past it; each is omitted when it is not known.
	Line         int      `json:"line,omitempty"`
	Column       int      `json:"column,omitempty"`
	EndLine      int      `json:"end_line,omitempty"`
	EndColumn    int      `json:"end_column,omitempty"`
	Replacements []string `json:"replacements,omitempty"`
	// Protected are the specifics Match holds that a fix should keep.
	Protected []proofread.Protected `json:"protected,omitempty"`
}

func main() {
	// The composition point for proofread's catalog: a PRF prefix that clashes with
	// another registered one is a build defect, so it stops here.
	if err := diagnostics.RegisterCatalog("proofread", proofread.Codes()); err != nil {
		fmt.Fprintf(os.Stderr, "proofread: %v\n", err)
		os.Exit(1)
	}
	command.Main(root())
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

// kindCommand returns the subcommand that judges kind. A name that is no
// built-in kind is one a decisions table may declare, read from files or
// stdin.
func kindCommand(kind proofread.Kind) (subcommand, bool) {
	if kind == "" {
		return subcommand{}, false
	}

	i := slices.IndexFunc(subcommands, func(s subcommand) bool { return s.kind == kind })
	if i < 0 {
		return subcommand{string(kind), kind, inputStdin, "FILE...|--stdin", "judge text of a declared kind"}, true
	}

	return subcommands[i], true
}

// declared reports whether kind is no built-in kind, so only a decisions
// table can declare it.
func declared(kind proofread.Kind) bool {
	_, builtin := proofread.ParseKind(string(kind))

	return !builtin
}

var subcommands = []subcommand{
	{"doc-comment", proofread.KindDocComment, inputSymbols, "--stdin", "judge the doc of each symbol in a JSON array on stdin"},
	{"reference", proofread.KindReference, inputFiles, "FILE...", "judge Markdown pages a reader looks things up in"},
	{"principle", proofread.KindPrinciple, inputFiles, "FILE...", "judge design-principle pages, argued in the author's voice"},
	{"guide", proofread.KindGuide, inputFiles, "FILE...", "judge procedural pages, held to the guide rules too"},
	{"agent-brief", proofread.KindAgentBrief, inputFiles, "FILE...", "judge hand-written instructions an agent loads as written"},
	{"agent-compact", proofread.KindAgentCompact, inputFiles, "FILE...", "judge grammar-free text only an agent reads"},
	{"skill-template", proofread.KindSkillTemplate, inputFiles, "FILE...", "judge skill template bodies that render into a short and a full form"},
	{"change-description", proofread.KindChangeDescription, inputStdin, "--stdin", "judge a pull request on stdin, its title on the first line"},
	{"review-summary", proofread.KindReviewSummary, inputStdin, "--stdin", "judge a review's body on stdin, its verdict in --event"},
	{"review-inline", proofread.KindReviewInline, inputStdin, "--stdin", "judge a review comment on a line of the diff, on stdin"},
	{"review-reply", proofread.KindReviewReply, inputStdin, "--stdin", "judge a reply in a review thread or a pull request comment, on stdin"},
	{"diagnostic", proofread.KindDiagnostic, inputStdin, "--stdin", "judge one diagnostic a program prints, on stdin"},
	{"commit-message", proofread.KindCommitMessage, inputStdin, "FILE|--stdin", "judge a commit message from FILE or stdin, its subject on the first line"},
	{"cli-help", proofread.KindCLIHelp, inputStdin, "--stdin", "judge a command's or a flag's help text on stdin"},
	{"issue", proofread.KindIssue, inputStdin, "--stdin", "judge an issue on stdin, its title on the first line"},
	{"release-notes", proofread.KindReleaseNotes, inputFiles, "FILE...", "judge the notes a release ships with"},
	{"changelog", proofread.KindChangelog, inputFiles, "FILE...", "judge a changelog or its fragments, in the Keep a Changelog shape"},
	{"agent-reply", proofread.KindAgentReply, inputStdin, "--stdin", "judge what a coding agent writes back to its person, on stdin"},
	{"tool-description", proofread.KindToolDescription, inputStdin, "--stdin", "judge a tool's or a skill's description, on stdin"},
	{"stylesheet", proofread.KindStylesheet, inputStdin, "FILE...|--stdin", "judge the comments of stylesheets, or of one on stdin"},
	{"code-comment", proofread.KindCodeComment, inputStdin, "FILE...|--stdin", "judge the comments of Go, TypeScript and Buzz files, or of one on stdin"},
}

// The process exit codes: 0 for a run with no deny finding, 1 for one with a
// deny finding, a grade under its minimum included, 2 for a run that could not
// judge.
const (
	exitOK       = 0
	exitFindings = 1
	exitError    = 2
)

// root is the proofread command: a child for each built-in kind and for the
// catalog and voice commands. A name no child holds is a kind a decisions table
// may declare, or an alias of a built-in kind, which judgeDeclared resolves.
func root() command.Command {
	r := rootCmd
	r.Children = append([]command.Command{rulesCommand(), statsCommand(), explainCommand(), voiceCmd}, kindChildren()...)

	return r
}

// rootCmd is the root less its children, which root fills.
var rootCmd = command.Command{
	Name:  "proofread",
	Short: "judge prose and doc comments by rule",
	Long: "A text silences a finding with \"<!-- proofread off RULE: REASON -->\" in Markdown or \"proofread:ignore RULE REASON\" in a doc comment.\n\n" +
		"Findings are a JSON array on stdout. A name that is no subcommand judges text of the kind the --decisions table declares.",
	Usage: "proofread <subcommand> [flags] [operand]",
	ExitStatus: []command.ExitCode{
		{Code: exitOK, Meaning: "no deny finding"},
		{Code: exitFindings, Meaning: "a deny finding, a grade under its minimum included"},
		{Code: exitError, Meaning: "unusable subcommand, flags, decisions or input"},
	},
	Run: judgeDeclared,
}

// kindChildren is the command that judges each built-in kind.
func kindChildren() []command.Command {
	out := make([]command.Command, 0, len(subcommands))

	for _, s := range subcommands {
		out = append(out, judgeCommand(s.name, s.kind, s.operand, s.summary))
	}

	return out
}

// judgeDeclared judges text of a kind no child names: an alias of a built-in
// kind, or one a decisions table declares, which runKind resolves once it has read
// the table.
func judgeDeclared(ctx context.Context, inv command.Invocation) error {
	if len(inv.Args) == 0 {
		return exit.Usagef("a subcommand is required: `%s -h` lists them", inv.Path)
	}

	name := inv.Args[0]
	kind := proofread.Kind(name)

	if k, ok := proofread.ParseKind(name); ok { // compat: see proofread's kindAliases
		kind = k
	}

	c := judgeCommand(name, kind, "FILE...|--stdin", "judge text of a kind the --decisions table declares")
	c.Name = "proofread " + name

	return reported(command.Execute(ctx, c, inv.Args[1:], inv.Streams))
}

// reported turns a status that was already explained on stderr into the error
// that exits with it.
func reported(code int) error {
	if code == exitOK {
		return nil
	}

	return exit.Reported(code)
}

// run returns the process exit code. A deny finding fails the run, a grade
// under its kind's minimum among them; an advise finding only lowers the grade.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return command.Execute(context.Background(), root(), args, command.Streams{Stdin: stdin, Stdout: stdout, Stderr: stderr})
}

func unknownSubcommand(stderr io.Writer, name string) int {
	fmt.Fprintf(stderr, "proofread: unknown subcommand %q: `proofread -h` lists them\n", name)

	return exitError
}

// checkInput holds a kind to one source of text: stdin under --stdin, or the
// files named as arguments.
func checkInput(kind proofread.Kind, fromStdin bool, args []string) error {
	cmd, _ := kindCommand(kind)

	switch {
	case fromStdin && len(args) > 0:
		return fmt.Errorf("--stdin reads the %s, so name no file", kind)
	case fromStdin && cmd.input == inputFiles:
		return fmt.Errorf("a %s is read from files, not stdin", kind)
	case !fromStdin && len(args) == 0 && cmd.input != inputFiles && cmd.operand != "--stdin":
		return fmt.Errorf("name a file, or pass --stdin to read the %s from stdin", kind)
	case !fromStdin && len(args) == 0 && cmd.input != inputFiles:
		return fmt.Errorf("pass --stdin to read the %s from stdin", kind)
	}

	return nil
}

func failure(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "proofread: %v\n", err)

	return exitError
}

// kindOptions are the flags a kind takes.
type kindOptions struct {
	stdin          bool
	decisions      string
	only           string
	threadLength   int
	event          string
	futureSections string
	localBranches  string
	anchored       bool
	voice          string
	format         string
	minimum        string
	record         bool
	baseline       string
	prune          bool
	metrics        bool
	set            bool
	language       string
	budgets        string
}

// kindOpts holds the flags of the one command a process runs, which every kind's command shares.
var kindOpts kindOptions

// kindFlags are the flags a kind takes.
var kindFlags = []command.Flag{
	{Name: "stdin", Kind: command.Bool, Default: false, Doc: "read the text to judge from stdin", Target: &kindOpts.stdin},
	{Name: "decisions", Kind: command.String, Doc: "read the decisions table from this `file`, or - for stdin", Target: &kindOpts.decisions},
	{Name: "only", Kind: command.String, Doc: "judge by these comma-separated `rules` alone", Target: &kindOpts.only},
	{Name: "thread-length", Kind: command.Int, Default: 0, Doc: "the `count` of replies the author already posted in the thread", Target: &kindOpts.threadLength},
	{Name: "event", Kind: command.String, Doc: "the `verdict` a review summary is posted with: APPROVE, REQUEST_CHANGES or COMMENT", Target: &kindOpts.event},
	{Name: "future-sections", Kind: command.String, Doc: "let the sections under these comma-separated `headings` speak in the future tense", Target: &kindOpts.futureSections},
	{Name: "local-branches", Kind: command.String, Doc: "report the branches this `file` names, one a line, as only this machine's", Target: &kindOpts.localBranches},
	{Name: "anchored", Kind: command.Bool, Default: false, Doc: "with review-reply, judge as review-inline", Target: &kindOpts.anchored},
	{Name: "voice", Kind: command.String, Doc: "judge with the voice `file` proofread voice build wrote, or default for the one it writes with no --out", Target: &kindOpts.voice},
	{Name: "o", Kind: command.String, Default: "json", Doc: "write findings as `json`, sarif, rdjson or text", Target: &kindOpts.format},
	{Name: "minimum", Kind: command.String, Doc: "the `grade`, 0 to 100, under which a text fails, for a decisions table with no minimum for the kind", Target: &kindOpts.minimum},
	{Name: "record", Kind: command.Bool, Default: false, Doc: "keep this run's findings on this machine to count what later runs fix", Target: &kindOpts.record},
	{Name: "baseline", Kind: command.String, Doc: "report only findings past the counts this baseline `file` holds", Target: &kindOpts.baseline},
	{Name: "prune", Kind: command.Bool, Default: false, Doc: "with --baseline, rewrite the file to the counts this run found", Target: &kindOpts.prune},
	{Name: "metrics", Kind: command.Bool, Default: false, Doc: "write each text's readability metrics instead of its findings", Target: &kindOpts.metrics},
	{Name: "set", Kind: command.Bool, Default: false, Doc: "with --stdin, judge the NUL-separated texts as one set by the rules a short text is too small to show", Target: &kindOpts.set},
	{Name: "language", Kind: command.String, Doc: "with code-comment --stdin, the `language` of the source: go, ts or buzz", Target: &kindOpts.language},
	{Name: "budgets", Kind: command.String, Doc: "with code-comment, the comment budgets as a JSON `object`, each at most its strict default", Target: &kindOpts.budgets},
}

// judgeCommand declares the command that judges text of kind: name is its word,
// operand the operands its usage line shows.
func judgeCommand(name string, kind proofread.Kind, operand, summary string) command.Command {
	return command.Command{
		Name:  name,
		Short: summary,
		Usage: "proofread " + name + " [flags] " + operand,
		Flags: kindFlags,
		Run: func(_ context.Context, inv command.Invocation) error {
			return reported(runKind(kind, &kindOpts, inv.Args, inv.Stdin, inv.Stdout, inv.Stderr))
		},
	}
}

func runKind(kind proofread.Kind, o *kindOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	var minimum *int

	if o.minimum != "" {
		n, err := strconv.Atoi(o.minimum)
		if err != nil || n < 0 || n > 100 {
			return failure(stderr, fmt.Errorf("--minimum: want a grade from 0 to 100, not %q", o.minimum))
		}

		minimum = &n
	}

	// compat(until: no caller passes -anchored, the guard and the docs/guides
	// proofread-reply scripts included): before review-inline was a kind, an inline
	// comment was a review-reply with this flag.
	if o.anchored && kind == proofread.KindReviewReply {
		kind = proofread.KindReviewInline
	}

	t, err := readTable(o.decisions, kind, stdin, args)
	if err != nil {
		return failure(stderr, err)
	}

	s, known := t.spec(kind)
	if !known {
		return unknownSubcommand(stderr, string(kind))
	}

	if err := checkInput(kind, o.stdin, args); err != nil {
		return failure(stderr, err)
	}

	if o.metrics {
		if declared(kind) {
			return failure(stderr, fmt.Errorf("--metrics measures the built-in kinds, and %s is declared", kind))
		}

		return runMetrics(kind, args, stdin, stdout, stderr)
	}

	if !slices.Contains([]string{"", "APPROVE", "REQUEST_CHANGES", "COMMENT"}, o.event) {
		return failure(stderr, fmt.Errorf("unknown --event %q: want APPROVE, REQUEST_CHANGES or COMMENT", o.event))
	}

	if o.record && !fromFiles(kind, args) {
		return failure(stderr, errors.New("--record needs a file to judge, since an outcome is kept per path"))
	}

	onlyRules, err := onlyList(o.only, t)
	if err != nil {
		return failure(stderr, err)
	}

	opts := []proofread.Option{
		proofread.WithOnly(onlyRules...), proofread.WithThreadLength(o.threadLength), proofread.WithEvent(proofread.Event(o.event)),
		proofread.WithKinds(t.kinds...),
	}

	codeOpts, err := codeCommentOptions(kind, o)
	if err != nil {
		return failure(stderr, err)
	}

	opts = append(opts, codeOpts...)

	if o.futureSections != "" {
		opts = append(opts, proofread.WithFutureSections(strings.Split(o.futureSections, ",")...))
	}

	if o.localBranches != "" {
		body, err := os.ReadFile(o.localBranches)
		if err != nil {
			return failure(stderr, fmt.Errorf("--local-branches: %w", err))
		}
		opts = append(opts, proofread.WithLocalBranches(strings.Fields(string(body))...))
	}

	if o.voice != "" {
		voice, err := voiceOptions(o.voice, stderr)
		if err != nil {
			return failure(stderr, err)
		}

		opts = append(opts, voice...)
	}

	set, fromTable := t.minimums[kind]
	if s.Minimum != nil {
		set, fromTable = *s.Minimum, true
	}

	// A set runs no grade, so it needs no minimum.
	grades := !o.set && slices.Contains(s.RuleList(), proofread.RuleGrade) && t.turnsOn(proofread.RuleGrade, false) &&
		(len(onlyRules) == 0 || slices.Contains(onlyRules, proofread.RuleGrade))

	switch {
	case minimum != nil && fromTable:
		return failure(stderr, fmt.Errorf("--minimum and the decisions table both set the %s minimum: pass one", kind))
	case minimum != nil:
		opts = append(opts, proofread.WithMinimum(*minimum))
	case fromTable:
		opts = append(opts, proofread.WithMinimum(set))
	case grades:
		return failure(stderr, fmt.Errorf("no minimum grade for %s: set \"minimums\": {%q: N} in --decisions, or pass --minimum N",
			kind, kind))
	}

	judgeInput := judge
	if o.set {
		judgeInput = judgeSet(o.stdin, stderr)
	}

	out, err := judgeInput(kind, args, stdin, t, opts)
	if err != nil {
		return failure(stderr, err)
	}

	if o.record {
		if err := recordRun(kind, args, t, onlyRules, opts, out); err != nil {
			return failure(stderr, err)
		}
	}

	out, err = applyBaseline(o.baseline, o.prune, out)
	if err != nil {
		return failure(stderr, err)
	}

	if err := writeFindings(stdout, o.format, out); err != nil {
		return failure(stderr, err)
	}

	if o.format == "text" {
		fmt.Fprint(stderr, textSummary(out))
	}

	if slices.ContainsFunc(out, func(f finding) bool { return f.Decision == proofread.DecisionDeny.String() }) {
		return exitFindings
	}

	return exitOK
}

func rulesCommand() command.Command {
	return command.Command{
		Name: "rules", Short: "write every rule as the docs render it, as JSON", Effect: command.EffectRead,
		Run: func(_ context.Context, inv command.Invocation) error {
			return reported(runRules(inv.Args, inv.Stdout, inv.Stderr))
		},
	}
}

func explainCommand() command.Command {
	return command.Command{
		Name: "explain", Short: "print what a rule catches, why, its default decisions and its page", Usage: "proofread explain RULE|CODE", Effect: command.EffectRead,
		Run: func(_ context.Context, inv command.Invocation) error {
			return reported(runExplain(inv.Args, inv.Stdout, inv.Stderr))
		},
	}
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
			return nil, fmt.Errorf("--only names %q, which is off: set it to advise or deny in --decisions", r)
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

		if kind == proofread.KindCodeComment {
			lang, ok := proofread.LanguageOf(p)
			if !ok {
				return nil, fmt.Errorf("%s is not Go, TypeScript or Buzz, the languages code-comment reads", p)
			}

			fileOpts = append(fileOpts, proofread.WithLanguage(lang))
		}

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
		Decision: f.Decision.String(), Message: f.Message, Match: f.Match, URL: f.URL, Weight: f.Weight, WeightSet: f.WeightSet,
		Line: f.Line, Column: f.Column, EndLine: f.EndLine, EndColumn: f.EndColumn, Replacements: f.Replacements,
		Protected: f.Protected,
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
			Name:  r.Name,
			Owner: r.Owner,
			Doc:   r.Doc,
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
// files each glob matches, in the order the file lists them, and the grade
// each kind it names is held to.
type table struct {
	rules    map[proofread.Rule]proofread.Decision
	paths    []scoped
	minimums map[proofread.Kind]int
	// kinds are the kinds the table declares, resolved.
	kinds []proofread.KindSpec
}

// spec returns kind's spec: a built-in kind's, then one t declares.
func (t table) spec(kind proofread.Kind) (proofread.KindSpec, bool) {
	if s, ok := proofread.BuiltinKind(kind); ok {
		return s, true
	}

	for _, s := range t.kinds {
		if s.Name == kind {
			return s, true
		}
	}

	return proofread.KindSpec{}, false
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
// against the rules and against args, then adds the user's kinds. An empty p
// is an empty table.
func readTable(p string, kind proofread.Kind, stdin io.Reader, args []string) (table, error) {
	if p == "" {
		return withUserKinds(table{}, "")
	}

	var data []byte

	var err error

	if p == "-" {
		if !fromFiles(kind, args) {
			return table{}, fmt.Errorf("--decisions -: name the table's file, since the %s itself is read from stdin", kind)
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

	return withUserKinds(t, p)
}

// parseTable reads {"rules": {rule: decision}, "paths": {glob: {rule:
// decision}}, "minimums": {kind: grade}}, keeping the globs in the order the
// file lists them. Its "kinds" object declares kinds, as
// [proofread.DeclareKinds] reads them.
func parseTable(data []byte) (table, error) {
	var raw struct {
		Rules    map[string]string             `json:"rules"`
		Paths    json.RawMessage               `json:"paths"`
		Minimums map[string]int                `json:"minimums"`
		Kinds    map[string]proofread.KindDecl `json:"kinds"`
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&raw); err != nil {
		return table{}, err
	}

	rules, err := proofread.RuleDecisions(raw.Rules)
	if err != nil {
		return table{}, err
	}

	kinds, err := proofread.DeclareKinds(raw.Kinds)
	if err != nil {
		return table{}, err
	}

	minimums, err := parseMinimums(raw.Minimums, raw.Kinds)
	if err != nil {
		return table{}, err
	}

	t := table{rules: rules, minimums: minimums, kinds: kinds}

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

		scopedRules, err := proofread.RuleDecisions(entry)
		if err != nil {
			return table{}, fmt.Errorf("path %q: %w", glob, err)
		}

		t.paths = append(t.paths, scoped{glob: glob, rules: scopedRules})
	}

	return t, nil
}

// parseMinimums checks each kind a table's minimums name, built in or
// declared, and each grade. A declared kind that sets its own minimum may not
// be named here too.
func parseMinimums(in map[string]int, declared map[string]proofread.KindDecl) (map[proofread.Kind]int, error) {
	out := map[proofread.Kind]int{}

	for _, name := range slices.Sorted(maps.Keys(in)) {
		kind, ok := proofread.ParseKind(name)
		if decl, isDeclared := declared[name]; isDeclared {
			if decl.Minimum != nil {
				return nil, fmt.Errorf("minimums: %s: kinds.%s.minimum sets it too: keep one", name, name)
			}

			kind, ok = proofread.Kind(name), true
		}

		if !ok || string(kind) != name {
			return nil, fmt.Errorf("minimums: unknown kind %q", name)
		}

		if n := in[name]; n < 0 || n > 100 {
			return nil, fmt.Errorf("minimums: %s: want a grade from 0 to 100, not %d", name, n)
		}

		out[kind] = in[name]
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
