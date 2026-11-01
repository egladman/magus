package historymetrics

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/egladman/magus/internal/service/console"
	graphv1 "github.com/egladman/magus/proto/gen/go/magus/graph/v1alpha1"
	hmv1 "github.com/egladman/magus/proto/gen/go/magus/historymetrics/v1alpha1"
	"github.com/egladman/magus/proto/gen/go/magus/historymetrics/v1alpha1/historymetricsv1alpha1connect"
	"github.com/egladman/magus/types"
)

type source interface {
	HistoryMetrics(ctx context.Context) (console.Board, error)
}

// Service implements HistoryMetricsServiceHandler over src.
type Service struct {
	src source
}

// NewService builds the HistoryMetricsService Connect handler reading from src.
func NewService(src source) *Service { return &Service{src: src} }

var _ historymetricsv1alpha1connect.HistoryMetricsServiceHandler = (*Service)(nil)

// GetHistoryMetrics returns every metric field in one response.
func (s *Service) GetHistoryMetrics(ctx context.Context, _ *connect.Request[hmv1.GetHistoryMetricsRequest]) (*connect.Response[hmv1.GetHistoryMetricsResponse], error) {
	board, err := s.src.HistoryMetrics(ctx)
	if err != nil {
		if errors.Is(err, console.ErrNoWorkspace) {
			return nil, connect.NewError(connect.CodeUnavailable, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(boardToProto(board)), nil
}

func boardToProto(b console.Board) *hmv1.GetHistoryMetricsResponse {
	return &hmv1.GetHistoryMetricsResponse{
		Hotspots: &hmv1.Hotspots{
			Definition: b.Hotspots.Definition,
			Commits:    int32(b.Hotspots.Commits),
			Since:      b.Hotspots.Since,
			Nodes:      mapSlice(b.Hotspots.Nodes, nodeToProto),
			Files:      mapSlice(b.Hotspots.Files, fileHotspotToProto),
		},
		Affinity: &hmv1.Affinity{
			Definition: b.Affinity.Definition,
			Commits:    int32(b.Affinity.Commits),
			Since:      b.Affinity.Since,
			Pairs:      mapSlice(b.Affinity.Pairs, coChangeToProto),
		},
		Ownership: &hmv1.Ownership{
			Definition: b.Ownership.Definition,
			Commits:    int32(b.Ownership.Commits),
			Since:      b.Ownership.Since,
			Projects:   mapSlice(b.Ownership.Projects, ownershipToProto),
		},
		Trend: &hmv1.Trend{
			Definition: b.Trend.Definition,
			Commits:    int32(b.Trend.Commits),
			Since:      b.Trend.Since,
			Projects:   mapSlice(b.Trend.Projects, trendToProto),
		},
		Volatility:  volatilityToProto(b.Volatility),
		ModelTiers:  modelTiersToProto(b.ModelTiers),
		Conformance: conformanceToProto(b.Conformance),
	}
}

func conformanceToProto(o *types.Conformance) *hmv1.Conformance {
	if o == nil {
		return nil
	}
	return &hmv1.Conformance{
		Definition: o.Definition,
		Rows: mapSlice(o.Rows, func(r types.ConformanceRow) *hmv1.ConformanceRow {
			return &hmv1.ConformanceRow{
				Key: &hmv1.ConformanceKey{
					Metric:     string(r.Key.Metric),
					Population: string(r.Key.Population),
					Scope:      r.Key.Scope,
				},
				N:          int32(r.N),
				P50:        int32(r.P50),
				P90:        int32(r.P90),
				P95:        int32(r.P95),
				P99:        int32(r.P99),
				Live:       int32(r.Live),
				Value:      int32(r.Value),
				Propose:    int32(r.Propose),
				Violations: mapSlice(r.Violations, conformanceItemToProto),
			}
		}),
		Excluded: int32(o.Excluded),
		Answer:   answerToProto(o.Answer),
	}
}

func conformanceItemToProto(i types.ConformanceItem) *hmv1.ConformanceItem {
	return &hmv1.ConformanceItem{Id: i.ID, Source: i.Source, Measure: int32(i.Measure)}
}

func answerToProto(a types.KnowledgeAnswer) *graphv1.Answer {
	return &graphv1.Answer{
		Verdict: string(a.Verdict),
		Reason:  string(a.Reason),
		Gaps: mapSlice(a.Gaps, func(g types.KnowledgeSymbolGap) *graphv1.SymbolGap {
			return &graphv1.SymbolGap{
				ProjectPath: g.Project.Path,
				ProjectName: g.Project.Name,
				State:       string(g.State),
				Detail:      g.Detail,
			}
		}),
	}
}

func modelTiersToProto(o *types.ModelTiers) *hmv1.ModelTiers {
	if o == nil {
		return nil
	}
	return &hmv1.ModelTiers{
		Definition: o.Definition,
		Rows: mapSlice(o.Rows, func(r types.ModelTierRecord) *hmv1.ModelTierRecord {
			return &hmv1.ModelTierRecord{
				Rank:            int32(r.Rank),
				ModelTier:       r.ModelTier,
				Kind:            string(r.Kind),
				Jobs:            int32(r.Jobs),
				Pass:            int32(r.Pass),
				Fail:            int32(r.Fail),
				NoReturn:        int32(r.NoReturn),
				PassLower:       r.PassLower,
				ChecksPerJob:    r.ChecksPerJob,
				ForksPerId:      r.ForksPerID,
				ReworkPerJob:    r.ReworkPerJob,
				WideningsPerJob: r.WideningsPerJob,
				MeanCostUsd:     r.MeanCostUSD,
				MedianTokens:    r.MedianTokens,
				FirstAttempts:   int32(r.FirstAttempts),
				FirstPass:       int32(r.FirstPass),
				FirstPassLower:  r.FirstPassLower,
				NotRetryable:    int32(r.NotRetryable),
			}
		}),
	}
}

func nodeToProto(n types.Node) *hmv1.ProjectNode {
	p := &hmv1.ProjectNode{
		Path: n.Path, Name: n.Name, SpellName: n.SpellName, Children: n.Children, Dir: n.Dir,
		BlastRadius: int32(n.BlastRadius), DurationMs: n.DurationMs,
		Churn: int32(n.Churn), Authors: int32(n.Authors),
	}
	if n.LastCommit != nil {
		p.LastCommitTime = tsFromTime(*n.LastCommit)
	}
	return p
}

func fileHotspotToProto(f types.FileHotspot) *hmv1.FileHotspot {
	return &hmv1.FileHotspot{
		Path: f.Path, Commits: int32(f.Commits), Complexity: int32(f.Complexity),
		Score: int32(f.Score), Authors: int32(f.Authors),
		LastCommitTime: tsFromTime(f.LastCommit), Moves: int32(f.Moves),
	}
}

func coChangeToProto(c types.CoChange) *hmv1.CoChange {
	return &hmv1.CoChange{A: c.A, AName: c.AName, B: c.B, BName: c.BName, Count: int32(c.Count), Hidden: c.Hidden}
}

func ownershipToProto(o types.OwnershipEntry) *hmv1.OwnershipEntry {
	return &hmv1.OwnershipEntry{
		Path: o.Path, Name: o.Name, Commits: int32(o.Commits), Authors: int32(o.Authors),
		Primary: o.Primary, PrimaryShare: int32(o.PrimaryShare), BusFactor_1: o.BusFactor1,
		Stale: o.Stale, LastCommitTime: tsFromTime(o.LastCommit),
	}
}

func trendToProto(t types.TrendEntry) *hmv1.TrendEntry {
	return &hmv1.TrendEntry{
		Path: t.Path, Name: t.Name, Recent: int32(t.Recent), Earlier: int32(t.Earlier), Delta: int32(t.Delta),
	}
}

func volatilityToProto(r *types.Volatility) *hmv1.Volatility {
	if r == nil {
		return nil
	}
	return &hmv1.Volatility{
		Threshold: r.Threshold,
		Targets: mapSlice(r.Targets, func(t types.VolatilityTarget) *hmv1.VolatilityTarget {
			return &hmv1.VolatilityTarget{
				Project: t.Project, Target: t.Target, Score: t.Score, Volatile: t.Volatile,
				Pass: int32(t.Pass), Fail: int32(t.Fail), VolatileCount: int32(t.VolatileCount),
				Samples: int32(t.Samples), LastPassTime: tsFromTime(t.LastPass),
			}
		}),
	}
}

func mapSlice[T any, P any](in []T, fn func(T) P) []P {
	if len(in) == 0 {
		return nil
	}
	out := make([]P, 0, len(in))
	for _, v := range in {
		out = append(out, fn(v))
	}
	return out
}

func tsFromTime(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}
