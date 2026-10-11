package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestOutcomesPathFollowsXDGStateHome(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)

	got, err := outcomesPath()
	if err != nil || got != filepath.Join(state, "proofread", "outcomes.json") {
		t.Errorf("outcomesPath() = %q, %v", got, err)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")

	want := filepath.Join(home, ".local", "state", "proofread", "outcomes.json")
	if got, err := outcomesPath(); err != nil || got != want {
		t.Errorf("with XDG_STATE_HOME unset: outcomesPath() = %q, %v, want %q", got, err, want)
	}

	t.Setenv("XDG_STATE_HOME", "relative/state")

	if got, err := outcomesPath(); err != nil || got != want {
		t.Errorf("with a relative XDG_STATE_HOME: outcomesPath() = %q, %v, want %q", got, err, want)
	}
}

func TestLoadOutcomesRefusesWhatItCannotRead(t *testing.T) {
	dir := t.TempDir()

	missing, err := loadOutcomes(filepath.Join(dir, "none.json"))
	if err != nil || missing.Version != outcomesVersion || len(missing.Pending) != 0 || len(missing.Tally) != 0 {
		t.Errorf("a missing file: %+v, %v, want an empty state", missing, err)
	}

	cases := []struct{ name, body, want string }{
		{"not JSON", "{", "unexpected end of JSON input"},
		{"another version", `{"version":2}`, "is version 2, want 1"},
	}

	for _, tc := range cases {
		path := writeFile(t, dir, tc.name+".json", tc.body)

		if _, err := loadOutcomes(path); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to contain %q", tc.name, err, tc.want)
		}
	}
}

func TestSaveOutcomesRoundTripsAndKeepsTheFilePrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "proofread", "outcomes.json")
	in := outcomeState{
		Version: outcomesVersion,
		Pending: map[string]map[string]map[string]string{"/a.md": {"filler": {"abc": statusOpen}}},
		Tally:   map[string]outcomeTally{"filler": {Fixed: 2, Suppressed: 1}},
	}

	if err := saveOutcomes(path, in); err != nil {
		t.Fatal(err)
	}

	got, err := loadOutcomes(path)
	if err != nil || !reflect.DeepEqual(got, in) {
		t.Errorf("round trip: %+v, %v, want %+v", got, err, in)
	}

	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("state file: %v, %v, want mode 0600", info, err)
	}
}

func TestUpdateCountsEachOutcomeOnce(t *testing.T) {
	always := func(string) bool { return true }
	hidden := func() map[string]bool { return map[string]bool{"covered": true} }
	s := outcomeState{Pending: map[string]map[string]map[string]string{}, Tally: map[string]outcomeTally{}}

	s.update("/a.md", map[string]string{"gone": "filler", "kept": "filler", "covered": "filler"}, always, hidden)

	want := map[string]map[string]string{"filler": {"gone": statusOpen, "kept": statusOpen, "covered": statusOpen}}
	if !reflect.DeepEqual(s.Pending["/a.md"], want) || len(s.Tally) != 0 {
		t.Fatalf("a first run: pending %v tally %v, want every finding open", s.Pending, s.Tally)
	}

	s.update("/a.md", map[string]string{"kept": "filler", "new": "filler"}, always, hidden)

	want = map[string]map[string]string{"filler": {"kept": statusReported, "new": statusOpen}}
	if !reflect.DeepEqual(s.Pending["/a.md"], want) || s.Tally["filler"] != (outcomeTally{Fixed: 1, Suppressed: 1}) {
		t.Fatalf("a second run: pending %v tally %v", s.Pending, s.Tally)
	}

	s.update("/a.md", map[string]string{"kept": "filler", "new": "filler"}, always, hidden)

	if s.Tally["filler"] != (outcomeTally{Fixed: 1, Suppressed: 1}) {
		t.Errorf("a third run with nothing changed moved the tally: %v", s.Tally)
	}

	s.update("/a.md", nil, always, hidden)

	if _, ok := s.Pending["/a.md"]; ok || s.Tally["filler"] != (outcomeTally{Fixed: 3, Suppressed: 1}) {
		t.Errorf("a clean run: pending %v tally %v, want the path forgotten and three fixed", s.Pending, s.Tally)
	}
}

func TestUpdateLeavesRulesTheRunDidNotJudge(t *testing.T) {
	s := outcomeState{
		Pending: map[string]map[string]map[string]string{"/a.md": {"filler": {"x": statusOpen}, "terms": {"y": statusOpen}}},
		Tally:   map[string]outcomeTally{},
	}

	s.update("/a.md", nil, func(rule string) bool { return rule == "filler" }, func() map[string]bool { return nil })

	if !reflect.DeepEqual(s.Pending["/a.md"], map[string]map[string]string{"terms": {"y": statusOpen}}) ||
		s.Tally["filler"].Fixed != 1 || s.Tally["terms"].Fixed != 0 {
		t.Errorf("pending %v tally %v, want only filler resolved", s.Pending, s.Tally)
	}
}

func TestStatsFlagsRulesByNotUsefulRate(t *testing.T) {
	s := outcomeState{
		Pending: map[string]map[string]map[string]string{
			"/a.md": {"reported-rule": {"1": statusReported, "2": statusOpen}},
		},
		Tally: map[string]outcomeTally{
			"good":      {Fixed: 10},
			"edge":      {Fixed: 9, Suppressed: 1},
			"probation": {Fixed: 8, Suppressed: 2},
			"ship-off":  {Fixed: 3, Suppressed: 1},
		},
	}

	got := map[string]ruleStat{}
	for _, r := range s.stats() {
		got[r.Rule] = r
	}

	want := map[string]ruleStat{
		"good":          {Rule: "good", Fixed: 10},
		"edge":          {Rule: "edge", Fixed: 9, Suppressed: 1, NotUsefulRate: 0.1, Flag: flagProbation},
		"probation":     {Rule: "probation", Fixed: 8, Suppressed: 2, NotUsefulRate: 0.2, Flag: flagProbation},
		"ship-off":      {Rule: "ship-off", Fixed: 3, Suppressed: 1, NotUsefulRate: 0.25, Flag: flagShipOff},
		"reported-rule": {Rule: "reported-rule", Reported: 1, NotUsefulRate: 1, Flag: flagShipOff},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("stats = %+v, want %+v", got, want)
	}

	rows := s.stats()
	for i := 1; i < len(rows); i++ {
		if rows[i-1].Rule > rows[i].Rule {
			t.Errorf("stats are not sorted by rule: %v before %v", rows[i-1].Rule, rows[i].Rule)
		}
	}
}

func TestWithoutDirectivesBlanksSuppressionsInPlace(t *testing.T) {
	in := "# A\n<!-- proofread off filler: quoting -->\nIt simply works. proofread:ignore filler quoted\nEnd.\n"
	got := withoutDirectives(in)

	if len(got) != len(in) || strings.Count(got, "\n") != strings.Count(in, "\n") || strings.Contains(got, "proofread") {
		t.Errorf("withoutDirectives = %q, want the same layout with the directives blanked", got)
	}

	if !strings.Contains(got, "It simply works.") {
		t.Errorf("withoutDirectives dropped the text itself: %q", got)
	}
}

func TestRecordCountsFixedSuppressedAndReportedAcrossRuns(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)

	dir := t.TempDir()
	page := writeFile(t, dir, "a.md", "# A\n\nIt simply works. It basically works.\n")

	record := func() {
		t.Helper()

		if code, _, errOut := runArgs(t, []string{"reference", "-record", page}, ""); code != exitOK || errOut != "" {
			t.Fatalf("record: code %d stderr %q", code, errOut)
		}
	}

	stats := func() []ruleStat {
		t.Helper()

		code, out, errOut := runArgs(t, []string{"stats"}, "")
		if code != exitOK || errOut != "" {
			t.Fatalf("stats: code %d stderr %q", code, errOut)
		}

		var rows []ruleStat
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			t.Fatalf("stats output is not JSON: %v\n%s", err, out)
		}

		return rows
	}

	record()

	if rows := stats(); len(rows) != 0 {
		t.Fatalf("after one run: stats = %+v, want none, since nothing was seen twice", rows)
	}

	writeFile(t, dir, "a.md", "# A\n\nIt basically works.\n")
	record()

	want := []ruleStat{{Rule: "filler", Fixed: 1, Reported: 1, NotUsefulRate: 0.5, Flag: flagShipOff}}
	if rows := stats(); !reflect.DeepEqual(rows, want) {
		t.Fatalf("after fixing one: stats = %+v, want %+v", rows, want)
	}

	writeFile(t, dir, "a.md", "# A\n\n<!-- proofread off filler: quoting the user -->\nIt basically works.\n")
	record()

	want = []ruleStat{{Rule: "filler", Fixed: 1, Suppressed: 1, NotUsefulRate: 0.5, Flag: flagShipOff}}
	if rows := stats(); !reflect.DeepEqual(rows, want) {
		t.Fatalf("after suppressing the other: stats = %+v, want %+v", rows, want)
	}

	code, out, _ := runArgs(t, []string{"stats", "-format", "text"}, "")
	if code != exitOK || out != "filler               fixed 1  suppressed 1  reported 0  not useful 50.0%  ship-off\n" {
		t.Errorf("stats -format text: code %d stdout %q", code, out)
	}

	raw, err := os.ReadFile(filepath.Join(state, "proofread", "outcomes.json"))
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(raw), "basically") || strings.Contains(string(raw), "simply") {
		t.Errorf("the state file holds the judged text:\n%s", raw)
	}
}

func TestRecordKeepsTextsApartByPath(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	a := writeFile(t, t.TempDir(), "a.md", "# A\n\nIt simply works.\n")
	b := writeFile(t, t.TempDir(), "b.md", "# B\n\nIt simply works.\n")

	for _, p := range []string{a, b} {
		runArgs(t, []string{"reference", "-record", p}, "")
	}

	writeFile(t, filepath.Dir(a), "a.md", "# A\n\nIt works.\n")
	runArgs(t, []string{"reference", "-record", a}, "")

	_, out, _ := runArgs(t, []string{"stats"}, "")

	var rows []ruleStat
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatal(err)
	}

	if want := []ruleStat{{Rule: "filler", Fixed: 1}}; !reflect.DeepEqual(rows, want) {
		t.Errorf("stats = %+v, want only a.md's finding fixed: %+v", rows, want)
	}
}

func TestStatsWithNothingRecorded(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	if code, out, _ := runArgs(t, []string{"stats"}, ""); code != exitOK || out != "[]\n" {
		t.Errorf("stats: code %d stdout %q, want []", code, out)
	}

	if code, out, _ := runArgs(t, []string{"stats", "-format", "text"}, ""); code != exitOK || !strings.HasPrefix(out, "no outcomes recorded") {
		t.Errorf("stats -format text: code %d stdout %q", code, out)
	}

	if code, _, errOut := runArgs(t, []string{"stats", "-format", "xml"}, ""); code != exitError || errOut != "proofread: unknown -format \"xml\": want json or text\n" {
		t.Errorf("stats -format xml: code %d stderr %q", code, errOut)
	}
}
