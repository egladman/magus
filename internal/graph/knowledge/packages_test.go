package knowledge

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func packageNode(t *testing.T, s Shard, id string) types.KnowledgeNode {
	t.Helper()
	for _, n := range s.Nodes {
		if n.ID == id {
			return n
		}
	}
	require.FailNowf(t, "node not found", "%s not among %d nodes", id, len(s.Nodes))
	return types.KnowledgeNode{}
}

// TestAssemblePackages_SharesANodeBetweenProjects is the reason this shard is a
// singleton rather than one per project: two modules requiring the same dependency
// describe ONE package, reached by a depends_on edge from each. Per-project shards
// would make that node's existence depend on which shards happened to be loaded.
func TestAssemblePackages_SharesANodeBetweenProjects(t *testing.T) {
	t.Parallel()
	s := assemblePackages(map[string][]types.KnowledgePackage{
		".":               {{Manager: "gomod", Name: "connectrpc.com/connect", Version: "v1.20.0"}},
		"libs/gopherbuzz": {{Manager: "gomod", Name: "connectrpc.com/connect", Version: "v1.20.0"}},
	})

	assert.Len(t, s.Nodes, 1, "one dependency, one node, however many projects require it")
	assert.Len(t, s.Edges, 2, "one depends_on edge per requiring project")

	n := packageNode(t, s, "package:gomod connectrpc.com/connect")
	assert.Equal(t, types.KindPackage, n.Kind)
	assert.Equal(t, "v1.20.0", n.Attrs[AttrPackageVersion])
	assert.NotContains(t, n.Attrs, attrPackageVersionConflict, "agreeing pins are not a conflict")
}

// TestAssemblePackages_ManagerSeparatesNamespaces pins the collision packageID exists to
// prevent. The npm package and the Go module are unrelated things that share a name, and
// folding them onto one node would report one ecosystem's version for the other's
// dependency - parseMoniker keys on the manager for exactly this reason.
func TestAssemblePackages_ManagerSeparatesNamespaces(t *testing.T) {
	t.Parallel()
	s := assemblePackages(map[string][]types.KnowledgePackage{
		".": {
			{Manager: "gomod", Name: "yaml", Version: "v1.0.0"},
			{Manager: "npm", Name: "yaml", Version: "2.4.1"},
		},
	})
	assert.Len(t, s.Nodes, 2, "same name, different managers, different packages")
	assert.Equal(t, "v1.0.0", packageNode(t, s, "package:gomod yaml").Attrs[AttrPackageVersion])
	assert.Equal(t, "2.4.1", packageNode(t, s, "package:npm yaml").Attrs[AttrPackageVersion])
}

// TestAssemblePackages_DisagreeingPinsAreFlagged covers the cost of excluding the version
// from the node ID: two projects can pin one package differently, and the version attr
// holds only one. Reporting the lowest and naming the full set beats letting map order
// decide which project's pin the workspace appears to be on.
func TestAssemblePackages_DisagreeingPinsAreFlagged(t *testing.T) {
	t.Parallel()
	s := assemblePackages(map[string][]types.KnowledgePackage{
		"a": {{Manager: "gomod", Name: "example.com/x", Version: "v1.2.0"}},
		"b": {{Manager: "gomod", Name: "example.com/x", Version: "v1.5.0"}},
	})
	n := packageNode(t, s, "package:gomod example.com/x")
	assert.Equal(t, "v1.2.0", n.Attrs[AttrPackageVersion], "the lowest pin, chosen deterministically")
	assert.Equal(t, "v1.2.0,v1.5.0", n.Attrs[attrPackageVersionConflict], "every version involved")
}

// TestAssemblePackages_DirectAnywhereIsDirect pins the fold direction on the indirect
// attr, which is easy to write backwards. The attr answers "is this ours to bump", so one
// project choosing a dependency deliberately settles it even when every other project
// merely inherited the same package transitively.
func TestAssemblePackages_DirectAnywhereIsDirect(t *testing.T) {
	t.Parallel()
	s := assemblePackages(map[string][]types.KnowledgePackage{
		"a": {{Manager: "gomod", Name: "example.com/x", Version: "v1.0.0", Indirect: true}},
		"b": {{Manager: "gomod", Name: "example.com/x", Version: "v1.0.0"}},
		"c": {{Manager: "gomod", Name: "example.com/y", Version: "v1.0.0", Indirect: true}},
	})
	assert.NotContains(t, packageNode(t, s, "package:gomod example.com/x").Attrs, attrPackageIndirect,
		"direct to one project is direct")
	assert.Equal(t, "true", packageNode(t, s, "package:gomod example.com/y").Attrs[attrPackageIndirect],
		"indirect everywhere stays indirect")
}

// TestAssemblePackages_IsDeterministic pins the property the shard's remote-shareability
// rests on: the input is a map, and iterating it directly would reorder nodes and edges
// between runs, changing the shard fingerprint without any manifest changing.
func TestAssemblePackages_IsDeterministic(t *testing.T) {
	t.Parallel()
	in := map[string][]types.KnowledgePackage{
		"c": {{Manager: "gomod", Name: "example.com/c", Version: "v1.0.0"}},
		"a": {{Manager: "gomod", Name: "example.com/a", Version: "v1.0.0"}},
		"b": {{Manager: "gomod", Name: "example.com/b", Version: "v1.0.0"}},
	}
	first := assemblePackages(in)
	for range 8 {
		assert.Equal(t, first, assemblePackages(in), "same input must yield a byte-identical shard")
	}
}

// TestAssemblePackages_EmptyAndMalformed covers what the caller relies on to decide
// whether to append the shard at all, plus the records that carry nothing to key on.
func TestAssemblePackages_EmptyAndMalformed(t *testing.T) {
	t.Parallel()
	assert.Empty(t, assemblePackages(nil).Nodes, "no input, no shard")
	s := assemblePackages(map[string][]types.KnowledgePackage{
		".": {
			{Manager: "", Name: "example.com/x", Version: "v1.0.0"},
			{Manager: "gomod", Name: "", Version: "v1.0.0"},
			{Manager: "gomod", Name: "example.com/ok", Version: "v1.0.0"},
		},
	})
	assert.Len(t, s.Nodes, 1, "a record with no manager or no name has nothing to key on")
}
