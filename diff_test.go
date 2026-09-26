package magus

import (
	"fmt"
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

// conformanceHead assembles a head graph the way a workspace build does: nine context readers
// in separate packages, eight named <X>FromContext, and the named functions the test adds to
// internal/trail/trail.go, each defined on the line given.
func conformanceHead(extra map[string]int) *knowledge.Graph {
	var syms []types.KnowledgeSymbol
	reader := func(pkg, name, file string, line int) types.KnowledgeSymbol {
		ns := "gomod example.com/m `example.com/m/" + pkg + "`/"
		return types.KnowledgeSymbol{
			Key: ns + name + "().", Label: name, Language: "go", SymbolKind: "Function", Namespace: ns,
			Signature: "func " + name + "(ctx context.Context) *T", Source: fmt.Sprintf("%s:%d", file, line), Defs: []string{file},
		}
	}
	for _, name := range []string{"Cwd", "Lease", "Root", "Base", "Charms", "Progress", "Runtime", "Writer"} {
		syms = append(syms, reader("internal/"+name, name+"FromContext", "internal/"+name+"/ctx.go", 3))
	}
	syms = append(syms, reader("internal/obs", "RecorderFrom", "internal/obs/ctx.go", 3))
	for name, line := range extra {
		syms = append(syms, reader("internal/trail", name, "internal/trail/trail.go", line))
	}
	g := knowledge.NewGraph()
	for _, sh := range knowledge.AssembleShards(knowledge.Inputs{
		Graph:   types.TargetGraphOutput{Projects: []types.TargetGraphProject{{Path: "."}}},
		Symbols: map[string][]types.KnowledgeSymbol{".": syms},
	}) {
		g.Merge(sh.Nodes, sh.Edges)
	}
	return g
}

func trailID(name string) string {
	return "symbol:gomod example.com/m `example.com/m/internal/trail`/" + name + "()."
}

// trailPatch adds EntryPointFrom on line 5 of an existing file whose Trail, on line 3, only had
// its body edited.
const trailPatch = `diff --git a/internal/trail/trail.go b/internal/trail/trail.go
--- a/internal/trail/trail.go
+++ b/internal/trail/trail.go
@@ -3,2 +3,4 @@
 func Trail(ctx context.Context) *T {
-	return nil
+	return &T{}
+}
+func EntryPointFrom(ctx context.Context) *T { return nil }
`

// Without a baseline, the patch decides what is new. The symbol has no referents yet, so the
// review never listed it; the finding is what puts it on the file.
func TestAttachConformanceFindsANewNameWithoutABaseline(t *testing.T) {
	head := conformanceHead(map[string]int{"EntryPointFrom": 6, "Trail": 3})
	out := types.Diff{Files: []types.DiffFile{{Path: "internal/trail/trail.go", Project: "."}}}
	byPath := map[string]*types.DiffFile{"internal/trail/trail.go": &out.Files[0]}

	attachConformance(byPath, head, conformanceInput{patch: trailPatch})

	require.Len(t, out.Files[0].Symbols, 1, "Trail's definition line is unchanged, so only EntryPointFrom is a subject")
	got := out.Files[0].Symbols[0]
	assert.Equal(t, trailID("EntryPointFrom"), got.ID)
	assert.Equal(t, "EntryPointFrom", got.Qualified)
	assert.Equal(t, types.DiffChangeAdded, got.Change, "the patch added its definition")
	require.Len(t, got.Checks, 1)
	assert.Equal(t, types.CheckNamingAffix, got.Checks[0].Name)
	assert.Equal(t, types.CheckAdvice, got.Checks[0].Status)
}

// A symbol whose definition moved, or whose signature changed, has its old line removed in the
// same patch: it is not new, and nothing is said about its name.
func TestAttachConformanceSkipsAMovedSymbol(t *testing.T) {
	head := conformanceHead(map[string]int{"EntryPointFrom": 6})
	out := types.Diff{Files: []types.DiffFile{{Path: "internal/trail/trail.go", Project: "."}}}
	moved := trailPatch + `diff --git a/internal/obs/old.go b/internal/obs/old.go
--- a/internal/obs/old.go
+++ b/internal/obs/old.go
@@ -1,1 +0,0 @@
-func EntryPointFrom(ctx context.Context) *T { return nil }
`

	attachConformance(map[string]*types.DiffFile{"internal/trail/trail.go": &out.Files[0]}, head, conformanceInput{patch: moved})

	assert.Empty(t, out.Files[0].Symbols)
}

func TestAttachConformanceFollowsTheBaselineClassification(t *testing.T) {
	head := conformanceHead(map[string]int{"EntryPointFrom": 6})
	out := types.Diff{Files: []types.DiffFile{{Path: "internal/trail/trail.go", Project: "."}}}
	byPath := map[string]*types.DiffFile{"internal/trail/trail.go": &out.Files[0]}

	attachConformance(byPath, head, conformanceInput{changes: map[string]string{trailID("EntryPointFrom"): types.DiffChangeBody}})
	assert.Empty(t, out.Files[0].Symbols, "a body edit is not a naming decision")

	attachConformance(byPath, head, conformanceInput{changes: map[string]string{trailID("EntryPointFrom"): types.DiffChangeAdded}})
	require.Len(t, out.Files[0].Symbols, 1)
	assert.Equal(t, types.DiffChangeAdded, out.Files[0].Symbols[0].Change)
	assert.NotEmpty(t, out.Files[0].Symbols[0].Checks)
}

func TestAttachConformanceSkipsGeneratedFiles(t *testing.T) {
	head := conformanceHead(map[string]int{"EntryPointFrom": 6})
	out := types.Diff{Files: []types.DiffFile{{Path: "internal/trail/trail.go", Project: ".", Role: types.DiffRoleOutput}}}

	attachConformance(map[string]*types.DiffFile{"internal/trail/trail.go": &out.Files[0]}, head, conformanceInput{patch: trailPatch})

	assert.Empty(t, out.Files[0].Symbols, "a regenerated file's names are the generator's decision")
}

func TestPairRenames(t *testing.T) {
	head := conformanceHead(map[string]int{"EntryPointFromContext": 6, "Unrelated": 9})
	base := types.KnowledgeNode{
		ID: trailID("EntryPointFrom"), Kind: types.KindSymbol, Label: "EntryPointFrom",
		Attrs: map[string]string{knowledge.AttrSignature: "func EntryPointFrom(ctx context.Context) *T"},
	}
	changes := map[string]string{trailID("EntryPointFromContext"): types.DiffChangeAdded, trailID("Unrelated"): types.DiffChangeBody}
	renamed := map[string]string{}

	pairRenames(definedSymbols(head.Nodes()), changes, []types.KnowledgeNode{base}, renamed)
	assert.Equal(t, map[string]string{trailID("EntryPointFromContext"): "EntryPointFrom"}, renamed)

	// Two added symbols fit equally well, so neither is claimed.
	changes[trailID("Unrelated")] = types.DiffChangeAdded
	clear(renamed)
	pairRenames(definedSymbols(head.Nodes()), changes, []types.KnowledgeNode{base}, renamed)
	assert.Empty(t, renamed)
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
