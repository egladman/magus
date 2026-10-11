package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func found(file, rule, match string, line int) finding {
	return finding{
		Node: file, Source: file + ":" + string(rune('0'+line)), Rule: rule, Match: match, Line: line,
		Decision: "deny",
	}
}

func TestApplyBaselineReportsTheFindingsPastTheCounts(t *testing.T) {
	a1, a2, a3 := found("a.md", "filler", "Simply", 1), found("a.md", "filler", "Basically", 2), found("a.md", "filler", "Just", 3)
	w1 := found("a.md", "wordy", "in order to", 4)
	b1 := found("b.md", "filler", "Simply", 1)

	cases := []struct {
		name   string
		counts string
		in     []finding
		want   []finding
	}{
		{"every finding within its count", `{"a.md":{"filler":3,"wordy":1},"b.md":{"filler":1}}`,
			[]finding{a1, a2, a3, w1, b1}, []finding{}},
		{"a count is a ceiling", `{"a.md":{"filler":9}}`, []finding{a1}, []finding{}},
		{"the last findings are the excess", `{"a.md":{"filler":1}}`, []finding{a1, a2, a3}, []finding{a2, a3}},
		{"a rule the file does not name", `{"a.md":{"filler":3}}`, []finding{a1, w1}, []finding{w1}},
		{"a file the baseline does not name", `{"a.md":{"filler":3}}`, []finding{a1, b1}, []finding{b1}},
		{"a zero count", `{"a.md":{"filler":0}}`, []finding{a1}, []finding{a1}},
		{"an empty baseline", `{}`, []finding{a1, b1}, []finding{a1, b1}},
		{"no findings", `{"a.md":{"filler":1}}`, nil, []finding{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFile(t, t.TempDir(), "baseline.json", `{"version":1,"counts":`+tc.counts+`}`)

			got, err := applyBaseline(path, false, tc.in)
			if err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("excess:\n got %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestApplyBaselineCountsPerFileFromTheSourcePath(t *testing.T) {
	symbol := finding{Node: "go:pkg.Run", Source: "pkg/run.go:12:3", Rule: "filler"}
	other := finding{Node: "go:pkg.Stop", Source: "pkg/run.go:40", Rule: "filler"}
	path := writeFile(t, t.TempDir(), "baseline.json", `{"version":1,"counts":{"pkg/run.go":{"filler":1}}}`)

	got, err := applyBaseline(path, false, []finding{symbol, other})
	if err != nil {
		t.Fatal(err)
	}

	if want := []finding{other}; !reflect.DeepEqual(got, want) {
		t.Errorf("excess = %+v, want %+v", got, want)
	}
}

func TestApplyBaselinePruneWritesThisRunsCounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.json")
	in := []finding{
		found("b.md", "filler", "Simply", 1), found("a.md", "wordy", "in order to", 2),
		found("a.md", "filler", "Basically", 3), found("a.md", "filler", "Just", 4),
	}

	got, err := applyBaseline(path, true, in)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(got, []finding{}) {
		t.Errorf("a prune reports %+v, want none", got)
	}

	const want = `{
  "version": 1,
  "counts": {
    "a.md": {
      "filler": 2,
      "wordy": 1
    },
    "b.md": {
      "filler": 1
    }
  }
}
`

	if body, _ := os.ReadFile(path); string(body) != want {
		t.Errorf("baseline:\n got %s\nwant %s", body, want)
	}

	again, err := applyBaseline(path, false, in)
	if err != nil || len(again) != 0 {
		t.Errorf("a run over the same findings reports %+v, %v; want none", again, err)
	}
}

func TestApplyBaselinePruneLowersACeiling(t *testing.T) {
	path := writeFile(t, t.TempDir(), "baseline.json", `{"version":1,"counts":{"a.md":{"filler":5},"gone.md":{"filler":2}}}`)

	if _, err := applyBaseline(path, true, []finding{found("a.md", "filler", "Simply", 1)}); err != nil {
		t.Fatal(err)
	}

	got, err := applyBaseline(path, false, []finding{found("a.md", "filler", "Simply", 1), found("a.md", "filler", "Just", 2)})
	if err != nil || len(got) != 1 {
		t.Errorf("after the prune a second finding is excess: got %+v, %v", got, err)
	}

	if body, _ := os.ReadFile(path); strings.Contains(string(body), "gone.md") {
		t.Errorf("a prune keeps a file with no findings:\n%s", body)
	}
}

func TestApplyBaselinePruneWritesAnEmptyBaselineForNoFindings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.json")

	if _, err := applyBaseline(path, true, nil); err != nil {
		t.Fatal(err)
	}

	if body, _ := os.ReadFile(path); string(body) != "{\n  \"version\": 1,\n  \"counts\": {}\n}\n" {
		t.Errorf("baseline = %q", body)
	}
}

func TestApplyBaselineErrors(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name, path string
		prune      bool
		want       string
	}{
		{"prune without a baseline", "", true, "-prune needs -baseline"},
		{"a missing file", filepath.Join(dir, "missing.json"), false, "missing.json does not exist: run with -prune to create it"},
		{"not JSON", writeFile(t, dir, "bad.json", "nope"), false, "read baseline"},
		{"an unknown field", writeFile(t, dir, "field.json", `{"version":1,"counts":{},"extra":1}`), false, "unknown field"},
		{"another version", writeFile(t, dir, "version.json", `{"version":2,"counts":{}}`), false, "is version 2, want 1"},
		{"no version", writeFile(t, dir, "none.json", `{"counts":{}}`), false, "is version 0, want 1"},
		{"data after the object", writeFile(t, dir, "tail.json", `{"version":1,"counts":{}} {}`), false, "data after the object"},
		{"a directory for the file", dir, false, "read baseline"},
		{"an unwritable path", filepath.Join(dir, "no", "such", "dir", "b.json"), true, "write baseline"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := applyBaseline(tc.path, tc.prune, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestApplyBaselineWithoutAPathPassesFindingsThrough(t *testing.T) {
	in := []finding{found("a.md", "filler", "Simply", 1)}

	got, err := applyBaseline("", false, in)
	if err != nil || !reflect.DeepEqual(got, in) {
		t.Errorf("got %+v, %v; want the findings unchanged", got, err)
	}
}
