package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/proofread"
)

const (
	fillerSimply = "Drop 'simply': state the fact."
	weakCrucial  = "Replace 'crucial' with what is actually so."
	leakTBD      = "Remove 'TBD': it is left over from a tool or a template."
	termsMessage = "Write 'subagent', not 'sub-agent'."
)

// row is the finding proofread writes for rule, its code and page read from
// the catalog.
// at places f's match on line from column to just before end.
func (f finding) at(line, column, end int) finding {
	f.Line, f.Column, f.EndLine, f.EndColumn = line, column, line, end

	return f
}

// replacing gives f the replacements its rule offers for its match.
func (f finding) replacing(texts ...string) finding {
	f.Replacements = texts

	return f
}

func row(node, source, kind string, rule proofread.Rule, decision proofread.Decision, message, match string) finding {
	f := finding{
		Node: node, Source: source, Kind: kind, Rule: string(rule), Decision: string(decision),
		Message: message, Match: match,
	}

	for _, doc := range proofread.Catalog() {
		if doc.Name == rule {
			if decision == proofread.DecisionAdvise {
				f.Weight = doc.Weight
			}

			f.Code = string(doc.Code)
			f.URL = "https://eli.gladman.cc/magus/reference/proofread/" + string(rule) + "/"
		}
	}

	return f
}

// rows is fs as proofread encodes it.
func rows(t *testing.T, fs ...finding) string {
	t.Helper()

	if fs == nil {
		fs = []finding{}
	}

	var b bytes.Buffer
	if err := json.NewEncoder(&b).Encode(fs); err != nil {
		t.Fatal(err)
	}

	return b.String()
}

func runJudge(t *testing.T, stdin string) (code int, stdout, stderr string) {
	t.Helper()

	var out, errOut bytes.Buffer

	code = run(ungraded([]string{"doc-comment", "--stdin"}), strings.NewReader(stdin), &out, &errOut)

	return code, out.String(), errOut.String()
}

func assertRun(t *testing.T, stdin string, wantCode int, wantStdout, wantStderr string) {
	t.Helper()

	code, stdout, stderr := runJudge(t, stdin)
	if code != wantCode || stdout != wantStdout || stderr != wantStderr {
		t.Errorf("run:\n got code %d stdout %q stderr %q\nwant code %d stdout %q stderr %q",
			code, stdout, stderr, wantCode, wantStdout, wantStderr)
	}
}

// assertArgs runs proofread with args, [ungraded], over stdin and wants want
// on stdout, with exit 1 when want holds a deny finding and 0 otherwise.
func assertArgs(t *testing.T, args []string, stdin, want string) {
	t.Helper()

	wantCode := exitOK
	if strings.Contains(want, `"decision":"deny"`) {
		wantCode = exitFindings
	}

	code, out, errOut := runArgs(t, args, stdin)
	if code != wantCode || out != want || errOut != "" {
		t.Errorf("run:\n got code %d stdout %q stderr %q\nwant code %d stdout %q", code, out, errOut, wantCode, want)
	}
}

// writeFile writes body to name in dir and returns its path.
func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()

	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	return p
}

// houseTable writes a decisions table turning the named house rules on.
func houseTable(t *testing.T, rules ...proofread.Rule) string {
	t.Helper()

	entries := make([]string, len(rules))
	for i, r := range rules {
		entries[i] = `"` + string(r) + `":"deny"`
	}

	return writeFile(t, t.TempDir(), "decisions.json", `{"rules":{`+strings.Join(entries, ",")+`}}`)
}

func TestRunWritesAFindingForAJudgedDoc(t *testing.T) {
	in := `[{"node":"n1","source":"a.go","language":"go","name":"Resolve","kind":"function","owner":"","doc":"Resolve returns the path. TBD."}]`

	assertRun(t, in, 0, rows(t, row("n1", "a.go", "doc-comment", proofread.RuleLeak, proofread.DecisionAdvise, leakTBD, "TBD")), "")
}

func TestRunWritesAnEmptyArrayWhenNothingIsFound(t *testing.T) {
	in := `[{"node":"n1","source":"a.go","language":"go","name":"Resolve","kind":"function","owner":"","doc":"Resolve returns the path."}]`

	assertRun(t, in, 0, "[]\n", "")
}

func TestRunWritesAnEmptyArrayForAnEmptyInput(t *testing.T) {
	assertRun(t, `[]`, 0, "[]\n", "")
}

func TestRunKeepsInputOrderThenFindingOrder(t *testing.T) {
	in := `[` +
		`{"node":"b","name":"Run","kind":"function","doc":"Run hands work to a sub-agent. TBD."},` +
		`{"node":"a","name":"Open","kind":"function","doc":"Open opens. TBD."}]`
	want := rows(t,
		row("b", "", "doc-comment", proofread.RuleTerms, proofread.DecisionDeny, termsMessage, "sub-agent").replacing("subagent"),
		row("b", "", "doc-comment", proofread.RuleLeak, proofread.DecisionAdvise, leakTBD, "TBD"),
		row("a", "", "doc-comment", proofread.RuleLeak, proofread.DecisionAdvise, leakTBD, "TBD"),
	)

	assertArgs(t, []string{"doc-comment", "--stdin", "-decisions", houseTable(t, proofread.RuleTerms)}, in, want)
}

func TestRunJudgesReferenceFilesInArgumentOrder(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.md", "# A\n\nIt is crucial.\n")
	b := writeFile(t, dir, "b.md", "- **Cache:** on\n")

	want := rows(t,
		row(b, b+":1", "reference", proofread.RuleReplyVoice, proofread.DecisionAdvise,
			"Drop the bold label '**Cache:**': write the item as a sentence that opens with its subject.", "**Cache:**").at(1, 3, 13),
		row(a, a+":3", "reference", proofread.RuleBuzzwordWeak, proofread.DecisionAdvise, weakCrucial, "crucial").at(3, 7, 14),
	)

	assertArgs(t, []string{"reference", b, a}, "", want)
}

func TestRunJudgesATemplateAtItsSourceLine(t *testing.T) {
	path := writeFile(t, t.TempDir(), "SKILL.md", "# Skill\n\n{{if .Full}}One.\n\nTwo.\n{{end}}\nRun it in order to replay.\n")

	want := rows(t, row(path, path+":7", "skill-template", proofread.RuleWordy, proofread.DecisionAdvise,
		"Write 'to', not 'in order to'.", "in order to").at(7, 8, 19).replacing("to"))

	assertArgs(t, []string{"skill-template", path}, "", want)
}

func TestRunReadsAFormerKindNameAsItsKind(t *testing.T) {
	path := writeFile(t, t.TempDir(), "SKILL.md", "# Skill\n\nRun it in order to replay.\n")

	want := rows(t, row(path, path+":3", "skill-template", proofread.RuleWordy, proofread.DecisionAdvise,
		"Write 'to', not 'in order to'.", "in order to").at(3, 8, 19).replacing("to"))

	assertArgs(t, []string{"agent-instructions-template", path}, "", want)
}

func TestRunJudgesAGuideOnTheGuideRules(t *testing.T) {
	path := writeFile(t, t.TempDir(), "guide.md", "# Guide\n\nThen we run it.\n")
	// second-person ships off while its labeled precision is short, so the table turns it on.
	decisions := writeFile(t, t.TempDir(), "d.json", `{"rules":{"second-person":"deny"}}`)

	want := rows(t, row(path, path+":3", "guide", proofread.RuleSecondPerson, proofread.DecisionDeny,
		"Address the reader as you, not 'we': a guide speaks to the person following it, "+
			"and names magus or the project where it means them.", "we").at(3, 6, 8))

	assertArgs(t, []string{"guide", "-decisions", decisions, path}, "", want)
}

func TestRunJudgesAChangeDescriptionFromStdin(t *testing.T) {
	want := rows(t, row("change-description", "change-description:2", "change-description", proofread.RuleLeadContext,
		proofread.DecisionDeny, "It opens with a heading: open the description with what a reader can now do "+
			"or no longer has to do, then how the work came up and why it mattered, then the changes, as in 'The first "+
			"query after an edit answers from a graph that is already current. Until now the graph rebuilt inline on that "+
			"query.'", "## Summary").at(2, 1, 11))

	assertArgs(t, []string{"change-description", "--stdin"}, "fix: pin the key\n## Summary\n", want)
}

func TestRunJudgesAStylesheetCommentAtItsSourceLine(t *testing.T) {
	path := writeFile(t, t.TempDir(), "a.css", ".a { color: red; }\n\n/* Simply put.\n   Still fine. */\n.b { color: blue; }\n")
	decisions := writeFile(t, t.TempDir(), "d.json", `{"rules":{"filler":"advise"}}`)

	// A stylesheet finding has a line and no column: its comment is judged with
	// the delimiters gone.
	f := row(path, path+":3", "stylesheet", proofread.RuleFiller, proofread.DecisionAdvise,
		"Drop 'Simply': state the fact.", "Simply").replacing("")
	f.Line = 3

	assertArgs(t, []string{"stylesheet", "-decisions", decisions, path}, "", rows(t, f))
}

func TestRunJudgesAStylesheetFromStdinWhenNoPathIsNamed(t *testing.T) {
	decisions := writeFile(t, t.TempDir(), "d.json", `{"rules":{"history":"deny"}}`)

	f := row("stylesheet", "stylesheet:4", "stylesheet", proofread.RuleHistory, proofread.DecisionDeny,
		"Comment narrates a change (\"Used to\"); describe the code as it stands and leave its history to the "+
			"commit message.", "Used to")
	f.Line = 4

	assertArgs(t, []string{"stylesheet", "--stdin", "-decisions", decisions}, "/* Fine. */\n.a { color: red; }\n\n/* Used to be a grid. */\n"+
		".b { color: blue; }\n", rows(t, f))
}

func TestRunJudgesAReviewReplyFromStdin(t *testing.T) {
	want := rows(t, row("review-reply", "review-reply:1", "review-reply", proofread.RuleBuzzword, proofread.DecisionDeny,
		"Replace 'delve' with what is actually so.", "delve").at(1, 8, 13))

	assertArgs(t, []string{"review-reply", "--stdin"}, "Let me delve into the race.", want)
}

// An inline comment's line is its referent, so with -anchored nonspecific
// leaves the reply alone.
func TestRunTakesAnchored(t *testing.T) {
	const reply = "This is confusing."

	advise := writeFile(t, t.TempDir(), "d.json", `{"rules":{"nonspecific":"advise"}}`)
	message := "Name what 'confusing' refers to and what to change: a line, an identifier in backticks, an " +
		"example or the reason, as in 'This reads the map without the lock: `cache.get` races with `cache.put`.'"

	assertArgs(t, []string{"review-reply", "--stdin", "-decisions", advise}, reply,
		rows(t, row("review-reply", "review-reply:1", "review-reply", proofread.RuleNonspecific, proofread.DecisionAdvise,
			message, "confusing").at(1, 9, 18)))
	assertArgs(t, []string{"review-reply", "--stdin", "-anchored", "-decisions", advise}, reply, "[]\n")
	assertArgs(t, []string{"review-inline", "--stdin", "-decisions", advise}, reply, "[]\n")
}

// A summary's verdict decides whether its body contradicts it, so an approval
// naming a blocker draws a finding and a comment does not.
func TestRunTakesTheReviewEvent(t *testing.T) {
	const body = "Approving, but the lock must be fixed before merge."

	advise := writeFile(t, t.TempDir(), "d.json", `{"rules":{"approve-blocker":"advise"}}`)

	var stdout, stderr bytes.Buffer
	if code := run(ungraded([]string{"review-summary", "--stdin", "-event", "APPROVE", "-decisions", advise}), strings.NewReader(body), &stdout, &stderr); code != exitOK ||
		!strings.Contains(stdout.String(), `"approve-blocker"`) {
		t.Errorf("APPROVE: code %d, stdout %s, stderr %s; want an approve-blocker finding", code, stdout.String(), stderr.String())
	}

	stdout.Reset()

	if code := run(ungraded([]string{"review-summary", "--stdin", "-event", "COMMENT", "-decisions", advise}), strings.NewReader(body), &stdout, &stderr); code != exitOK ||
		strings.Contains(stdout.String(), `"approve-blocker"`) {
		t.Errorf("COMMENT: code %d, stdout %s; want no approve-blocker finding", code, stdout.String())
	}

	if code := run([]string{"review-summary", "--stdin", "-event", "MERGE"}, strings.NewReader(body), &stdout, &stderr); code != exitError {
		t.Errorf("-event MERGE: code %d; want %d", code, exitError)
	}
}

// A reply that is its author's fourth in the thread draws one finding, which
// advises unless a table says otherwise.
func TestRunTakesTheThreadLengthAndTheDecisions(t *testing.T) {
	const reply = "The map is shared by the two workers."

	message := "This is reply 4 from you in the thread: offer a call to settle it, as in 'Want to talk this " +
		"through for ten minutes?'"
	deny := writeFile(t, t.TempDir(), "d.json", `{"rules":{"long-thread":"deny"}}`)
	off := writeFile(t, t.TempDir(), "d.json", `{"rules":{"long-thread":"off"}}`)

	assertArgs(t, []string{"review-reply", "--stdin", "-thread-length", "2"}, reply, "[]\n")
	assertArgs(t, []string{"review-reply", "--stdin", "-thread-length", "3"}, reply,
		rows(t, row("review-reply", "review-reply", "review-reply", proofread.RuleLongThread, proofread.DecisionAdvise, message, "")))
	assertArgs(t, []string{"review-reply", "--stdin", "-thread-length", "3", "-decisions", deny}, reply,
		rows(t, row("review-reply", "review-reply", "review-reply", proofread.RuleLongThread, proofread.DecisionDeny, message, "")))
	assertArgs(t, []string{"review-reply", "--stdin", "-thread-length", "3", "-decisions", off}, reply, "[]\n")
}

func TestRunSelectsRulesByOnlyAndDecisions(t *testing.T) {
	const doc = `[{"node":"n","name":"Run","kind":"function","doc":"Run hands work to a sub-agent. TBD."}]`

	leak := row("n", "", "doc-comment", proofread.RuleLeak, proofread.DecisionAdvise, leakTBD, "TBD")
	terms := row("n", "", "doc-comment", proofread.RuleTerms, proofread.DecisionDeny, termsMessage, "sub-agent").replacing("subagent")
	decisions := houseTable(t, proofread.RuleTerms, proofread.RuleCommentBlock)

	assertArgs(t, []string{"doc-comment", "--stdin"}, doc, rows(t, leak))
	assertArgs(t, []string{"doc-comment", "--stdin", "-decisions", decisions}, doc, rows(t, terms, leak))
	assertArgs(t, []string{"doc-comment", "--stdin", "-decisions", decisions, "-only", "terms, comment-block"}, doc, rows(t, terms))
}

// A glob's entry overrides the table's rules for the files it matches, and a
// later glob overrides an earlier one.
func TestRunAppliesPathDecisionsToTheFilesTheyMatch(t *testing.T) {
	dir := t.TempDir()
	post := writeFile(t, dir, "blog/post.md", "It simply works.\n")
	page := writeFile(t, dir, "docs/page.md", "It simply works.\n")
	decisions := writeFile(t, t.TempDir(), "d.json", `{"rules":{"filler":"advise"},"paths":{`+
		`"`+filepath.ToSlash(dir)+`/**":{"filler":"deny"},`+
		`"`+filepath.ToSlash(dir)+`/blog/*.md":{"filler":"off"}}}`)

	want := rows(t, row(page, page+":1", "reference", proofread.RuleFiller, proofread.DecisionDeny, fillerSimply, "simply").at(1, 4, 10).replacing(""))

	assertArgs(t, []string{"reference", "-decisions", decisions, post, page}, "", want)
}

func TestRunReadsTheDecisionsFromStdinForFiles(t *testing.T) {
	page := writeFile(t, t.TempDir(), "page.md", "It is crucial.\n")

	assertArgs(t, []string{"reference", "-decisions", "-", page}, `{"rules":{"buzzword-weak":"off"}}`, "[]\n")
}

func TestRunWritesTheRules(t *testing.T) {
	var out, errOut bytes.Buffer

	if code := run([]string{"rules"}, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("run: code %d stderr %q", code, errOut.String())
	}

	var docs []proofread.RuleDoc
	if err := json.Unmarshal(out.Bytes(), &docs); err != nil {
		t.Fatal(err)
	}

	if len(docs) != len(proofread.Rules()) || docs[0].Name != proofread.Rules()[0] || docs[0].Code == "" {
		t.Errorf("rules: got %d rules, first %+v", len(docs), docs[0])
	}
}

func TestRunExplainsARuleByNameOrCode(t *testing.T) {
	want := "filler (PRF4001)\n\n" +
		"Catches: throat-clearing (\"Note that\") and filler adverbs (\"simply\", \"basically\")\n"

	for _, arg := range []string{"filler", "PRF4001", "prf4001"} {
		var out, errOut bytes.Buffer

		if code := run([]string{"explain", arg}, strings.NewReader(""), &out, &errOut); code != 0 || errOut.String() != "" {
			t.Fatalf("explain %s: code %d stderr %q", arg, code, errOut.String())
		}

		for _, line := range []string{
			want,
			"\nDefault decisions:\n",
			"  doc-comment                  advise\n",
			"\nPage: https://eli.gladman.cc/magus/reference/proofread/filler/\n",
		} {
			if !strings.Contains(out.String(), line) {
				t.Errorf("explain %s: %q is not in\n%s", arg, line, out.String())
			}
		}
	}
}

func TestRunExplainsAHouseRuleAsOff(t *testing.T) {
	var out, errOut bytes.Buffer

	if code := run([]string{"explain", "terms"}, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("explain terms: code %d stderr %q", code, errOut.String())
	}

	for _, line := range []string{"House style: runs only where a decisions table names it.\n", "  reference                    off\n"} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("explain terms: %q is not in\n%s", line, out.String())
		}
	}
}

func TestRunPrintsTheUsageForNoSubcommandOrAnUnknownOne(t *testing.T) {
	cases := []struct {
		name string
		args []string
		lead string
	}{
		{"no subcommand", nil, "usage: proofread"},
		{"an unknown subcommand", []string{"doc"}, "proofread: unknown subcommand \"doc\"\n\nusage: proofread"},
		{"the old flag", []string{"-kind", "reference"}, "proofread: unknown subcommand \"-kind\"\n\nusage: proofread"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer

			code := run(tc.args, strings.NewReader(""), &out, &errOut)
			if code != exitError || out.String() != "" || !strings.HasPrefix(errOut.String(), tc.lead) {
				t.Fatalf("run: got code %d stdout %q stderr %q, want code 2 and stderr starting %q", code, out.String(), errOut.String(), tc.lead)
			}

			for _, s := range subcommands {
				if !strings.Contains(errOut.String(), "  "+s.name+" ") {
					t.Errorf("the usage does not name %q:\n%s", s.name, errOut.String())
				}
			}
		})
	}
}

func TestEveryKindTheRulesJudgeIsASubcommand(t *testing.T) {
	named := map[proofread.Kind]bool{}

	for _, s := range subcommands {
		named[s.kind] = true
	}

	for _, doc := range proofread.Catalog() {
		for _, k := range doc.Kinds {
			if !named[k] {
				t.Errorf("rule %s judges kind %s, which no subcommand names", doc.Name, k)
			}
		}
	}
}

func TestRunExitsTwoOnAFlagItCannotUse(t *testing.T) {
	dir := t.TempDir()
	table := func(body string) string { return writeFile(t, t.TempDir(), "d.json", body) }
	unknownRule := table(`{"rules":{"fillers":"deny"}}`)
	badDecision := table(`{"rules":{"filler":"error"}}`)
	unknownScopedRule := table(`{"paths":{"*.md":{"tone":"off"}}}`)
	noMatch := table(`{"paths":{"blog/**":{"filler":"off"}}}`)
	unknownField := table(`{"rule":{"filler":"off"}}`)
	noMinimum := table(`{"rules":{"filler":"advise"}}`)
	referenceMinimum := table(`{"minimums":{"reference":80}}`)
	unknownKindMinimum := table(`{"minimums":{"page":80}}`)
	page := writeFile(t, dir, "page.md", "It works.\n")

	cases := []struct {
		name       string
		args       []string
		wantStderr string
	}{
		{"a kind with no minimum", []string{"reference", "-decisions", noMinimum, page},
			"proofread: no minimum grade for reference: set \"minimums\": {\"reference\": N} in --decisions, or pass --minimum N\n"},
		{"two minimums", []string{"reference", "-decisions", referenceMinimum, "-minimum", "50", page},
			"proofread: --minimum and the decisions table both set the reference minimum: pass one\n"},
		{"a minimum for no kind", []string{"reference", "-decisions", unknownKindMinimum, page},
			"proofread: decisions " + unknownKindMinimum + ": minimums: unknown kind \"page\"\n"},
		{"record with no file", []string{"change-description", "--stdin", "-record"},
			"proofread: --record needs a file to judge, since an outcome is kept per path\n"},
		{"two message files", []string{"commit-message", "-minimum", "0", page, page},
			"proofread: commit-message takes one message file, or reads stdin\n"},
		{"stats with an argument", []string{"stats", "filler"}, "proofread: stats takes no arguments\n"},
		{"a path for symbols", []string{"doc-comment", "-minimum", "0", "a.md"},
			"proofread: a path needs the reference subcommand, since symbols are read from stdin\n"},
		{"stdin with no --stdin", []string{"review-reply"}, "proofread: pass --stdin to read the review-reply from stdin\n"},
		{"a message with no file or --stdin", []string{"commit-message"},
			"proofread: name a file, or pass --stdin to read the commit-message from stdin\n"},
		{"--stdin beside a file", []string{"stylesheet", "--stdin", page}, "proofread: --stdin reads the stylesheet, so name no file\n"},
		{"--stdin for a kind read from files", []string{"reference", "--stdin"}, "proofread: a reference is read from files, not stdin\n"},
		{"rules with an argument", []string{"rules", "filler"}, "proofread: rules takes no arguments\n"},
		{"explain with no rule", []string{"explain"}, "proofread: explain takes one rule name or code\n"},
		{"explain an unknown rule", []string{"explain", "fillers"}, "proofread: unknown rule or code \"fillers\"\n"},
		{"a path for a change description", []string{"change-description", "-minimum", "0", "a.md"},
			"proofread: a change-description is read from stdin, not from a path\n"},
		{"a missing file", []string{"reference", "-minimum", "0", "missing.md"},
			"proofread: read missing.md: open missing.md: no such file or directory\n"},
		{"unknown rule to keep", []string{"doc-comment", "--stdin", "-only", "filler,fillers"}, "proofread: unknown rule \"fillers\"\n"},
		{"an off rule to keep", []string{"doc-comment", "--stdin", "-only", "terms"},
			"proofread: --only names \"terms\", which is off: set it to advise or deny in --decisions\n"},
		{"unknown rule in the table", []string{"doc-comment", "--stdin", "-decisions", unknownRule},
			"proofread: decisions " + unknownRule + ": unknown rule \"fillers\"\n"},
		{"unknown decision", []string{"doc-comment", "--stdin", "-decisions", badDecision},
			"proofread: decisions " + badDecision + ": rule \"filler\": unknown decision \"error\": want off, advise or deny\n"},
		{"unknown rule under a path", []string{"reference", "-decisions", unknownScopedRule, page},
			"proofread: decisions " + unknownScopedRule + ": path \"*.md\": unknown rule \"tone\"\n"},
		{"a glob matching no argument", []string{"reference", "-decisions", noMatch, page},
			"proofread: decisions " + noMatch + ": path \"blog/**\" matches no file argument\n"},
		{"an unknown field", []string{"doc-comment", "--stdin", "-decisions", unknownField},
			"proofread: decisions " + unknownField + ": json: unknown field \"rule\"\n"},
		{"decisions on stdin beside the text", []string{"review-reply", "--stdin", "-decisions", "-"},
			"proofread: --decisions -: name the table's file, since the review-reply itself is read from stdin\n"},
		{"a missing table", []string{"doc-comment", "--stdin", "-decisions", "missing.json"},
			"proofread: read decisions missing.json: open missing.json: no such file or directory\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer

			code := run(tc.args, strings.NewReader("[]"), &out, &errOut)
			if code != exitError || out.String() != "" || errOut.String() != tc.wantStderr {
				t.Errorf("run: got code %d stdout %q stderr %q, want code 2 stderr %q", code, out.String(), errOut.String(), tc.wantStderr)
			}
		})
	}
}

func TestRunExitsTwoOnInputThatIsNotAnArrayOfRecords(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantStderr string
	}{
		{"malformed", `[{"node":`, "proofread: read symbols: unexpected EOF\n"},
		{"empty stdin", ``, "proofread: read symbols: EOF\n"},
		{"unknown field", `[{"node":"n","extra":1}]`, "proofread: read symbols: json: unknown field \"extra\"\n"},
		{"trailing data", `[] []`, "proofread: read symbols: data after the array\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertRun(t, tc.in, exitError, "", tc.wantStderr)
		})
	}
}

func TestRunWritesSARIFAndRDJSONForTheFormatFlag(t *testing.T) {
	path := writeFile(t, t.TempDir(), "a.md", "# A\n\nIt is crucial.\n")

	var log sarifLog

	runInto(t, []string{"reference", "-o", "sarif", path}, &log)

	if res := log.Runs[0].Results; len(res) != 1 || res[0].RuleID != "buzzword-weak" ||
		res[0].Locations[0].PhysicalLocation.ArtifactLocation.URI != path ||
		res[0].Locations[0].PhysicalLocation.Region.StartLine != 3 {
		t.Errorf("sarif results = %+v", res)
	}

	var result rdResult

	runInto(t, []string{"reference", "-o", "rdjson", path}, &result)

	if d := result.Diagnostics; len(d) != 1 || d[0].Code.Value != "PRF4007" || d[0].Location.Path != path ||
		d[0].Severity != "WARNING" {
		t.Errorf("rdjson diagnostics = %+v", d)
	}
}

func TestRunRejectsAnUnknownFormat(t *testing.T) {
	var out, errOut bytes.Buffer

	code := run(ungraded([]string{"reference", "-o", "xml"}), strings.NewReader(""), &out, &errOut)
	if code != exitError || out.String() != "" || errOut.String() != "proofread: unknown -o \"xml\": want json, sarif, rdjson or text\n" {
		t.Errorf("run: got code %d stdout %q stderr %q", code, out.String(), errOut.String())
	}
}

func TestRunHoldsAFileToItsBaseline(t *testing.T) {
	dir := t.TempDir()
	page := writeFile(t, dir, "a.md", "# A\n\nIt is crucial.\n")
	baselinePath := filepath.Join(dir, "baseline.json")

	var out, errOut bytes.Buffer

	code := run(ungraded([]string{"reference", "-baseline", baselinePath, page}), strings.NewReader(""), &out, &errOut)
	if code != exitError || out.String() != "" || !strings.Contains(errOut.String(), "does not exist: run with --prune to create it") {
		t.Fatalf("a missing baseline: code %d stdout %q stderr %q", code, out.String(), errOut.String())
	}

	assertArgs(t, []string{"reference", "-baseline", baselinePath, "-prune", page}, "", "[]\n")
	assertArgs(t, []string{"reference", "-baseline", baselinePath, page}, "", "[]\n")

	writeFile(t, dir, "a.md", "# A\n\nIt is crucial.\n\nIt is valuable.\n")

	want := rows(t, row(page, page+":5", "reference", proofread.RuleBuzzwordWeak, proofread.DecisionAdvise,
		"Replace 'valuable' with what is actually so.", "valuable").at(5, 7, 15))
	assertArgs(t, []string{"reference", "-baseline", baselinePath, page}, "", want)
}

func TestRunNeedsABaselineForPrune(t *testing.T) {
	var out, errOut bytes.Buffer

	code := run(ungraded([]string{"reference", "-prune"}), strings.NewReader(""), &out, &errOut)
	if code != exitError || errOut.String() != "proofread: --prune needs --baseline\n" {
		t.Errorf("run: got code %d stderr %q", code, errOut.String())
	}
}

func TestRunHonorsASuppressionAndReportsAnIdleOne(t *testing.T) {
	dir := t.TempDir()
	quiet := writeFile(t, dir, "quiet.md", "# A\n\n<!-- proofread off buzzword-weak: quoting the user -->\nIt is crucial.\n")
	idle := writeFile(t, dir, "idle.md", "# A\n\n<!-- proofread off buzzword-weak: quoting the user -->\nIt works.\n")

	comment := "<!-- proofread off buzzword-weak: quoting the user -->"
	want := rows(t, row(idle, idle+":3", "reference", proofread.RuleSuppressionUnused, proofread.DecisionDeny,
		"Remove the suppression of buzzword-weak: it matches no finding.", comment).at(3, 1, 1+len(comment)))

	assertArgs(t, []string{"reference", quiet}, "", "[]\n")
	assertArgs(t, []string{"reference", idle}, "", want)
}

func TestRunWritesTextAndACountOnStderr(t *testing.T) {
	dir := t.TempDir()
	bad := writeFile(t, dir, "bad.md", "# A\n\nIt is crucial.\n")
	clean := writeFile(t, dir, "ok.md", "# A\n\nIt works.\n")

	code, out, errOut := runArgs(t, []string{"reference", "-o", "text", bad}, "")
	wantOut := bad + ":3:7: PRF4007 buzzword-weak [advise] " + weakCrucial + "\n"

	if code != exitOK || out != wantOut || errOut != "proofread: 1 finding (0 deny, 1 advise)\n" {
		t.Errorf("a finding: code %d stdout %q stderr %q, want stdout %q", code, out, errOut, wantOut)
	}

	code, out, errOut = runArgs(t, []string{"reference", "-o", "text", clean}, "")
	if code != exitOK || out != "" || errOut != "" {
		t.Errorf("no findings: code %d stdout %q stderr %q, want nothing", code, out, errOut)
	}
}

func TestRunFailsOnADenyFindingOrAGradeUnderTheMinimum(t *testing.T) {
	dir := t.TempDir()
	bad := writeFile(t, dir, "bad.md", "# A\n\nIt simply works.\n")
	clean := writeFile(t, dir, "ok.md", "# A\n\nIt works.\n")
	suppressed := writeFile(t, dir, "quiet.md", "# A\n\n<!-- proofread off filler: quoting the user -->\nIt simply works.\n")
	deny := writeFile(t, dir, "deny.json", `{"rules":{"filler":"deny"},"minimums":{"reference":0}}`)
	low := writeFile(t, dir, "low.json", `{"minimums":{"reference":90}}`)
	lenient := writeFile(t, dir, "lenient.json", `{"minimums":{"reference":10}}`)

	cases := []struct {
		name string
		args []string
		want int
	}{
		{"a deny finding", []string{"reference", "-decisions", deny, bad}, exitFindings},
		{"a grade under the table's minimum", []string{"reference", "-decisions", low, bad}, exitFindings},
		{"a grade at the table's minimum", []string{"reference", "-decisions", lenient, bad}, exitOK},
		{"a grade under --minimum", []string{"reference", "-minimum", "90", bad}, exitFindings},
		{"a clean file", []string{"reference", "-minimum", "100", clean}, exitOK},
		{"a suppressed finding", []string{"reference", "-minimum", "100", suppressed}, exitOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer

			if code := run(tc.args, strings.NewReader(""), &out, &errOut); code != tc.want {
				t.Errorf("run %v: code %d, want %d: %s", tc.args, code, tc.want, errOut.String())
			}
		})
	}

	var got []finding

	runInto(t, []string{"reference", "-minimum", "90", bad}, &got)

	if len(got) != 2 || got[0].Rule != "filler" || got[0].Weight == 0 || got[1].Rule != "grade" ||
		got[1].Decision != "deny" || got[1].Weight != 0 {
		t.Errorf("findings = %+v, want a weighed filler finding, then a grade finding", got)
	}

	code, _, errOut := runArgs(t, []string{"reference", "-minimum", "101", bad}, "")
	if code != exitError || !strings.Contains(errOut, "want a grade from 0 to 100, not \"101\"") {
		t.Errorf("a minimum past 100: code %d stderr %q", code, errOut)
	}
}

func TestRunFailsOnlyOnFindingsPastTheBaseline(t *testing.T) {
	dir := t.TempDir()
	bad := writeFile(t, dir, "bad.md", "# A\n\nIt simply works.\n")
	deny := writeFile(t, dir, "deny.json", `{"rules":{"filler":"deny"}}`)
	baselinePath := filepath.Join(dir, "baseline.json")

	runArgs(t, []string{"reference", "-decisions", deny, "-baseline", baselinePath, "-prune", bad}, "")

	if code, _, _ := runArgs(t, []string{"reference", "-decisions", deny, "-baseline", baselinePath, bad}, ""); code != exitOK {
		t.Errorf("a baselined finding: code %d, want %d", code, exitOK)
	}
}

// ungraded gives a run of a kind that names no -minimum a minimum of 0, so a
// case about anything but the grade needs no decisions table to hold one.
func ungraded(args []string) []string {
	if len(args) == 0 || slices.Contains(args, "-minimum") {
		return args
	}

	if _, ok := proofread.ParseKind(args[0]); !ok {
		return args
	}

	return slices.Concat(args[:1], []string{"-minimum", "0"}, args[1:])
}

// runArgs runs proofread with args over stdin, [ungraded].
func runArgs(t *testing.T, args []string, stdin string) (code int, stdout, stderr string) {
	t.Helper()

	var out, errOut bytes.Buffer

	code = run(ungraded(args), strings.NewReader(stdin), &out, &errOut)

	return code, out.String(), errOut.String()
}

// runInto runs proofread with args, [ungraded], and decodes its stdout into
// into. A run with a deny finding exits 1 and still writes them.
func runInto(t *testing.T, args []string, into any) {
	t.Helper()

	var out, errOut bytes.Buffer

	if code := run(ungraded(args), strings.NewReader(""), &out, &errOut); code > exitFindings || errOut.String() != "" {
		t.Fatalf("run %v: code %d stderr %q", args, code, errOut.String())
	}

	if err := json.Unmarshal(out.Bytes(), into); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out.String())
	}
}
