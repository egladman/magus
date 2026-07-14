package magus

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/symbols"
	"github.com/egladman/magus/types"
)

// ingest builds the symbolIngestInputs the loaders take, with a default logger.
func ingest(cfg config.Config, root, cacheDir string, projects types.ProjectsOutput, spells types.SpellsOutput) symbolIngestInputs {
	return symbolIngestInputs{cfg: cfg, root: root, cacheDir: cacheDir, projects: projects, spells: spells, log: slog.Default()}
}

// writeSCIP writes a minimal one-definition index to path (creating parent dirs).
func writeSCIP(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	idx := &scip.Index{Documents: []*scip.Document{{
		RelativePath: "pkg/a/a.go",
		Occurrences: []*scip.Occurrence{
			{Symbol: "scip-go gomod example.com/a v1 Foo#", SymbolRoles: int32(scip.SymbolRole_Definition), Range: []int32{0, 0, 3}},
		},
	}}}
	data, err := proto.Marshal(idx)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o644))
}

// goWorkspace describes a workspace whose "go" spell is symbol-capable (it exposes the
// reserved scip op) and one project bound to it - the auto-enable inputs, no config.
func goWorkspace(project string) (types.ProjectsOutput, types.SpellsOutput) {
	projects := types.ProjectsOutput{Projects: []types.ProjectEntry{
		{Path: project, Spell: "go", Spells: []string{"go"}},
	}}
	spells := types.SpellsOutput{Spells: []types.SpellEntry{
		{Name: "go", Targets: []string{"go-build", symbols.IndexOp}},
	}}
	return projects, spells
}

func TestLoadKnowledgeSymbolsAutoDerives(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, ".magus")
	// The index lives in the cache at the derived path, never in the project tree.
	writeSCIP(t, symbols.IndexPath(cacheDir, filepath.Join(root, "pkg/a")))

	projects, spells := goWorkspace("pkg/a")
	got := loadKnowledgeSymbols(ingest(config.Config{}, root, cacheDir, projects, spells))

	require.Len(t, got["pkg/a"], 1, "a project bound to a symbol-capable spell is ingested with no config")
	assert.Equal(t, "gomod example.com/a Foo#", got["pkg/a"][0].Key)
}

func TestLoadKnowledgeSymbolsSkipsUnbuilt(t *testing.T) {
	root := t.TempDir()
	projects, spells := goWorkspace("pkg/a") // no index written yet
	got := loadKnowledgeSymbols(ingest(config.Config{}, root, filepath.Join(root, ".magus"), projects, spells))
	assert.Empty(t, got, "a derived index whose scip target has not run is skipped")
}

func TestLoadKnowledgeSymbolsNoneCapable(t *testing.T) {
	root := t.TempDir()
	projects := types.ProjectsOutput{Projects: []types.ProjectEntry{{Path: "web", Spell: "ts", Spells: []string{"ts"}}}}
	spells := types.SpellsOutput{Spells: []types.SpellEntry{{Name: "ts", Targets: []string{"tsc"}}}} // no scip op
	got := loadKnowledgeSymbols(ingest(config.Config{}, root, filepath.Join(root, ".magus"), projects, spells))
	assert.Nil(t, got, "a project whose spell exposes no scip op is not ingested")
}

func TestLoadKnowledgeSymbolsSkipsCorrupt(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, ".magus")
	idx := symbols.IndexPath(cacheDir, filepath.Join(root, "pkg/a"))
	require.NoError(t, os.MkdirAll(filepath.Dir(idx), 0o755))
	require.NoError(t, os.WriteFile(idx, []byte("not a protobuf"), 0o644))

	projects, spells := goWorkspace("pkg/a")
	got := loadKnowledgeSymbols(ingest(config.Config{}, root, cacheDir, projects, spells))
	assert.Empty(t, got, "an undecodable index is skipped, not fatal")
}

func TestLoadKnowledgeSymbolsExplicitOverride(t *testing.T) {
	root := t.TempDir()
	// The override points at a tree path; the index lives there, not in the cache.
	writeSCIP(t, filepath.Join(root, "build/custom.scip"))
	cfg := config.Config{Knowledge: config.Knowledge{Symbols: []config.SymbolIndex{
		{Project: "pkg/a", Index: "build/custom.scip"},
	}}}
	got := loadKnowledgeSymbols(ingest(cfg, root, filepath.Join(root, ".magus"), types.ProjectsOutput{}, types.SpellsOutput{}))

	require.Len(t, got["pkg/a"], 1, "an explicit override is read from its tree path")
	assert.Equal(t, "gomod example.com/a Foo#", got["pkg/a"][0].Key)
}

func TestSymbolIndexDeclarationsOverrideWinsOverDerived(t *testing.T) {
	root := t.TempDir()
	projects, spells := goWorkspace("pkg/a")
	cfg := config.Config{Knowledge: config.Knowledge{Symbols: []config.SymbolIndex{
		{Project: "pkg/a", Index: "build/custom.scip"},
	}}}
	decls := symbolIndexDeclarations(ingest(cfg, root, filepath.Join(root, ".magus"), projects, spells))

	require.Len(t, decls, 1, "one project yields one declaration, not two")
	assert.Equal(t, filepath.Join(root, "build/custom.scip"), decls[0].path, "the override path wins over the derived cache path")
}

func TestSymbolIndexDeclarationsDerivesCachePath(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, ".magus")
	projects, spells := goWorkspace("pkg/a")
	decls := symbolIndexDeclarations(ingest(config.Config{}, root, cacheDir, projects, spells))

	require.Len(t, decls, 1)
	assert.Equal(t, symbols.IndexPath(cacheDir, filepath.Join(root, "pkg/a")), decls[0].path, "the derived index lives under the cache dir")
}

func TestSymbolIndexDeclarationsRejectsPathEscape(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{Knowledge: config.Knowledge{Symbols: []config.SymbolIndex{
		{Project: "pkg/a", Index: "../outside.scip"},
	}}}
	decls := symbolIndexDeclarations(ingest(cfg, root, filepath.Join(root, ".magus"), types.ProjectsOutput{}, types.SpellsOutput{}))
	assert.Empty(t, decls, "an override path that escapes the workspace is rejected")
}
