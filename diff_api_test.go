package magus

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
)

// goSymbol spells a scip-go moniker for a symbol in package pkg, so a test reads as the
// declaration it stands for rather than as punctuation.
func goSymbol(pkg, descriptors string) string {
	return "symbol:gomod example.com/m `example.com/m/" + pkg + "`/" + descriptors
}

// symbolNode is one indexed symbol as a graph carries it.
func symbolNode(id, label, source, signature, digest string) types.KnowledgeNode {
	return types.KnowledgeNode{
		ID: id, Kind: types.KindSymbol, Label: label, Source: source,
		Attrs: map[string]string{knowledge.AttrSignature: signature, knowledge.AttrBodyDigest: digest},
	}
}

func graphOf(nodes ...types.KnowledgeNode) *knowledge.Graph {
	g := knowledge.NewGraph()
	g.Merge(nodes, nil)
	return g
}

// noExternals is the referent lookup for a workspace where nothing crosses a project
// boundary, so a test states exposure through the Go export rule alone.
func noExternals(*knowledge.Graph, string, string) ([]string, int) { return nil, 0 }

func TestAttachAPIDeltaClassifiesAndBumps(t *testing.T) {
	const (
		kept    = "api.go:10"
		gone    = "api.go:20"
		private = "api.go:30"
	)
	base := types.KnowledgeGraphOutput{
		SchemaVersion: types.KnowledgeSchemaVersion,
		Nodes: []types.KnowledgeNode{
			symbolNode(goSymbol("api", "Open()."), "Open", kept, "func Open(root string) error", "d1"),
			symbolNode(goSymbol("api", "Close()."), "Close", gone, "func Close() error", "d2"),
			symbolNode(goSymbol("api", "Steady()."), "Steady", "api.go:40", "func Steady() error", "d3"),
			symbolNode(goSymbol("api", "hidden()."), "hidden", private, "func hidden() error", "d4"),
		},
	}
	head := graphOf(
		symbolNode(goSymbol("api", "Open()."), "Open", kept, "func Open(root string, quiet bool) error", "d1x"),
		symbolNode(goSymbol("api", "Steady()."), "Steady", "api.go:40", "func Steady() error", "d3"),
		symbolNode(goSymbol("api", "hidden()."), "hidden", private, "func hidden() error", "d4x"),
		symbolNode(goSymbol("api", "Fresh()."), "Fresh", "api.go:50", "func Fresh() error", "d5"),
	)
	out := types.Diff{Files: []types.DiffFile{{Path: "api.go", Project: "."}}}
	byPath := map[string]*types.DiffFile{"api.go": &out.Files[0]}

	attachAPIDelta(&out, byPath, head, diffConfig{baseline: &base, baselineLabel: "main"}, noExternals)

	require.NotNil(t, out.API)
	assert.Equal(t, types.DiffAPI{
		Base: "main", Floor: types.DiffBumpMajor, Likely: types.DiffBumpMajor,
		Added: 1, Removed: 1, Signature: 1, Body: 0,
	}, *out.API, "a removed export is a proven major; the private body change moves no count")

	changes := map[string]string{}
	for _, s := range out.Files[0].Symbols {
		changes[s.Qualified] = s.Change
	}
	assert.Equal(t, map[string]string{
		"Open":  types.DiffChangeSignature,
		"Close": types.DiffChangeRemoved,
		"Fresh": types.DiffChangeAdded,
	}, changes, "Steady is untouched in a changed file; hidden changed but no consumer can see it")
	assert.Equal(t, types.DiffSurfacePublic, out.Files[0].Surface)
}

// TestAttachAPIDeltaLikelyOnlyFromSignatures pins the split between what magus proves and
// what it suspects: a changed signature never raises the floor.
func TestAttachAPIDeltaLikelyOnlyFromSignatures(t *testing.T) {
	base := types.KnowledgeGraphOutput{
		SchemaVersion: types.KnowledgeSchemaVersion,
		Nodes: []types.KnowledgeNode{
			symbolNode(goSymbol("api", "Open()."), "Open", "api.go:10", "func Open() error", "d1"),
		},
	}
	head := graphOf(symbolNode(goSymbol("api", "Open()."), "Open", "api.go:10", "func Open(quiet bool) error", "d2"))
	out := types.Diff{Files: []types.DiffFile{{Path: "api.go", Project: "."}}}

	attachAPIDelta(&out, map[string]*types.DiffFile{"api.go": &out.Files[0]}, head,
		diffConfig{baseline: &base, baselineLabel: "main"}, noExternals)

	require.NotNil(t, out.API)
	assert.Equal(t, types.DiffBumpPatch, out.API.Floor, "nothing was added or removed, so the floor is a patch")
	assert.Equal(t, types.DiffBumpMajor, out.API.Likely)
}

// TestAttachAPIDeltaMembersClassifyThemselves guards the container rule: scip-go renders a
// struct with every field, so adding a field moves the type's signature too, and reporting
// the type as re-signed would call an added field a likely break.
func TestAttachAPIDeltaMembersClassifyThemselves(t *testing.T) {
	typeID := goSymbol("api", "Config#")
	base := types.KnowledgeGraphOutput{
		SchemaVersion: types.KnowledgeSchemaVersion,
		Nodes: []types.KnowledgeNode{
			symbolNode(typeID, "Config", "api.go:10", "type Config struct { Root string }", "d1"),
			symbolNode(typeID+"Root.", "Root", "api.go:11", "struct field Root string", "d2"),
		},
	}
	head := graphOf(
		symbolNode(typeID, "Config", "api.go:10", "type Config struct { Root string; Quiet bool }", "d1x"),
		symbolNode(typeID+"Root.", "Root", "api.go:11", "struct field Root string", "d2"),
		symbolNode(typeID+"Quiet.", "Quiet", "api.go:12", "struct field Quiet bool", "d3"),
	)
	out := types.Diff{Files: []types.DiffFile{{Path: "api.go", Project: "."}}}

	attachAPIDelta(&out, map[string]*types.DiffFile{"api.go": &out.Files[0]}, head,
		diffConfig{baseline: &base, baselineLabel: "main"}, noExternals)

	require.NotNil(t, out.API)
	assert.Equal(t, 0, out.API.Signature, "the type defers to its members")
	assert.Equal(t, 1, out.API.Added, "the new field is the change")
	assert.Equal(t, types.DiffBumpMinor, out.API.Likely)
}

func TestAttachAPIDeltaRefusesABaselineWithoutSymbols(t *testing.T) {
	out := types.Diff{Files: []types.DiffFile{{Path: "api.go", Project: "."}}}
	base := types.KnowledgeGraphOutput{SchemaVersion: types.KnowledgeSchemaVersion}

	attachAPIDelta(&out, map[string]*types.DiffFile{"api.go": &out.Files[0]}, graphOf(),
		diffConfig{baseline: &base, baselineLabel: "main"}, noExternals)

	assert.Nil(t, out.API, "no symbols on the base side means unknown, never an empty delta")
	require.Len(t, out.Notes, 1)
	assert.Contains(t, out.Notes[0], "--symbols")
}

func TestAttachAPIDeltaRefusesAnOlderSchema(t *testing.T) {
	out := types.Diff{}
	base := types.KnowledgeGraphOutput{SchemaVersion: 13, Nodes: []types.KnowledgeNode{
		symbolNode(goSymbol("api", "Open()."), "Open", "api.go:10", "", ""),
	}}

	attachAPIDelta(&out, map[string]*types.DiffFile{}, graphOf(), diffConfig{baseline: &base, baselineLabel: "main"}, noExternals)

	assert.Nil(t, out.API)
	require.Len(t, out.Notes, 1)
	assert.Contains(t, out.Notes[0], "predates recorded signatures")
}

func TestQualifiedName(t *testing.T) {
	for _, tc := range []struct{ name, id, label, want string }{
		{"function", goSymbol("api", "Open()."), "Open", "Open"},
		{"method", goSymbol("api", "Magus#Diff()."), "Diff", "Magus.Diff"},
		{"field", goSymbol("types", "DiffAPI#Signature."), "Signature", "DiffAPI.Signature"},
		{"parameter", goSymbol("api", "Open().(root)"), "root", "Open"},
		{"typescript", "symbol:scip-typescript npm web 1.0.0 src/`a.ts`/Tab#open().", "open", "Tab.open"},
		{"no namespace", "symbol:weird", "fallback", "fallback"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, qualifiedName(tc.id, tc.label))
		})
	}
}

func TestGoDescriptorsExported(t *testing.T) {
	for _, tc := range []struct {
		name         string
		id           string
		want, wantOK bool
	}{
		{"exported func", goSymbol("api", "Open()."), true, true},
		{"unexported func", goSymbol("api", "open()."), false, true},
		{"method on an exported type", goSymbol("api", "Magus#Run()."), true, true},
		{"method on an unexported type", goSymbol("api", "magus#Run()."), false, true},
		{"unexported field of an exported type", goSymbol("api", "Config#root."), false, true},
		{"parameter", goSymbol("api", "Open().(root)"), false, true},
		{"not a go moniker", "symbol:whatever", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := goDescriptorsExported(tc.id)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}
