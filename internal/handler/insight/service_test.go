package insight

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/egladman/magus/internal/service/console"
	insightv1 "github.com/egladman/magus/proto/gen/go/magus/insight/v1"
	"github.com/egladman/magus/types"
)

// fakeSource is a Source returning a canned view or a fixed error.
type fakeSource struct {
	view types.InsightView
	err  error
}

func (f fakeSource) Insight(context.Context) (types.InsightView, error) { return f.view, f.err }

func get(t *testing.T, src Source) (*insightv1.GetInsightResponse, error) {
	t.Helper()
	resp, err := NewService(src).GetInsight(context.Background(), connect.NewRequest(&insightv1.GetInsightRequest{}))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func TestGetInsight_MapsEveryLens(t *testing.T) {
	commit := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	lastPass := time.Date(2026, 7, 2, 9, 30, 0, 0, time.UTC)
	src := fakeSource{view: types.InsightView{
		Hotspots: types.HotspotOutput{
			Definition: types.HotspotDefinition,
			Commits:    42,
			Since:      "3 months ago",
			Nodes: []types.Node{{
				Path:        "internal/cache",
				Name:        "cache",
				SpellName:   "go",
				Children:    []string{"types"},
				Dir:         "internal/cache",
				Exclusive:   true,
				BlastRadius: 7,
				DurationMs:  1200,
				Churn:       9,
				Authors:     3,
				LastCommit:  &commit,
			}},
			Files: []types.FileHotspot{{
				Path:       "internal/cache/cache.go",
				Commits:    9,
				Complexity: 40,
				Score:      360,
				Authors:    3,
				LastCommit: commit,
			}},
		},
		Affinity: types.AffinityOutput{
			Definition: types.AffinityDefinition,
			Commits:    42,
			Pairs:      []types.CoChange{{A: "a", AName: "A", B: "b", BName: "B", Count: 5, Hidden: true}},
		},
		Ownership: types.OwnershipOutput{
			Definition: types.OwnershipDefinition,
			Commits:    42,
			Projects: []types.Ownership{{
				Path:         "internal/cache",
				Name:         "cache",
				Commits:      9,
				Authors:      1,
				Primary:      "eli",
				PrimaryShare: 100,
				BusFactor1:   true,
				Stale:        true,
				LastCommit:   commit,
			}},
		},
		Trend: types.TrendOutput{
			Definition: types.TrendDefinition,
			Commits:    42,
			Projects:   []types.Trend{{Path: "internal/cache", Name: "cache", Recent: 7, Earlier: 2, Delta: 5}},
		},
		Volatility: &types.VolatilityReport{
			Threshold: 0.05,
			Targets: []types.VolatilityTarget{{
				Project:       "p",
				Target:        "test",
				Score:         0.4,
				Volatile:      true,
				Pass:          8,
				Fail:          2,
				VolatileCount: 1,
				Samples:       10,
				LastPass:      lastPass,
			}},
		},
	}}

	want := &insightv1.GetInsightResponse{Insight: &insightv1.Insight{
		Hotspots: &insightv1.HotspotOutput{
			Definition: types.HotspotDefinition,
			Commits:    42,
			Since:      "3 months ago",
			Nodes: []*insightv1.ProjectNode{{
				Path:        "internal/cache",
				Name:        "cache",
				SpellName:   "go",
				Children:    []string{"types"},
				Dir:         "internal/cache",
				Exclusive:   true,
				BlastRadius: 7,
				DurationMs:  1200,
				Churn:       9,
				Authors:     3,
				LastCommit:  timestamppb.New(commit),
			}},
			Files: []*insightv1.FileHotspot{{
				Path:       "internal/cache/cache.go",
				Commits:    9,
				Complexity: 40,
				Score:      360,
				Authors:    3,
				LastCommit: timestamppb.New(commit),
			}},
		},
		Affinity: &insightv1.AffinityOutput{
			Definition: types.AffinityDefinition,
			Commits:    42,
			Pairs:      []*insightv1.CoChange{{A: "a", AName: "A", B: "b", BName: "B", Count: 5, Hidden: true}},
		},
		Ownership: &insightv1.OwnershipOutput{
			Definition: types.OwnershipDefinition,
			Commits:    42,
			Projects: []*insightv1.Ownership{{
				Path:         "internal/cache",
				Name:         "cache",
				Commits:      9,
				Authors:      1,
				Primary:      "eli",
				PrimaryShare: 100,
				BusFactor_1:  true,
				Stale:        true,
				LastCommit:   timestamppb.New(commit),
			}},
		},
		Trend: &insightv1.TrendOutput{
			Definition: types.TrendDefinition,
			Commits:    42,
			Projects:   []*insightv1.Trend{{Path: "internal/cache", Name: "cache", Recent: 7, Earlier: 2, Delta: 5}},
		},
		Volatility: &insightv1.VolatilityReport{
			Threshold: 0.05,
			Targets: []*insightv1.VolatilityTarget{{
				Project:       "p",
				Target:        "test",
				Score:         0.4,
				Volatile:      true,
				Pass:          8,
				Fail:          2,
				VolatileCount: 1,
				Samples:       10,
				LastPass:      timestamppb.New(lastPass),
			}},
		},
	}}

	got, err := get(t, src)
	require.NoError(t, err)
	require.True(t, proto.Equal(want, got), "wire mismatch:\ngot  %v\nwant %v", got, want)
}

// A view with no volatility report and no rows at all: the lens envelopes are still present
// (a reader always has four git lenses to render), volatility is ABSENT rather than an empty
// report, and no empty row list is encoded.
func TestGetInsight_NilVolatility(t *testing.T) {
	got, err := get(t, fakeSource{view: types.InsightView{Hotspots: types.HotspotOutput{Commits: 3}}})
	require.NoError(t, err)
	want := &insightv1.GetInsightResponse{Insight: &insightv1.Insight{
		Hotspots:  &insightv1.HotspotOutput{Commits: 3},
		Affinity:  &insightv1.AffinityOutput{},
		Ownership: &insightv1.OwnershipOutput{},
		Trend:     &insightv1.TrendOutput{},
	}}
	require.True(t, proto.Equal(want, got), "wire mismatch:\ngot  %v\nwant %v", got, want)
	require.Nil(t, got.GetInsight().GetVolatility(), "a nil report must stay absent on the wire")
}

// A zero time.Time is UNSET on the wire, not the year-0001 sentinel the JSON route emitted.
func TestGetInsight_ZeroTimeIsUnset(t *testing.T) {
	got, err := get(t, fakeSource{view: types.InsightView{
		// Two node shapes reach UNSET differently: no pointer at all, and a pointer to a zero time.
		Hotspots: types.HotspotOutput{
			Files: []types.FileHotspot{{Path: "a.go"}},
			Nodes: []types.Node{{Path: "p"}, {Path: "q", LastCommit: &time.Time{}}},
		},
		Volatility: &types.VolatilityReport{
			Targets: []types.VolatilityTarget{{Project: "p", Target: "test"}},
		},
	}})
	require.NoError(t, err)
	hot := got.GetInsight().GetHotspots()
	require.Nil(t, hot.GetNodes()[0].GetLastCommit())
	require.Nil(t, hot.GetNodes()[1].GetLastCommit())
	require.Nil(t, hot.GetFiles()[0].GetLastCommit())
	require.Nil(t, got.GetInsight().GetVolatility().GetTargets()[0].GetLastPass())
}

func TestGetInsight_NoWorkspaceIsUnavailable(t *testing.T) {
	_, err := get(t, fakeSource{err: console.ErrNoWorkspace})
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
}

func TestGetInsight_ScanErrorIsInternal(t *testing.T) {
	_, err := get(t, fakeSource{err: errors.New("scan boom")})
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}
