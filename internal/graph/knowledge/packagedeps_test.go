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

func TestImportGraphKeysByDirectoryAndSorts(t *testing.T) {
	t.Parallel()

	f := newPrecedentFixture()
	for _, dir := range []string{"internal/server", "internal/httpx", "internal/handler/mcp", "cmd/app", "."} {
		f.pkg(dir, 2)
	}
	f.imports("internal/server", "internal/httpx")
	f.imports("internal/server", "internal/handler/mcp")
	f.imports("cmd/app", "internal/server")
	f.imports("cmd/app", ".")
	// A second language's namespace in the same directory is the same package, so its
	// import of httpx folds into the Go one rather than listing httpx twice.
	ts := precedentNamespace("internal/server", "typescript")
	f.file("internal/server/client.ts", ts, "server", "typescript")
	f.edge("file:internal/server/client.ts", precedentNamespace("internal/httpx", "go"), types.RelationReferences)
	// Defined by no workspace file: a module dependency, not a workspace package.
	ext := "symbol:gomod golang.org/x/sync `golang.org/x/sync/errgroup`/"
	f.g.AddNode(types.KnowledgeNode{ID: ext, Kind: types.KindSymbol, Label: "errgroup", Attrs: map[string]string{attrNamespace: ext}})
	f.edge("file:internal/server/f0.go", ext, types.RelationReferences)
	// Only a test imports this, and a test's imports do not constrain the package.
	f.file("internal/httpx/httpx_test.go", precedentNamespace("internal/httpx", "go"), "httpx", "go")
	f.edge("file:internal/httpx/httpx_test.go", precedentNamespace("cmd/app", "go"), types.RelationReferences)

	assert.Equal(t, types.ImportGraph{Indexed: true, Packages: map[string][]string{
		"cmd/app":         {".", "internal/server"},
		"internal/server": {"internal/handler/mcp", "internal/httpx"},
	}}, f.g.ImportGraph())
}

func TestImportGraphUnindexedIsEmptyNotNil(t *testing.T) {
	t.Parallel()

	g := NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "file:internal/a/a.go", Kind: types.KindFile, Label: "internal/a/a.go", Source: "internal/a/a.go"})

	assert.Equal(t, types.ImportGraph{Indexed: false, Packages: map[string][]string{}}, g.ImportGraph())
}

func TestImportGraphDropsSelfImports(t *testing.T) {
	t.Parallel()

	f := newPrecedentFixture()
	f.pkg("internal/a", 2)
	f.imports("internal/a", "internal/a")
	ts := precedentNamespace("internal/a", "typescript")
	f.file("internal/a/a.ts", ts, "a", "typescript")
	f.edge("file:internal/a/a.ts", precedentNamespace("internal/a", "go"), types.RelationReferences)

	assert.Equal(t, types.ImportGraph{Indexed: true, Packages: map[string][]string{}}, f.g.ImportGraph())
}
