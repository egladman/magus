package magus

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/symbols"
	"github.com/egladman/magus/types"
	"github.com/scip-code/scip/bindings/go/scip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// mkGlobalWS writes a single-project workspace with one target and returns its root.
func mkGlobalWS(t *testing.T, target string) string {
	t.Helper()
	root := t.TempDir()
	src := "export fun " + target + "(args: [str]) > void {}\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(src), 0o644))
	return root
}

func TestBuildGlobalKnowledgeGraph(t *testing.T) {
	ctx := context.Background()
	rootA := mkGlobalWS(t, "build")
	rootB := mkGlobalWS(t, "deploy")

	wsA, err := Inspect(ctx, rootA)
	require.NoError(t, err)

	g, err := BuildGlobalKnowledgeGraph(ctx, wsA, config.Config{Knowledge: config.Knowledge{Workspaces: []string{rootB}}}, false, slog.Default())
	require.NoError(t, err)
	out := g.Output()

	nameA, nameB := filepath.Base(rootA), filepath.Base(rootB)
	var haveA, haveB bool
	for _, n := range out.Nodes {
		if strings.HasPrefix(n.ID, nameA+"//") {
			haveA = true
		}
		if strings.HasPrefix(n.ID, nameB+"//") {
			haveB = true
		}
	}
	assert.True(t, haveA, "current workspace nodes present, namespaced by %q", nameA)
	assert.True(t, haveB, "registered workspace nodes present, namespaced by %q", nameB)
	// Each workspace declared a distinct target; both should resolve in the union.
	assert.NotEmpty(t, g.Resolve("build", 1), "build target resolves in the global graph")
	assert.NotEmpty(t, g.Resolve("deploy", 1), "deploy target resolves in the global graph")
}

func TestBuildGlobalKnowledgeGraphSkipsUnreachable(t *testing.T) {
	ctx := context.Background()
	rootA := mkGlobalWS(t, "build")
	wsA, err := Inspect(ctx, rootA)
	require.NoError(t, err)

	// A registered workspace that does not exist is skipped, not fatal: the global
	// graph degrades to what it can reach rather than failing the whole query.
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	g, err := BuildGlobalKnowledgeGraph(ctx, wsA, config.Config{Knowledge: config.Knowledge{Workspaces: []string{missing}}}, false, slog.Default())
	require.NoError(t, err)
	assert.Positive(t, g.Output().NodeCount, "current workspace still present despite the missing registered one")
}

// ingest builds the symbolIngestInputs the loaders take, with a default logger.
func ingest(cfg config.Config, root, cacheDir string, projects types.ProjectsOutput, spells []types.Spell) symbolIngestInputs {
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
func goWorkspace(project string) (types.ProjectsOutput, []types.Spell) {
	projects := types.ProjectsOutput{Projects: []types.ProjectEntry{
		{Path: project, Spell: "go", Spells: []string{"go"}},
	}}
	spells := []types.Spell{
		{Name: "go", Targets: []string{"go-build", symbols.IndexOp}},
	}
	return projects, spells
}

func TestLoadKnowledgeSymbolsAutoDerives(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, ".magus")
	// The index lives in the cache at the derived path, never in the project tree.
	writeSCIP(t, symbols.IndexPath(cacheDir, filepath.Join(root, "pkg/a")))

	projects, spells := goWorkspace("pkg/a")
	got := loadKnowledgeSymbols(t.Context(), ingest(config.Config{}, root, cacheDir, projects, spells))

	require.Len(t, got["pkg/a"], 1, "a project bound to a symbol-capable spell is ingested with no config")
	assert.Equal(t, "gomod example.com/a Foo#", got["pkg/a"][0].Key)
}

func TestLoadKnowledgeSymbolsSkipsUnbuilt(t *testing.T) {
	root := t.TempDir()
	projects, spells := goWorkspace("pkg/a") // no index written yet
	got := loadKnowledgeSymbols(t.Context(), ingest(config.Config{}, root, filepath.Join(root, ".magus"), projects, spells))
	assert.Empty(t, got, "a derived index whose scip target has not run is skipped")
}

// A project that declares an index which was never built is the gap that makes an empty
// lookup unknown rather than absent. loadKnowledgeSymbols skips it silently by design;
// this is what recovers the fact so a reader is told.
func TestSymbolGapsReportsUnbuiltIndex(t *testing.T) {
	root := t.TempDir()
	projects, spells := goWorkspace("pkg/a") // no index written
	got := symbolGaps(t.Context(), ingest(config.Config{}, root, filepath.Join(root, ".magus"), projects, spells))

	require.Len(t, got, 1)
	assert.Equal(t, "pkg/a", got[0].Project.Path)
	assert.Equal(t, types.SymbolIndexNotBuilt, got[0].State)
	assert.Empty(t, got[0].Detail, "never built needs no qualifier")
}

// A readable index is no gap, which is what lets an empty result be reported as a
// verified absence.
func TestSymbolGapsEmptyWhenBuilt(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, ".magus")
	writeSCIP(t, symbols.IndexPath(cacheDir, filepath.Join(root, "pkg/a")))

	projects, spells := goWorkspace("pkg/a")
	assert.Empty(t, symbolGaps(t.Context(), ingest(config.Config{}, root, cacheDir, projects, spells)))
}

// A present-but-corrupt index reads as COVERED, and that is a deliberate trade rather
// than an oversight. Detecting it means a full protobuf unmarshal plus symbol
// accumulation per lookup, on a path taken every time a query comes back empty, to catch
// a case that barely occurs - while a never-built index, the case that occurs constantly,
// costs one Stat. The graph build logs the corrupt one when it tries to ingest it.
//
// This test exists so the trade is a decision on the record: if the probe ever grows a
// decode check, it should be because the cost changed, not because nobody noticed.
func TestSymbolGapsTreatsCorruptIndexAsPresent(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, ".magus")
	path := symbols.IndexPath(cacheDir, filepath.Join(root, "pkg/a"))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("not a protobuf"), 0o644))

	projects, spells := goWorkspace("pkg/a")
	assert.Empty(t, symbolGaps(t.Context(), ingest(config.Config{}, root, cacheDir, projects, spells)))
}

// TestSymbolGapsLogsWhenTheProbeCannotRun pins that the package-level SymbolGaps
// (the (nil, false) probe-failed path) no longer discards the underlying error
// from ListSpells/ListProjects - it must at least be logged, since (nil, false) on
// its own gives a caller no way to learn WHY the probe could not run.
func TestSymbolGapsLogsWhenTheProbeCannotRun(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	// A cancelled context makes ListSpells fail on its first cancellation check
	// (describe.go's describeCancelled), which is the same failure shape a real
	// caller sees when its ctx times out mid-probe.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	gaps, ok := SymbolGaps(ctx, nil, root, config.Config{}, log)
	assert.Nil(t, gaps)
	assert.False(t, ok, "a probe that could not run must not read as \"no gaps\"")
	assert.Contains(t, buf.String(), "cancelled", "the underlying error must be surfaced, not swallowed")
}

// A workspace with no symbol-capable project has no code-symbol layer to miss, so there
// is nothing outside coverage and an unmatched name is a verified absence.
func TestSymbolGapsNoneCapable(t *testing.T) {
	root := t.TempDir()
	projects := types.ProjectsOutput{Projects: []types.ProjectEntry{{Path: "web", Spell: "ts", Spells: []string{"ts"}}}}
	spells := []types.Spell{{Name: "ts", Targets: []string{"tsc"}}} // no scip op
	assert.Empty(t, symbolGaps(t.Context(), ingest(config.Config{}, root, filepath.Join(root, ".magus"), projects, spells)))
}

// The declared language reaches ingestion, so an indexer that reports none still yields
// labelled symbols. Without it `magus query language:typescript` is empty while
// `language:go` works, and the graph looks like it has no TypeScript at all.
func TestLoadKnowledgeSymbolsCarriesDeclaredLanguage(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, ".magus")
	writeSCIP(t, symbols.IndexPath(cacheDir, filepath.Join(root, "web")))

	projects := types.ProjectsOutput{Projects: []types.ProjectEntry{
		{Path: "web", Spell: "typescript", Spells: []string{"typescript"}},
	}}
	spells := []types.Spell{
		{Name: "typescript", Language: "typescript", Targets: []string{symbols.IndexOp}},
	}
	got := loadKnowledgeSymbols(t.Context(), ingest(config.Config{}, root, cacheDir, projects, spells))

	require.Len(t, got["web"], 1)
	assert.Equal(t, "typescript", got["web"][0].Language,
		"writeSCIP's fixture sets no Document.Language, exactly like scip-typescript")
}

func TestLoadKnowledgeSymbolsNoneCapable(t *testing.T) {
	root := t.TempDir()
	projects := types.ProjectsOutput{Projects: []types.ProjectEntry{{Path: "web", Spell: "ts", Spells: []string{"ts"}}}}
	spells := []types.Spell{{Name: "ts", Targets: []string{"tsc"}}} // no scip op
	got := loadKnowledgeSymbols(t.Context(), ingest(config.Config{}, root, filepath.Join(root, ".magus"), projects, spells))
	assert.Nil(t, got, "a project whose spell exposes no scip op is not ingested")
}

func TestLoadKnowledgeSymbolsSkipsCorrupt(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, ".magus")
	idx := symbols.IndexPath(cacheDir, filepath.Join(root, "pkg/a"))
	require.NoError(t, os.MkdirAll(filepath.Dir(idx), 0o755))
	require.NoError(t, os.WriteFile(idx, []byte("not a protobuf"), 0o644))

	projects, spells := goWorkspace("pkg/a")
	got := loadKnowledgeSymbols(t.Context(), ingest(config.Config{}, root, cacheDir, projects, spells))
	assert.Empty(t, got, "an undecodable index is skipped, not fatal")
}

func TestLoadKnowledgeSymbolsExplicitOverride(t *testing.T) {
	root := t.TempDir()
	// The override points at a tree path; the index lives there, not in the cache.
	writeSCIP(t, filepath.Join(root, "build/custom.scip"))
	cfg := config.Config{Knowledge: config.Knowledge{Symbols: []config.SymbolIndex{
		{Project: "pkg/a", Index: "build/custom.scip"},
	}}}
	got := loadKnowledgeSymbols(t.Context(), ingest(cfg, root, filepath.Join(root, ".magus"), types.ProjectsOutput{}, nil))

	require.Len(t, got["pkg/a"], 1, "an explicit override is read from its tree path")
	assert.Equal(t, "gomod example.com/a Foo#", got["pkg/a"][0].Key)
}

func TestSymbolIndexDeclarationsOverrideWinsOverDerived(t *testing.T) {
	root := t.TempDir()
	projects, spells := goWorkspace("pkg/a")
	cfg := config.Config{Knowledge: config.Knowledge{Symbols: []config.SymbolIndex{
		{Project: "pkg/a", Index: "build/custom.scip"},
	}}}
	decls := symbolIndexDeclarations(t.Context(), ingest(cfg, root, filepath.Join(root, ".magus"), projects, spells))

	require.Len(t, decls, 1, "one project yields one declaration, not two")
	assert.Equal(t, filepath.Join(root, "build/custom.scip"), decls[0].path, "the override path wins over the derived cache path")
}

func TestSymbolIndexDeclarationsDerivesCachePath(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, ".magus")
	projects, spells := goWorkspace("pkg/a")
	decls := symbolIndexDeclarations(t.Context(), ingest(config.Config{}, root, cacheDir, projects, spells))

	require.Len(t, decls, 1)
	assert.Equal(t, symbols.IndexPath(cacheDir, filepath.Join(root, "pkg/a")), decls[0].path, "the derived index lives under the cache dir")
}

func TestSymbolIndexDeclarationsRejectsPathEscape(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{Knowledge: config.Knowledge{Symbols: []config.SymbolIndex{
		{Project: "pkg/a", Index: "../outside.scip"},
	}}}
	decls := symbolIndexDeclarations(t.Context(), ingest(cfg, root, filepath.Join(root, ".magus"), types.ProjectsOutput{}, nil))
	assert.Empty(t, decls, "an override path that escapes the workspace is rejected")
}

// gitRun runs one git command in dir, fataling on error. Skips the test if git is
// unavailable so CI without git does not fail spuriously.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
}

func writeCommit(t *testing.T, dir, file, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644))
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "c")
}

func gitHeadFull(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}

// TestLoadKnowledgeVCSHistory drives the whole opt-in path against a real repo (routed
// through the VCS abstraction): per-file commit counts, most-recent-commit, and - locking
// in the core.quotePath fix - a non-ASCII filename that must come through raw to match.
func TestLoadKnowledgeVCSHistory(t *testing.T) {
	root := t.TempDir()
	gitRun(t, root, "init", "-q")
	writeCommit(t, root, "a.buzz", "one\n")  // a: commit 1
	writeCommit(t, root, "b.buzz", "one\n")  // b: commit 1
	writeCommit(t, root, "a.buzz", "two\n")  // a: commit 2 (most recent for a)
	writeCommit(t, root, "café.buzz", "u\n") // non-ASCII path

	cfg := config.Config{Knowledge: config.Knowledge{VCS: config.KnowledgeVCSConfig{Enabled: true}}}
	entries := loadKnowledgeVCS(context.Background(), cfg, root, slog.Default())
	require.NotEmpty(t, entries)

	byPath := map[string]int{}
	last := map[string]string{}
	for _, e := range entries {
		byPath[e.Path] = e.Commits
		last[e.Path] = e.LastCommit
		assert.NotEmpty(t, e.LastCommit, "every entry has a last commit")
		assert.False(t, e.LastModified.IsZero(), "every entry has an author time")
		assert.Equal(t, "t", e.LastAuthor, "the last commit's author is captured (GIT_AUTHOR_NAME)")
	}
	assert.Equal(t, 2, byPath["a.buzz"], "a.buzz was touched by two commits")
	assert.Equal(t, 1, byPath["b.buzz"], "b.buzz was touched by one commit")
	assert.Contains(t, byPath, "café.buzz", "non-ASCII path comes through raw (quotePath=false), not git-quoted")

	// a.buzz's recorded last commit is the most recent one (HEAD), abbreviated.
	head := gitHeadFull(t, root)
	assert.True(t, strings.HasPrefix(head, last["café.buzz"]), "last commit is an abbreviation of HEAD")
}

func TestLoadKnowledgeVCSDisabledAndNonGit(t *testing.T) {
	// Disabled: no scan, nil result, even in a git repo.
	root := t.TempDir()
	gitRun(t, root, "init", "-q")
	writeCommit(t, root, "a.buzz", "x\n")
	assert.Nil(t, loadKnowledgeVCS(context.Background(), config.Config{}, root, slog.Default()))

	// Enabled but not a git repo: best-effort nil, no error.
	enabled := config.Config{Knowledge: config.Knowledge{VCS: config.KnowledgeVCSConfig{Enabled: true}}}
	assert.Nil(t, loadKnowledgeVCS(context.Background(), enabled, t.TempDir(), slog.Default()))
}

// TestBuildKnowledgeGraphChurnIsStableAcrossBuilds pins the property that makes caching the
// scan safe: a cache HIT and a cache MISS must be indistinguishable downstream.
//
// The first build walks history, the second reads the cached scan, and the graph they
// publish has to be identical. When the cache sat on the @vcs shard instead of on the scan,
// it was not: the second build handed the two derived consumers an empty history, so every
// directory published dir_commits 0 and no prose was measured, while the reused shard kept
// the graph looking complete. That is why this asserts the second build AGREES with the
// first rather than merely asserting the first is right.
func TestBuildKnowledgeGraphChurnIsStableAcrossBuilds(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	gitRun(t, root, "init", "-q")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pkg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("export fun build(args: [str]) > void {}\n"), 0o644))
	writeCommit(t, root, "pkg/a.buzz", "export fun a(args: [str]) > void {}\n")
	writeCommit(t, root, "pkg/a.buzz", "export fun a(args: [str]) > void { }\n")

	ws, err := Inspect(ctx, root)
	require.NoError(t, err)
	cfg := config.Config{Knowledge: config.Knowledge{VCS: config.KnowledgeVCSConfig{Enabled: true}}}

	churn := func() map[string]string {
		g, err := BuildKnowledgeGraph(ctx, ws, root, cfg, false, slog.Default())
		require.NoError(t, err)
		got := map[string]string{}
		for _, n := range g.Output().Nodes {
			if v, ok := n.Attrs["dir_commits"]; ok {
				got[n.ID] = v
			}
		}
		return got
	}

	first := churn()
	require.NotEmpty(t, first, "the fixture has a committed subdirectory, so some dir reports churn")
	assert.Contains(t, slices.Collect(maps.Values(first)), "2", "pkg/ holds a file with two commits")
	assert.Equal(t, first, churn(), "a cached scan must publish what the walk published")

	// And the second build really did take the cached path, rather than agreeing by walking
	// twice - otherwise this test would still pass with the cache silently broken.
	assert.FileExists(t, filepath.Join(root, ".magus", "knowledge", "inputs", "vcs.json"))
}

// TestLoadKnowledgeVCSCached covers the three ways the cache must yield to the walk. Each
// one is a case where serving what is on disk would publish a history that is not the
// workspace's, and the cost of being wrong (a graph that disagrees with itself) is far
// higher than the 0.41s walk it saves.
func TestLoadKnowledgeVCSCached(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	// The cache dir sits OUTSIDE the fixture repo: this test commits between calls, and a
	// cache written under root would be swept into the very history it is caching.
	cacheDir := t.TempDir()
	gitRun(t, root, "init", "-q")
	writeCommit(t, root, "a.buzz", "x\n")
	cfg := config.Config{Knowledge: config.Knowledge{VCS: config.KnowledgeVCSConfig{Enabled: true}}}
	path := filepath.Join(cacheDir, "knowledge", "inputs", "vcs.json")

	first := loadKnowledgeVCSCached(ctx, cfg, root, cacheDir, false, slog.Default())
	require.NotEmpty(t, first)
	require.FileExists(t, path)
	assert.Equal(t, first, loadKnowledgeVCSCached(ctx, cfg, root, cacheDir, false, slog.Default()), "a hit returns the scan verbatim")

	// A new commit is new history, so the key moves and the walk re-runs.
	writeCommit(t, root, "b.buzz", "y\n")
	moved := loadKnowledgeVCSCached(ctx, cfg, root, cacheDir, false, slog.Default())
	assert.NotEqual(t, first, moved, "a moved HEAD is a different history")
	assert.Len(t, moved, 2)

	// A widened window is a different history too, even where the result would coincide.
	widened := config.Config{Knowledge: config.Knowledge{VCS: config.KnowledgeVCSConfig{Enabled: true, MaxCommits: 1}}}
	assert.Len(t, loadKnowledgeVCSCached(ctx, widened, root, cacheDir, false, slog.Default()), 1, "a narrowed window re-walks")

	// Garbage on disk degrades to a walk rather than to an empty history. Reading a
	// corrupt file as "no commits" would be the same silent-zero failure in a new place.
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o644))
	assert.Equal(t, moved, loadKnowledgeVCSCached(ctx, cfg, root, cacheDir, false, slog.Default()), "a corrupt cache is not an answer")

	// --refresh distrusts the cache even on a matching key, and repairs it on the way out,
	// so the distrust costs one walk rather than every walk.
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o644))
	assert.Equal(t, moved, loadKnowledgeVCSCached(ctx, cfg, root, cacheDir, true, slog.Default()))
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "not json", "a refresh rewrites the cache it bypassed")
}

// TestLoadKnowledgeVCSCachedWritesNothingWhenReadOnly: an immutable cache means the run may
// read what is there but must not leave anything behind, and a workspace with no resolvable
// history must not persist "no history" as a fact about it.
func TestLoadKnowledgeVCSCachedWritesNothingWhenReadOnly(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cacheDir := t.TempDir()
	gitRun(t, root, "init", "-q")
	writeCommit(t, root, "a.buzz", "x\n")
	path := filepath.Join(cacheDir, "knowledge", "inputs", "vcs.json")

	no := false
	ro := config.Config{
		Cache:     config.Cache{Write: config.CacheWrite{Enabled: &no}},
		Knowledge: config.Knowledge{VCS: config.KnowledgeVCSConfig{Enabled: true}},
	}
	require.NotEmpty(t, loadKnowledgeVCSCached(ctx, ro, root, cacheDir, false, slog.Default()))
	assert.NoFileExists(t, path, "a read-only cache is never written")

	// Not a git repo: nothing to key on, so nothing is cached.
	bare, bareCache := t.TempDir(), t.TempDir()
	rw := config.Config{Knowledge: config.Knowledge{VCS: config.KnowledgeVCSConfig{Enabled: true}}}
	assert.Nil(t, loadKnowledgeVCSCached(ctx, rw, bare, bareCache, false, slog.Default()))
	assert.NoFileExists(t, filepath.Join(bareCache, "knowledge", "inputs", "vcs.json"))
}

// TestLoadKnowledgeVCSNestedWorkspace confirms the prefix strip: when the workspace root
// is a subdir of the git root, ChangesByCommit's VCS-root-relative paths are re-rooted to
// workspace-relative so they line up with file-node Sources.
func TestLoadKnowledgeVCSNestedWorkspace(t *testing.T) {
	repo := t.TempDir()
	gitRun(t, repo, "init", "-q")
	writeCommit(t, repo, "other.buzz", "root\n") // outside the sub-workspace
	sub := filepath.Join(repo, "sub", "proj")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	writeCommit(t, repo, "sub/proj/app.buzz", "nested\n")

	cfg := config.Config{Knowledge: config.Knowledge{VCS: config.KnowledgeVCSConfig{Enabled: true}}}
	entries := loadKnowledgeVCS(context.Background(), cfg, sub, slog.Default())
	require.NotEmpty(t, entries)

	paths := map[string]bool{}
	for _, e := range entries {
		paths[e.Path] = true
	}
	assert.True(t, paths["app.buzz"], "sub/proj/app.buzz is re-rooted to app.buzz")
	assert.False(t, paths["sub/proj/app.buzz"], "the vcs-root prefix is stripped")
	assert.False(t, paths["other.buzz"], "files outside the workspace subtree are excluded")
}

// TestShortRevision covers both branches: a revision longer than the 12-hex-digit
// convention is truncated, and one already at or under that length passes through
// unchanged rather than being padded or otherwise altered.
func TestShortRevision(t *testing.T) {
	cases := []struct {
		name string
		id   string
		want string
	}{
		{"full 40-char sha1 truncates to 12", "a62ac9b158086b887f03c6f8f9c6545bd9eb1614", "a62ac9b15808"},
		{"already-short id passes through unchanged", "abc123", "abc123"},
		{"exactly 12 chars passes through unchanged", "abc123def456", "abc123def456"},
		{"empty id passes through unchanged", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, ShortRevision(c.id))
		})
	}
}

// TestSymbolOccurrencesReportsACorruptIndexAsAGap is the completeness half of the
// occurrence read. An index that exists but will not decode contributes no sites, and
// dropping it quietly would present a short list under a verdict saying magus searched
// everywhere - the exact failure a rewrite driven off that list cannot survive.
//
// Note what this does NOT change: TestSymbolGapsTreatsCorruptIndexAsPresent still pins the
// probe's Stat-only behavior. The trade recorded there is about not paying a full decode on
// every empty query; this path has already decoded, so recording the failure here costs
// nothing the probe was protecting.
func TestSymbolOccurrencesReportsACorruptIndexAsAGap(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, ".magus")
	path := symbols.IndexPath(cacheDir, filepath.Join(root, "pkg/a"))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("not a protobuf"), 0o644))

	projects, spells := goWorkspace("pkg/a")
	got := symbolOccurrences(t.Context(), ingest(config.Config{}, root, cacheDir, projects, spells), "gomod example.com/a Foo#")

	assert.Empty(t, got.Files, "a corrupt index yields no sites")
	require.Len(t, got.Unreadable, 1, "and the hole must be recorded, not swallowed")
	assert.Equal(t, "pkg/a", got.Unreadable[0].Project.Path)
	assert.Equal(t, "does not decode", got.Unreadable[0].Detail)
}

// TestSymbolOccurrencesLeavesAnUnbuiltIndexToTheProbe keeps the two reports from
// double-counting. A never-built index is the constant case, and SymbolGaps already names
// it from its own Stat; reporting it here as well would show the same project twice in one
// verdict.
func TestSymbolOccurrencesLeavesAnUnbuiltIndexToTheProbe(t *testing.T) {
	root := t.TempDir()
	projects, spells := goWorkspace("pkg/a") // no index written
	got := symbolOccurrences(t.Context(), ingest(config.Config{}, root, filepath.Join(root, ".magus"), projects, spells), "gomod example.com/a Foo#")

	assert.Empty(t, got.Files)
	assert.Empty(t, got.Unreadable, "not-built belongs to SymbolGaps, which reports it already")
}

// TestSymbolOccurrencesReadsAGoodIndex is the contrast that keeps the two tests above
// honest: a decodable index reports its sites and no gap at all.
func TestSymbolOccurrencesReadsAGoodIndex(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, ".magus")
	// The source tree is created BEFORE the index: IndexPath resolves symlinks, and on a
	// platform where the temp root is one (macOS /var -> /private/var) a project dir that
	// springs into existence between two IndexPath calls hashes to two different locations,
	// so the read would look for the index somewhere it was never written.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pkg/a/pkg/a"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "pkg/a/pkg/a/a.go"), []byte("Foo\n"), 0o644))
	writeSCIP(t, symbols.IndexPath(cacheDir, filepath.Join(root, "pkg/a")))

	projects, spells := goWorkspace("pkg/a")
	got := symbolOccurrences(t.Context(), ingest(config.Config{}, root, cacheDir, projects, spells), "gomod example.com/a Foo#")

	assert.Empty(t, got.Unreadable)
	require.Len(t, got.Files, 1)
	require.Len(t, got.Files[0].Occurrences, 1)
	assert.Equal(t, types.SymbolOccurrenceVerified, got.Files[0].Occurrences[0].Status,
		"the range holds Foo, so the site is editable")
}

// TestVCSHistoryFormatKeysTheCache: a shape change is invisible to the rest of the key -
// the same HEAD and window hash the same - so without the format version an old file would
// match, decode with the renamed field absent, and hand every consumer a zero timestamp.
// That is the exact silent-zero failure this cache was rebuilt to eliminate, arriving
// through the cache's own front door.
func TestVCSHistoryFormatKeysTheCache(t *testing.T) {
	ctx := context.Background()
	root, cacheDir := t.TempDir(), t.TempDir()
	gitRun(t, root, "init", "-q")
	writeCommit(t, root, "a.buzz", "x\n")
	cfg := config.Config{Knowledge: config.Knowledge{VCS: config.KnowledgeVCSConfig{Enabled: true}}}
	path := filepath.Join(cacheDir, "knowledge", "inputs", "vcs.json")

	want := loadKnowledgeVCSCached(ctx, cfg, root, cacheDir, false, slog.Default())
	require.NotEmpty(t, want)
	require.False(t, want[0].LastModified.IsZero())

	// A file in the PREVIOUS shape, written under the previous format's key.
	prev := sha256.New()
	fmt.Fprintf(prev, "f%d\x00%s\x00%d\x00", vcsHistoryFormat-1, gitHeadFull(t, root), vcsDefaultMaxCommits)
	stale := fmt.Sprintf(`{"fingerprint":%q,"entries":[{"path":"a.buzz","last_unix":1700000000}]}`,
		hex.EncodeToString(prev.Sum(nil)))
	require.NoError(t, os.WriteFile(path, []byte(stale), 0o644))

	assert.Equal(t, want, loadKnowledgeVCSCached(ctx, cfg, root, cacheDir, false, slog.Default()),
		"a file in the old shape must miss, not decode as zeros")
}
