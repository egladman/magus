package evidence

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/job"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// TestMain isolates the environment the workspaces these tests open read.
func TestMain(m *testing.M) { testkit.Main(m) }

// deltaGraph defines each symbol name at file:line, with nothing referencing any of them.
func deltaGraph(defs map[string]string) *knowledge.Graph {
	var nodes []types.KnowledgeNode
	var edges []types.KnowledgeEdge
	for name, at := range defs {
		id := fmt.Sprintf("symbol:go a `a`/%s().", name)
		file := at[:len(at)-2]
		nodes = append(nodes, types.KnowledgeNode{ID: id, Kind: types.KindSymbol, Label: name, Source: at})
		edges = append(edges, types.KnowledgeEdge{Source: "file:" + file, Target: id, Relation: types.RelationDefines,
			Confidence: types.ConfidenceExtracted, Score: 1})
	}
	g := knowledge.NewGraph()
	g.Merge(nodes, edges)
	return g
}

// describe reads the graph under a stale index whose changed files all miss the row's write
// paths, and keeps today's refusal when one falls inside them or the index names none.
func TestJobGraphReadsUnderAStaleIndexOnlyWhenItsChangesMissTheWritePaths(t *testing.T) {
	t.Parallel()

	built := knowledge.NewGraph()
	listed := func(files ...string) types.KnowledgeStaleIndex {
		return types.KnowledgeStaleIndex{Project: ".", Language: "go", Op: "scip", Changed: files, ChangedCount: len(files)}
	}
	for name, tc := range map[string]struct {
		paths  []string
		stale  types.KnowledgeStaleIndex
		reads  bool
		domain bool
	}{
		"changes elsewhere":            {paths: []string{"internal/job/**"}, stale: listed("run.go", "symbol.go"), reads: true},
		"a change inside a glob":       {paths: []string{"internal/job/**"}, stale: listed("internal/job/plan.go")},
		"a change to a claimed file":   {paths: []string{"run.go#Run"}, stale: listed("run.go")},
		"no files named":               {paths: []string{"internal/job/**"}, stale: types.KnowledgeStaleIndex{Project: "."}},
		"a list short of its count":    {paths: []string{"internal/job/**"}, stale: types.KnowledgeStaleIndex{Project: ".", Changed: []string{"run.go"}, ChangedCount: 2}},
		"no write paths to clear":      {stale: listed("run.go")},
		"the domain graph, elsewhere":  {paths: []string{"docs/**"}, stale: listed("run.go"), reads: true, domain: true},
		"the domain graph, inside one": {paths: []string{"docs/**"}, stale: listed("docs/x.md"), domain: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			loads := 0
			load := func(context.Context) (*knowledge.Graph, error) { loads++; return built, nil }
			graph := &Graph{
				stale:  func(context.Context) []types.KnowledgeStaleIndex { return []types.KnowledgeStaleIndex{tc.stale} },
				build:  load,
				domain: load,
			}
			var got *knowledge.Graph
			if tc.domain {
				got = graph.LoadDomain(t.Context(), tc.paths)
			} else {
				got = graph.LoadScoped(tc.paths)(t.Context())
			}
			assert.Equal(t, tc.reads, got != nil)
			assert.Equal(t, tc.reads, loads == 1)
			if tc.reads {
				assert.Empty(t, graph.StaleIndexes(t.Context()), "an index that kept nothing from the read names no ungraded goal")
			} else {
				assert.Equal(t, []types.KnowledgeStaleIndex{tc.stale}, graph.StaleIndexes(t.Context()))
			}
		})
	}
}

// The sibling tests a fork adds to a row are recorded as write paths and as soft paths, so
// they overlap no other row until their holder writes one.
func TestExtendedFootprintRecordsSiblingTestsAsSoftPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, rel := range []string{"cli/registry.go", "cli/registry_test.go"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, "cli"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, rel), []byte(rel), 0o644))
	}
	g := knowledge.NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "file:cli/registry.go", Kind: types.KindFile, Label: "cli/registry.go"})
	g.AddNode(types.KnowledgeNode{ID: "symbol:cli.Registry", Kind: types.KindSymbol, Label: "Registry", Source: "cli/registry.go:3"})
	g.AddEdge(types.KnowledgeEdge{Source: "file:cli/registry.go", Target: "symbol:cli.Registry", Relation: types.RelationDefines})
	g.AddEdge(types.KnowledgeEdge{Source: "file:cli/registry_test.go", Target: "symbol:cli.Registry", Relation: types.RelationReferences, Provenance: "scip count=2"})
	graph := &Graph{
		rebuild: true,
		build:   func(context.Context) (*knowledge.Graph, error) { return g, nil },
	}
	store := job.NewStore(job.Location{Root: root, StateBase: t.TempDir(), CacheDir: t.TempDir(), Actor: &job.Actor{}})
	ws := job.Workspace{Projects: []*types.Project{{Path: ".", Sources: types.MustParseGlobs("**/*.go")}}}

	rec, notes := ExtendedFootprint(t.Context(), store, graph, root,
		jobtypes.Declaration{ID: "w", Paths: jobtypes.Paths{Write: []string{"cli/registry.go"}}}, ws)

	assert.Equal(t, jobtypes.Declaration{
		ID: "w",
		Paths: jobtypes.Paths{
			Write: []string{"cli/registry.go", "cli/registry_test.go"},
			Soft:  []string{"cli/registry_test.go"},
		},
	}, rec)
	assert.Len(t, notes, 1)
}

// A lazy graph builds at most once per command: a load, a failed one and a skipped one
// each answer every later get alike without asking again.
func TestLazyGraphLoadsOnce(t *testing.T) {
	t.Parallel()

	built := knowledge.NewGraph()
	for name, tc := range map[string]struct {
		skip  bool
		err   error
		want  *knowledge.Graph
		loads int
	}{
		"a load":         {want: built, loads: 1},
		"a failed load":  {err: errors.New("no index"), loads: 1},
		"a skipped load": {skip: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var lazy lazyGraph
			var skips, loads int
			skip := func(context.Context) bool { skips++; return tc.skip }
			build := func(context.Context) (*knowledge.Graph, error) {
				loads++
				if tc.err != nil {
					return nil, tc.err
				}
				return built, nil
			}
			for range 3 {
				assert.Same(t, tc.want, lazy.get(t.Context(), skip, build))
			}
			assert.Equal(t, []int{1, tc.loads}, []int{skips, loads})
		})
	}
}

type announcedLoadSuite struct {
	suite.Suite
	said     bytes.Buffer
	previous *slog.Logger
}

func TestAnnouncedLoadSuite(t *testing.T) { suite.Run(t, new(announcedLoadSuite)) }

func (s *announcedLoadSuite) SetupTest() {
	s.previous = slog.Default()
	s.said.Reset()
	slog.SetDefault(slog.New(slog.NewTextHandler(&s.said, nil)))
}

func (s *announcedLoadSuite) TearDownTest() { slog.SetDefault(s.previous) }

func (s *announcedLoadSuite) TestSaysItIsLoadingBeforeTheLoadRuns() {
	var before string
	load := announcedLoad("the symbol graph", func(context.Context) (*knowledge.Graph, error) {
		before = s.said.String()
		return knowledge.NewGraph(), nil
	})
	_, err := load(s.T().Context())
	s.Require().NoError(err)
	s.Contains(before, "loading the symbol graph")
	s.Contains(before, "can take minutes")
	s.Contains(s.said.String(), "loaded the symbol graph in ")
}

func (s *announcedLoadSuite) TestSaysAFailureTheLazyGraphWouldSwallow() {
	load := announcedLoad("the symbol graph", func(context.Context) (*knowledge.Graph, error) {
		return nil, errors.New("index is corrupt")
	})
	_, err := load(s.T().Context())
	s.Require().Error(err)
	s.Contains(s.said.String(), "could not load the symbol graph after ")
	s.Contains(s.said.String(), "index is corrupt")
}

// ForkGraphSuite pins that a fork merges the symbol shards only for a row whose footprint
// its referrers could extend.
type ForkGraphSuite struct {
	suite.Suite
	root   string
	builds int
}

func TestForkGraphSuite(t *testing.T) {
	suite.Run(t, new(ForkGraphSuite))
}

func (s *ForkGraphSuite) SetupTest() {
	testkit.Isolate(s.T())
	s.root = testkit.NewRepository(s.T(), map[string]string{
		"magus.yaml": "{}\n", "magusfile.buzz": "", "go.mod": "module x\n\ngo 1.22\n",
		"run.go": "package run\n", "run_test.go": "package run\n", "docs/guide.md": "# guide\n",
	}).Root()
	s.builds = 0
}

func (s *ForkGraphSuite) footprint(paths ...string) {
	graph := &Graph{rebuild: true, build: func(context.Context) (*knowledge.Graph, error) {
		s.builds++
		return knowledge.NewGraph(), nil
	}}
	store := job.NewStore(job.Location{StateBase: s.T().TempDir(), Root: s.root})
	ws := job.Workspace{Projects: []*types.Project{{Name: ".", Path: "."}}}
	ExtendedFootprint(s.T().Context(), store, graph, s.root, jobtypes.Declaration{ID: "w", Paths: jobtypes.Paths{Write: paths}}, ws)
}

func (s *ForkGraphSuite) TestARowWritingDocsLoadsNoGraph() {
	s.footprint("docs/guide.md", "docs/new.md")
	s.Zero(s.builds)
}

func (s *ForkGraphSuite) TestARowWritingOnlyTestsLoadsNoGraph() {
	s.footprint("run_test.go")
	s.Zero(s.builds)
}

func (s *ForkGraphSuite) TestARowWritingSourceBesideATestLoadsTheGraph() {
	s.footprint("run.go")
	s.Equal(1, s.builds)
}

// A checkout the process did not open at startup answers every read from its own
// workspace and leaves the one the process holds alone.
func TestGraphOpensAnotherCheckoutInItsOwnScope(t *testing.T) {
	other := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(other, "magusfile.buzz"), nil, 0o644))

	graph := newTree(other, true, Env{FindStale: func(context.Context, *magus.Magus) []types.KnowledgeStaleIndex { return nil }})

	require.NotPanics(t, func() { graph.StaleIndexes(t.Context()) })
	require.NotPanics(t, func() { graph.LoadScoped(nil)(t.Context()) })
	require.NotPanics(t, func() { graph.LoadDomain(t.Context(), nil) })
}
