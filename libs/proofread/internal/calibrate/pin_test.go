package calibrate

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/egladman/magus/libs/proofread"
)

// casePin holds every labeled case's findings as of ADR 0033. A kind fact
// moved out of a judge wrongly shows as a changed line. CASE_PIN_UPDATE=1
// rewrites it.
const casePin = "testdata/case-findings.jsonl"

type pinnedFinding struct {
	Rule     proofread.Rule     `json:"rule"`
	Decision proofread.Decision `json:"decision"`
	Line     int                `json:"line"`
	Column   int                `json:"column,omitempty"`
	Match    string             `json:"match,omitempty"`
	Weight   float64            `json:"weight,omitempty"`
}

type pinnedCase struct {
	File string         `json:"file"`
	Line int            `json:"line"`
	Kind proofread.Kind `json:"kind"`
	// House holds the findings with every rule set to deny, house style
	// included.
	Defaults []pinnedFinding `json:"defaults"`
	House    []pinnedFinding `json:"house"`
}

func pinned(found []proofread.Finding) []pinnedFinding {
	out := []pinnedFinding{}
	for _, f := range found {
		out = append(out, pinnedFinding{f.Rule, f.Decision, f.Line, f.Column, f.Match, f.Weight})
	}

	return out
}

func TestEveryCaseJudgesAsPinned(t *testing.T) {
	cases, err := LoadCases(LabeledCases())
	if err != nil {
		t.Fatal(err)
	}

	everyRule := map[proofread.Rule]proofread.Decision{}
	for _, r := range proofread.Rules() {
		everyRule[r] = proofread.DecisionDeny
	}

	var b bytes.Buffer

	enc := json.NewEncoder(&b)
	for _, c := range cases {
		if err := enc.Encode(pinnedCase{
			File: c.File, Line: c.Line, Kind: c.Kind,
			Defaults: pinned(c.Findings()), House: pinned(c.Findings(proofread.WithDecisions(everyRule))),
		}); err != nil {
			t.Fatal(err)
		}
	}

	if os.Getenv("CASE_PIN_UPDATE") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(casePin, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}

		return
	}

	want, err := os.ReadFile(casePin)
	if err != nil {
		t.Fatalf("%v: run with CASE_PIN_UPDATE=1 to write it", err)
	}

	got, wantLines := bytes.Split(b.Bytes(), []byte("\n")), bytes.Split(want, []byte("\n"))
	for i := range min(len(got), len(wantLines)) {
		if !bytes.Equal(got[i], wantLines[i]) {
			t.Errorf("%s:%d moved:\n got %s\nwant %s", casePin, i+1, got[i], wantLines[i])
		}
	}

	if len(got) != len(wantLines) {
		t.Fatalf("%s holds %d cases, the cases now %d", casePin, len(wantLines), len(got))
	}
}
