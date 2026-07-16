package console

import (
	"context"
	"testing"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// minimalGraph builds a small in-memory graph so Graph paths have something to return.
func minimalGraph() *knowledge.Graph {
	g := knowledge.NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "project/foo", Kind: "file", Label: "foo"})
	g.AddNode(types.KnowledgeNode{ID: "project/bar", Kind: "file", Label: "bar"})
	g.AddEdge(types.KnowledgeEdge{Source: "project/foo", Target: "project/bar", Relation: "imports"})
	return g
}

func minimalTargetGraph() types.TargetGraphOutput {
	return types.TargetGraphOutput{
		Definition: "magusfile.buzz",
		Projects: []types.TargetGraphProject{
			{Path: "project/foo", DependsOn: []string{"project/bar"}},
			{Path: "project/bar"},
		},
	}
}

// testService wires a Service with the graph/target seams so no real workspace is needed.
func testService(opts ...Option) *Service {
	g := minimalGraph()
	tg := minimalTargetGraph()
	base := append([]Option{
		WithKnowledgeGraphFn(func(context.Context, bool) (*knowledge.Graph, error) { return g, nil }),
		WithDescribeGraphFn(func() types.TargetGraphOutput { return tg }),
	}, opts...)
	return NewService(nil, config.Config{}, types.StatusBase{}, "1.2.3", base...)
}

func TestServiceGraphFull(t *testing.T) {
	out, err := testService().Graph(context.Background(), "full", "")
	require.NoError(t, err)
	assert.Equal(t, 2, out.NodeCount)
	assert.Len(t, out.Nodes, 2)
	assert.Len(t, out.Links, 1)
}

func TestServiceGraphSelect(t *testing.T) {
	out, err := testService().Graph(context.Background(), "select", "foo")
	require.NoError(t, err)
	assert.NotEmpty(t, out.Nodes)
}

func TestServiceGraphSkeleton(t *testing.T) {
	out, err := testService().Graph(context.Background(), "skeleton", "")
	require.NoError(t, err)
	require.Len(t, out.Nodes, 2)
	assert.Equal(t, "project", out.Nodes[0].Kind)
	require.Len(t, out.Links, 1)
	assert.Equal(t, "depends_on", out.Links[0].Relation)
	assert.True(t, out.Directed)
	assert.Equal(t, types.KnowledgeSchemaVersion, out.SchemaVersion)
}

func TestServiceTargetGraph(t *testing.T) {
	out, err := testService().TargetGraph(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "magusfile.buzz", out.Definition)
	assert.Len(t, out.Projects, 2)
}

// TestServiceStatusReportSeam checks the injected report is returned verbatim.
func TestServiceStatusReportSeam(t *testing.T) {
	want := types.StatusReport{Pool: &types.StatusOutput{Mode: "daemon", Capacity: 4, Running: 1}}
	svc := NewService(nil, config.Config{}, types.StatusBase{}, "1.2.3",
		WithStatusReportFn(func(context.Context) types.StatusReport { return want }))
	assert.Equal(t, want, svc.StatusReport(context.Background()))
}

// TestServiceStatusReportPoolError checks a failed daemon query surfaces as PoolError while
// the static base fields still ride through.
func TestServiceStatusReportPoolError(t *testing.T) {
	base := types.StatusBase{Cache: types.CacheStatus{SizeMB: 42}}
	svc := NewService(nil, config.Config{}, base, "1.2.3", WithDaemonSocket("127.0.0.1:1"))
	got := svc.StatusReport(context.Background())
	assert.Nil(t, got.Pool)
	assert.NotEmpty(t, got.PoolError)
	assert.Equal(t, 42, got.Cache.SizeMB)
}

// TestServiceVersion checks the running version stamped at construction is returned verbatim.
func TestServiceVersion(t *testing.T) {
	svc := NewService(nil, config.Config{}, types.StatusBase{}, "9.9.9")
	assert.Equal(t, "9.9.9", svc.Version())
}

// TestStatusOutputFromReply pins the proc.StatusReply -> types.StatusOutput conversion:
// scalar fields copy across, calls and workspaces map element-wise, and Affected is left
// unset (deferred).
func TestStatusOutputFromReply(t *testing.T) {
	assert.Nil(t, statusOutputFromReply(nil), "nil reply -> nil output")

	loaded := time.UnixMilli(1_700_000_000_000)
	access := time.UnixMilli(1_700_000_100_000)
	started := time.UnixMilli(1_700_000_050_000)
	reply := &proc.StatusReply{
		ParentPID: 4242, DaemonVersion: "d1", Mode: "daemon", Capacity: 8, Running: 3, Queued: 1,
		Calls: []proc.Call{
			{Args: []string{"run", "build"}, Workspace: "/ws", StartedAt: started, SubOp: "spawn", Inv: "inv1"},
		},
		Workspaces: []proc.Workspace{
			{Root: "/ws", LoadedAt: loaded, LastAccess: access, CacheHit: 5, CacheMiss: 2, CacheError: 1, CacheBytes: 1024},
		},
	}

	got := statusOutputFromReply(reply)
	require.NotNil(t, got)
	assert.Equal(t, &types.StatusOutput{
		ParentPID:     4242,
		DaemonVersion: "d1",
		Mode:          "daemon",
		Capacity:      8,
		Running:       3,
		Queued:        1,
		RunningTargets: []types.StatusRunningTarget{
			{Args: []string{"run", "build"}, Workspace: "/ws", StartedAt: started, Step: "spawn", Inv: "inv1"},
		},
		Workspaces: []types.StatusWorkspace{
			{Root: "/ws", LoadedAt: loaded, LastAccess: access, CacheHit: 5, CacheMiss: 2, CacheError: 1, CacheBytes: 1024},
		},
	}, got)
	assert.Empty(t, got.Affected, "Affected is deferred and left unset")
}

// TestResolveStatusAddr checks the address precedence: config address first, then the injected
// daemon socket seam.
func TestResolveStatusAddr(t *testing.T) {
	// Config address wins outright.
	svc := NewService(nil, config.Config{Daemon: config.Daemon{Address: "unix:///cfg.sock"}},
		types.StatusBase{}, "1.0.0", WithDaemonSocket("unix:///seam.sock"))
	addr, err := svc.resolveStatusAddr(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "unix:///cfg.sock", addr)

	// No config address: fall back to the injected daemon socket.
	svc = NewService(nil, config.Config{}, types.StatusBase{}, "1.0.0", WithDaemonSocket("unix:///seam.sock"))
	addr, err = svc.resolveStatusAddr(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "unix:///seam.sock", addr)
}

func TestIsGraphRelevant(t *testing.T) {
	assert.True(t, isGraphRelevant([]string{"a/b.buzz"}))
	assert.True(t, isGraphRelevant([]string{"docs/x.md"}))
	assert.True(t, isGraphRelevant([]string{"pkg/magus.yaml"}))
	assert.False(t, isGraphRelevant([]string{"main.go", "README.txt"}))
}
