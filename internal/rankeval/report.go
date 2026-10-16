package rankeval

import (
	"cmp"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"text/tabwriter"
	"time"
)

// Cutoff is the number of files every @10 metric scores. Its unit is a file, never a
// node: a ranker that returns files would otherwise reach deeper than one that
// returns the symbols inside them.
const Cutoff = 10

// FileDepth is how many files a Run keeps: the condensed list drops unjudged files
// and still needs Cutoff left.
const FileDepth = 100

// Run is one judgment replayed. Files is the ranking at file granularity, as FileCut
// builds it to FileDepth; every scored metric reads it. Ranked is the node ranking
// with each hit's file and directory after it, which only the node-level report of
// agent-agreement reads. Judgment.Gold holds only the ids that still resolve; Dropped
// counts the ones that did not. Unknown is a verdict of unknown rather than an answer.
type Run struct {
	Judgment Judgment
	Ranked   []string
	Files    []string
	Bytes    int
	Latency  time.Duration
	Unknown  bool
	Dropped  int
}

// FileCut maps each ranked id to the file id fileOf returns for it, keeps each file's
// first position and stops at k files. An id fileOf maps to "", such as a directory,
// takes no position.
func FileCut(ranked []string, fileOf func(id string) string, k int) []string {
	seen := make(map[string]bool, k)
	out := make([]string, 0, min(k, len(ranked)))
	for _, id := range ranked {
		if len(out) == k {
			break
		}
		file := fileOf(id)
		if file == "" || seen[file] {
			continue
		}
		seen[file] = true
		out = append(out, file)
	}
	return out
}

// Metrics summarizes the runs of one slice at file granularity, cut at Cutoff files.
// Rates and means are over Queries, the runs with at least one relevant file; a run
// with none counts in Unscorable only. Judged10 is the share of the top Cutoff files
// that carry a judgment, over the runs that returned a file. CondensedNDCG10 scores
// the list with unjudged files removed (Sakai, SIGIR 2007).
type Metrics struct {
	Queries         int           `json:"queries"`
	Unscorable      int           `json:"unscorable"`
	DroppedGold     int           `json:"dropped_gold"`
	NDCG10          float64       `json:"ndcg_at_10"`
	CondensedNDCG10 float64       `json:"condensed_ndcg_at_10"`
	Judged10        float64       `json:"judged_at_10"`
	Recall10        float64       `json:"recall_at_10"`
	RR10            float64       `json:"rr_at_10"`
	ZeroResult      float64       `json:"zero_result_rate"`
	Unknown         float64       `json:"unknown_rate"`
	LatencyP50      time.Duration `json:"latency_p50"`
	LatencyP95      time.Duration `json:"latency_p95"`
	MeanBytes       float64       `json:"mean_bytes"`
}

// NodeMetrics scores agent-agreement's node ranking against its file: and dir: gold.
// A hit's file and directory follow it in that ranking, so @10 counts about three
// real hits; it is never comparable to the file-level Metrics.
type NodeMetrics struct {
	Queries  int     `json:"queries"`
	Recall10 float64 `json:"recall_at_10"`
	NDCG10   float64 `json:"ndcg_at_10"`
	MRR      float64 `json:"mrr"`
}

// QueryScore is one query's file-level scores, kept so two reports can be compared
// query by query. Cluster is the session an agent-agreement row came from, and empty
// for a set whose queries are independent.
type QueryScore struct {
	ID              string  `json:"id"`
	Cluster         string  `json:"cluster,omitempty"`
	NDCG10          float64 `json:"ndcg_at_10"`
	CondensedNDCG10 float64 `json:"condensed_ndcg_at_10"`
}

// SetReport is one eval set's Metrics overall, per shape and per verb, its node-level
// report where it has one, and its per-query scores. Absent says why a set has no
// runs, such as a file not built yet or a holdout left sealed.
type SetReport struct {
	Set     EvalSet                `json:"eval_set"`
	Absent  string                 `json:"absent,omitempty"`
	Overall Metrics                `json:"overall"`
	Shapes  map[QueryShape]Metrics `json:"shapes,omitempty"`
	Verbs   map[string]Metrics     `json:"verbs,omitempty"`
	Nodes   *NodeMetrics           `json:"nodes,omitempty"`
	Scores  []QueryScore           `json:"scores,omitempty"`
}

// Report holds one SetReport per named set, in EvalSets order.
type Report struct {
	Sets []SetReport `json:"sets"`
}

// Summarize scores every run of set. It only reports: nothing here passes or fails.
func Summarize(set EvalSet, runs []Run) SetReport {
	byShape := map[QueryShape][]Run{}
	byVerb := map[string][]Run{}
	for _, r := range runs {
		byShape[r.Judgment.Shape] = append(byShape[r.Judgment.Shape], r)
		byVerb[r.Judgment.Verb] = append(byVerb[r.Judgment.Verb], r)
	}
	rep := SetReport{Set: set, Overall: summarize(runs)}
	if len(byShape) > 1 {
		rep.Shapes = make(map[QueryShape]Metrics, len(byShape))
		for shape, rs := range byShape {
			rep.Shapes[shape] = summarize(rs)
		}
	}
	if len(byVerb) > 1 {
		rep.Verbs = make(map[string]Metrics, len(byVerb))
		for verb, rs := range byVerb {
			rep.Verbs[verb] = summarize(rs)
		}
	}
	if set == SetAgentAgreement {
		rep.Nodes = summarizeNodes(runs)
	}
	for _, r := range runs {
		relevant, judged := fileGold(r.Judgment)
		if len(relevant) == 0 {
			continue
		}
		rep.Scores = append(rep.Scores, QueryScore{
			ID:              r.Judgment.ID,
			Cluster:         r.Judgment.Session,
			NDCG10:          NDCGAt(r.Files, relevant, Cutoff),
			CondensedNDCG10: NDCGAt(condensed(r.Files, judged), relevant, Cutoff),
		})
	}
	slices.SortFunc(rep.Scores, func(a, b QueryScore) int { return cmp.Compare(a.ID, b.ID) })
	return rep
}

// fileGold splits j's file: gold into the relevant files with their gain grade and
// every judged file. agent-agreement is scored binary: its grades rank what an agent
// did with a file (written over read), not how relevant the file was.
func fileGold(j Judgment) (relevant map[string]int, judged map[string]bool) {
	relevant = map[string]int{}
	judged = map[string]bool{}
	for id, grade := range j.Gold {
		if !strings.HasPrefix(id, "file:") {
			continue
		}
		judged[id] = true
		if grade > 0 {
			if j.Set == SetAgentAgreement {
				grade = 1
			}
			relevant[id] = grade
		}
	}
	return relevant, judged
}

func condensed(files []string, judged map[string]bool) []string {
	return slices.DeleteFunc(slices.Clone(files), func(f string) bool { return !judged[f] })
}

func summarize(runs []Run) Metrics {
	var m Metrics
	var latencies []time.Duration
	var bytes, returned int
	for _, r := range runs {
		m.DroppedGold += r.Dropped
		relevant, judged := fileGold(r.Judgment)
		if len(relevant) == 0 {
			m.Unscorable++
			continue
		}
		m.Queries++
		top := r.Files[:min(Cutoff, len(r.Files))]
		m.NDCG10 += NDCGAt(r.Files, relevant, Cutoff)
		m.CondensedNDCG10 += NDCGAt(condensed(r.Files, judged), relevant, Cutoff)
		m.Recall10 += RecallAt(top, relevant, Cutoff)
		m.RR10 += ReciprocalRank(top, relevant)
		if len(top) == 0 {
			m.ZeroResult++
		} else {
			returned++
			hit := 0
			for _, f := range top {
				if judged[f] {
					hit++
				}
			}
			m.Judged10 += float64(hit) / float64(len(top))
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
	m.NDCG10 /= n
	m.CondensedNDCG10 /= n
	m.Recall10 /= n
	m.RR10 /= n
	m.ZeroResult /= n
	m.Unknown /= n
	if returned > 0 {
		m.Judged10 /= float64(returned)
	}
	m.MeanBytes = float64(bytes) / n
	slices.Sort(latencies)
	m.LatencyP50 = nearestRank(latencies, 50)
	m.LatencyP95 = nearestRank(latencies, 95)
	return m
}

func summarizeNodes(runs []Run) *NodeMetrics {
	var m NodeMetrics
	for _, r := range runs {
		gold := map[string]int{}
		for id := range r.Judgment.Gold {
			gold[id] = 1
		}
		if len(gold) == 0 {
			continue
		}
		m.Queries++
		m.Recall10 += RecallAt(r.Ranked, gold, Cutoff)
		m.NDCG10 += NDCGAt(r.Ranked, gold, Cutoff)
		m.MRR += ReciprocalRank(r.Ranked, gold)
	}
	if m.Queries > 0 {
		n := float64(m.Queries)
		m.Recall10 /= n
		m.NDCG10 /= n
		m.MRR /= n
	}
	return &m
}

// nearestRank is the p-th percentile of sorted by the nearest-rank method.
func nearestRank(sorted []time.Duration, p int) time.Duration {
	i := (p*len(sorted) + 99) / 100
	return sorted[max(i, 1)-1]
}

// Text renders the report: per set a heading, then one aligned row per slice.
func (r Report) Text() string {
	var b strings.Builder
	for _, s := range r.Sets {
		heading := string(s.Set)
		if s.Set.ReportOnly() {
			heading += " (report-only)"
		}
		if s.Absent != "" {
			fmt.Fprintf(&b, "== %s: %s\n", heading, s.Absent)
			continue
		}
		fmt.Fprintf(&b, "== %s\n", heading)
		w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "slice\tqueries\tunscorable\tdropped gold\tnDCG@10\tcond nDCG@10\tjudged@10\tR@10\tRR@10\tzero\tunknown\tp50\tp95\tmean bytes")
		row := func(name string, m Metrics) {
			fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%.3f\t%.3f\t%.3f\t%.3f\t%.3f\t%.3f\t%.3f\t%s\t%s\t%.0f\n",
				name, m.Queries, m.Unscorable, m.DroppedGold, m.NDCG10, m.CondensedNDCG10, m.Judged10,
				m.Recall10, m.RR10, m.ZeroResult, m.Unknown, m.LatencyP50, m.LatencyP95, m.MeanBytes)
		}
		row("all", s.Overall)
		for _, shape := range slices.Sorted(maps.Keys(s.Shapes)) {
			row("shape="+string(shape), s.Shapes[shape])
		}
		for _, verb := range slices.Sorted(maps.Keys(s.Verbs)) {
			row("verb="+verb, s.Verbs[verb])
		}
		w.Flush()
		if n := s.Nodes; n != nil {
			fmt.Fprintf(&b, "node-level, not comparable to the rows above: queries %d, R@10 %.3f, nDCG@10 %.3f, MRR %.3f\n",
				n.Queries, n.Recall10, n.NDCG10, n.MRR)
		}
	}
	return b.String()
}

// Randomization fixes the sign-flip test every Comparison runs: its Monte Carlo
// standard error at p = 0.05 is about 0.0007.
const (
	randomizationSamples        = 100_000
	randomizationSeed    uint64 = 1
)

// Comparison is one metric of one set compared between two reports over the queries
// both scored. Delta is `mean(now - base)` over those pairs. P is the two-sided paired
// randomization p, flipping one sign per session for a clustered set, and PHolm is P
// adjusted by Holm over every comparison of the same Compare. Lo, Hi and Effect are
// the 95% t-interval and Cohen's d_z; for a clustered set they are taken over
// per-session means, so Sessions pairs rather than Pairs enter them. Skipped says why
// a set has no test, such as fewer than 2 pairs.
type Comparison struct {
	Set      EvalSet `json:"eval_set"`
	Metric   string  `json:"metric"`
	Pairs    int     `json:"pairs"`
	Sessions int     `json:"sessions,omitzero"`
	Base     float64 `json:"base"`
	Now      float64 `json:"now"`
	Delta    float64 `json:"delta"`
	Lo       float64 `json:"lo"`
	Hi       float64 `json:"hi"`
	Effect   float64 `json:"effect"`
	P        float64 `json:"p"`
	PHolm    float64 `json:"p_holm"`
	Skipped  string  `json:"skipped,omitempty"`
}

// Compare tests nDCG@10 and condensed nDCG@10 of every set both reports scored, in
// EvalSets order. A set either report lacks is left out.
func Compare(base, now Report) []Comparison {
	baseSets := map[EvalSet]SetReport{}
	for _, s := range base.Sets {
		baseSets[s.Set] = s
	}
	out := make([]Comparison, 0, 2*len(now.Sets))
	for _, s := range now.Sets {
		b, ok := baseSets[s.Set]
		if !ok || s.Absent != "" || b.Absent != "" {
			continue
		}
		out = append(out,
			compareScores(s.Set, "ndcg@10", b.Scores, s.Scores, func(q QueryScore) float64 { return q.NDCG10 }),
			compareScores(s.Set, "condensed ndcg@10", b.Scores, s.Scores, func(q QueryScore) float64 { return q.CondensedNDCG10 }))
	}
	ps := make([]float64, 0, len(out))
	tested := make([]int, 0, len(out))
	for i, c := range out {
		if c.Skipped == "" {
			ps = append(ps, c.P)
			tested = append(tested, i)
		}
	}
	for j, p := range Holm(ps) {
		out[tested[j]].PHolm = p
	}
	return out
}

func compareScores(set EvalSet, metric string, base, now []QueryScore, value func(QueryScore) float64) Comparison {
	c := Comparison{Set: set, Metric: metric}
	byID := make(map[string]QueryScore, len(base))
	for _, q := range base {
		byID[q.ID] = q
	}
	a, b := make([]float64, 0, len(now)), make([]float64, 0, len(now))
	clusters := make([]string, 0, len(now))
	for _, q := range now {
		old, ok := byID[q.ID]
		if !ok {
			continue
		}
		a, b = append(a, value(q)), append(b, value(old))
		clusters = append(clusters, q.Cluster)
	}
	c.Pairs = len(a)
	for i := range a {
		c.Now += a[i] / float64(len(a))
		c.Base += b[i] / float64(len(a))
	}
	clustered := slices.ContainsFunc(clusters, func(s string) bool { return s != "" })
	ia, ib := a, b
	if clustered {
		ia, ib = clusterMeans(a, b, clusters)
		c.Sessions = len(ia)
	}
	if len(ia) < 2 {
		c.Skipped = fmt.Sprintf("%d paired unit(s); a test needs 2", len(ia))
		return c
	}
	if clustered {
		c.Delta, c.P = ClusteredRandomization(a, b, clusters, randomizationSamples, randomizationSeed)
	} else {
		c.Delta, c.P = PairedRandomization(a, b, randomizationSamples, randomizationSeed)
	}
	_, c.Lo, c.Hi = PairedTInterval(ia, ib, 0.95)
	c.Effect = EffectSize(ia, ib)
	return c
}

// clusterMeans averages a and b within each cluster, in first-seen order.
func clusterMeans(a, b []float64, clusters []string) (ma, mb []float64) {
	index := map[string]int{}
	var n []float64
	for i, c := range clusters {
		j, ok := index[c]
		if !ok {
			j = len(ma)
			index[c] = j
			ma, mb, n = append(ma, 0), append(mb, 0), append(n, 0)
		}
		ma[j] += a[i]
		mb[j] += b[i]
		n[j]++
	}
	for j := range ma {
		ma[j] /= n[j]
		mb[j] /= n[j]
	}
	return ma, mb
}

// ComparisonText renders comparisons as one aligned row each.
func ComparisonText(cs []Comparison) string {
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "set\tmetric\tpairs\tsessions\tbase\tnow\tdelta\t95% CI\td_z\tp\tp (Holm)")
	for _, c := range cs {
		if c.Skipped != "" {
			fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%.3f\t%.3f\t\t\t\t\tnot tested: %s\n", c.Set, c.Metric, c.Pairs, c.Sessions, c.Base, c.Now, c.Skipped)
			continue
		}
		effect := "n/a"
		if !math.IsNaN(c.Effect) && !math.IsInf(c.Effect, 0) {
			effect = fmt.Sprintf("%.2f", c.Effect)
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%.3f\t%.3f\t%+.3f\t[%+.3f, %+.3f]\t%s\t%.4f\t%.4f\n",
			c.Set, c.Metric, c.Pairs, c.Sessions, c.Base, c.Now, c.Delta, c.Lo, c.Hi, effect, c.P, c.PHolm)
	}
	_ = w.Flush()
	return b.String()
}
