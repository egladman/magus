package knowledge

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

func TestPackageDepsExceptLeavesOutWhatSkippedFilesImportAndCall(t *testing.T) {
	t.Parallel()

	f := newPrecedentFixture()
	a, x, app := precedentNamespace("internal/a", "go"), precedentNamespace("internal/x", "go"), precedentNamespace("cmd/app", "go")
	f.pkg("internal/a", 1)
	f.pkg("internal/x", 1)
	f.pkg("cmd/app", 1)
	f.file("internal/a/zz_generated.go", a, "a", "go")
	f.imports("internal/a", "internal/x")
	f.edge("file:internal/a/zz_generated.go", app, types.RelationReferences)
	f.g.AddNode(types.KnowledgeNode{ID: a + "Gen().", Kind: types.KindSymbol, Label: "Gen", Source: "internal/a/zz_generated.go:3",
		Attrs: map[string]string{attrNamespace: a}})
	f.g.AddNode(types.KnowledgeNode{ID: app + "Run().", Kind: types.KindSymbol, Label: "Run", Source: "cmd/app/f0.go:3",
		Attrs: map[string]string{attrNamespace: app}})
	f.edge(a+"Gen().", app+"Run().", types.RelationCalls)
	generated := func(file string) bool { return file == "internal/a/zz_generated.go" }

	assert.Equal(t, packageGraph{a: {x: true}}, f.g.packageDepsExcept(generated))
	assert.Equal(t, packageGraph{a: {x: true, app: true}}, f.g.packageDeps())
}

func TestPackageDepsFileNamespacesAreSorted(t *testing.T) {
	t.Parallel()

	f := newPrecedentFixture()
	nss := []string{
		precedentNamespace("internal/a", "go"),
		precedentNamespace("internal/b", "go"),
		precedentNamespace("internal/a", "typescript"),
	}
	for _, ns := range nss {
		f.file("internal/a/x.go", ns, "a", "go")
	}
	// Map order decides the append order, so one read in a few would come back unsorted.
	for range 20 {
		assert.Equal(t, map[string][]string{"file:internal/a/x.go": {nss[0], nss[1], nss[2]}}, f.g.fileNamespaces())
	}
}

// ImportGraph reads the stored fold: every language's packages keyed by directory, each
// named with its language, the default graph's dir node winning over the edge's.
func TestImportGraphReadsTheFoldedEdges(t *testing.T) {
	t.Parallel()

	projects := []types.TargetGraphProject{{Path: "."}, {Path: "libs/x"}, {Path: "console"}}
	g := mergeAll(append([]Shard{packageDirs([]string{"libs/x/x.go", "libs/x/y.py", "libs/x/z.py"}, projects, nil)},
		assembleSymbolShards(foldFixture(), projects)...))

	assert.Equal(t, types.ImportGraph{
		Indexed: true,
		Packages: map[string][]string{
			"cmd/app":     {"libs/x"},
			"console/src": {"console/src/lib"},
			"internal/a":  {"internal/b"},
			"internal/c":  {"libs/x"},
		},
		Languages: map[string]string{
			"cmd/app": "go", "console/src": "typescript", "console/src/lib": "typescript",
			"internal/a": "go", "internal/b": "go", "internal/c": "go", "libs/x": "python",
		},
	}, g.ImportGraph())
}

// A refold would find the file -references-> namespace edges; the stored fold is the only
// thing ImportGraph reads.
func TestImportGraphDoesNotRefold(t *testing.T) {
	t.Parallel()

	f := newPrecedentFixture()
	f.pkg("internal/a", 1)
	f.pkg("internal/b", 1)
	f.imports("internal/a", "internal/b")

	assert.Equal(t, types.ImportGraph{Indexed: true, Packages: map[string][]string{}, Languages: map[string]string{}}, f.g.ImportGraph())
}

func TestImportGraphUnindexedIsEmptyNotNil(t *testing.T) {
	t.Parallel()

	g := NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "file:internal/a/a.go", Kind: types.KindFile, Label: "internal/a/a.go", Source: "internal/a/a.go"})

	assert.Equal(t, types.ImportGraph{Indexed: false, Packages: map[string][]string{}, Languages: map[string]string{}}, g.ImportGraph())
}
