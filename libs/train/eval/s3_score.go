package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Set is one evaluation set as a run reads it: the sampled spans, the size of the whole
// set, and the tree its files are checked in.
type Set struct {
	Name     string
	Language string
	Spans    []Span
	Of       int
	Tree     Tree
	// Sample says how the spans were kept: "all", or "first N by span id".
	Sample string
}

// FloorPrecision is the precision the shipped threshold must hold (plan 5.5, G5).
const FloorPrecision = 0.8

// FixedTau is the second threshold the rows report at, so a system that never reaches
// FloorPrecision still has a precision and a recall to compare.
const FixedTau = 0.5

// CheckSpans runs the language's checker over every span with a prediction. Buzz is
// `magus buzz --check` over spliced siblings of the original files in the set's tree,
// in batches of CheckBatch per process. Go is the in-process type check, which asks for no
// error the original file lacks. TypeScript has no checker linked (no tsgo), so every
// span's check is unknown and the check metrics are not scored.
func (e S3) CheckSpans(set Set, preds map[string]Pred, goc *GoChecker) (map[string]Check, error) {
	switch set.Language {
	case "buzz":
		return e.checkBuzz(set, preds)
	case "go":
		return checkGo(set, preds, goc)
	default:
		return map[string]Check{}, nil
	}
}

func shownSpans(set Set, preds map[string]Pred) []Span {
	var out []Span
	for _, s := range set.Spans {
		if p, ok := preds[s.ID]; ok && Score(s, p, Check{}).Shown {
			out = append(out, s)
		}
	}
	return out
}

func (e S3) checkBuzz(set Set, preds map[string]Pred) (map[string]Check, error) {
	shown := shownSpans(set, preds)
	texts := map[string]string{}
	var paths []string
	for _, s := range shown {
		if _, ok := texts[s.Path]; !ok {
			t, err := set.Tree.Read(s.Path, s.Blob)
			if err != nil {
				return nil, err
			}
			texts[s.Path] = t
			paths = append(paths, s.Path)
		}
	}
	slices.Sort(paths)
	var variants []string
	byPath := map[string]string{}
	for _, s := range shown {
		spliced, err := Splice(texts[s.Path], s.Start, s.End, preds[s.ID].Text)
		if err != nil {
			return nil, fmt.Errorf("span %s: %w", s.ID, err)
		}
		name := SpliceName(s.Path, s.ID)
		if err := os.WriteFile(filepath.Join(set.Tree.Dir, name), []byte(spliced), 0o644); err != nil {
			return nil, err
		}
		variants = append(variants, name)
		byPath[s.ID] = name
	}
	defer func() {
		for _, v := range variants {
			_ = os.Remove(filepath.Join(set.Tree.Dir, v))
		}
	}()
	rows, err := CheckBuzz(e.Magus, set.Tree.Dir, append(slices.Clone(paths), variants...), CheckBatch)
	if err != nil {
		return nil, err
	}
	ok := map[string]bool{}
	for _, r := range rows {
		ok[r.Path] = r.OK
	}
	out := map[string]Check{}
	for _, s := range shown {
		out[s.ID] = Check{Known: ok[s.Path], Pass: ok[byPath[s.ID]]}
	}
	return out, nil
}

func checkGo(set Set, preds map[string]Pred, goc *GoChecker) (map[string]Check, error) {
	out := map[string]Check{}
	texts := map[string]string{}
	base := map[string][]string{}
	baseParsed := map[string]bool{}
	for _, s := range shownSpans(set, preds) {
		if _, ok := texts[s.Path]; !ok {
			t, err := set.Tree.Read(s.Path, s.Blob)
			if err != nil {
				return nil, err
			}
			texts[s.Path] = t
			base[s.Path], baseParsed[s.Path] = goc.Errors(s.Path, t)
		}
		if !baseParsed[s.Path] {
			continue
		}
		spliced, err := Splice(texts[s.Path], s.Start, s.End, preds[s.ID].Text)
		if err != nil {
			return nil, fmt.Errorf("span %s: %w", s.ID, err)
		}
		out[s.ID] = Check{Known: true, Pass: goc.CheckSplice(s.Path, spliced, base[s.Path])}
	}
	return out, nil
}

// CompleteMetrics is one system's rows on one set and the per-unit values behind them,
// kept so a later system's p-value can pair against this one's.
type CompleteMetrics struct {
	Rows  []Row
	Units map[string]Units
}

type tau struct {
	name string
	at   float64
	ok   bool
}

// ScoreComplete turns a system's predictions on a set into the plan 6.1 rows: AUPRC,
// precision and recall at the threshold that first holds precision FloorPrecision and
// at FixedTau, identifier precision and recall, check-pass precision and recall, and
// correct abstention on blank references.
func ScoreComplete(sys System, set Set, preds map[string]Pred, checks map[string]Check, run string) CompleteMetrics {
	m := CompleteMetrics{Units: map[string]Units{}}
	scored := make([]Scored, 0, len(set.Spans))
	byID := map[string]Span{}
	for _, s := range set.Spans {
		p, ok := preds[s.ID]
		if !ok {
			continue
		}
		scored = append(scored, Score(s, p, checks[s.ID]))
		byID[s.ID] = s
	}
	points := Curve(scored)
	add := func(metric string, u Units, note string) {
		r := sys.Row(set.Name, metric, run)
		r.Of, r.Sample = set.Of, set.Sample
		u.Fill(&r)
		r.Interval = joinNote(r.Interval, note)
		m.Rows = append(m.Rows, r)
		m.Units[metric] = u
	}
	m.auprc(sys, set, scored, run)
	p80, ok := AtPrecision(points, FloorPrecision)
	taus := []tau{{"p80", p80.Tau, ok}, {strconv.FormatFloat(FixedTau, 'f', -1, 64), FixedTau, true}}
	for _, t := range taus {
		if !t.ok {
			m.notScored(sys, set, run, t.name, "no confidence threshold reaches precision 0.8")
			continue
		}
		if t.name == "p80" {
			r := sys.Row(set.Name, "tau@p80", run)
			r.Value, r.N, r.Of, r.Sample = floatPointer(t.at), len(scored), set.Of, set.Sample
			m.Rows = append(m.Rows, r)
		}
		for _, row := range atTau(scored, t.at) {
			if set.Language == "typescript" && strings.HasPrefix(row.metric, "check_") {
				continue
			}
			add(row.metric+"@"+t.name, row.units, row.note)
		}
	}
	m.identifiers(sys, set, preds, byID, run)
	if set.Language == "typescript" {
		for _, metric := range []string{"check_precision", "check_recall"} {
			r := sys.Row(set.Name, metric, run)
			r.NotScored, r.Of, r.Sample = "no TypeScript checker is linked (tsgo is not); useful counts first-line matches only", set.Of, set.Sample
			m.Rows = append(m.Rows, r)
		}
	}
	return m
}

func joinNote(a, b string) string {
	if b == "" {
		return a
	}
	if a == "" {
		return b
	}
	return a + "; " + b
}

func (m *CompleteMetrics) notScored(sys System, set Set, run, name, why string) {
	for _, metric := range []string{"precision", "recall", "check_precision", "check_recall", "abstention"} {
		r := sys.Row(set.Name, metric+"@"+name, run)
		r.NotScored, r.Of, r.Sample = why, set.Of, set.Sample
		m.Rows = append(m.Rows, r)
	}
}

func (m *CompleteMetrics) auprc(sys System, set Set, scored []Scored, run string) {
	groups := make([]string, len(scored))
	for i, s := range scored {
		groups[i] = s.Group
	}
	est, pseudo := Jackknife(groups, func(keep []int) float64 {
		sub := make([]Scored, len(keep))
		for i, k := range keep {
			sub[i] = scored[k]
		}
		return AUPRC(Curve(sub))
	})
	r := sys.Row(set.Name, "auprc", run)
	r.Of, r.Sample = set.Of, set.Sample
	JackknifeRow(&r, est, pseudo)
	r.N = len(scored)
	m.Rows = append(m.Rows, r)
	m.Units["auprc"] = pseudo
}

type atRow struct {
	metric string
	units  Units
	note   string
}

// atTau builds the per-unit values of the metrics that depend on a threshold.
func atTau(scored []Scored, at float64) []atRow {
	prec, rec := NewUnits(), NewUnits()
	cprec, crec := NewUnits(), NewUnits()
	abst := NewUnits()
	for _, s := range scored {
		shown := s.Shown && s.Confidence >= at
		switch {
		case s.RefEmpty:
			abst.Add(s.ID, s.Group, b2f(!shown))
		default:
			rec.Add(s.ID, s.Group, b2f(shown && s.Useful))
			if s.Check.Known {
				crec.Add(s.ID, s.Group, b2f(shown && s.Check.Pass))
			}
		}
		if shown {
			prec.Add(s.ID, s.Group, b2f(s.Useful))
			if s.Check.Known {
				cprec.Add(s.ID, s.Group, b2f(s.Check.Pass))
			}
		}
	}
	return []atRow{{"precision", prec, ""}, {"recall", rec, ""}, {"check_precision", cprec, ""}, {"check_recall", crec, ""},
		{"abstention", abst, "share of blank-reference spans where nothing is shown"}}
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// identifiers adds the CrossCodeEval-style identifier precision and recall, one unit per
// span whose denominator is non-empty.
func (m *CompleteMetrics) identifiers(sys System, set Set, preds map[string]Pred, byID map[string]Span, run string) {
	ip, ir := NewUnits(), NewUnits()
	for id, s := range byID {
		p, rr, pOK, rOK := IdentifierPR(preds[id].Text, s.Middle)
		if pOK {
			ip.Add(id, s.Group, p)
		}
		if rOK {
			ir.Add(id, s.Group, rr)
		}
	}
	for _, e := range []struct {
		metric string
		u      Units
	}{{"ident_precision", ip}, {"ident_recall", ir}} {
		r := sys.Row(set.Name, e.metric, run)
		r.Of, r.Sample = set.Of, set.Sample
		e.u.Fill(&r)
		m.Rows = append(m.Rows, r)
		m.Units[e.metric] = e.u
	}
}

// Compare fills each row's p-value against the reference system's units, and names the
// reference. The reference's own rows keep none.
func (m *CompleteMetrics) Compare(ref CompleteMetrics, refKey string) {
	for i := range m.Rows {
		r := &m.Rows[i]
		u, ok := m.Units[r.Metric]
		ru, rok := ref.Units[r.Metric]
		if !ok || !rok || r.NotScored != "" {
			continue
		}
		r.P, r.Ref = u.PValue(ru), refKey
	}
}
