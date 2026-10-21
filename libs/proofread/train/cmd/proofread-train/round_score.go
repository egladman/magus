package main

import (
	"errors"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"maps"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/egladman/magus/libs/proofread/internal/calibrate"
)

// The labels a labeler may give. UNSURE counts as WRONG in the precision.
const (
	labelCorrect = "CORRECT"
	labelWrong   = "WRONG"
	labelUnsure  = "UNSURE"
)

// tally is one rule's or one source's score, the shape scores.json keeps per
// rule and per source.
type tally struct {
	N             int      `json:"n"`
	Correct       int      `json:"correct"`
	Precision     *float64 `json:"precision"`
	WilsonLB      *float64 `json:"wilson_lb"`
	Disagreements int      `json:"disagreements"`
	// Unnoted counts disagreements one labeler left without a note.
	Unnoted       int      `json:"unnoted_disagreements"`
	UnnotedIDs    []string `json:"unnoted_ids,omitempty"`
	RawAgreement  *float64 `json:"raw_agreement"`
	Alpha         *float64 `json:"alpha"`
	PrecisionGate bool     `json:"precision_gate"`
	AgreementGate bool     `json:"agreement_gate"`
}

type worstSource struct {
	Source   string  `json:"source"`
	N        int     `json:"n"`
	WilsonLB float64 `json:"wilson_lb"`
	Pass     bool    `json:"pass"`
}

type ruleScore struct {
	tally

	BySource    map[string]tally `json:"by_source"`
	WorstSource *worstSource     `json:"worst_source"`
	AdviseGate  bool             `json:"advise_gate"`
	SpotCheck   []string         `json:"spot_check"`
}

type scoresRecord struct {
	Round      int                   `json:"round"`
	Seed       int64                 `json:"seed"`
	RuleCommit string                `json:"rule_commit"`
	Replay     bool                  `json:"replay,omitempty"`
	Gates      gates                 `json:"gates"`
	Method     map[string]string     `json:"method"`
	Rules      map[string]*ruleScore `json:"rules"`
}

func runRoundScore(args []string, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("proofread-train calibrate round score", flag.ContinueOnError)
	fset.SetOutput(stderr)
	dir := fset.String("dir", "", "score the round labeled in this `dir`")
	out := fset.String("out", "", "write the scores to this `file` (default DIR/scores.json)")
	replay := fset.Bool("replay", false, "score a round whose rule files changed since its freeze, to reproduce its numbers; "+
		"the result decides nothing")

	if err := fset.Parse(args); err != nil {
		return exitError
	}

	if *dir == "" || fset.NArg() > 0 {
		return failure(stderr, errors.New("round score takes --dir DIR and no operand"))
	}

	rec, err := scoreRound(*dir, *replay)
	if err != nil {
		return failure(stderr, err)
	}

	path := *out
	if path == "" {
		path = filepath.Join(*dir, scoresFile)
	}

	if err := writeJSONFile(path, rec); err != nil {
		return failure(stderr, err)
	}

	if err := writeScoresTable(stdout, rec); err != nil {
		return failure(stderr, err)
	}

	return exitOK
}

func scoreRound(dir string, replay bool) (*scoresRecord, error) {
	fr, err := readFreeze(filepath.Join(dir, freezeFile))
	if err != nil {
		return nil, err
	}

	if !replay {
		if err := checkFrozen(dir, fr); err != nil {
			return nil, err
		}
	}

	cands, err := readJSONLines[candidate](filepath.Join(dir, candidatesFile))
	if err != nil {
		return nil, err
	}

	a, err := readLabels(filepath.Join(dir, labelsAFile), cands)
	if err != nil {
		return nil, err
	}

	b, err := readLabels(filepath.Join(dir, labelsBFile), cands)
	if err != nil {
		return nil, err
	}

	final, err := finalLabels(filepath.Join(dir, adjudicatedFile), cands, a, b)
	if err != nil {
		return nil, err
	}

	rec := &scoresRecord{
		Round: fr.Round, Seed: fr.SamplerSeed, RuleCommit: fr.RuleCommit, Replay: replay, Gates: fr.Gates,
		Rules: map[string]*ruleScore{}, Method: scoreMethod(fr.Gates),
	}

	rules := slices.Clone(fr.Rules)

	for _, c := range cands {
		if !slices.Contains(rules, c.Rule) {
			rules = append(rules, c.Rule)
		}
	}

	for _, r := range rules {
		rec.Rules[r] = scoreRule(r, cands, a, b, final, fr)
	}

	return rec, nil
}

func scoreMethod(g gates) map[string]string {
	return map[string]string{
		"final_label": "the adjudicated label where adjudicated.jsonl has one, else the label both labelers gave; UNSURE counts as WRONG",
		"wilson":      fmt.Sprintf("Wilson score interval lower bound, z=%g, on final CORRECT over n", g.Advise.WilsonZ),
		"alpha":       "Krippendorff alpha, nominal, labelers A and B, null when only one label occurs",
		"agreement_gate": fmt.Sprintf("no disagreement left without a note by either labeler, and disagreements <= n/%d or alpha >= %.2f, and n > 0",
			g.Agreement.PerDisagreement, g.Agreement.Alpha),
		"precision_gate": fmt.Sprintf("Wilson lower bound >= %.2f over >= %d firings", g.Advise.LowerBound, g.Advise.MinFirings),
		"advise_gate": fmt.Sprintf("precision and agreement gates pooled, and the worst source with >= %d firings also at "+
			"lower bound >= %.2f; no source reaching %d fails the gate", g.Source.MinFirings, g.Advise.LowerBound, g.Source.MinFirings),
		"deny_gate": fmt.Sprintf("not decided by one round: %d frozen rounds on different sources, pooled to %d firings, "+
			"at most %d wrong", g.Deny.Rounds, g.Deny.PooledFirings, g.Deny.MaxWrong),
		"spot_check": "per rule passing advise: 10 ids drawn from the sorted agreed-CORRECT ids with PCG(seed+1, fnv64a(rule))",
	}
}

func scoreRule(rule string, cands []candidate, a, b map[string]labelRow, final map[string]string, fr *freezeRecord) *ruleScore {
	g := fr.Gates
	bySource := map[string][]candidate{}

	var mine []candidate

	for _, c := range cands {
		if c.Rule == rule {
			mine = append(mine, c)
			bySource[c.Source] = append(bySource[c.Source], c)
		}
	}

	rs := &ruleScore{tally: tallyCandidates(mine, a, b, final, g), BySource: map[string]tally{}, SpotCheck: []string{}}

	for src, cs := range bySource {
		t := tallyCandidates(cs, a, b, final, g)
		rs.BySource[src] = t

		if t.N < g.Source.MinFirings || t.WilsonLB == nil {
			continue
		}

		if rs.WorstSource == nil || *t.WilsonLB < rs.WorstSource.WilsonLB ||
			*t.WilsonLB == rs.WorstSource.WilsonLB && src < rs.WorstSource.Source {
			rs.WorstSource = &worstSource{Source: src, N: t.N, WilsonLB: *t.WilsonLB, Pass: *t.WilsonLB >= g.Advise.LowerBound}
		}
	}

	rs.AdviseGate = rs.PrecisionGate && rs.AgreementGate && rs.WorstSource != nil && rs.WorstSource.Pass

	if rs.AdviseGate {
		rs.SpotCheck = spotCheck(rule, mine, a, b, final, fr.SamplerSeed)
	}

	return rs
}

func tallyCandidates(cs []candidate, a, b map[string]labelRow, final map[string]string, g gates) tally {
	t := tally{N: len(cs)}
	units := make([][]string, 0, len(cs))

	for _, c := range cs {
		if final[c.ID] == labelCorrect {
			t.Correct++
		}

		la, lb := a[c.ID], b[c.ID]
		units = append(units, []string{la.Label, lb.Label})

		if la.Label != lb.Label {
			t.Disagreements++

			if strings.TrimSpace(la.Note) == "" || strings.TrimSpace(lb.Note) == "" {
				t.Unnoted++
				t.UnnotedIDs = append(t.UnnotedIDs, c.ID)
			}
		}
	}

	if t.N == 0 {
		return t
	}

	t.Precision = rounded(float64(t.Correct) / float64(t.N))
	t.WilsonLB = rounded(calibrate.WilsonLower(t.Correct, t.N, g.Advise.WilsonZ))
	t.RawAgreement = rounded(float64(t.N-t.Disagreements) / float64(t.N))

	if alpha, ok := calibrate.NominalAlpha(units); ok {
		t.Alpha = rounded(alpha)
	}

	lower := calibrate.WilsonLower(t.Correct, t.N, g.Advise.WilsonZ)
	t.PrecisionGate = t.N >= g.Advise.MinFirings && lower >= g.Advise.LowerBound
	t.AgreementGate = t.Unnoted == 0 && (t.Disagreements*g.Agreement.PerDisagreement <= t.N ||
		t.Alpha != nil && *t.Alpha >= g.Agreement.Alpha)

	return t
}

// spotCheck draws the agreed-CORRECT labels a person audits before a rule is
// promoted.
func spotCheck(rule string, cs []candidate, a, b map[string]labelRow, final map[string]string, seed int64) []string {
	var agreed []string

	for _, c := range cs {
		if a[c.ID].Label == labelCorrect && b[c.ID].Label == labelCorrect && final[c.ID] == labelCorrect {
			agreed = append(agreed, c.ID)
		}
	}

	slices.Sort(agreed)

	h := fnv.New64a()
	h.Write([]byte(rule))

	rng := rand.New(rand.NewPCG(uint64(seed+1), h.Sum64())) //nolint:gosec // a reproducible draw, not a secret
	rng.Shuffle(len(agreed), func(i, j int) { agreed[i], agreed[j] = agreed[j], agreed[i] })

	return agreed[:min(10, len(agreed))]
}

func rounded(v float64) *float64 {
	r := math.Round(v*1e4) / 1e4

	return &r
}

// readLabels reads one labeler's sheet: one row per candidate, a known label
// on each, in any case.
func readLabels(p string, cands []candidate) (map[string]labelRow, error) {
	rows, err := readJSONLines[labelRow](p)
	if err != nil {
		return nil, err
	}

	ids := candidateIDs(cands)
	out := map[string]labelRow{}

	for _, r := range rows {
		r.Label = strings.ToUpper(strings.TrimSpace(r.Label))

		switch {
		case !ids[r.ID]:
			return nil, fmt.Errorf("%s: id %q is no candidate", p, r.ID)
		case out[r.ID].ID != "":
			return nil, fmt.Errorf("%s: id %q is labeled twice", p, r.ID)
		case r.Label != labelCorrect && r.Label != labelWrong && r.Label != labelUnsure:
			return nil, fmt.Errorf("%s: id %q has label %q, want CORRECT, WRONG or UNSURE", p, r.ID, r.Label)
		}

		out[r.ID] = r
	}

	var missing []string

	for _, c := range cands {
		if _, ok := out[c.ID]; !ok {
			missing = append(missing, c.ID)
		}
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("%s leaves %d candidates unlabeled: %s", p, len(missing), strings.Join(missing, ", "))
	}

	return out, nil
}

// finalLabels is each candidate's label: the adjudicated one, else the one
// both labelers agree on. A disagreement nobody adjudicated is an error.
func finalLabels(p string, cands []candidate, a, b map[string]labelRow) (map[string]string, error) {
	adjudicated := map[string]string{}

	if _, err := os.Stat(p); err == nil {
		rows, err := readJSONLines[adjudicatedRow](p)
		if err != nil {
			return nil, err
		}

		ids := candidateIDs(cands)

		for _, r := range rows {
			label := strings.ToUpper(strings.TrimSpace(r.Label))

			switch {
			case !ids[r.ID]:
				return nil, fmt.Errorf("%s: id %q is no candidate", p, r.ID)
			case label != labelCorrect && label != labelWrong && label != labelUnsure:
				return nil, fmt.Errorf("%s: id %q has label %q, want CORRECT, WRONG or UNSURE", p, r.ID, r.Label)
			}

			adjudicated[r.ID] = label
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	out := map[string]string{}

	var open []string

	for _, c := range cands {
		label, ok := adjudicated[c.ID]

		switch {
		case ok:
		case a[c.ID].Label == b[c.ID].Label:
			label = a[c.ID].Label
		default:
			open = append(open, c.ID)
		}

		if label == labelUnsure {
			label = labelWrong
		}

		out[c.ID] = label
	}

	if len(open) > 0 {
		return nil, fmt.Errorf("%d disagreements have no row in %s: %s", len(open), p, strings.Join(open, ", "))
	}

	return out, nil
}

func candidateIDs(cands []candidate) map[string]bool {
	ids := make(map[string]bool, len(cands))
	for _, c := range cands {
		ids[c.ID] = true
	}

	return ids
}

func writeScoresTable(w io.Writer, rec *scoresRecord) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RULE\tSOURCE\tN\tCORRECT\tPRECISION\tLOWER\tDISAGREE\tUNNOTED\tALPHA\tAGREEMENT\tADVISE")

	for _, r := range slices.Sorted(maps.Keys(rec.Rules)) {
		rs := rec.Rules[r]

		worst := "no source reaches the minimum"
		if rs.WorstSource != nil {
			worst = fmt.Sprintf("worst %s %s", rs.WorstSource.Source, verdict(rs.WorstSource.Pass))
		}

		fmt.Fprintf(tw, "%s\t(all)\t%s\t%s (%s)\n", r, tallyColumns(rs.tally), verdict(rs.AdviseGate), worst)

		for _, src := range slices.Sorted(maps.Keys(rs.BySource)) {
			fmt.Fprintf(tw, "%s\t%s\t%s\t\n", r, src, tallyColumns(rs.BySource[src]))
		}
	}

	return tw.Flush()
}

func tallyColumns(t tally) string {
	return fmt.Sprintf("%d\t%d\t%s\t%s\t%d\t%d\t%s\t%s", t.N, t.Correct, percent(t.Precision), percent(t.WilsonLB),
		t.Disagreements, t.Unnoted, decimal(t.Alpha), verdict(t.AgreementGate))
}

func verdict(pass bool) string {
	if pass {
		return "pass"
	}

	return "fail"
}

func decimal(p *float64) string {
	if p == nil {
		return "-"
	}

	return fmt.Sprintf("%.3f", *p)
}
