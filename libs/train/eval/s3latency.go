package eval

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/egladman/magus/libs/train/completion/fim"
	"github.com/egladman/magus/libs/train/completion/serve"
	"github.com/egladman/magus/libs/train/probe"
)

// Plan 4.2's two request shapes: an idle request stops at a newline within 24 tokens, an
// explicit one runs to 64.
const (
	SingleLineTokens = 24
	LongTokens       = 64
)

// Speculation is the --spec-type values the latency run compares: none, and the
// model-free ngram-simple of plan 4.2(4).
var Speculation = []string{"", "ngram-simple"}

// Latency is one request's timings.
type Latency struct {
	Group  string
	WallMS float64
	// Cached and Evaluated are llama-server's count of prompt tokens reused from the slot
	// and evaluated afresh.
	Cached, Evaluated int
	Drafted, Accepted int
}

// LatencyRun is the runs.jsonl line of one system and speculation's latency run.
type LatencyRun struct {
	Kind        string            `json:"kind"`
	Run         string            `json:"run"`
	System      string            `json:"system"`
	Order       string            `json:"order"`
	Context     string            `json:"context"`
	Spec        string            `json:"spec"`
	Inputs      map[string]string `json:"inputs_sha256"`
	Llama       serve.Build       `json:"llama_server"`
	Args        []string          `json:"args"`
	Requests    int               `json:"requests"`
	Set         Taken             `json:"set"`
	WallSeconds float64           `json:"wall_seconds"`
	Load1Start  float64           `json:"load1_start"`
	Load1End    float64           `json:"load1_end"`
	// MedianWarmCached is the median share of a warm request's prompt the slot already
	// held, the evidence the replay hit the cache it claims to.
	MedianWarmCached float64 `json:"median_warm_cached_share"`
}

// shape is one measured request kind.
type shape struct {
	name   string
	tokens int
	stop   []string
}

var shapes = []shape{{"single", SingleLineTokens, []string{"\n"}}, {"long", LongTokens, nil}}

// cursors reads the dev set as the replay's cursors, checked against its pin.
func (e S3) cursors(name string) ([]probe.Cursor, Taken, error) {
	if _, err := e.PinnedSet("fim-" + name); err != nil {
		return nil, Taken{}, err
	}
	spans, err := ReadRows[Span](e.File("fim-" + name + ".jsonl"))
	if err != nil {
		return nil, Taken{}, err
	}
	sum, err := FileDigest(e.File("fim-" + name + ".jsonl"))
	if err != nil {
		return nil, Taken{}, err
	}
	cs := make([]probe.Cursor, len(spans))
	for i, s := range spans {
		cs[i] = probe.Cursor{Group: s.Group, Path: s.Path, Start: s.Start, Prefix: s.Prefix, Middle: s.Middle, Suffix: s.Suffix}
	}
	return cs, Taken{Spans: len(spans), Of: len(spans), SHA256: sum}, nil
}

func record(c serve.Completion, group string) Latency {
	return Latency{Group: group, WallMS: float64(c.Wall) / float64(time.Millisecond), Cached: c.Timings.CacheN, Evaluated: c.Timings.PromptN,
		Drafted: c.Timings.DraftN, Accepted: c.Timings.DraftAccepted}
}

// measure replays the pairs against the server and returns the cold and warm timings of
// a shape. Cold erases the slot first, so the whole prompt prefills. Warm sends the
// primer without timing it and then the request after typing.
func measure(ctx context.Context, srv *serve.Llama, pairs []probe.Pair, sh shape) (cold, warm []Latency, err error) {
	req := func(prefix, suffix string, cache bool) serve.Request {
		return serve.Request{Prefix: prefix, Suffix: suffix, NPredict: sh.tokens, Stop: sh.stop, Seed: 1, CachePrompt: cache, Slot: 0}
	}
	for _, p := range pairs {
		if err := srv.EraseSlot(ctx, 0); err != nil {
			return nil, nil, err
		}
		c, err := srv.Infill(ctx, req(p.Prefix, p.Suffix, false))
		if err != nil {
			return nil, nil, err
		}
		cold = append(cold, record(c, p.Group))
		if err := srv.EraseSlot(ctx, 0); err != nil {
			return nil, nil, err
		}
		if _, err := srv.Infill(ctx, req(p.Primer, p.Suffix, true)); err != nil {
			return nil, nil, err
		}
		c, err = srv.Infill(ctx, req(p.Prefix, p.Suffix, true))
		if err != nil {
			return nil, nil, err
		}
		warm = append(warm, record(c, p.Group))
	}
	return cold, warm, nil
}

// LatencyResult is one system and speculation's timings by shape and temperature, and
// the rows made from them.
type LatencyResult struct {
	Wall map[string][]Latency
	Rows []Row
}

func latencyRows(sys System, spec, set, run string, name string, ls []Latency, of int) []Row {
	groups := make([]string, len(ls))
	walls := make([]float64, len(ls))
	mean := NewUnits()
	for i, l := range ls {
		groups[i], walls[i] = l.Group, l.WallMS
		mean.Add(strconv.Itoa(i), l.Group, l.WallMS)
	}
	row := func(metric string) Row {
		r := sys.Row(set, name+"."+metric, run)
		r.Spec, r.Of = spec, of
		return r
	}
	var out []Row
	for _, q := range []struct {
		metric string
		q      float64
	}{{"p50_ms", 0.5}, {"p95_ms", 0.95}} {
		r := row(q.metric)
		est, pseudo := Jackknife(groups, func(keep []int) float64 {
			sub := make([]float64, len(keep))
			for i, k := range keep {
				sub[i] = walls[k]
			}
			return Quantile(sub, q.q)
		})
		JackknifeRow(&r, est, pseudo)
		r.N = len(ls)
		out = append(out, r)
	}
	r := row("mean_ms")
	mean.Fill(&r)
	return append(out, r)
}

// RunLatency starts the system's model once per speculation setting, replays the dev
// spans in typing order, and appends the rows and the run. Every request is measured
// cold and warm, single-line and 64-token.
func (e S3) RunLatency(ctx context.Context, sys System, setName string, max int, log io.Writer) (map[string]LatencyResult, error) {
	cs, taken, err := e.cursors(setName)
	if err != nil {
		return nil, err
	}
	pairs := probe.Typing(cs, max)
	if len(pairs) == 0 {
		return nil, fmt.Errorf("%s has no span with a middle to replay", setName)
	}
	set := setName + "-latency"
	results := map[string]LatencyResult{}
	for _, spec := range Speculation {
		res, err := e.latencyOnce(ctx, sys, spec, set, pairs, taken, log)
		if err != nil {
			return nil, err
		}
		results[spec] = res
	}
	return results, nil
}

func (e S3) latencyOnce(ctx context.Context, sys System, spec, set string, pairs []probe.Pair, taken Taken, log io.Writer) (LatencyResult, error) {
	opts := serve.Options{Bin: e.Llama, Model: sys.Model, Ctx: ServerCtx, Slots: 1, SPM: sys.Order == fim.SPM, CacheReuse: 256, SpecType: spec,
		SlotSavePath: filepath.Join(e.Out, "slots")}
	run := LatencyRun{Kind: "latency", Run: e.Run, System: sys.Name, Order: string(sys.Order), Context: sys.Context, Spec: spec,
		Inputs: map[string]string{sys.Name: sys.SHA256}, Requests: len(pairs), Set: taken}
	var err error
	if err = os.MkdirAll(opts.SlotSavePath, 0o755); err != nil {
		return LatencyResult{}, err
	}
	if run.Load1Start, err = probe.Load1(); err != nil {
		return LatencyResult{}, err
	}
	start := time.Now()
	srv, err := serve.Start(ctx, opts, 15*time.Minute)
	if err != nil {
		return LatencyResult{}, err
	}
	defer srv.Close()
	run.Llama, run.Args = srv.Build, serve.Args(opts, 0)
	res := LatencyResult{Wall: map[string][]Latency{}}
	for _, sh := range shapes {
		fmt.Fprintf(log, "%s spec=%q %s: replaying %d pairs\n", sys.Key(), spec, sh.name, len(pairs))
		cold, warm, err := measure(ctx, srv, pairs, sh)
		if err != nil {
			return LatencyResult{}, err
		}
		for temp, ls := range map[string][]Latency{"cold": cold, "warm": warm} {
			name := temp + "." + sh.name
			res.Wall[name] = ls
			res.Rows = append(res.Rows, latencyRows(sys, spec, set, e.Run, name, ls, taken.Of)...)
		}
		if sh.name == "single" {
			run.MedianWarmCached = medianCachedShare(warm)
		}
	}
	run.WallSeconds = time.Since(start).Seconds()
	if run.Load1End, err = probe.Load1(); err != nil {
		return LatencyResult{}, err
	}
	for _, r := range res.Rows {
		if err := probe.Append(e.Evals, r); err != nil {
			return LatencyResult{}, err
		}
	}
	return res, probe.Append(e.Runs, run)
}

func medianCachedShare(ls []Latency) float64 {
	shares := make([]float64, 0, len(ls))
	for _, l := range ls {
		if total := l.Cached + l.Evaluated; total > 0 {
			shares = append(shares, float64(l.Cached)/float64(total))
		}
	}
	if len(shares) == 0 {
		return 0
	}
	return Quantile(shares, 0.5)
}

// SpeculationRow compares ngram-simple's warm single-line latency to none's on the same
// requests: the metric is the mean per-request saving in ms, with the t interval of the
// per-request differences and the clustered p-value against no saving. Plan 4.2(4) keeps
// speculation only when the median improves by at least 10%; the median saving is in the
// row's Interval note, not decided here.
func SpeculationRow(sys System, set, run string, none, ngram []Latency) (Row, error) {
	r := sys.Row(set, "warm.single.ngram_saving_ms", run)
	r.Spec = "ngram-simple"
	if len(none) != len(ngram) {
		return r, fmt.Errorf("speculation compare: %d requests without it, %d with", len(none), len(ngram))
	}
	diff, zero := NewUnits(), NewUnits()
	nm := make([]float64, len(none))
	gm := make([]float64, len(none))
	for i := range none {
		id := strconv.Itoa(i)
		diff.Add(id, none[i].Group, none[i].WallMS-ngram[i].WallMS)
		zero.Add(id, none[i].Group, 0)
		nm[i], gm[i] = none[i].WallMS, ngram[i].WallMS
	}
	diff.Fill(&r)
	r.P, r.Ref = diff.PValue(zero), "no speculation"
	p50n, p50g := Quantile(nm, 0.5), Quantile(gm, 0.5)
	r.Interval = fmt.Sprintf("%s; median %.1f ms without, %.1f ms with: %.1f%% better", r.Interval, p50n, p50g, 100*(p50n-p50g)/p50n)
	return r, nil
}
