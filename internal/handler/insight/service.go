// Package insight is the console-facing InsightService handler: it serves every insight lens
// (the four VCS-history lenses from one cached git-log scan, plus the run-outcome volatility
// lens folded in fresh) as the magus.insight.v1alpha1 wire type. It is READ-only and maps
// types.InsightView to the wire at the boundary - the console service owns assembly and
// caching, this owns the wire, and types/ stays free of protobuf.
//
// It is the typed replacement for the hand-marshalled JSON GET /api/v1/insight route, which
// remains mounted for now: that route is documented, so retiring it is its own breaking change.
package insight

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/egladman/magus/internal/service/console"
	insightv1 "github.com/egladman/magus/proto/gen/go/magus/insight/v1alpha1"
	"github.com/egladman/magus/proto/gen/go/magus/insight/v1alpha1/insightv1alpha1connect"
	"github.com/egladman/magus/types"
)

// Source is the narrow consumer contract this handler needs: assemble every insight lens.
// Satisfied by *console.Service; the handler package holds no concrete service.
type Source interface {
	Insight(ctx context.Context) (types.InsightView, error)
}

// Service implements insightv1alpha1connect.InsightServiceHandler over src.
type Service struct {
	src Source
}

// NewService builds the InsightService Connect handler reading from src.
func NewService(src Source) *Service { return &Service{src: src} }

var _ insightv1alpha1connect.InsightServiceHandler = (*Service)(nil)

// GetInsight returns every lens in one message. A daemon with no workspace cannot scan
// anything, which is a transient condition of THIS daemon rather than a bad request or a bug,
// so it maps to CodeUnavailable (the Connect twin of the JSON route's 503) and the client
// keeps whatever it last rendered.
func (s *Service) GetInsight(ctx context.Context, _ *connect.Request[insightv1.GetInsightRequest]) (*connect.Response[insightv1.Insight], error) {
	view, err := s.src.Insight(ctx)
	if err != nil {
		if errors.Is(err, console.ErrNoWorkspace) {
			return nil, connect.NewError(connect.CodeUnavailable, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(insightToProto(view)), nil
}

// insightToProto maps the domain view onto the wire. Every lens is always present; only
// volatility is nilable, and a nil report stays nil so a reader can tell "no run history
// recorded" from "no volatile targets".
func insightToProto(v types.InsightView) *insightv1.Insight {
	return &insightv1.Insight{
		Hotspots: &insightv1.HotspotOutput{
			Definition: v.Hotspots.Definition,
			Commits:    int32(v.Hotspots.Commits),
			Since:      v.Hotspots.Since,
			Nodes:      mapSlice(v.Hotspots.Nodes, nodeToProto),
			Files:      mapSlice(v.Hotspots.Files, fileHotspotToProto),
		},
		Affinity: &insightv1.AffinityOutput{
			Definition: v.Affinity.Definition,
			Commits:    int32(v.Affinity.Commits),
			Since:      v.Affinity.Since,
			Pairs:      mapSlice(v.Affinity.Pairs, coChangeToProto),
		},
		Ownership: &insightv1.OwnershipOutput{
			Definition: v.Ownership.Definition,
			Commits:    int32(v.Ownership.Commits),
			Since:      v.Ownership.Since,
			Projects:   mapSlice(v.Ownership.Projects, ownershipToProto),
		},
		Trend: &insightv1.TrendOutput{
			Definition: v.Trend.Definition,
			Commits:    int32(v.Trend.Commits),
			Since:      v.Trend.Since,
			Projects:   mapSlice(v.Trend.Projects, trendToProto),
		},
		Volatility: volatilityToProto(v.Volatility),
	}
}

func nodeToProto(n types.Node) *insightv1.ProjectNode {
	p := &insightv1.ProjectNode{
		Path:        n.Path,
		Name:        n.Name,
		SpellName:   n.SpellName,
		Children:    n.Children,
		Dir:         n.Dir,
		Exclusive:   n.Exclusive,
		BlastRadius: int32(n.BlastRadius),
		DurationMs:  n.DurationMs,
		Churn:       int32(n.Churn),
		Authors:     int32(n.Authors),
	}
	if n.LastCommit != nil {
		p.LastCommitTime = tsFromTime(*n.LastCommit)
	}
	return p
}

func fileHotspotToProto(f types.FileHotspot) *insightv1.FileHotspot {
	return &insightv1.FileHotspot{
		Path:           f.Path,
		Commits:        int32(f.Commits),
		Complexity:     int32(f.Complexity),
		Score:          int32(f.Score),
		Authors:        int32(f.Authors),
		LastCommitTime: tsFromTime(f.LastCommit),
		Moves:          int32(f.Moves),
	}
}

func coChangeToProto(c types.CoChange) *insightv1.CoChange {
	return &insightv1.CoChange{
		A:      c.A,
		AName:  c.AName,
		B:      c.B,
		BName:  c.BName,
		Count:  int32(c.Count),
		Hidden: c.Hidden,
	}
}

func ownershipToProto(o types.OwnershipEntry) *insightv1.Ownership {
	return &insightv1.Ownership{
		Path:           o.Path,
		Name:           o.Name,
		Commits:        int32(o.Commits),
		Authors:        int32(o.Authors),
		Primary:        o.Primary,
		PrimaryShare:   int32(o.PrimaryShare),
		BusFactor_1:    o.BusFactor1,
		Stale:          o.Stale,
		LastCommitTime: tsFromTime(o.LastCommit),
	}
}

func trendToProto(t types.TrendEntry) *insightv1.Trend {
	return &insightv1.Trend{
		Path:    t.Path,
		Name:    t.Name,
		Recent:  int32(t.Recent),
		Earlier: int32(t.Earlier),
		Delta:   int32(t.Delta),
	}
}

func volatilityToProto(r *types.VolatilityReport) *insightv1.VolatilityReport {
	if r == nil {
		return nil
	}
	return &insightv1.VolatilityReport{
		Threshold: r.Threshold,
		Targets: mapSlice(r.Targets, func(t types.VolatilityTarget) *insightv1.VolatilityTarget {
			return &insightv1.VolatilityTarget{
				Project:       t.Project,
				Target:        t.Target,
				Score:         t.Score,
				Volatile:      t.Volatile,
				Pass:          int32(t.Pass),
				Fail:          int32(t.Fail),
				VolatileCount: int32(t.VolatileCount),
				Samples:       int32(t.Samples),
				LastPassTime:  tsFromTime(t.LastPass),
			}
		}),
	}
}

// mapSlice converts a domain slice to its wire slice, keeping an empty input nil so an absent
// lens row set stays absent on the wire rather than encoding as an empty list.
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

// tsFromTime maps a domain time.Time to the well-known Timestamp per AIP-142, treating a zero
// value as UNSET (nil) rather than encoding the year-0001 sentinel a JSON marshal would.
func tsFromTime(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}
