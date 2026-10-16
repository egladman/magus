package rankeval

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"
	"time"
)

// Run is one judgment replayed: the ids the verb ranked, in order, and what answering
// cost. Judgment.Gold holds only the ids that still resolve; Dropped counts the ones
// that did not. Unknown is a verdict of unknown rather than an answer.
type Run struct {
	Judgment Judgment
	Ranked   []string
	Bytes    int
	Latency  time.Duration
	Unknown  bool
	Dropped  int
}

// Metrics summarizes a set of runs. Rates and means are over Queries, the runs with at
// least one gold id left; a run whose every gold id was dropped counts in Unscorable
// only.
type Metrics struct {
	Queries     int           `json:"queries"`
	Unscorable  int           `json:"unscorable"`
	DroppedGold int           `json:"dropped_gold"`
	Recall5     float64       `json:"recall_at_5"`
	Recall10    float64       `json:"recall_at_10"`
	Recall50    float64       `json:"recall_at_50"`
	MRR         float64       `json:"mrr"`
	NDCG10      float64       `json:"ndcg_at_10"`
	ZeroResult  float64       `json:"zero_result_rate"`
	Unknown     float64       `json:"unknown_rate"`
	LatencyP50  time.Duration `json:"latency_p50"`
	LatencyP95  time.Duration `json:"latency_p95"`
	MeanBytes   float64       `json:"mean_bytes"`
}

// Report is Metrics overall and per slice, so a gain on one shape, split or verb cannot
// hide a loss on another. VerbSplits is keyed by verb, then split.
type Report struct {
	Overall    Metrics                       `json:"overall"`
	Shapes     map[QueryShape]Metrics        `json:"shapes"`
	Splits     map[string]Metrics            `json:"splits"`
	Verbs      map[string]Metrics            `json:"verbs"`
	VerbSplits map[string]map[string]Metrics `json:"verb_splits"`
}

// Delta is one metric of one slice that differs between two reports. Slice is "all",
// "shape=<shape>", "split=<split>", "verb=<verb>" or "verb=<verb>,split=<split>".
type Delta struct {
	Metric string  `json:"metric"`
	Slice  string  `json:"slice"`
	Base   float64 `json:"base"`
	Now    float64 `json:"now"`
}

// Summarize scores every run. It only reports: nothing here passes or fails.
func Summarize(runs []Run) Report {
	byShape := map[QueryShape][]Run{}
	bySplit := map[string][]Run{}
	byVerb := map[string][]Run{}
	byVerbSplit := map[string]map[string][]Run{}
	for _, r := range runs {
		j := r.Judgment
		byShape[j.Shape] = append(byShape[j.Shape], r)
		bySplit[j.Split] = append(bySplit[j.Split], r)
		byVerb[j.Verb] = append(byVerb[j.Verb], r)
		if byVerbSplit[j.Verb] == nil {
			byVerbSplit[j.Verb] = map[string][]Run{}
		}
		byVerbSplit[j.Verb][j.Split] = append(byVerbSplit[j.Verb][j.Split], r)
	}
	rep := Report{
		Overall:    summarize(runs),
		Shapes:     make(map[QueryShape]Metrics, len(byShape)),
		Splits:     make(map[string]Metrics, len(bySplit)),
		Verbs:      make(map[string]Metrics, len(byVerb)),
		VerbSplits: make(map[string]map[string]Metrics, len(byVerbSplit)),
	}
	for shape, rs := range byShape {
		rep.Shapes[shape] = summarize(rs)
	}
	for split, rs := range bySplit {
		rep.Splits[split] = summarize(rs)
	}
	for verb, rs := range byVerb {
		rep.Verbs[verb] = summarize(rs)
	}
	for verb, splits := range byVerbSplit {
		rep.VerbSplits[verb] = make(map[string]Metrics, len(splits))
		for split, rs := range splits {
			rep.VerbSplits[verb][split] = summarize(rs)
		}
	}
	return rep
}

func summarize(runs []Run) Metrics {
	var m Metrics
	var latencies []time.Duration
	var bytes int
	for _, r := range runs {
		m.DroppedGold += r.Dropped
		gold := r.Judgment.Gold
		if len(gold) == 0 {
			m.Unscorable++
			continue
		}
		m.Queries++
		m.Recall5 += RecallAt(r.Ranked, gold, 5)
		m.Recall10 += RecallAt(r.Ranked, gold, 10)
		m.Recall50 += RecallAt(r.Ranked, gold, 50)
		m.MRR += ReciprocalRank(r.Ranked, gold)
		m.NDCG10 += NDCGAt(r.Ranked, gold, 10)
		if len(r.Ranked) == 0 {
			m.ZeroResult++
		}
		if r.Unknown {
			m.Unknown++
		}
		latencies = append(latencies, r.Latency)
		bytes += r.Bytes
	}
	if m.Queries == 0 {
		return m
	}
	n := float64(m.Queries)
	m.Recall5 /= n
	m.Recall10 /= n
	m.Recall50 /= n
	m.MRR /= n
	m.NDCG10 /= n
	m.ZeroResult /= n
	m.Unknown /= n
	m.MeanBytes = float64(bytes) / n
	slices.Sort(latencies)
	m.LatencyP50 = nearestRank(latencies, 50)
	m.LatencyP95 = nearestRank(latencies, 95)
	return m
}

// nearestRank is the p-th percentile of sorted by the nearest-rank method.
func nearestRank(sorted []time.Duration, p int) time.Duration {
	i := (p*len(sorted) + 99) / 100
	return sorted[max(i, 1)-1]
}

type namedValue struct {
	name  string
	value float64
}

func (m Metrics) values() []namedValue {
	return []namedValue{
		{"queries", float64(m.Queries)},
		{"unscorable", float64(m.Unscorable)},
		{"dropped_gold", float64(m.DroppedGold)},
		{"recall@5", m.Recall5},
		{"recall@10", m.Recall10},
		{"recall@50", m.Recall50},
		{"mrr", m.MRR},
		{"ndcg@10", m.NDCG10},
		{"zero_result_rate", m.ZeroResult},
		{"unknown_rate", m.Unknown},
		{"latency_p50_ms", float64(m.LatencyP50) / float64(time.Millisecond)},
		{"latency_p95_ms", float64(m.LatencyP95) / float64(time.Millisecond)},
		{"mean_bytes", m.MeanBytes},
	}
}

func (r Report) slices() map[string]Metrics {
	out := map[string]Metrics{"all": r.Overall}
	for shape, m := range r.Shapes {
		out["shape="+string(shape)] = m
	}
	for split, m := range r.Splits {
		out["split="+split] = m
	}
	for verb, m := range r.Verbs {
		out["verb="+verb] = m
	}
	for verb, splits := range r.VerbSplits {
		for split, m := range splits {
			out["verb="+verb+",split="+split] = m
		}
	}
	return out
}

// sliceOrder puts "all" first, then shapes, splits, verbs and each verb's splits, each
// sorted by name.
func sliceOrder(names []string) []string {
	slices.SortFunc(names, func(a, b string) int {
		return cmp.Or(cmp.Compare(sliceRank(a), sliceRank(b)), cmp.Compare(a, b))
	})
	return names
}

func sliceRank(name string) int {
	switch {
	case name == "all":
		return 0
	case strings.HasPrefix(name, "shape="):
		return 1
	case strings.HasPrefix(name, "split="):
		return 2
	case strings.Contains(name, ",split="):
		return 4
	default:
		return 3
	}
}

// Text renders the report as one aligned row per slice.
func (r Report) Text() string {
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "slice\tqueries\tunscorable\tdropped gold\tR@5\tR@10\tR@50\tMRR\tnDCG@10\tzero\tunknown\tp50\tp95\tmean bytes")
	all := r.slices()
	for _, name := range sliceOrder(slices.Collect(maps.Keys(all))) {
		m := all[name]
		fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%.3f\t%.3f\t%.3f\t%.3f\t%.3f\t%.3f\t%.3f\t%s\t%s\t%.0f\n",
			name, m.Queries, m.Unscorable, m.DroppedGold, m.Recall5, m.Recall10, m.Recall50,
			m.MRR, m.NDCG10, m.ZeroResult, m.Unknown, m.LatencyP50, m.LatencyP95, m.MeanBytes)
	}
	_ = w.Flush()
	return b.String()
}

// Compare lists every metric whose value differs between base and now, slice by slice.
// A slice present in only one report compares against zero Metrics, so it shows as
// appearing or vanishing rather than being skipped.
func Compare(base, now Report) []Delta {
	b, n := base.slices(), now.slices()
	names := slices.Collect(maps.Keys(b))
	for name := range n {
		if _, ok := b[name]; !ok {
			names = append(names, name)
		}
	}
	var out []Delta
	for _, name := range sliceOrder(names) {
		bv, nv := b[name].values(), n[name].values()
		for i := range bv {
			if bv[i].value != nv[i].value {
				out = append(out, Delta{Metric: bv[i].name, Slice: name, Base: bv[i].value, Now: nv[i].value})
			}
		}
	}
	return out
}
