package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

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
			graph := &jobGraph{
				stale:  func(context.Context) []types.KnowledgeStaleIndex { return []types.KnowledgeStaleIndex{tc.stale} },
				build:  load,
				domain: load,
			}
			var got *knowledge.Graph
			if tc.domain {
				got = graph.loadDomain(t.Context(), tc.paths)
			} else {
				got = graph.loadScoped(tc.paths)(t.Context())
			}
			assert.Equal(t, tc.reads, got != nil)
			assert.Equal(t, tc.reads, loads == 1)
			if tc.reads {
				assert.Empty(t, graph.staleIndexes(t.Context()), "an index that kept nothing from the read names no ungraded goal")
			} else {
				assert.Equal(t, []types.KnowledgeStaleIndex{tc.stale}, graph.staleIndexes(t.Context()))
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
	graph := &jobGraph{
		rebuild: true,
		build:   func(context.Context) (*knowledge.Graph, error) { return g, nil },
	}
	store := job.NewStore(job.Location{Root: root, StateBase: t.TempDir(), CacheDir: t.TempDir(), Actor: &job.Actor{}})
	ws := job.Workspace{Projects: []*types.Project{{Path: ".", Sources: types.MustParseGlobs("**/*.go")}}}

	rec, notes := extendedFootprint(t.Context(), store, graph, root,
		types.Declaration{ID: "w", WritePaths: []string{"cli/registry.go"}}, ws)

	assert.Equal(t, types.Declaration{
		ID:         "w",
		WritePaths: []string{"cli/registry.go", "cli/registry_test.go"},
		SoftPaths:  []string{"cli/registry_test.go"},
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
