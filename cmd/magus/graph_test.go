package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/render"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Which diagnostic this is does not matter; it is an edge endpoint.
var diagID = "diagnostic:" + string(types.ExecDenied)

// runtimeGraph carries both halves of what --static must drop: observed attrs beside a
// static one, and a runtime edge beside a static one.
func runtimeGraph() *knowledge.Graph {
	g := knowledge.NewGraph()
	g.AddNode(types.KnowledgeNode{
		ID: "target:pkg/a:build", Kind: types.KindTarget, Label: "build",
		Attrs: map[string]string{
			knowledge.AttrEngine:        "buzz",
			knowledge.AttrDurationP75Ms: "4200",
			knowledge.AttrLastOutputRef: "out1a2b3c",
			knowledge.AttrLastRunOK:     "true",
		},
	})
	g.AddNode(types.KnowledgeNode{ID: diagID, Kind: types.KindDiagnostic, Label: string(types.ExecDenied)})
	g.AddEdge(types.KnowledgeEdge{
		Source: "target:pkg/a:build", Target: diagID, Relation: types.RelationDocuments,
		Confidence: types.ConfidenceExtracted, Score: 1.0, Provenance: "magusfile.buzz",
	})
	g.AddEdge(types.KnowledgeEdge{
		Source: "target:pkg/a:build", Target: diagID, Relation: types.RelationEmits,
		Confidence: types.ConfidenceExtracted, Score: 1.0, Provenance: knowledge.ProvenanceRuntime,
	})
	return g
}

// TestStripRuntimeAttrs pins what --static guarantees: the committed export carries no
// run history. Static attrs and edges survive, NodeCount holds, EdgeCount tracks.
func TestStripRuntimeAttrs(t *testing.T) {
	out := runtimeGraph().Output()
	require.Equal(t, 2, out.EdgeCount, "fixture starts with a static and a runtime edge")

	stripUnreproducible(&out)

	var build types.KnowledgeNode
	for _, n := range out.Nodes {
		if n.ID == "target:pkg/a:build" {
			build = n
		}
	}
	require.NotEmpty(t, build.ID, "target node survives")
	assert.Equal(t, map[string]string{knowledge.AttrEngine: "buzz"}, build.Attrs,
		"every observed attr is stripped and the static one is kept")

	require.Len(t, out.Links, 1)
	assert.Equal(t, types.RelationDocuments, out.Links[0].Relation, "only the static edge survives")
	assert.Equal(t, 1, out.EdgeCount, "EdgeCount tracks the kept links")
	assert.Equal(t, 2, out.NodeCount, "stripping removes attrs, never nodes")
}

// TestStripRuntimeAttrsLeavesGraphIntact is why stripRuntimeAttrs copies instead of
// deleting in place: Output shares Attrs maps with the live graph, which in the daemon is
// warm and long-lived, so an in-place delete would blind every later explain.
func TestStripRuntimeAttrsLeavesGraphIntact(t *testing.T) {
	g := runtimeGraph()
	out := g.Output()
	stripUnreproducible(&out)

	live := g.Output() // re-read the live graph after the strip
	var build types.KnowledgeNode
	for _, n := range live.Nodes {
		if n.ID == "target:pkg/a:build" {
			build = n
		}
	}
	require.NotEmpty(t, build.ID)
	assert.Equal(t, "4200", build.Attrs[knowledge.AttrDurationP75Ms], "the live graph keeps its observed attrs")
	assert.Equal(t, "out1a2b3c", build.Attrs[knowledge.AttrLastOutputRef])
	assert.Equal(t, 2, live.EdgeCount, "the live graph keeps its runtime edge")
}

// TestStripUnreproducibleDropsGitHistory is the half that runtime stripping never covered.
//
// An observed attr varies by MACHINE; git history varies by COMMIT, which is the sharper
// problem for a checked-in export: committing anything moves the churn, so the artifact
// invalidates itself and the drift gate fires on the very commit that regenerated it. This
// repo's committed graph carried four git attrs on 204 file nodes, churn on 142 dirs, and
// 345 authored edges - all of it a function of history rather than of the tree.
func TestStripUnreproducibleDropsGitHistory(t *testing.T) {
	out := types.KnowledgeGraphOutput{
		Nodes: []types.KnowledgeNode{
			{ID: "file:a.buzz", Kind: types.KindFile, Attrs: map[string]string{
				"language": "buzz", "vcs_last_commit": "cafe", "vcs_last_modified": "2026-08-13",
				"vcs_last_author": "Ada", "vcs_commits": "7",
			}},
			{ID: "dir:pkg", Kind: types.KindDir, Attrs: map[string]string{"dir_files": "3", "dir_commits": "91", "dir_languages": "buzz"}},
			{ID: "doc:x.md", Kind: types.KindDoc, Attrs: map[string]string{"role": "readme", "staleness": "petrified", "outrun_days": "400"}},
			{ID: "author:Ada", Kind: types.KindAuthor},
		},
		Links: []types.KnowledgeEdge{
			{Source: "author:Ada", Target: "file:a.buzz", Relation: types.RelationAuthored},
			{Source: "doc:x.md", Target: "file:a.buzz", Relation: types.RelationDocuments},
		},
	}
	stripUnreproducible(&out)

	byID := map[string]types.KnowledgeNode{}
	for _, n := range out.Nodes {
		byID[n.ID] = n
	}
	assert.Equal(t, map[string]string{"language": "buzz"}, byID["file:a.buzz"].Attrs, "every vcs_* attr goes")
	assert.Equal(t, map[string]string{"dir_files": "3", "dir_languages": "buzz"}, byID["dir:pkg"].Attrs,
		"churn goes, but the file count and language set are functions of the tree and stay")
	assert.Equal(t, map[string]string{"role": "readme"}, byID["doc:x.md"].Attrs, "staleness is derived from two commit dates")

	// An author node exists only because history does, so it goes whole rather than
	// surviving as a bare id with everything stripped off it.
	_, ok := byID["author:Ada"]
	assert.False(t, ok)
	require.Len(t, out.Links, 1, "the authored edge goes with it")
	assert.Equal(t, types.RelationDocuments, out.Links[0].Relation)
	assert.Equal(t, len(out.Nodes), out.NodeCount)
	assert.Equal(t, len(out.Links), out.EdgeCount)
}

// ---- graph export --open ----

// TestOpenViaBrowserEnv exercises the $BROWSER convention parsing: unset falls through
// (error), a bogus command errors, and a real command (with or without %s) launches.
func TestOpenViaBrowserEnv(t *testing.T) {
	t.Setenv("BROWSER", "")
	assert.Error(t, openViaBrowserEnv("http://x"), "unset BROWSER falls through to the platform opener")

	t.Setenv("BROWSER", "magus-no-such-browser-xyz")
	assert.Error(t, openViaBrowserEnv("http://x"), "a command that cannot start errors")

	// `true` exists on the PATH of every supported unix; it launches (Start succeeds),
	// which is all openViaBrowserEnv needs to consider the browser opened.
	t.Setenv("BROWSER", "true")
	assert.NoError(t, openViaBrowserEnv("http://x"))

	t.Setenv("BROWSER", "true %s")
	assert.NoError(t, openViaBrowserEnv("http://x"), "the URL is substituted for %s")

	// The first launchable entry wins even if an earlier one is missing.
	t.Setenv("BROWSER", "magus-no-such-browser-xyz:true")
	assert.NoError(t, openViaBrowserEnv("http://x"))
}

// TestEncodeFragmentDeterminism confirms that render.EncodeFragmentRaw produces
// byte-for-byte identical output for the same input across two calls. This relies
// on gzip.NewWriter leaving the header ModTime at its zero value by default, so the
// compressed stream is deterministic - a necessary property for stable #data= URL
// fragments in MAGUS.md. The test exercises the shared encoder that both the render
// package (per-project MAGUS.md deep links) and cmd/magus (graph open) use, proving
// browser wire-format parity is preserved when a single implementation is used.
func TestEncodeFragmentDeterminism(t *testing.T) {
	payload := []byte(`{"projects":[{"path":"pkg/foo","engine":"buzz","nodes":[{"name":"build","dependencies":["fmt"]},{"name":"fmt"}]}]}`)

	first, err := render.EncodeFragmentRaw(payload)
	require.NoError(t, err, "first EncodeFragmentRaw")

	second, err := render.EncodeFragmentRaw(payload)
	require.NoError(t, err, "second EncodeFragmentRaw")

	assert.Equal(t, first, second, "EncodeFragmentRaw must be deterministic:\n  first:  %s\n  second: %s", first, second)
}

// TestProbeLiveBridge covers the security-relevant branches of the real HTTP
// probe used by both graphOpenLive (blocker: never emit a token for an
// unreachable bridge) and liveBridgeReachable (the zero-arg auto-switch
// gate): a guarded route (401/403) proves the bridge is up; anything else,
// including connection refused, must be treated as down.
func TestProbeLiveBridge(t *testing.T) {
	t.Run("401 is reachable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/api/v1/graph", r.URL.Path)
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()
		require.NoError(t, probeLiveBridge(context.Background(), strings.TrimPrefix(srv.URL, "http://")))
	})

	t.Run("403 is reachable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer srv.Close()
		require.NoError(t, probeLiveBridge(context.Background(), strings.TrimPrefix(srv.URL, "http://")))
	})

	t.Run("200 is unexpected and treated as unreachable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()
		err := probeLiveBridge(context.Background(), strings.TrimPrefix(srv.URL, "http://"))
		require.Error(t, err)
	})

	t.Run("connection refused is unreachable", func(t *testing.T) {
		// Bind and immediately close to get a loopback port nothing listens on.
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		addr := ln.Addr().String()
		require.NoError(t, ln.Close())

		err = probeLiveBridge(context.Background(), addr)
		require.Error(t, err)
	})
}

// TestLiveBridgeReachable exercises the gate that decides whether the zero-arg
// `magus graph export --open` default may switch to live mode. It delegates to
// probeLiveBridge against the configured MCP address.
func TestLiveBridgeReachable(t *testing.T) {
	saved := globalCfg
	t.Cleanup(func() { globalCfg = saved })

	t.Run("reachable bridge", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()
		globalCfg.MCP.Address = strings.TrimPrefix(srv.URL, "http://")
		require.True(t, liveBridgeReachable(context.Background()))
	})

	t.Run("unreachable bridge", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		addr := ln.Addr().String()
		require.NoError(t, ln.Close())
		globalCfg.MCP.Address = addr
		require.False(t, liveBridgeReachable(context.Background()))
	})
}
