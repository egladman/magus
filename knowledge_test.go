package magus

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
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
		assert.Positive(t, e.LastUnix, "every entry has an author time")
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

// TestVCSInputFingerprint proves the scan's input fingerprint is stable on an unchanged
// tree and moves when HEAD or the window moves - which is what lets the caller skip the git
// scan and reuse the @vcs shard from disk on an unchanged commit (no bespoke cache file).
func TestVCSInputFingerprint(t *testing.T) {
	root := t.TempDir()
	gitRun(t, root, "init", "-q")
	writeCommit(t, root, "a.buzz", "x\n")
	cfg := config.Config{Knowledge: config.Knowledge{VCS: config.KnowledgeVCSConfig{Enabled: true}}}
	ctx := context.Background()

	fp1 := vcsInputFingerprint(ctx, cfg, root)
	require.NotEmpty(t, fp1, "an enabled git repo yields a fingerprint")
	assert.Equal(t, fp1, vcsInputFingerprint(ctx, cfg, root), "unchanged HEAD + window -> stable fingerprint")

	// A new commit moves HEAD, so the fingerprint changes (the scan must re-run).
	writeCommit(t, root, "b.buzz", "y\n")
	assert.NotEqual(t, fp1, vcsInputFingerprint(ctx, cfg, root), "a new commit changes the fingerprint")

	// The window is part of the key, so changing max_commits changes the fingerprint even
	// when the result would be identical (conservative: re-scan on a widened window).
	widened := config.Config{Knowledge: config.Knowledge{VCS: config.KnowledgeVCSConfig{Enabled: true, MaxCommits: 5}}}
	assert.NotEqual(t, vcsInputFingerprint(ctx, cfg, root), vcsInputFingerprint(ctx, widened, root), "a changed max_commits changes the fingerprint")

	// A working-tree change with no commit (delete a tracked file) moves the dirty set, so
	// the fingerprint changes - the cached @vcs shard must not outlive the file nodes it
	// filters onto. This is the regression guard for the "skip on unchanged HEAD" phantom.
	clean := vcsInputFingerprint(ctx, cfg, root)
	require.NoError(t, os.Remove(filepath.Join(root, "a.buzz")))
	assert.NotEqual(t, clean, vcsInputFingerprint(ctx, cfg, root), "an uncommitted deletion changes the fingerprint")

	// Disabled or non-git yields an empty fingerprint, so the caller never skips the scan.
	assert.Empty(t, vcsInputFingerprint(ctx, config.Config{}, root), "disabled -> empty")
	assert.Empty(t, vcsInputFingerprint(ctx, cfg, t.TempDir()), "non-git -> empty")
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
