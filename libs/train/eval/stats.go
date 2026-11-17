package eval

import (
	"cmp"
	"math"
	"slices"

	"github.com/egladman/magus/libs/embed/rankeval"
)

// Every stage S3 metric carries the plan 6 interval and test: a 95% interval from
// rankeval.PairedTInterval and a p-value from rankeval.ClusteredRandomization, clustered
// by provenance group, over Samples sign flips from the fixed Seed.
const (
	Confidence = 0.95
	Samples    = 10_000
	Seed       = 0x5333
)

// Row is one line of libs/train/evals.jsonl. Value is nil for a metric the run could
// not score, with NotScored saying why. Low and High are nil when fewer than two units
// carry the metric. P is the clustered p-value of this system against the Ref system on
// the units both have, and nil on the reference itself.
type Row struct {
	System    string   `json:"system"`
	Context   string   `json:"context"`
	Order     string   `json:"order,omitempty"`
	Spec      string   `json:"spec,omitempty"`
	Set       string   `json:"set"`
	Sample    string   `json:"sample,omitempty"`
	Metric    string   `json:"metric"`
	Value     *float64 `json:"value,omitempty"`
	Low       *float64 `json:"low,omitempty"`
	High      *float64 `json:"high,omitempty"`
	N         int      `json:"n"`
	Of        int      `json:"of,omitempty"`
	Groups    int      `json:"groups,omitempty"`
	Interval  string   `json:"interval,omitempty"`
	P         *float64 `json:"p_vs_ref,omitempty"`
	Ref       string   `json:"ref,omitempty"`
	NotScored string   `json:"not_scored,omitempty"`
	Run       string   `json:"run"`
}

func ptr(f float64) *float64 { return &f }

// Units are a metric's per-unit values keyed by unit id, with each unit's provenance
// group.
type Units struct {
	Value map[string]float64
	Group map[string]string
}

// NewUnits makes an empty Units.
func NewUnits() Units { return Units{Value: map[string]float64{}, Group: map[string]string{}} }

// Add records one unit.
func (u Units) Add(id, group string, v float64) {
	u.Value[id] = v
	u.Group[id] = group
}

func (u Units) ids() []string {
	ids := make([]string, 0, len(u.Value))
	for id := range u.Value {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func (u Units) groups() int {
	seen := map[string]bool{}
	for id := range u.Value {
		seen[u.Group[id]] = true
	}
	return len(seen)
}

// Mean is the unit mean, 0 for none.
func (u Units) Mean() float64 {
	if len(u.Value) == 0 {
		return 0
	}
	var s float64
	for _, v := range u.Value {
		s += v
	}
	return s / float64(len(u.Value))
}

// Fill sets the row's value, interval and counts from the unit values: the interval is
// PairedTInterval of the units against zero, so it is the t interval of their mean.
func (u Units) Fill(r *Row) {
	vals := make([]float64, 0, len(u.Value))
	for _, id := range u.ids() {
		vals = append(vals, u.Value[id])
	}
	r.N, r.Groups = len(vals), u.groups()
	if len(vals) == 0 {
		r.NotScored = "no unit carries this metric"
		return
	}
	r.Value = ptr(u.Mean())
	if len(vals) < 2 {
		r.Interval = "fewer than 2 units"
		return
	}
	_, lo, hi := rankeval.PairedTInterval(vals, make([]float64, len(vals)), Confidence)
	r.Low, r.High = ptr(lo), ptr(hi)
	r.Interval = "paired t over units"
}

// PValue is the clustered randomization p-value of u against ref on the units both
// carry, one sign per provenance group. It is nil when they share no unit.
func (u Units) PValue(ref Units) *float64 {
	var a, b []float64
	var clusters []string
	for _, id := range u.ids() {
		rv, ok := ref.Value[id]
		if !ok {
			continue
		}
		a, b = append(a, u.Value[id]), append(b, rv)
		clusters = append(clusters, u.Group[id])
	}
	if len(a) == 0 {
		return nil
	}
	_, p := rankeval.ClusteredRandomization(a, b, clusters, Samples, Seed)
	return ptr(p)
}

// Jackknife estimates a statistic over rows that carry a provenance group, for a
// statistic with no per-unit value (a quantile, an area under a curve). It deletes one
// group at a time, turns the estimates into pseudo-values, and takes the t interval of
// their mean: the delete-one-group jackknife. The Units it returns hold one pseudo-value
// per group, so PValue clusters them one sign each. stat gets the indexes of the rows
// kept.
func Jackknife(groups []string, stat func(keep []int) float64) (est float64, pseudo Units) {
	all := make([]int, len(groups))
	for i := range all {
		all[i] = i
	}
	est = stat(all)
	byGroup := map[string][]int{}
	for i, g := range groups {
		byGroup[g] = append(byGroup[g], i)
	}
	names := make([]string, 0, len(byGroup))
	for g := range byGroup {
		names = append(names, g)
	}
	slices.SortFunc(names, cmp.Compare[string])
	pseudo = NewUnits()
	g := float64(len(names))
	for _, name := range names {
		var keep []int
		for _, other := range names {
			if other != name {
				keep = append(keep, byGroup[other]...)
			}
		}
		if len(keep) == 0 {
			continue
		}
		pseudo.Add(name, name, g*est-(g-1)*stat(keep))
	}
	return est, pseudo
}

// JackknifeRow fills a row's value and interval from a jackknife: the value is the
// estimate on every row, the interval is the t interval of the pseudo-values.
func JackknifeRow(r *Row, est float64, pseudo Units) {
	pseudo.Fill(r)
	if r.NotScored != "" {
		return
	}
	r.Value = ptr(est)
	if r.Low != nil {
		r.Interval = "delete-one-group jackknife, t over pseudo-values"
	}
}

// Quantile is the q-quantile of xs by linear interpolation between order statistics.
func Quantile(xs []float64, q float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	pos := q * float64(len(s)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	return s[lo] + (s[hi]-s[lo])*(pos-float64(lo))
}
