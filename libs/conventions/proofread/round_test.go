package proofread

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestWilsonLower(t *testing.T) {
	tests := []struct {
		name       string
		correct, n int
		z          float64
		want       float64
	}{
		// Newcombe, Two-sided confidence intervals for the single proportion,
		// Statistics in Medicine 17 (1998), Table II, method 3 (score).
		{"newcombe 81 of 263", 81, 263, 1.96, 0.2553},
		{"newcombe 15 of 148", 15, 148, 1.96, 0.0624},
		{"newcombe 0 of 20", 0, 20, 1.96, 0},
		{"newcombe 1 of 29", 1, 29, 1.96, 0.0061},
		// Round 4's scores.json, computed by a separate script.
		{"round 4 participles", 36, 40, 1.96, 0.7695},
		{"round 4 issue-repro", 26, 40, 1.96, 0.4951},
		{"round 4 absolute", 6, 40, 1.96, 0.0706},
		{"no trials", 0, 0, 1.96, 0},
		// The method's advise bar: 4 wrong of 30 passes, 5 does not.
		{"advise bar passes", 26, 30, 1.96, 0.7032},
		{"advise bar fails", 25, 30, 1.96, 0.6644},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WilsonLower(tt.correct, tt.n, tt.z)
			if math.Abs(got-tt.want) > 5e-5 {
				t.Errorf("WilsonLower(%d, %d, %v) = %.6f, want %.4f", tt.correct, tt.n, tt.z, got, tt.want)
			}
		})
	}
}

func TestWilsonLowerPanicsPastN(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("WilsonLower(3, 2) did not panic")
		}
	}()

	WilsonLower(3, 2, 1.96)
}

func TestNominalAlpha(t *testing.T) {
	// Krippendorff, Computing Krippendorff's Alpha-Reliability (2011), the
	// binary example: two coders, ten units, no missing values; alpha 0.095.
	binary := pairs(
		"0100000010",
		"1110010000",
	)

	// The same paper's nominal example: four coders, twelve units, missing
	// values as "."; alpha 0.743. Unit 12 has one value and is not pairable.
	nominal := columns(
		"123321412...",
		"1233224125.3",
		".3332342251.",
		"12332441251.",
	)

	tests := []struct {
		name    string
		units   [][]string
		want    float64
		defined bool
	}{
		{"krippendorff binary", binary, 0.095, true},
		{"krippendorff nominal", nominal, 0.743, true},
		{"perfect agreement", pairs("CWCW", "CWCW"), 1, true},
		{"one value throughout", pairs("CCCC", "CCCC"), 0, false},
		{"no pairable unit", [][]string{{"C"}, {"W"}}, 0, false},
		// Round 4, absolute in git-commit: eight agreed WRONG, one split.
		{"one split among agreed", pairs("WWWWWWWWW", "WWWWWWWWC"), 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := NominalAlpha(tt.units)
			if ok != tt.defined {
				t.Fatalf("defined = %v, want %v", ok, tt.defined)
			}

			if math.Abs(got-tt.want) > 5e-4 {
				t.Errorf("alpha = %.4f, want %.3f", got, tt.want)
			}
		})
	}
}

// TestRoundRecordsCarryNoText holds the committed round records to counts:
// a field that could hold a candidate's text, a label or a note quoting one
// keeps third-party text out of git only if no record has it.
func TestRoundRecordsCarryNoText(t *testing.T) {
	paths, err := filepath.Glob("testdata/rounds/*.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no round records: %v", err)
	}

	textKeys := []string{"text", "sentence", "context", "match", "message", "note", "reason", "label", "thread"}
	known := Rules()

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}

		var rec struct {
			Round      int                        `json:"round"`
			RuleCommit string                     `json:"rule_commit"`
			Seed       json.RawMessage            `json:"seed"`
			Rules      map[string]json.RawMessage `json:"rules"`
		}
		if err := json.Unmarshal(data, &rec); err != nil {
			t.Fatalf("%s: %v", p, err)
		}

		if want := fmt.Sprintf("round%d.json", rec.Round); filepath.Base(p) != want || rec.RuleCommit == "" || len(rec.Seed) == 0 {
			t.Errorf("%s: round %d, rule_commit %q, seed %s; want the file named %s with both recorded or \"unrecorded\"",
				p, rec.Round, rec.RuleCommit, rec.Seed, want)
		}

		for name := range rec.Rules {
			if !slices.Contains(known, Rule(name)) {
				t.Errorf("%s: rule %q is no rule proofread knows", p, name)
			}
		}

		var tree any
		if err := json.Unmarshal(data, &tree); err != nil {
			t.Fatal(err)
		}

		walkJSON(tree, func(key string, v any) {
			if slices.Contains(textKeys, key) {
				t.Errorf("%s: field %q could carry a candidate's text or label", p, key)
			}

			if s, ok := v.(string); ok && len(s) > 400 {
				t.Errorf("%s: field %q holds %d bytes of text", p, key, len(s))
			}
		})
	}
}

func walkJSON(v any, visit func(key string, v any)) {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			visit(k, child)
			walkJSON(child, visit)
		}
	case []any:
		for _, child := range x {
			visit("", child)
			walkJSON(child, visit)
		}
	}
}

// pairs builds one unit per position from two coders' strings, one value per
// byte.
func pairs(a, b string) [][]string {
	out := make([][]string, len(a))
	for i := range a {
		out[i] = []string{a[i : i+1], b[i : i+1]}
	}

	return out
}

// columns builds one unit per position from each coder's string, one value
// per byte, "." for a missing value.
func columns(coders ...string) [][]string {
	n := 0
	for _, c := range coders {
		n = max(n, len(c))
	}

	out := make([][]string, n)

	for _, c := range coders {
		for i := range c {
			if c[i] != '.' {
				out[i] = append(out[i], c[i:i+1])
			}
		}
	}

	return out
}
