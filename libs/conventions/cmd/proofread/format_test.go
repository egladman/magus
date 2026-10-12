package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/conventions/proofread"
)

func TestSplitSource(t *testing.T) {
	cases := []struct {
		source, path string
		line         int
	}{
		{"docs/a.md:12", "docs/a.md", 12},
		{"pkg/run.go:12:3", "pkg/run.go", 12},
		{"docs/a.md:0", "docs/a.md", 0},
		{"change-description:0", "change-description", 0},
		{"docs/a.md", "docs/a.md", 0},
		{"review-reply", "review-reply", 0},
		{"a:b", "a:b", 0},
		{"", "", 0},
	}

	for _, tc := range cases {
		path, line := splitSource(tc.source)
		if path != tc.path || line != tc.line {
			t.Errorf("splitSource(%q) = %q, %d; want %q, %d", tc.source, path, line, tc.path, tc.line)
		}
	}
}

func TestPositionFallsBackToTheSourceLine(t *testing.T) {
	cases := []struct {
		name string
		f    finding
		want [4]int
	}{
		{"its own span", finding{Source: "a.md:3", Line: 3, Column: 2, EndLine: 3, EndColumn: 8}, [4]int{3, 2, 3, 8}},
		{"a line with no span", finding{Source: "a.md:3", Line: 3}, [4]int{3, 0, 0, 0}},
		{"the source's line", finding{Source: "pkg/run.go:40"}, [4]int{40, 0, 0, 0}},
		{"nowhere", finding{Source: "a.md:0"}, [4]int{}},
	}

	for _, tc := range cases {
		line, column, endLine, endColumn := position(tc.f)
		if got := [4]int{line, column, endLine, endColumn}; got != tc.want {
			t.Errorf("%s: position = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func rule(name string) finding {
	for _, d := range proofread.Catalog() {
		if string(d.Name) == name {
			return finding{Rule: name, Code: string(d.Code), URL: d.URL()}
		}
	}

	panic("no rule " + name)
}

func filler(source string, decision string) finding {
	f := rule("filler")
	f.Node, f.Source, f.Kind, f.Decision = source, source+":2", "reference", decision
	f.Message, f.Match = "Drop 'Simply': state the fact.", "Simply"

	return f.at(2, 5, 11)
}

func decodeInto(t *testing.T, format string, out []finding, into any) {
	t.Helper()

	var b bytes.Buffer
	if err := writeFindings(&b, format, out); err != nil {
		t.Fatal(err)
	}

	if err := json.Unmarshal(b.Bytes(), into); err != nil {
		t.Fatalf("%s output is not JSON: %v\n%s", format, err, b.String())
	}
}

func TestWriteFindingsJSONIsTheFindingArray(t *testing.T) {
	in := []finding{filler("a.md", "deny")}

	var b bytes.Buffer
	if err := writeFindings(&b, "json", in); err != nil {
		t.Fatal(err)
	}

	if b.String() != rows(t, in...) {
		t.Errorf("json = %q, want %q", b.String(), rows(t, in...))
	}
}

func TestWriteFindingsRejectsAnUnknownFormat(t *testing.T) {
	var b bytes.Buffer

	err := writeFindings(&b, "xml", nil)
	if err == nil || err.Error() != `unknown -format "xml": want json, sarif, rdjson or text` {
		t.Errorf("error = %v", err)
	}

	if b.Len() != 0 {
		t.Errorf("wrote %q before failing", b.String())
	}
}

func TestSARIFLogsEveryRuleAndEachResultsPlace(t *testing.T) {
	advised := filler("b.md", "advise")
	advised.Message = "advised"

	var log sarifLog

	decodeInto(t, "sarif", []finding{filler("a.md", "deny"), advised}, &log)

	if log.Version != "2.1.0" || log.Schema != sarifSchema || len(log.Runs) != 1 {
		t.Fatalf("log = %+v", log)
	}

	run := log.Runs[0]
	if run.Tool.Driver.Name != "proofread" {
		t.Errorf("driver = %q", run.Tool.Driver.Name)
	}

	catalog := proofread.Catalog()
	if len(run.Tool.Driver.Rules) != len(catalog) {
		t.Fatalf("%d rules, want the catalog's %d", len(run.Tool.Driver.Rules), len(catalog))
	}

	for i, d := range catalog {
		r := run.Tool.Driver.Rules[i]
		if r.ID != string(d.Name) || r.HelpURI != d.URL() || r.ShortDescription.Text != d.Catches {
			t.Errorf("rule %d = %+v, want %s %s %q", i, r, d.Name, d.URL(), d.Catches)
		}
	}

	if len(run.Results) != 2 {
		t.Fatalf("results = %+v", run.Results)
	}

	first, second := run.Results[0], run.Results[1]

	region := &sarifRegion{StartLine: 2, StartColumn: 5, EndLine: 2, EndColumn: 11}
	if first.RuleID != "filler" || first.Level != "error" || first.Message.Text != "Drop 'Simply': state the fact." ||
		first.Locations[0].PhysicalLocation.ArtifactLocation.URI != "a.md" ||
		!reflect.DeepEqual(first.Locations[0].PhysicalLocation.Region, region) ||
		run.Tool.Driver.Rules[first.RuleIndex].ID != "filler" {
		t.Errorf("first result = %+v", first)
	}

	if second.Level != "warning" {
		t.Errorf("an advise finding has level %q, want warning", second.Level)
	}

	if first.PartialFingerprints[fingerprintKey] == "" ||
		first.PartialFingerprints[fingerprintKey] == second.PartialFingerprints[fingerprintKey] {
		t.Errorf("fingerprints %v and %v: want two distinct values", first.PartialFingerprints, second.PartialFingerprints)
	}
}

func TestSARIFFingerprintIsStableAndSeparatesRepeats(t *testing.T) {
	a := filler("a.md", "deny")
	moved := a.at(9, 1, 7)
	moved.Source = "a.md:9"
	reworded := a
	reworded.Message = "another message"

	fingerprints := func(fs ...finding) []string {
		var log sarifLog

		decodeInto(t, "sarif", fs, &log)

		var out []string
		for _, r := range log.Runs[0].Results {
			out = append(out, r.PartialFingerprints[fingerprintKey])
		}

		return out
	}

	base := fingerprints(a)[0]

	if got := fingerprints(moved)[0]; got != base {
		t.Errorf("a finding that moved lines changed its fingerprint: %s, was %s", got, base)
	}

	if got := fingerprints(reworded)[0]; got != base {
		t.Errorf("a reworded message changed the fingerprint: %s, was %s", got, base)
	}

	other := filler("b.md", "deny")
	if got := fingerprints(other)[0]; got == base {
		t.Errorf("a finding in another file shares the fingerprint %s", base)
	}

	twice := fingerprints(a, a)
	if twice[0] != base || twice[1] == base {
		t.Errorf("a repeated finding: fingerprints %v, want the first to be %s and the second to differ", twice, base)
	}
}

func TestTextLineReadsPathLineColumnCodeRuleDecisionMessage(t *testing.T) {
	cases := []struct {
		name string
		f    finding
		want string
	}{
		{"a span", finding{Source: "a.md:3", Line: 3, Column: 4, Code: "PRF4001", Rule: "filler", Decision: "deny", Message: "Drop it."},
			"a.md:3:4: PRF4001 filler [deny] Drop it."},
		{"a line without a column", finding{Source: "a.md:3", Code: "PRF4001", Rule: "filler", Decision: "advise", Message: "Drop it."},
			"a.md:3: PRF4001 filler [advise] Drop it."},
		{"a whole text", finding{Source: "a.md", Node: "a.md", Code: "PRF2001", Rule: "budget", Decision: "deny", Message: "Cut it."},
			"a.md: PRF2001 budget [deny] Cut it."},
		{"no path", finding{Source: "", Node: "symbol-node", Code: "PRF6001", Rule: "comment-block", Decision: "deny", Message: "Split it."},
			"symbol-node: PRF6001 comment-block [deny] Split it."},
		{"a message on several lines", finding{Source: "a.md:1", Line: 1, Code: "PRF4001", Rule: "filler", Decision: "deny", Message: "Drop it:\n  state the fact."},
			"a.md:1: PRF4001 filler [deny] Drop it: state the fact."},
	}

	for _, tc := range cases {
		var b bytes.Buffer
		if err := writeFindings(&b, "text", []finding{tc.f}); err != nil {
			t.Fatal(err)
		}

		if b.String() != tc.want+"\n" {
			t.Errorf("%s: got %q, want %q", tc.name, b.String(), tc.want+"\n")
		}
	}
}

func TestTextSummaryCountsByDecision(t *testing.T) {
	deny, advise := finding{Decision: "deny"}, finding{Decision: "advise"}

	cases := []struct {
		in   []finding
		want string
	}{
		{nil, ""},
		{[]finding{deny}, "proofread: 1 finding (1 deny, 0 advise)\n"},
		{[]finding{deny, advise, advise}, "proofread: 3 findings (1 deny, 2 advise)\n"},
	}

	for _, tc := range cases {
		if got := textSummary(tc.in); got != tc.want {
			t.Errorf("textSummary(%d findings) = %q, want %q", len(tc.in), got, tc.want)
		}
	}
}

func TestFingerprintsAreTheSARIFPartialFingerprints(t *testing.T) {
	a := rule("filler")
	a.Source, a.Match, a.Decision = "a.md:3", "simply", "deny"

	var raw bytes.Buffer
	if err := writeFindings(&raw, "sarif", []finding{a, a}); err != nil {
		t.Fatal(err)
	}

	var log sarifLog
	if err := json.Unmarshal(raw.Bytes(), &log); err != nil {
		t.Fatal(err)
	}

	want := fingerprints([]finding{a, a})
	for i, r := range log.Runs[0].Results {
		if got := r.PartialFingerprints[fingerprintKey]; got != want[i] {
			t.Errorf("result %d: SARIF fingerprint %s, fingerprints() %s", i, got, want[i])
		}
	}
}

func TestSARIFRegionOmitsWhatIsNotKnown(t *testing.T) {
	budget := rule("comment-block")
	budget.Source, budget.Decision, budget.Message = "pkg/run.go:40", "deny", "Split it."

	nowhere := rule("message-length")
	nowhere.Source, nowhere.Decision, nowhere.Message = "message:0", "deny", "Shorten it."

	lineOnly := rule("filler")
	lineOnly.Source, lineOnly.Decision, lineOnly.Line = "a.md:7", "deny", 7

	var raw bytes.Buffer
	if err := writeFindings(&raw, "sarif", []finding{budget, nowhere, lineOnly}); err != nil {
		t.Fatal(err)
	}

	var log struct {
		Runs []struct {
			Results []struct {
				Locations []struct {
					PhysicalLocation map[string]json.RawMessage `json:"physicalLocation"`
				} `json:"locations"`
			} `json:"results"`
		} `json:"runs"`
	}

	if err := json.Unmarshal(raw.Bytes(), &log); err != nil {
		t.Fatal(err)
	}

	want := []string{`{"startLine":40}`, "", `{"startLine":7}`}

	for i, r := range log.Runs[0].Results {
		got := string(r.Locations[0].PhysicalLocation["region"])
		if got != want[i] {
			t.Errorf("result %d region = %q, want %q", i, got, want[i])
		}
	}
}

func TestSARIFOfNoFindingsHasEmptyResults(t *testing.T) {
	var b bytes.Buffer
	if err := writeFindings(&b, "sarif", nil); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(b.String(), `"results":[]`) {
		t.Errorf("results are not an empty array:\n%.200s", b.String())
	}
}

func TestRDJSONCarriesSeverityCodeRangeAndSuggestions(t *testing.T) {
	fix := rule("terms")
	fix.Source, fix.Decision, fix.Message, fix.Match = "docs/a.md:3", "deny", "Write 'subagent', not 'sub-agent'.", "sub-agent"
	fix.Replacements = []string{"subagent", "agent"}
	fix = fix.at(3, 10, 19)

	advised := filler("docs/b.md", "advise")

	var result rdResult

	decodeInto(t, "rdjson", []finding{fix, advised}, &result)

	if result.Source.Name != "proofread" || len(result.Diagnostics) != 2 {
		t.Fatalf("result = %+v", result)
	}

	rng := rdRange{Start: rdPosition{Line: 3, Column: 10}, End: &rdPosition{Line: 3, Column: 19}}
	want := rdDiagnostic{
		Message:  "Write 'subagent', not 'sub-agent'.",
		Location: rdLocation{Path: "docs/a.md", Range: &rng},
		Severity: "ERROR",
		Code:     rdCode{Value: fix.Code, URL: fix.URL},
		Suggestions: []rdSuggestion{
			{Range: rng, Text: "subagent"},
			{Range: rng, Text: "agent"},
		},
	}

	if !reflect.DeepEqual(result.Diagnostics[0], want) {
		t.Errorf("diagnostic:\n got %+v\nwant %+v", result.Diagnostics[0], want)
	}

	if d := result.Diagnostics[1]; d.Severity != "WARNING" || len(d.Suggestions) != 0 || d.Code.Value != "PRF4001" {
		t.Errorf("an advise finding with no replacements = %+v", d)
	}
}

func TestRDJSONSuggestsOnlyWithAFullSpan(t *testing.T) {
	f := rule("terms")
	f.Source, f.Decision, f.Replacements = "a.md:3", "deny", []string{"subagent"}
	f.Line = 3

	var result rdResult

	decodeInto(t, "rdjson", []finding{f}, &result)

	d := result.Diagnostics[0]
	if len(d.Suggestions) != 0 || d.Location.Range == nil || d.Location.Range.Start.Line != 3 ||
		d.Location.Range.Start.Column != 0 || d.Location.Range.End != nil {
		t.Errorf("a finding with a line and no columns = %+v", d)
	}
}

func TestRDJSONOfNoFindingsHasEmptyDiagnostics(t *testing.T) {
	var b bytes.Buffer
	if err := writeFindings(&b, "rdjson", nil); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(b.String(), `"diagnostics":[]`) {
		t.Errorf("diagnostics are not an empty array:\n%s", b.String())
	}
}
