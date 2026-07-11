// Package metrics is the daemon's derived-dashboard presentation layer for magus's OTel
// metrics. It rolls raw in-process metricdata (histogram buckets and counters, read via
// observability.Collector) into the magus.metrics.v1 wire types the /dashboard consumes,
// maintains a rolling sample ring for backfill, and implements the Connect MetricsService.
// It is the only place the generated metrics proto meets the OTel SDK; observability itself
// stays proto-free.
package metrics

import (
	"math"
	"sort"
	"time"

	"github.com/egladman/magus/internal/quantile"
	metricsv1 "github.com/egladman/magus/proto/gen/go/magus/metrics/v1"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// OTel instrument names magus registers (see internal/observability/provider_otel.go). Kept
// as constants so the aggregation and the instrument registration read from one vocabulary.
const (
	instTargetDuration = "magus.target.duration"
	instCacheDuration  = "magus.cache.duration"
	instPoolWait       = "magus.pool.wait.duration"
	instGraphQuery     = "magus.graph.query.duration"
	instRemoteHits     = "magus.cache.remote.hits"
	instRemoteMisses   = "magus.cache.remote.misses"
	instRemoteErrors   = "magus.cache.remote.errors"
	instRemoteDuration = "magus.cache.remote.duration"
	instRemoteIOSize   = "magus.cache.remote.io.size"

	instCacheHits   = "magus.cache.hits"
	instCacheMisses = "magus.cache.misses"
	instTargetRuns  = "magus.target.runs"

	instMCPCalls    = "magus.mcp.tool.calls"
	instMCPInput    = "magus.mcp.tool.input.size"
	instMCPOutput   = "magus.mcp.tool.output.size"
	instMCPDuration = "magus.mcp.tool.duration"

	instSandboxApply      = "magus.sandbox.apply.duration"
	instSandboxRules      = "magus.sandbox.rules"
	instSandboxEnvRules   = "magus.sandbox.env.rules"
	instSandboxChecks     = "magus.sandbox.checks"
	instSandboxEnvDropped = "magus.sandbox.env.dropped"

	instBuzzExec          = "magus.buzz.exec.duration"
	instBuzzCompile       = "magus.buzz.compile.duration"
	instBuzzHostCallDur   = "magus.buzz.host.call.duration"
	instBuzzHostCallCount = "magus.buzz.host.call.count"
	instBuzzSessionReuse  = "magus.buzz.session.pool.reuse"
	instBuzzSessionIdle   = "magus.buzz.session.pool.idle"
	instBuzzSessionEvict  = "magus.buzz.session.pool.evictions"
	instBuzzSessionWarm   = "magus.buzz.session.warm.duration"
	instBuzzImport        = "magus.buzz.import.duration"
	instBuzzSpellResolve  = "magus.buzz.spell.resolve.duration"
	instBuzzJITRuns       = "magus.buzz.jit.runs"
	instBuzzVMFaults      = "magus.buzz.vm.faults"

	// Attribute keys read off metric data points during grouped aggregation.
	attrProject  = "magus.project"
	attrSpell    = "magus.spell"
	attrTarget   = "magus.target"
	attrOutcome  = "outcome"
	attrCacheHit = "cache.hit"
	attrTool     = "tool"
	attrAccess   = "access"
	attrDecision = "decision"
)

// Aggregate rolls one metricdata.ResourceMetrics collection into a derived Snapshot: each
// latency histogram family becomes a Latency (count/sum plus interpolated p50/p95/p99/max),
// and the remote-cache instruments become a Remote. Missing instruments yield zero-valued
// (never nil) sub-messages, so the wire shape is stable from the first tick. at stamps
// captured_at.
func Aggregate(rm metricdata.ResourceMetrics, at time.Time) *metricsv1.Snapshot {
	snap := &metricsv1.Snapshot{
		CapturedAt: timestamppb.New(at),
		Target:     &metricsv1.Latency{},
		Cache:      &metricsv1.Latency{},
		PoolWait:   &metricsv1.Latency{},
		GraphQuery: &metricsv1.Latency{},
		Remote:     &metricsv1.Remote{},
		Buzz:       &metricsv1.Buzz{},
		Sandbox:    &metricsv1.Sandbox{},
	}

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch m.Name {
			case instTargetDuration:
				snap.Target = latencyOf(m.Data)
			case instCacheDuration:
				snap.Cache = latencyOf(m.Data)
			case instPoolWait:
				snap.PoolWait = latencyOf(m.Data)
			case instGraphQuery:
				snap.GraphQuery = latencyOf(m.Data)
			case instRemoteHits:
				snap.Remote.Hits = counterOf(m.Data)
			case instRemoteMisses:
				snap.Remote.Misses = counterOf(m.Data)
			case instRemoteErrors:
				snap.Remote.Errors = counterOf(m.Data)
			case instRemoteDuration:
				lat := latencyOf(m.Data)
				snap.Remote.DurationP50 = lat.P50
				snap.Remote.DurationP95 = lat.P95
				snap.Remote.IoCount = lat.Count
			case instRemoteIOSize:
				snap.Remote.BytesTotal = int64(latencyOf(m.Data).Sum)
			}
		}
	}

	snap.TargetStats = targetStats(rm)
	snap.McpTools = mcpToolStats(rm)
	snap.Buzz = buzzStats(rm)
	snap.Sandbox = sandboxStats(rm)
	return snap
}

// findMetric returns the first data aggregation named name across every scope, or (nil, false).
func findMetric(rm metricdata.ResourceMetrics, name string) (metricdata.Aggregation, bool) {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return m.Data, true
			}
		}
	}
	return nil, false
}

// attrOf reads a string attribute off a data point's set, or "" when absent.
func attrOf(set attribute.Set, key string) string {
	if v, ok := set.Value(attribute.Key(key)); ok {
		return v.Emit()
	}
	return ""
}

// forEachInt64DP invokes fn for every data point of a monotonic or up/down Int64 Sum. A
// non-Sum aggregation yields no calls.
func forEachInt64DP(agg metricdata.Aggregation, fn func(attribute.Set, int64)) {
	if s, ok := agg.(metricdata.Sum[int64]); ok {
		for _, dp := range s.DataPoints {
			fn(dp.Attributes, dp.Value)
		}
	}
}

// targetStats groups the magus.target.duration histogram's per-(project,spell,target,outcome,
// cache.hit) data points into one TargetStat per (project, target, spell): folded latency
// percentiles plus success/error tallies and the cache hit-rate. Rows are ordered by
// (project, spell, target) for a stable wire. Returns nil when the instrument is absent.
func targetStats(rm metricdata.ResourceMetrics) []*metricsv1.TargetStat {
	agg, ok := findMetric(rm, instTargetDuration)
	if !ok {
		return nil
	}
	h, ok := agg.(metricdata.Histogram[float64])
	if !ok {
		return nil
	}

	type key struct{ project, spell, target string }
	type acc struct {
		dps                        []metricdata.HistogramDataPoint[float64]
		count, success, errs, hits uint64
	}
	groups := map[key]*acc{}
	var order []key
	for _, dp := range h.DataPoints {
		k := key{
			project: attrOf(dp.Attributes, attrProject),
			spell:   attrOf(dp.Attributes, attrSpell),
			target:  attrOf(dp.Attributes, attrTarget),
		}
		a := groups[k]
		if a == nil {
			a = &acc{}
			groups[k] = a
			order = append(order, k)
		}
		a.dps = append(a.dps, dp)
		a.count += dp.Count
		if attrOf(dp.Attributes, attrOutcome) == "error" {
			a.errs += dp.Count
		} else {
			a.success += dp.Count
		}
		if attrOf(dp.Attributes, attrCacheHit) == "true" {
			a.hits += dp.Count
		}
	}

	sort.Slice(order, func(i, j int) bool {
		if order[i].project != order[j].project {
			return order[i].project < order[j].project
		}
		if order[i].spell != order[j].spell {
			return order[i].spell < order[j].spell
		}
		return order[i].target < order[j].target
	})

	rows := make([]*metricsv1.TargetStat, 0, len(order))
	for _, k := range order {
		a := groups[k]
		lat := foldHistogram(a.dps)
		var rate float64
		if a.count > 0 {
			rate = float64(a.hits) / float64(a.count)
		}
		rows = append(rows, &metricsv1.TargetStat{
			Project:      k.project,
			Target:       k.target,
			Spell:        k.spell,
			Count:        int64(a.count),
			P50:          lat.P50,
			P95:          lat.P95,
			P99:          lat.P99,
			CacheHitRate: rate,
			Success:      int64(a.success),
			Errors:       int64(a.errs),
		})
	}
	return rows
}

// mcpToolStats rolls the magus.mcp.tool.* instruments into one MCPToolStat per tool, ordered by
// tool name. Returns nil when no MCP instrument recorded anything.
func mcpToolStats(rm metricdata.ResourceMetrics) []*metricsv1.MCPToolStat {
	callsAgg, _ := findMetric(rm, instMCPCalls)
	inputAgg, _ := findMetric(rm, instMCPInput)
	outputAgg, _ := findMetric(rm, instMCPOutput)
	durAgg, _ := findMetric(rm, instMCPDuration)

	callsByTool := map[string]int64{}
	errsByTool := map[string]int64{}
	tools := map[string]struct{}{}
	forEachInt64DP(callsAgg, func(set attribute.Set, v int64) {
		tool := attrOf(set, attrTool)
		callsByTool[tool] += v
		if attrOf(set, attrOutcome) == "error" {
			errsByTool[tool] += v
		}
		tools[tool] = struct{}{}
	})

	inputByTool := latencyByAttr(inputAgg, attrTool)
	outputByTool := latencyByAttr(outputAgg, attrTool)
	durByTool := latencyByAttr(durAgg, attrTool)
	for t := range inputByTool {
		tools[t] = struct{}{}
	}
	for t := range outputByTool {
		tools[t] = struct{}{}
	}
	for t := range durByTool {
		tools[t] = struct{}{}
	}
	if len(tools) == 0 {
		return nil
	}

	order := make([]string, 0, len(tools))
	for t := range tools {
		order = append(order, t)
	}
	sort.Strings(order)

	rows := make([]*metricsv1.MCPToolStat, 0, len(order))
	for _, t := range order {
		in := latencyOr(inputByTool[t])
		out := latencyOr(outputByTool[t])
		dur := latencyOr(durByTool[t])
		rows = append(rows, &metricsv1.MCPToolStat{
			Tool:        t,
			Calls:       callsByTool[t],
			Errors:      errsByTool[t],
			InputP50:    in.P50,
			InputP95:    in.P95,
			InputTotal:  int64(in.Sum),
			OutputP50:   out.P50,
			OutputP95:   out.P95,
			OutputTotal: int64(out.Sum),
			DurationP50: dur.P50,
			DurationP95: dur.P95,
		})
	}
	return rows
}

// buzzStats rolls the magus.buzz.* families into a single Buzz message; a fully absent family
// leaves the corresponding fields zero.
func buzzStats(rm metricdata.ResourceMetrics) *metricsv1.Buzz {
	b := &metricsv1.Buzz{}
	if agg, ok := findMetric(rm, instBuzzExec); ok {
		lat := latencyOf(agg)
		b.ExecCount, b.ExecP50, b.ExecP95 = lat.Count, lat.P50, lat.P95
	}
	if agg, ok := findMetric(rm, instBuzzCompile); ok {
		lat := latencyOf(agg)
		b.CompileCount, b.CompileP50, b.CompileP95 = lat.Count, lat.P50, lat.P95
	}
	if agg, ok := findMetric(rm, instBuzzHostCallDur); ok {
		lat := latencyOf(agg)
		b.HostCallP50, b.HostCallP95 = lat.P50, lat.P95
	}
	if agg, ok := findMetric(rm, instBuzzHostCallCount); ok {
		b.HostCallCount = counterOf(agg)
	}
	if agg, ok := findMetric(rm, instBuzzSessionReuse); ok {
		b.SessionPoolReuse = counterOf(agg)
	}
	if agg, ok := findMetric(rm, instBuzzSessionIdle); ok {
		b.SessionPoolIdle = counterOf(agg)
	}
	if agg, ok := findMetric(rm, instBuzzSessionEvict); ok {
		b.SessionPoolEvictions = counterOf(agg)
	}
	if agg, ok := findMetric(rm, instBuzzSessionWarm); ok {
		lat := latencyOf(agg)
		b.SessionWarmP50, b.SessionWarmP95 = lat.P50, lat.P95
	}
	if agg, ok := findMetric(rm, instBuzzImport); ok {
		lat := latencyOf(agg)
		b.ImportCount, b.ImportP50, b.ImportP95 = lat.Count, lat.P50, lat.P95
	}
	if agg, ok := findMetric(rm, instBuzzSpellResolve); ok {
		lat := latencyOf(agg)
		b.SpellResolveCount, b.SpellResolveP50, b.SpellResolveP95 = lat.Count, lat.P50, lat.P95
	}
	if agg, ok := findMetric(rm, instBuzzJITRuns); ok {
		b.JitRuns = counterOf(agg)
	}
	if agg, ok := findMetric(rm, instBuzzVMFaults); ok {
		b.VmFaults = counterOf(agg)
	}
	return b
}

// sandboxStats rolls the magus.sandbox.* filesystem families into a single Sandbox message.
func sandboxStats(rm metricdata.ResourceMetrics) *metricsv1.Sandbox {
	s := &metricsv1.Sandbox{}
	if agg, ok := findMetric(rm, instSandboxApply); ok {
		lat := latencyOf(agg)
		s.ApplyP50, s.ApplyP95 = lat.P50, lat.P95
	}
	if agg, ok := findMetric(rm, instSandboxRules); ok {
		forEachInt64DP(agg, func(set attribute.Set, v int64) {
			switch attrOf(set, attrAccess) {
			case "read":
				s.RulesRead += v
			case "write":
				s.RulesWrite += v
			case "exec":
				s.RulesExec += v
			}
		})
	}
	if agg, ok := findMetric(rm, instSandboxEnvRules); ok {
		s.EnvRules = counterOf(agg)
	}
	if agg, ok := findMetric(rm, instSandboxChecks); ok {
		forEachInt64DP(agg, func(set attribute.Set, v int64) {
			switch attrOf(set, attrDecision) {
			case "allow":
				s.ChecksAllow += v
			case "deny":
				s.ChecksDeny += v
			}
		})
	}
	if agg, ok := findMetric(rm, instSandboxEnvDropped); ok {
		s.EnvDropped = counterOf(agg)
	}
	return s
}

// latencyByAttr folds a histogram's data points into one Latency per distinct value of key. A
// non-histogram or absent aggregation yields an empty map.
func latencyByAttr(agg metricdata.Aggregation, key string) map[string]*metricsv1.Latency {
	switch h := agg.(type) {
	case metricdata.Histogram[float64]:
		return foldByAttr(h.DataPoints, key)
	case metricdata.Histogram[int64]:
		return foldByAttr(h.DataPoints, key)
	default:
		return map[string]*metricsv1.Latency{}
	}
}

func foldByAttr[N int64 | float64](dps []metricdata.HistogramDataPoint[N], key string) map[string]*metricsv1.Latency {
	groups := map[string][]metricdata.HistogramDataPoint[N]{}
	for _, dp := range dps {
		v := attrOf(dp.Attributes, key)
		groups[v] = append(groups[v], dp)
	}
	out := make(map[string]*metricsv1.Latency, len(groups))
	for v, g := range groups {
		out[v] = foldHistogram(g)
	}
	return out
}

// latencyOr returns lat, or a zero Latency when lat is nil, so callers can read fields freely.
func latencyOr(lat *metricsv1.Latency) *metricsv1.Latency {
	if lat == nil {
		return &metricsv1.Latency{}
	}
	return lat
}

// counterTotals holds the cumulative counters the utilization sampler records per tick.
type counterTotals struct {
	cacheHits   int64
	cacheMisses int64
	targetRuns  int64
}

// counters reads the cumulative cache-hit, cache-miss, and target-run counters from a
// collection. Absent instruments read as zero.
func counters(rm metricdata.ResourceMetrics) counterTotals {
	var t counterTotals
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch m.Name {
			case instCacheHits:
				t.cacheHits = counterOf(m.Data)
			case instCacheMisses:
				t.cacheMisses = counterOf(m.Data)
			case instTargetRuns:
				t.targetRuns = counterOf(m.Data)
			}
		}
	}
	return t
}

// latencyOf rolls a histogram Aggregation into a Latency, summing across every data point
// (attribute set) in the family. A non-histogram or absent Aggregation yields a zero Latency.
func latencyOf(agg metricdata.Aggregation) *metricsv1.Latency {
	switch h := agg.(type) {
	case metricdata.Histogram[float64]:
		return foldHistogram(h.DataPoints)
	case metricdata.Histogram[int64]:
		return foldHistogram(h.DataPoints)
	default:
		return &metricsv1.Latency{}
	}
}

// counterOf totals a monotonic Sum Aggregation across its data points as an int64. A
// non-Sum or absent Aggregation reads as zero.
func counterOf(agg metricdata.Aggregation) int64 {
	switch s := agg.(type) {
	case metricdata.Sum[int64]:
		var total int64
		for _, dp := range s.DataPoints {
			total += dp.Value
		}
		return total
	case metricdata.Sum[float64]:
		var total float64
		for _, dp := range s.DataPoints {
			total += dp.Value
		}
		return int64(total)
	default:
		return 0
	}
}

// foldHistogram sums the count, sum, and bucket counts across a family's data points, then
// interpolates the percentiles from the combined buckets. Data points in one instrument
// share bucket boundaries (the SDK's default view), so bucket counts add elementwise.
func foldHistogram[N int64 | float64](dps []metricdata.HistogramDataPoint[N]) *metricsv1.Latency {
	lat := &metricsv1.Latency{}
	if len(dps) == 0 {
		return lat
	}

	var (
		count        uint64
		sum          float64
		bounds       []float64
		bucketCounts []uint64
		maxObserved  float64
		haveMax      bool
	)
	for _, dp := range dps {
		count += dp.Count
		sum += float64(dp.Sum)
		if bounds == nil {
			bounds = dp.Bounds
			bucketCounts = make([]uint64, len(dp.BucketCounts))
		}
		for i := range dp.BucketCounts {
			if i < len(bucketCounts) {
				bucketCounts[i] += dp.BucketCounts[i]
			}
		}
		if v, ok := dp.Max.Value(); ok {
			fv := float64(v)
			if !haveMax || fv > maxObserved {
				maxObserved = fv
				haveMax = true
			}
		}
	}

	lat.Count = int64(count)
	lat.Sum = sum
	if count == 0 {
		return lat
	}

	buckets := cumulativeBuckets(bounds, bucketCounts)
	lat.P50 = sanitize(quantile.Quantile(0.50, buckets))
	lat.P95 = sanitize(quantile.Quantile(0.95, buckets))
	lat.P99 = sanitize(quantile.Quantile(0.99, buckets))
	if haveMax {
		lat.Max = maxObserved
	} else {
		lat.Max = largestPopulatedBound(bounds, bucketCounts)
	}
	return lat
}

// cumulativeBuckets converts OTel explicit bounds plus per-bucket counts into the ascending
// cumulative buckets quantile.Quantile expects, appending the implied +Inf overflow bucket.
// bucketCounts has one more entry than bounds (the final +Inf bucket).
func cumulativeBuckets(bounds []float64, bucketCounts []uint64) []quantile.Bucket {
	buckets := make([]quantile.Bucket, 0, len(bucketCounts))
	var cum uint64
	for i, c := range bucketCounts {
		cum += c
		upper := math.Inf(+1)
		if i < len(bounds) {
			upper = bounds[i]
		}
		buckets = append(buckets, quantile.Bucket{UpperBound: upper, CumulativeCount: float64(cum)})
	}
	return buckets
}

// largestPopulatedBound returns the upper bound of the highest bucket that saw any
// observation, used as a max when the SDK did not record a Min/Max extremum. A populated
// +Inf overflow bucket has no finite bound, so it falls back to the largest finite bound.
func largestPopulatedBound(bounds []float64, bucketCounts []uint64) float64 {
	for i := len(bucketCounts) - 1; i >= 0; i-- {
		if bucketCounts[i] == 0 {
			continue
		}
		if i < len(bounds) {
			return bounds[i]
		}
		if len(bounds) > 0 {
			return bounds[len(bounds)-1]
		}
		return 0
	}
	return 0
}

// sanitize maps a NaN or infinite percentile (degenerate or empty histogram) to 0 so the
// wire carries a clean number.
func sanitize(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}
