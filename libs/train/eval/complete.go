package eval

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
)

// SimilarityFloor is plan 6.1's edit similarity at or above which a completion that
// passes the checker still counts as useful.
const SimilarityFloor = 0.9

// FirstLine is s up to its first line break, without a trailing carriage return.
func FirstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSuffix(line, "\r")
}

// EditSimilarity is 1 minus the Levenshtein distance over runes, divided by the longer
// length; two empty strings are identical.
func EditSimilarity(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 && len(rb) == 0 {
		return 1
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return 1 - float64(prev[len(rb)])/float64(max(len(ra), len(rb)))
}

var identRE = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// Identifiers is the set of identifier-shaped words in s.
func Identifiers(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range identRE.FindAllString(s, -1) {
		out[w] = true
	}
	return out
}

// IdentifierPR is the share of the prediction's identifiers the reference holds, and the
// share of the reference's identifiers the prediction recovers (CrossCodeEval's
// identifier match). Each is ok=false when its denominator is empty.
func IdentifierPR(pred, ref string) (precision, recall float64, pOK, rOK bool) {
	p, r := Identifiers(pred), Identifiers(ref)
	hit := 0
	for w := range p {
		if r[w] {
			hit++
		}
	}
	if len(p) > 0 {
		precision, pOK = float64(hit)/float64(len(p)), true
	}
	if len(r) > 0 {
		recall, rOK = float64(hit)/float64(len(r)), true
	}
	return precision, recall, pOK, rOK
}

// Pred is one stored model answer for a span.
type Pred struct {
	ID         string  `json:"id"`
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
	Tokens     int     `json:"tokens"`
	Stop       string  `json:"stop"`
}

// Check is the outcome of the checker on a span's completed file: Known is false when
// the language has no checker linked or the file does not pass with the reference
// middle, since a file broken before the completion says nothing about it.
type Check struct {
	Known bool
	Pass  bool
}

// Scored is one span against its prediction.
type Scored struct {
	ID, Group string
	// RefEmpty marks a span whose reference middle is blank: the right answer is to show
	// nothing.
	RefEmpty bool
	// Shown is true for a prediction with text; a blank one is an abstention.
	Shown      bool
	Confidence float64
	FirstLine  bool
	Similarity float64
	Check      Check
	Useful     bool
}

// Score judges a prediction against a span (plan 6.1): useful when its first line equals
// the reference's, or its edit similarity is at least SimilarityFloor and the completed
// file passes the checker. A blank reference is never useful: the span scores in
// abstention.
func Score(span Span, p Pred, c Check) Scored {
	sc := Scored{ID: span.ID, Group: span.Group, RefEmpty: strings.TrimSpace(span.Middle) == "",
		Shown: strings.TrimSpace(p.Text) != "", Confidence: p.Confidence, Check: c}
	if sc.RefEmpty || !sc.Shown {
		return sc
	}
	sc.FirstLine = strings.TrimRight(FirstLine(p.Text), " \t") == strings.TrimRight(FirstLine(span.Middle), " \t")
	sc.Similarity = EditSimilarity(p.Text, span.Middle)
	sc.Useful = sc.FirstLine || (sc.Similarity >= SimilarityFloor && c.Known && c.Pass)
	return sc
}

// Point is the precision and recall of showing every completion at or above Tau.
type Point struct {
	Tau       float64
	Precision float64
	Recall    float64
	Shown     int
	Useful    int
}

// Curve is the precision-recall curve over the scored spans: one point per distinct
// confidence, from the most confident down. Recall's denominator is every span with a
// non-empty reference, shown or not.
func Curve(rows []Scored) []Point {
	want := 0
	var shown []Scored
	for _, r := range rows {
		if !r.RefEmpty {
			want++
		}
		if r.Shown {
			shown = append(shown, r)
		}
	}
	slices.SortStableFunc(shown, func(a, b Scored) int { return cmp.Compare(b.Confidence, a.Confidence) })
	var out []Point
	useful := 0
	for i, r := range shown {
		if r.Useful {
			useful++
		}
		if i+1 < len(shown) && shown[i+1].Confidence == r.Confidence {
			continue
		}
		p := Point{Tau: r.Confidence, Shown: i + 1, Useful: useful, Precision: float64(useful) / float64(i+1)}
		if want > 0 {
			p.Recall = float64(useful) / float64(want)
		}
		out = append(out, p)
	}
	return out
}

// AUPRC is the area under the curve, summed as average precision: each point's
// precision weighted by the recall it adds.
func AUPRC(points []Point) float64 {
	var area, prev float64
	for _, p := range points {
		area += (p.Recall - prev) * p.Precision
		prev = p.Recall
	}
	return area
}

// AtPrecision is the lowest-confidence point whose precision is at least floor, the
// threshold with the most recall that still holds it. ok is false when none does.
func AtPrecision(points []Point, floor float64) (Point, bool) {
	for i := len(points) - 1; i >= 0; i-- {
		if points[i].Precision >= floor {
			return points[i], true
		}
	}
	return Point{}, false
}

// AtTau is the point for showing completions at or above tau: the last point whose
// confidence is not below it, or a zero point when nothing reaches it.
func AtTau(points []Point, tau float64) Point {
	var out Point
	for _, p := range points {
		if p.Tau >= tau {
			out = p
		}
	}
	return out
}
