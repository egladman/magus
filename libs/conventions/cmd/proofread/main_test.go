package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/conventions/proofread"
)

const (
	fillerSimply = "Drop 'simply': state the fact."
	termsMessage = "Write 'subagent', not 'sub-agent'."
	nameSuffix   = "Rename 'configFor': no function or method name ends in the word Of or For."
)

// row is the finding proofread writes for rule, its code and page read from
// the catalog.
func row(node, source, kind string, rule proofread.Rule, decision proofread.Decision, message, match string) finding {
	f := finding{
		Node: node, Source: source, Kind: kind, Rule: string(rule), Decision: string(decision),
		Message: message, Match: match,
	}

	for _, doc := range proofread.Catalog() {
		if doc.Name == rule {
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

	code = run([]string{"doc-comment"}, strings.NewReader(stdin), &out, &errOut)

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

// assertArgs runs proofread with args over stdin and wants exit 0 with want
// on stdout.
func assertArgs(t *testing.T, args []string, stdin, want string) {
	t.Helper()

	var out, errOut bytes.Buffer

	code := run(args, strings.NewReader(stdin), &out, &errOut)
	if code != 0 || out.String() != want || errOut.String() != "" {
		t.Errorf("run:\n got code %d stdout %q stderr %q\nwant code 0 stdout %q", code, out.String(), errOut.String(), want)
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
	in := `[{"node":"n1","source":"a.go","language":"go","name":"Resolve","kind":"function","owner":"","doc":"Resolve simply returns the path."}]`

	assertRun(t, in, 0, rows(t, row("n1", "a.go", "doc-comment", proofread.RuleFiller, proofread.DecisionDeny, fillerSimply, "simply")), "")
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
		`{"node":"b","name":"Run","kind":"function","doc":"Run simply hands work to a sub-agent."},` +
		`{"node":"a","name":"Open","kind":"function","doc":"Open simply opens."}]`
	want := rows(t,
		row("b", "", "doc-comment", proofread.RuleFiller, proofread.DecisionDeny, fillerSimply, "simply"),
		row("b", "", "doc-comment", proofread.RuleTerms, proofread.DecisionDeny, termsMessage, "sub-agent"),
		row("a", "", "doc-comment", proofread.RuleFiller, proofread.DecisionDeny, fillerSimply, "simply"),
	)

	assertArgs(t, []string{"doc-comment", "-decisions", houseTable(t, proofread.RuleTerms)}, in, want)
}

func TestRunJudgesANameSuffixOnlyOnACallable(t *testing.T) {
	decisions := houseTable(t, proofread.RuleNameSuffix)
	found := rows(t, row("n", "", "doc-comment", proofread.RuleNameSuffix, proofread.DecisionDeny, nameSuffix, "configFor"))

	cases := []struct {
		kind string
		want string
	}{
		{"function", found},
		{"method", found},
		{"type", "[]\n"},
		{"", "[]\n"},
	}

	for _, tc := range cases {
		t.Run("kind "+tc.kind, func(t *testing.T) {
			in := `[{"node":"n","name":"configFor","kind":"` + tc.kind + `","doc":""}]`

			assertArgs(t, []string{"doc-comment", "-decisions", decisions}, in, tc.want)
		})
	}
}

func TestRunJudgesReferenceFilesInArgumentOrder(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.md", "# A\n\nIt simply works.\n")
	b := writeFile(t, dir, "b.md", "- **Cache:** on\n")

	want := rows(t,
		row(b, b+":1", "reference", proofread.RuleReplyVoice, proofread.DecisionDeny,
			"Drop the bold label '**Cache:**': write the item as a sentence that opens with its subject.", "**Cache:**"),
		row(a, a+":3", "reference", proofread.RuleFiller, proofread.DecisionDeny, fillerSimply, "simply"),
	)

	assertArgs(t, []string{"reference", b, a}, "", want)
}

func TestRunJudgesATemplateAtItsSourceLine(t *testing.T) {
	path := writeFile(t, t.TempDir(), "SKILL.md", "# Skill\n\n{{if .Full}}One.\n\nTwo.\n{{end}}\nRun it in order to replay.\n")

	want := rows(t, row(path, path+":7", "agent-instructions-template", proofread.RuleWordy, proofread.DecisionDeny,
		"Write 'to', not 'in order to'.", "in order to"))

	assertArgs(t, []string{"agent-instructions-template", path}, "", want)
}

func TestRunJudgesAGuideOnTheGuideRules(t *testing.T) {
	path := writeFile(t, t.TempDir(), "guide.md", "# Guide\n\nThen we run it.\n")

	want := rows(t, row(path, path+":3", "guide", proofread.RuleSecondPerson, proofread.DecisionDeny,
		"Address the reader as you, not 'we': a guide speaks to the person following it, "+
			"and names magus or the project where it means them.", "we"))

	assertArgs(t, []string{"guide", path}, "", want)
}

func TestRunJudgesAChangeDescriptionFromStdin(t *testing.T) {
	want := rows(t, row("change-description", "change-description:2", "change-description", proofread.RuleLeadContext,
		proofread.DecisionDeny, "It opens with a heading: open the description with what a reader can now do "+
			"or no longer has to do, then how the work came up and why it mattered, then the changes, as in 'The first "+
			"query after an edit answers from a graph that is already current. Until now the graph rebuilt inline on that "+
			"query.'", "## Summary"))

	assertArgs(t, []string{"change-description"}, "fix: pin the key\n## Summary\n", want)
}

func TestRunJudgesAReviewReplyFromStdin(t *testing.T) {
	want := rows(t, row("review-reply", "review-reply:1", "review-reply", proofread.RuleReplyOpener, proofread.DecisionDeny,
		"Drop 'No,' and open with the fact and its evidence, as in 'This needs a lock: the map is "+
			"written from two goroutines.'", "No,"))

	assertArgs(t, []string{"review-reply"}, "No, the map is shared by the two workers.", want)
}

// A reply that is its author's fourth in the thread draws one finding, which
// advises unless a table says otherwise.
func TestRunTakesTheThreadLengthAndTheDecisions(t *testing.T) {
	const reply = "The map is shared by the two workers."

	message := "This is reply 4 from you in the thread: offer a call to settle it, as in 'Want to talk this " +
		"through for ten minutes?'"
	deny := writeFile(t, t.TempDir(), "d.json", `{"rules":{"long-thread":"deny"}}`)
	off := writeFile(t, t.TempDir(), "d.json", `{"rules":{"long-thread":"off"}}`)

	assertArgs(t, []string{"review-reply", "-thread-length", "2"}, reply, "[]\n")
	assertArgs(t, []string{"review-reply", "-thread-length", "3"}, reply,
		rows(t, row("review-reply", "review-reply:0", "review-reply", proofread.RuleLongThread, proofread.DecisionAdvise, message, "")))
	assertArgs(t, []string{"review-reply", "-thread-length", "3", "-decisions", deny}, reply,
		rows(t, row("review-reply", "review-reply:0", "review-reply", proofread.RuleLongThread, proofread.DecisionDeny, message, "")))
	assertArgs(t, []string{"review-reply", "-thread-length", "3", "-decisions", off}, reply, "[]\n")
}

func TestRunSelectsRulesByOnlyAndDecisions(t *testing.T) {
	const doc = `[{"node":"n","name":"Run","kind":"function","doc":"Run simply hands work to a sub-agent."}]`

	filler := row("n", "", "doc-comment", proofread.RuleFiller, proofread.DecisionDeny, fillerSimply, "simply")
	terms := row("n", "", "doc-comment", proofread.RuleTerms, proofread.DecisionDeny, termsMessage, "sub-agent")
	decisions := houseTable(t, proofread.RuleTerms, proofread.RuleCommentBlock)

	assertArgs(t, []string{"doc-comment"}, doc, rows(t, filler))
	assertArgs(t, []string{"doc-comment", "-decisions", decisions}, doc, rows(t, filler, terms))
	assertArgs(t, []string{"doc-comment", "-decisions", decisions, "-only", "terms, comment-block"}, doc, rows(t, terms))
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

	want := rows(t, row(page, page+":1", "reference", proofread.RuleFiller, proofread.DecisionDeny, fillerSimply, "simply"))

	assertArgs(t, []string{"reference", "-decisions", decisions, post, page}, "", want)
}

func TestRunReadsTheDecisionsFromStdinForFiles(t *testing.T) {
	page := writeFile(t, t.TempDir(), "page.md", "It simply works.\n")

	assertArgs(t, []string{"reference", "-decisions", "-", page}, `{"rules":{"filler":"off"}}`, "[]\n")
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
			"  doc-comment                  deny\n",
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
			if code != 1 || out.String() != "" || !strings.HasPrefix(errOut.String(), tc.lead) {
				t.Fatalf("run: got code %d stdout %q stderr %q, want code 1 and stderr starting %q", code, out.String(), errOut.String(), tc.lead)
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

func TestRunExitsOneOnAFlagItCannotUse(t *testing.T) {
	dir := t.TempDir()
	table := func(body string) string { return writeFile(t, t.TempDir(), "d.json", body) }
	unknownRule := table(`{"rules":{"fillers":"deny"}}`)
	badDecision := table(`{"rules":{"filler":"error"}}`)
	unknownScopedRule := table(`{"paths":{"*.md":{"tone":"off"}}}`)
	noMatch := table(`{"paths":{"blog/**":{"filler":"off"}}}`)
	unknownField := table(`{"rule":{"filler":"off"}}`)
	page := writeFile(t, dir, "page.md", "It works.\n")

	cases := []struct {
		name       string
		args       []string
		wantStderr string
	}{
		{"a path for symbols", []string{"doc-comment", "a.md"},
			"proofread: symbols are read from stdin; a path needs the reference subcommand\n"},
		{"rules with an argument", []string{"rules", "filler"}, "proofread: rules takes no arguments\n"},
		{"explain with no rule", []string{"explain"}, "proofread: explain takes one rule name or code\n"},
		{"explain an unknown rule", []string{"explain", "fillers"}, "proofread: unknown rule or code \"fillers\"\n"},
		{"a path for a change description", []string{"change-description", "a.md"},
			"proofread: a change-description is read from stdin, not from a path\n"},
		{"a missing file", []string{"reference", "missing.md"},
			"proofread: read missing.md: open missing.md: no such file or directory\n"},
		{"unknown rule to keep", []string{"doc-comment", "-only", "filler,fillers"}, "proofread: unknown rule \"fillers\"\n"},
		{"an off rule to keep", []string{"doc-comment", "-only", "terms"},
			"proofread: -only names \"terms\", which is off: set it to advise or deny in -decisions\n"},
		{"unknown rule in the table", []string{"doc-comment", "-decisions", unknownRule},
			"proofread: decisions " + unknownRule + ": unknown rule \"fillers\"\n"},
		{"unknown decision", []string{"doc-comment", "-decisions", badDecision},
			"proofread: decisions " + badDecision + ": rule \"filler\": unknown decision \"error\": want off, advise or deny\n"},
		{"unknown rule under a path", []string{"reference", "-decisions", unknownScopedRule, page},
			"proofread: decisions " + unknownScopedRule + ": path \"*.md\": unknown rule \"tone\"\n"},
		{"a glob matching no argument", []string{"reference", "-decisions", noMatch, page},
			"proofread: decisions " + noMatch + ": path \"blog/**\" matches no file argument\n"},
		{"an unknown field", []string{"doc-comment", "-decisions", unknownField},
			"proofread: decisions " + unknownField + ": json: unknown field \"rule\"\n"},
		{"decisions on stdin beside the text", []string{"review-reply", "-decisions", "-"},
			"proofread: -decisions -: the review-reply itself is read from stdin; name the table's file\n"},
		{"a missing table", []string{"doc-comment", "-decisions", "missing.json"},
			"proofread: read decisions missing.json: open missing.json: no such file or directory\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer

			code := run(tc.args, strings.NewReader("[]"), &out, &errOut)
			if code != 1 || out.String() != "" || errOut.String() != tc.wantStderr {
				t.Errorf("run: got code %d stdout %q stderr %q, want code 1 stderr %q", code, out.String(), errOut.String(), tc.wantStderr)
			}
		})
	}
}

func TestRunExitsOneOnInputThatIsNotAnArrayOfRecords(t *testing.T) {
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
			assertRun(t, tc.in, 1, "", tc.wantStderr)
		})
	}
}
