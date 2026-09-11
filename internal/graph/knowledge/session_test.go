package knowledge

import (
	"maps"
	"slices"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sessionKnown is the node set a loaded transcript is resolved against: one file node
// and the two dir nodes above it, with no node for internal/gone.go, so a test can tell
// a resolved path from a dropped one.
func sessionKnown() map[string]bool {
	return map[string]bool{
		fileID("internal/graph/knowledge/store.go"): true,
		dirID("internal/graph"):                     true,
		dirID("internal/graph/knowledge"):           true,
	}
}

func sessionNode(t *testing.T, s Shard, id string) types.KnowledgeNode {
	t.Helper()
	i := slices.IndexFunc(s.Nodes, func(n types.KnowledgeNode) bool { return n.ID == id })
	require.GreaterOrEqual(t, i, 0, "shard has no node %s", id)
	return s.Nodes[i]
}

func TestAssembleSessionFoldsOntoFileAndRollsUpToDirs(t *testing.T) {
	const path = "internal/graph/knowledge/store.go"
	s := assembleSession([]AgentContact{
		{Session: "s1", Path: path, Read: true, AtMs: 100},
		{Session: "s1", Path: path, Write: true, AtMs: 300},
		{Session: "s2", Path: path, Read: true, AtMs: 200},
		{Session: "s2", Path: path, Write: true, Denied: true, AtMs: 250},
	}, sessionKnown())

	require.Zero(t, s.Dropped)
	// The same tallies land on the file and on every ancestor dir node: agent_sessions
	// counts DISTINCT sessions (two), not events (four).
	want := map[string]string{
		AttrAgentSessions:    "2",
		AttrAgentReads:       "2",
		AttrAgentWrites:      "2",
		AttrAgentDenials:     "1",
		AttrAgentLastTouched: "300",
	}
	file := sessionNode(t, s, fileID(path))
	assert.Equal(t, types.KindFile, file.Kind)
	assert.Equal(t, want, file.Attrs)
	for _, dir := range []string{"internal/graph", "internal/graph/knowledge"} {
		node := sessionNode(t, s, dirID(dir))
		assert.Equal(t, types.KindDir, node.Kind)
		assert.Equal(t, want, node.Attrs, "dir %s", dir)
	}
	assert.Len(t, s.Nodes, 3, "no node beyond the file and its two known ancestors")
	assert.Empty(t, file.Label, "partial node: the real file node owns label and source")
}

func TestAssembleSessionCountsUnresolvedRatherThanMinting(t *testing.T) {
	s := assembleSession([]AgentContact{
		{Session: "s1", Path: "internal/gone.go", Read: true, AtMs: 100},
		{Session: "s1", Path: "", Denied: true, AtMs: 110},
		{Session: "s1", Path: "internal/graph/knowledge/store.go", Read: true, AtMs: 120},
	}, sessionKnown())

	assert.Equal(t, 2, s.Dropped, "a deleted path and a path-less command are both counted")
	for _, n := range s.Nodes {
		assert.NotEqual(t, fileID("internal/gone.go"), n.ID, "an unresolved path must never mint a node")
	}
	assert.Equal(t, "1", sessionNode(t, s, fileID("internal/graph/knowledge/store.go")).Attrs[AttrAgentReads])
}

// A path-less deny is the shell.command case the attribution rule turns away: it must
// reach no node at all, not the directories the session happened to be working in.
func TestAssembleSessionDoesNotAttributePathlessDenials(t *testing.T) {
	s := assembleSession([]AgentContact{
		{Session: "s1", Path: "", Denied: true, AtMs: 100},
		{Session: "s1", Path: "internal/graph/knowledge/store.go", Read: true, AtMs: 200},
	}, sessionKnown())

	assert.Equal(t, 1, s.Dropped)
	for _, n := range s.Nodes {
		assert.NotContains(t, n.Attrs, AttrAgentDenials, "node %s", n.ID)
	}
}

func TestAssembleSessionEmptyInput(t *testing.T) {
	s := assembleSession(nil, sessionKnown())
	assert.Empty(t, s.Nodes)
	assert.Zero(t, s.Dropped)
}

func TestSessionShardIsLocalAndLazy(t *testing.T) {
	assert.True(t, isSessionShard(sessionShardName))
	assert.False(t, isSessionShard(coverageShardName))
	assert.True(t, isMachineLocalShard(sessionShardName), "loaded transcripts must never reach a remote cache")
	assert.True(t, isLazyShard(sessionShardName), "its file and dir nodes are the symbol shards'")
}

// TestSessionAttrsCoversAssembled derives the attr set from behaviour, the way
// TestRuntimeAttrsCoversAssembled does: an attr assembleSession emits but sessionAttrs
// omits would survive stripUnreproducible and land in the committed export.
func TestSessionAttrsCoversAssembled(t *testing.T) {
	s := assembleSession([]AgentContact{
		{Session: "s1", Path: "internal/graph/knowledge/store.go", Read: true, AtMs: 100},
		{Session: "s2", Path: "internal/graph/knowledge/store.go", Write: true, Denied: true, AtMs: 200},
	}, sessionKnown())

	emitted := map[string]bool{}
	for _, n := range s.Nodes {
		for k := range n.Attrs {
			emitted[k] = true
		}
	}
	require.NotEmpty(t, emitted, "guards against a vacuous pass if the fixture stops emitting")
	assert.ElementsMatch(t, sessionAttrs, slices.Collect(maps.Keys(emitted)),
		"sessionAttrs must match what the session shard actually emits")
	for k := range emitted {
		assert.True(t, IsSessionAttr(k), "IsSessionAttr must recognize %s", k)
	}
}

// The overlay must be invisible to the default graph, which is what keeps a session load
// from moving `magus graph export --reproducible` or MAGUS.md's routing table. Asserted
// against AssembleShards rather than the assembler alone, because the property that
// matters is which shard the contacts land in.
func TestSessionContactsStayOutOfTheDefaultGraph(t *testing.T) {
	in := sampleInputs()
	before := mergeAll(AssembleShards(in)).Output()

	in.AgentContacts = []AgentContact{{Session: "s1", Path: "internal/graph/knowledge/store.go", Read: true, AtMs: 100}}
	shards := AssembleShards(in)

	var merged []Shard
	for _, sh := range shards {
		if !isLazyShard(sh.Name) {
			merged = append(merged, sh)
		}
	}
	after := mergeAll(merged).Output()
	assert.Equal(t, before.NodeCount, after.NodeCount)
	assert.Equal(t, before.EdgeCount, after.EdgeCount)
	for _, n := range after.Nodes {
		for k := range n.Attrs {
			assert.False(t, IsSessionAttr(k), "session attr %s reached the default graph on %s", k, n.ID)
		}
	}
}
