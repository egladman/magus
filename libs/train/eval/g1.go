package eval

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/egladman/magus/libs/train/dedup"
)

// G1 thresholds, plan 2.3.
const (
	HoldoutMin       = 0.08
	HoldoutMax       = 0.12
	MinFIMBuzzSpans  = 2000
	MinFIMBuzzGroups = 30
	MinPassKTasks    = 120
	MinCheckPass     = 0.95
	// BigGroupShare is the share of a language's bytes past which a group is never held out.
	BigGroupShare = 0.03
)

// Bullet is one assertion of G1: what the plan wants, what was measured, and whether it holds.
type Bullet struct {
	ID    string  `json:"id"`
	Want  string  `json:"want"`
	Got   float64 `json:"got"`
	Shown string  `json:"shown"`
	Pass  bool    `json:"pass"`
}

// G1Report is every 2.3 bullet and the numbers behind them. It is written whether or not
// a bullet fails; Failed lists the ids that do.
type G1Report struct {
	Rev     string   `json:"rev"`
	Cutoff  Cutoff   `json:"cutoff"`
	Bullets []Bullet `json:"bullets"`
	Failed  []string `json:"failed"`

	Bytes         map[string]map[string]int `json:"bytes"`
	Files         map[string]map[string]int `json:"files"`
	BigGroups     map[string]int            `json:"groups_over_3_percent"`
	BigGroupBytes map[string]int            `json:"bytes_in_groups_over_3_percent"`
	Pairs         map[string]int            `json:"dedup_pairs"`
	CrossPairs    []dedup.Pair              `json:"cross_split_pairs"`
	GramHits      int                       `json:"gram_hits"`
	TrainScanned  int                       `json:"train_files_scanned"`
	CheckFails    map[string]int            `json:"check_failures_by_reason"`
	CheckAbsent   int                       `json:"check_absent_at_cutoff"`
	// CheckAtRev counts held-out Buzz files that pass in place at Rev, the version the sets hold.
	CheckAtRev      int            `json:"check_pass_in_place_at_rev"`
	CheckProject    int            `json:"check_excluded_project_import"`
	CheckAtRevFails map[string]int `json:"check_failures_at_rev_by_reason"`
	CheckFilesTotal int            `json:"held_out_buzz_files"`
	FIMBuzzTest     int            `json:"fim_buzz_test_spans"`
	FIMBuzzGroups   int            `json:"fim_buzz_test_groups"`
	Sets            SetCounts      `json:"sets"`
	Train           TrainCounts    `json:"train"`
}

func bullet(id, want string, got float64, shown string, pass bool) Bullet {
	return Bullet{ID: id, Want: want, Got: got, Shown: shown, Pass: pass}
}

// RunG1 reads the datasets of the stages before it, recounts each G1 bullet from them
// and from the git tree, and writes g1.json. It never judges beyond the plan's thresholds.
func RunG1(e Env, cutoff Cutoff) (G1Report, error) {
	r := G1Report{Rev: e.Rev, Cutoff: cutoff, Bytes: map[string]map[string]int{}, Files: map[string]map[string]int{},
		BigGroups: map[string]int{}, BigGroupBytes: map[string]int{}, Pairs: map[string]int{}, CheckFails: map[string]int{}, CheckAtRevFails: map[string]int{}}
	entries, err := ReadSplit(e.File("split.jsonl"))
	if err != nil {
		return r, err
	}
	side := map[string]string{}
	total := map[string]int{}
	groupBytes := map[string]map[string]int{}
	for _, en := range entries {
		side[en.Path] = en.Side
		if r.Bytes[en.Language] == nil {
			r.Bytes[en.Language], r.Files[en.Language] = map[string]int{}, map[string]int{}
		}
		r.Bytes[en.Language][en.Side] += en.Bytes
		r.Files[en.Language][en.Side]++
		total[en.Language] += en.Bytes
		if groupBytes[en.Group] == nil {
			groupBytes[en.Group] = map[string]int{}
		}
		groupBytes[en.Group][en.Language] += en.Bytes
	}
	for g, byLang := range groupBytes {
		for lang, b := range byLang {
			if float64(b) > BigGroupShare*float64(total[lang]) {
				r.BigGroups[lang]++
				r.BigGroupBytes[lang] += b
				_ = g
			}
		}
	}

	// Dedup recount over the split's own files at Rev, independent of links.jsonl.
	blobs, err := OpenBlobs(e.Root, e.Rev)
	if err != nil {
		return r, err
	}
	var docs []dedup.Doc
	slices.SortFunc(entries, func(a, b Entry) int { return cmp.Compare(a.Path, b.Path) })
	for _, en := range entries {
		_, data, err := blobs.Read(en.Path)
		if err != nil {
			blobs.Close()
			return r, err
		}
		if !utf8.Valid(data) {
			continue
		}
		docs = append(docs, dedup.Doc{Path: en.Path, Language: en.Language, Text: string(data)})
	}
	blobs.Close()
	pairs, _ := dedup.Find(docs)
	for _, p := range pairs {
		r.Pairs[p.Kind]++
		if side[p.A] != side[p.B] {
			r.CrossPairs = append(r.CrossPairs, p)
		}
	}
	r.Bullets = append(r.Bullets, bullet("cross-split-pairs", "0 exact or near-dup pairs across the split",
		float64(len(r.CrossPairs)), fmt.Sprintf("%d (of %d exact, %d near in all)", len(r.CrossPairs), r.Pairs["exact"], r.Pairs["near"]), len(r.CrossPairs) == 0))

	// 13-gram recount over the training files the fim stage kept, at their cutoff blobs.
	texts, err := HeldOutTexts(e)
	if err != nil {
		return r, err
	}
	dec := NewDecontaminator(texts)
	trainFiles, err := ReadRows[TrainFile](e.File("fim-train-files.jsonl"))
	if err != nil {
		return r, fmt.Errorf("fim-train-files.jsonl (run the fim target): %w", err)
	}
	cut, err := OpenBlobs(e.Root, e.Cutoff)
	if err != nil {
		return r, err
	}
	langOf := map[string]string{}
	for _, en := range entries {
		langOf[en.Path] = en.Language
	}
	for _, tf := range trainFiles {
		_, data, err := cut.Read(tf.Path)
		if err != nil {
			cut.Close()
			return r, err
		}
		r.TrainScanned++
		sc := Scrub(dec, langOf[tf.Path], trainText(Entry{Dev: tf.Dev}, data))
		if dec.Match(sc.Text) != "" || len(dec.Hits(sc.Text)) > 0 {
			r.GramHits++
		}
	}
	cut.Close()
	r.Bullets = append(r.Bullets, bullet("gram-hits", "0 13-gram hits of training text against eval text",
		float64(r.GramHits), fmt.Sprintf("%d in %d training files scanned, after taking out the lines that repeat an eval text", r.GramHits, r.TrainScanned), r.GramHits == 0))

	for _, lang := range Languages {
		share := 0.0
		if total[lang] > 0 {
			share = float64(r.Bytes[lang][SideTest]) / float64(total[lang])
		}
		r.Bullets = append(r.Bullets, bullet("holdout-"+lang, "held-out bytes 8% to 12% of the language's hand-written bytes",
			share, fmt.Sprintf("%.2f%% (%d of %d B)", share*100, r.Bytes[lang][SideTest], total[lang]), share >= HoldoutMin && share <= HoldoutMax))
	}

	spans, err := ReadRows[Span](e.File("fim-buzz-test.jsonl"))
	if err != nil {
		return r, fmt.Errorf("fim-buzz-test.jsonl (run the eval-sets target): %w", err)
	}
	groups := map[string]bool{}
	for _, s := range spans {
		groups[s.Group] = true
	}
	r.FIMBuzzTest, r.FIMBuzzGroups = len(spans), len(groups)
	r.Bullets = append(r.Bullets, bullet("fim-buzz-test-spans", "FIM-Buzz test set at least 2,000 spans",
		float64(len(spans)), fmt.Sprintf("%d", len(spans)), len(spans) >= MinFIMBuzzSpans))
	r.Bullets = append(r.Bullets, bullet("fim-buzz-test-groups", "FIM-Buzz test spans from at least 30 groups",
		float64(len(groups)), fmt.Sprintf("%d", len(groups)), len(groups) >= MinFIMBuzzGroups))

	tasks, err := ReadRows[Task](e.File("passk-buzz.jsonl"))
	if err != nil {
		return r, fmt.Errorf("passk-buzz.jsonl (run the eval-sets target): %w", err)
	}
	r.Bullets = append(r.Bullets, bullet("passk-buzz-tasks", "Buzz pass@k set at least 120 tasks",
		float64(len(tasks)), fmt.Sprintf("%d", len(tasks)), len(tasks) >= MinPassKTasks))

	checks, err := ReadRows[CheckRow](e.File("checks.jsonl"))
	if err != nil {
		return r, fmt.Errorf("checks.jsonl (run the eval-sets target): %w", err)
	}
	r.CheckFilesTotal = len(checks)
	cs := CountChecks(checks)
	r.CheckAtRev = cs.Pass
	r.CheckProject = cs.ProjectImport
	for _, c := range checks {
		if c.Absent {
			r.CheckAbsent++
		}
		if !c.Absent && !c.OKAtRev {
			r.CheckAtRevFails[failureReason(c.ReasonAtRev)]++
		}
		if !c.Absent && !c.OK {
			r.CheckFails[failureReason(c.Reason)]++
		}
	}
	share := 0.0
	if cs.Counted > 0 {
		share = float64(cs.Pass) / float64(cs.Counted)
	}
	r.Bullets = append(r.Bullets, bullet("buzz-check-pass", "at least 95% of held-out Buzz files pass magus buzz --check, on the version the sets hold, not counting files that fail only on a project/<path> import",
		share, fmt.Sprintf("%.2f%% (%d of %d files counted; %d more fail only on a project/<path> import, which needs the workspace, and are counted separately)",
			share*100, cs.Pass, cs.Counted, cs.ProjectImport), share >= MinCheckPass))

	for _, b := range r.Bullets {
		if !b.Pass {
			r.Failed = append(r.Failed, b.ID)
		}
	}
	out, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return r, err
	}
	return r, os.WriteFile(e.File("g1.json"), append(out, '\n'), 0o644)
}

var bzzCode = regexp.MustCompile(`BZZ\d{4}`)

// failureReason buckets a check diagnostic by its BZZ code, else "other".
func failureReason(line string) string {
	if code := bzzCode.FindString(line); code != "" {
		return code
	}
	return "other"
}

// CheckCounts is the check-pass tally over held-out Buzz files at the version the sets
// hold. Counted excludes the files whose first diagnostic is an unresolved project/<path>
// import (BZZ2001), which --check still cannot resolve without the workspace; those are
// ProjectImport.
type CheckCounts struct {
	Pass, Counted, ProjectImport int
}

// CountChecks tallies rows by OKAtRev. Every row is checked at the version the sets
// hold, whether or not the file exists at the cutoff.
func CountChecks(rows []CheckRow) CheckCounts {
	var c CheckCounts
	for _, r := range rows {
		switch {
		case r.OKAtRev:
			c.Pass++
			c.Counted++
		case strings.Contains(r.ReasonAtRev, "BZZ2001") && strings.Contains(r.ReasonAtRev, `import "project/`):
			c.ProjectImport++
		default:
			c.Counted++
		}
	}
	return c
}
