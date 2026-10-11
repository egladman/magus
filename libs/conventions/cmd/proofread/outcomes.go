package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/conventions/proofread"
)

const (
	outcomesVersion = 1

	// statusOpen marks a finding the last -record run reported once, and
	// statusReported one a later run reported again.
	statusOpen     = "open"
	statusReported = "reported"

	// probationRate and shipOffRate are the not-useful rates, as percent, at
	// which a rule is flagged: the Tricorder thresholds.
	probationRate = 10
	shipOffRate   = 25

	flagProbation = "probation"
	flagShipOff   = "ship-off"
)

// outcomeState is the file -record keeps. It lives on this machine and
// holds fingerprints and counts only, never the judged text.
type outcomeState struct {
	Version int `json:"version"`
	// Pending maps a text's absolute path, then a rule's name, then a
	// finding's fingerprint to its status.
	Pending map[string]map[string]map[string]string `json:"pending"`
	// Tally maps a rule's name to the findings that left Pending.
	Tally map[string]outcomeTally `json:"tally"`
}

type outcomeTally struct {
	Fixed      int `json:"fixed"`
	Suppressed int `json:"suppressed"`
}

// ruleStat is one rule's outcomes as proofread stats prints them. NotUsefulRate
// is (suppressed + reported) / (suppressed + reported + fixed).
type ruleStat struct {
	Rule          string  `json:"rule"`
	Fixed         int     `json:"fixed"`
	Suppressed    int     `json:"suppressed"`
	Reported      int     `json:"reported"`
	NotUsefulRate float64 `json:"not_useful_rate"`
	Flag          string  `json:"flag,omitempty"`
}

var (
	// ignoreDirective and offDirective match the suppressions the proofread
	// package honors, so a text can be judged again as if it had none.
	ignoreDirective = regexp.MustCompile(`proofread:ignore[ \t]+[A-Za-z0-9_-]+(?:,[A-Za-z0-9_-]+)*(?:[ \t]+.*\S)?`)
	offDirective    = regexp.MustCompile(`<!--\s*proofread\s+off\s+[A-Za-z0-9_-]+(?:\s*,\s*[A-Za-z0-9_-]+)*\s*(?::\s*.*?)?\s*-->`)
)

// outcomesPath is the one file proofread records to: outcomes.json under
// $XDG_STATE_HOME/proofread, or ~/.local/state/proofread when the variable is
// unset or not absolute.
func outcomesPath() (string, error) {
	dir := os.Getenv("XDG_STATE_HOME")

	if dir == "" || !filepath.IsAbs(dir) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("find the state directory: %w", err)
		}

		dir = filepath.Join(home, ".local", "state")
	}

	return filepath.Join(dir, "proofread", "outcomes.json"), nil
}

// loadOutcomes reads the state at path; a missing file is an empty state.
func loadOutcomes(path string) (outcomeState, error) {
	empty := outcomeState{Version: outcomesVersion, Pending: map[string]map[string]map[string]string{}, Tally: map[string]outcomeTally{}}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return empty, nil
	}

	if err != nil {
		return empty, fmt.Errorf("read outcomes: %w", err)
	}

	var s outcomeState
	if err := json.Unmarshal(data, &s); err != nil {
		return empty, fmt.Errorf("outcomes %s: %w", path, err)
	}

	if s.Version != outcomesVersion {
		return empty, fmt.Errorf("outcomes %s is version %d, want %d", path, s.Version, outcomesVersion)
	}

	if s.Pending == nil {
		s.Pending = empty.Pending
	}

	if s.Tally == nil {
		s.Tally = empty.Tally
	}

	return s, nil
}

// saveOutcomes writes s to path through a rename, so a crash leaves the old
// file whole.
func saveOutcomes(path string, s outcomeState) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("write outcomes: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("write outcomes: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write outcomes: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("write outcomes: %w", err)
	}

	return nil
}

// recordRun folds this run's findings into the outcomes file. For each text
// path, a finding the last run reported that is gone is fixed, or suppressed
// when the text, judged again without its suppressions, still has it; one
// still present is reported. Rules this run did not judge keep their state.
func recordRun(kind proofread.Kind, paths []string, t table, only []proofread.Rule, opts []proofread.Option, out []finding) error {
	file, err := outcomesPath()
	if err != nil {
		return err
	}

	state, err := loadOutcomes(file)
	if err != nil {
		return err
	}

	hashes := fingerprints(out)

	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return fmt.Errorf("record %s: %w", p, err)
		}

		current := map[string]string{}

		for i, f := range out {
			if f.Node == p {
				current[hashes[i]] = f.Rule
			}
		}

		decisions := t.decisionsAt(p)

		active := func(rule string) bool { return ruleRuns(proofread.Rule(rule), kind, decisions, only) }

		withoutSuppressions := func() map[string]bool {
			text, err := os.ReadFile(p)
			if err != nil {
				return nil
			}

			plain := withoutDirectives(string(text))
			if plain == string(text) {
				return nil
			}

			fileOpts := append(slices.Clone(opts), proofread.WithDecisions(decisions))
			again := fileFindings(p, plain, kind, fileOpts)

			covered := map[string]bool{}
			for _, h := range fingerprints(again) {
				covered[h] = true
			}

			return covered
		}

		state.update(abs, current, active, withoutSuppressions)
	}

	return saveOutcomes(file, state)
}

// ruleRuns reports whether a run of kind with these decisions and -only
// judged rule at all.
func ruleRuns(rule proofread.Rule, kind proofread.Kind, decisions map[proofread.Rule]proofread.Decision, only []proofread.Rule) bool {
	if len(only) > 0 && !slices.Contains(only, rule) {
		return false
	}

	for _, doc := range proofread.Catalog() {
		if doc.Name != rule {
			continue
		}

		if !slices.Contains(doc.Kinds, kind) {
			return false
		}

		if d, set := decisions[rule]; set {
			return d != proofread.DecisionOff
		}

		return doc.Decisions[kind] != proofread.DecisionOff
	}

	return false
}

// update records one text's run. current maps the fingerprint of each finding
// the run reported to its rule, active says which rules the run judged, and
// covered lazily returns the fingerprints the text's suppressions hide.
func (s *outcomeState) update(file string, current map[string]string, active func(rule string) bool, covered func() map[string]bool) {
	rules := s.Pending[file]
	if rules == nil {
		rules = map[string]map[string]string{}
	}

	var hidden map[string]bool

	hiddenKnown := false

	for rule, fps := range rules {
		if !active(rule) {
			continue
		}

		for fp := range fps {
			if _, still := current[fp]; still {
				fps[fp] = statusReported

				continue
			}

			if !hiddenKnown {
				hidden, hiddenKnown = covered(), true
			}

			tally := s.Tally[rule]

			if hidden[fp] {
				tally.Suppressed++
			} else {
				tally.Fixed++
			}

			s.Tally[rule] = tally

			delete(fps, fp)
		}
	}

	for fp, rule := range current {
		if _, known := rules[rule][fp]; known {
			continue
		}

		if rules[rule] == nil {
			rules[rule] = map[string]string{}
		}

		rules[rule][fp] = statusOpen
	}

	for rule, fps := range rules {
		if len(fps) == 0 {
			delete(rules, rule)
		}
	}

	if len(rules) == 0 {
		delete(s.Pending, file)

		return
	}

	s.Pending[file] = rules
}

// withoutDirectives blanks every suppression in text, keeping its line breaks
// and the position of everything else.
func withoutDirectives(text string) string {
	blank := func(m string) string {
		return strings.Map(func(r rune) rune {
			if r == '\n' {
				return r
			}

			return ' '
		}, m)
	}

	return ignoreDirective.ReplaceAllStringFunc(offDirective.ReplaceAllStringFunc(text, blank), blank)
}

// stats returns each rule's outcomes, sorted by rule. A rule is flagged
// probation at 10% not useful and ship-off at 25%.
func (s outcomeState) stats() []ruleStat {
	reported := map[string]int{}

	for _, rules := range s.Pending {
		for rule, fps := range rules {
			for _, status := range fps {
				if status == statusReported {
					reported[rule]++
				}
			}
		}
	}

	names := map[string]bool{}
	for rule := range s.Tally {
		names[rule] = true
	}

	for rule := range reported {
		names[rule] = true
	}

	out := []ruleStat{}

	for _, rule := range slices.Sorted(maps.Keys(names)) {
		st := ruleStat{Rule: rule, Fixed: s.Tally[rule].Fixed, Suppressed: s.Tally[rule].Suppressed, Reported: reported[rule]}
		notUseful := st.Suppressed + st.Reported

		if total := notUseful + st.Fixed; total > 0 {
			st.NotUsefulRate = math.Round(float64(notUseful)/float64(total)*1e4) / 1e4

			switch {
			case notUseful*100 >= shipOffRate*total:
				st.Flag = flagShipOff
			case notUseful*100 >= probationRate*total:
				st.Flag = flagProbation
			}
		}

		out = append(out, st)
	}

	return out
}

// runStats prints the recorded outcomes of each rule.
func runStats(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("proofread stats", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", "json", "write the stats as `json` or text")

	if err := fs.Parse(args); err != nil {
		return exitError
	}

	if fs.NArg() > 0 {
		return failure(stderr, errors.New("stats takes no arguments"))
	}

	file, err := outcomesPath()
	if err != nil {
		return failure(stderr, err)
	}

	state, err := loadOutcomes(file)
	if err != nil {
		return failure(stderr, err)
	}

	rows := state.stats()

	switch *format {
	case "json":
		if err := json.NewEncoder(stdout).Encode(rows); err != nil {
			return failure(stderr, fmt.Errorf("write the stats: %w", err))
		}
	case "text":
		if len(rows) == 0 {
			fmt.Fprintln(stdout, "no outcomes recorded: run a kind with -record")
		}

		for _, r := range rows {
			fmt.Fprintf(stdout, "%-20s fixed %d  suppressed %d  reported %d  not useful %.1f%%", r.Rule, r.Fixed, r.Suppressed, r.Reported, r.NotUsefulRate*100)

			if r.Flag != "" {
				fmt.Fprintf(stdout, "  %s", r.Flag)
			}

			fmt.Fprintln(stdout)
		}
	default:
		return failure(stderr, fmt.Errorf("unknown -format %q: want json or text", *format))
	}

	return exitOK
}
