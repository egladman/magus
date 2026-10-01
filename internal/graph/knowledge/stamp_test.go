package knowledge

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stampFixture is buildFixture with one input in every class, so each class has a shard.
func stampFixture(t *testing.T) (string, Inputs) {
	cacheDir, in := buildFixture(t)
	in.Runtime = []types.DiagnosticEvent{{Unit: "pkg/a:build", Code: types.SandboxWeakened}}
	in.Symbols = map[string][]types.KnowledgeSymbol{
		"pkg/a": {{Key: "example.com/a Foo#", Label: "Foo", Source: "pkg/a/a.go:1", Defs: []string{"pkg/a/a.go"}}},
	}
	in.Coverage = []FileCoverage{{Path: "pkg/a/a.go", Covered: 1, Total: 2}}
	return cacheDir, in
}

func stampsAll(v string) Stamps {
	out := Stamps{}
	for _, c := range AllClasses {
		out[c] = v + string(c)
	}
	return out
}

// ensure runs Ensure and records which classes gather was asked for; nil means it was
// never called.
func ensure(t *testing.T, cacheDir string, stamps Stamps, want []ShardClass, in Inputs) (*Graph, []ShardClass) {
	t.Helper()
	var asked []ShardClass
	g, err := Ensure(context.Background(), cacheDir, BuildOptions{Stamps: stamps}, want, func(stale []ShardClass) (Inputs, error) {
		asked = stale
		return in, nil
	}, nil)
	require.NoError(t, err)
	return g, asked
}

func outputJSON(t *testing.T, g *Graph) string {
	t.Helper()
	b, err := json.Marshal(g.Output())
	require.NoError(t, err)
	return string(b)
}

func TestEnsureAnswersFromTheStoreWhenStampsMatch(t *testing.T) {
	cacheDir, in := stampFixture(t)
	built, asked := ensure(t, cacheDir, stampsAll("v1"), AllClasses, in)
	require.ElementsMatch(t, AllClasses, asked, "a store with no stamps reassembles every class")

	loaded, asked := ensure(t, cacheDir, stampsAll("v1"), DefaultClasses, Inputs{})

	assert.Nil(t, asked, "matching stamps must not gather a single input")
	assert.Equal(t, outputJSON(t, built), outputJSON(t, loaded), "the stored graph is the assembled one")
}

func TestEnsureReassemblesOnlyTheClassWhoseStampMoved(t *testing.T) {
	cacheDir, in := stampFixture(t)
	ensure(t, cacheDir, stampsAll("v1"), AllClasses, in)
	before := readManifest(t, cacheDir)
	docShard := filepath.Join(StoreDir(cacheDir), "shards", shardSlug("pkg/b")+".json")
	statBefore, err := os.Stat(docShard)
	require.NoError(t, err)

	stamps := stampsAll("v1")
	stamps[ClassRuntime] = "v2runtime"
	in.Runtime = append(in.Runtime, types.DiagnosticEvent{Unit: "pkg/b:build", Code: types.SandboxWeakened})
	g, asked := ensure(t, cacheDir, stamps, DefaultClasses, in)

	assert.Equal(t, []ShardClass{ClassRuntime}, asked)
	after := readManifest(t, cacheDir)
	assert.NotEqual(t, before.Shards[runtimeShardName].Fingerprint, after.Shards[runtimeShardName].Fingerprint)
	for name, meta := range before.Shards {
		if name != runtimeShardName {
			assert.Equalf(t, meta, after.Shards[name], "shard %q is outside the stale class", name)
		}
	}
	assert.Equal(t, "v2runtime", after.Inputs[ClassRuntime])
	assert.Equal(t, before.Inputs[ClassSymbols], after.Inputs[ClassSymbols], "a lazy class nobody asked for keeps its stamp")
	statAfter, err := os.Stat(docShard)
	require.NoError(t, err)
	assert.Equal(t, statBefore.ModTime(), statAfter.ModTime(), "an untouched class's shard is not rewritten")
	assert.Contains(t, g.Edges(), extractedEdge("target:pkg/b:build", diagnosticID(string(types.SandboxWeakened)), types.RelationEmits, ProvenanceRuntime),
		"the reassembled runtime edge reaches the stored graph")
}

func TestEnsureReassemblesAClassWhoseShardChangedUnderTheManifest(t *testing.T) {
	cacheDir, in := stampFixture(t)
	ensure(t, cacheDir, stampsAll("v1"), AllClasses, in)
	path := filepath.Join(StoreDir(cacheDir), "shards", shardSlug("pkg/a")+".json")
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	var sf shardFile
	require.NoError(t, json.Unmarshal(b, &sf))
	sf.Fingerprint = "written-by-another-build"
	b, err = json.Marshal(sf)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, b, 0o644))

	_, asked := ensure(t, cacheDir, stampsAll("v1"), DefaultClasses, in)

	assert.Equal(t, []ShardClass{ClassDomain}, asked)
}

func TestEnsureRefreshReassemblesEveryWantedClass(t *testing.T) {
	cacheDir, in := stampFixture(t)
	ensure(t, cacheDir, stampsAll("v1"), AllClasses, in)

	var asked []ShardClass
	_, err := Ensure(context.Background(), cacheDir, BuildOptions{Stamps: stampsAll("v1"), Refresh: true}, AllClasses,
		func(stale []ShardClass) (Inputs, error) { asked = stale; return in, nil }, nil)

	require.NoError(t, err)
	assert.ElementsMatch(t, AllClasses, asked)
}

func TestEnsureWithoutAStampNeverMatches(t *testing.T) {
	cacheDir, in := stampFixture(t)
	ensure(t, cacheDir, stampsAll("v1"), AllClasses, in)
	stamps := stampsAll("v1")
	stamps[ClassDomain] = ""

	_, asked := ensure(t, cacheDir, stamps, DefaultClasses, in)

	assert.Equal(t, []ShardClass{ClassDomain}, asked)
}

// The session overlay resolves contacts against every class's nodes; rebuilt alone, it
// must still see the file node only a stored symbols shard holds.
func TestEnsureSessionAloneResolvesAgainstStoredShards(t *testing.T) {
	cacheDir, in := stampFixture(t)
	ensure(t, cacheDir, stampsAll("v1"), AllClasses, in)
	stamps := stampsAll("v1")
	stamps[ClassSession] = "v2session"
	in.AgentContacts = []AgentContact{{Session: "s1", Path: "pkg/a/a.go", Read: true}, {Session: "s1", Path: "nowhere.go", Read: true}}

	_, asked := ensure(t, cacheDir, stamps, LazyClasses, in)

	require.Equal(t, []ShardClass{ClassSession}, asked)
	g := NewGraph()
	require.NoError(t, NewStore(cacheDir, false, 0, nil, nil).MergeSymbolShards(context.Background(), g))
	n, ok := g.node(fileID("pkg/a/a.go"))
	require.True(t, ok)
	assert.Equal(t, "1", n.Attrs[AttrAgentReads])
}

// The session store moves on every agent event, so a symbol read must not rebuild @session
// for it: only the symbol and coverage classes are brought up to date.
func TestEnsureSymbolClassesLeavesAStaleSessionAlone(t *testing.T) {
	cacheDir, in := stampFixture(t)
	in.AgentContacts = []AgentContact{{Session: "s1", Path: "pkg/a/a.go", Read: true}}
	ensure(t, cacheDir, stampsAll("v1"), AllClasses, in)
	stamps := stampsAll("v1")
	stamps[ClassSession] = "a session-store append"

	_, asked := ensure(t, cacheDir, stamps, SymbolClasses, in)

	assert.Nil(t, asked, "nothing a symbol read needs moved")
	assert.Equal(t, "v1session", readManifest(t, cacheDir).Inputs[ClassSession], "the stored @session stays as it was")
}

// @session merges onto nodes the graph already has and mints none, so an overlay built
// before a file was deleted cannot bring the file back.
func TestMergeOverlayPutsSessionOnlyOnExistingNodes(t *testing.T) {
	g := NewGraph()
	g.AddNode(types.KnowledgeNode{ID: fileID("kept.go"), Kind: types.KindFile, Label: "kept.go"})
	session := Shard{Name: sessionShardName, Nodes: []types.KnowledgeNode{
		{ID: fileID("kept.go"), Kind: types.KindFile, Attrs: map[string]string{AttrAgentReads: "2"}},
		{ID: fileID("deleted.go"), Kind: types.KindFile, Attrs: map[string]string{AttrAgentReads: "1"}},
	}}

	mergeOverlay(g, session)

	n, _ := g.node(fileID("kept.go"))
	assert.Equal(t, "2", n.Attrs[AttrAgentReads])
	_, minted := g.node(fileID("deleted.go"))
	assert.False(t, minted)
}

func TestTreeWalkDigestMovesWithTheFilesTheScansRead(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	write("docs/a.md", "# a\n")
	write("lib/x.buzz", "fn x() > void {}\n")
	base := WalkTree(root).Digest()
	assert.Equal(t, base, WalkTree(root).Digest(), "an unchanged tree digests alike")

	write("node_modules/dep/README.md", "# vendored\n")
	assert.Equal(t, base, WalkTree(root).Digest(), "a directory every scan skips is not part of the tree")

	write("docs/a.md", "# b\n")
	edited := WalkTree(root).Digest()
	assert.NotEqual(t, base, edited, "an edit inside the racy window is caught by content")

	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(root, "docs", "a.md"), old, old))
	settled := WalkTree(root).Digest()
	write("docs/a.md", "# c\n")
	require.NoError(t, os.Chtimes(filepath.Join(root, "docs", "a.md"), old.Add(time.Second), old.Add(time.Second)))
	assert.NotEqual(t, settled, WalkTree(root).Digest(), "a settled edit is caught by mtime")

	write("docs/new.md", "# new\n")
	w := WalkTree(root)
	assert.True(t, w.Contains("docs/new.md"))
	assert.False(t, w.Contains("node_modules/dep/README.md"))
}

// One walk serves every scan, and the first scan's ignore query answers for all of them.
func TestTreeWalkScansShareOneWalkAndOneIgnoreQuery(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = root
	require.NoError(t, cmd.Run())
	write(".gitignore", "dist/\n*.out.md\n")
	write("README.md", "# readme\n")
	write("notes.out.md", "# ignored\n")
	write("lib/x.buzz", "fn x() > void {}\n")
	write("lib/x.go", "package lib\n")
	write("lib/x_test.go", "package lib\n")
	write("gen/g.go", "package gen\n")
	write("dist/bundle.go", "package dist\n")
	write("testdata/fixture.buzz", "fn f() > void {}\n")

	w := WalkTree(root)
	docs := w.docFiles("")
	require.Len(t, w.ignored, len(w.files), "the first scan asked about every walked file at once")
	asked := len(w.ignored)

	assert.Equal(t, []string{"README.md"}, docs)
	assert.Equal(t, []string{"lib/x.buzz"}, w.buzzFiles())
	assert.Equal(t, []string{"lib/x.buzz", "lib/x.go"}, w.commentSources())
	assert.Equal(t, []string{"gen/g.go", "lib/x.go"}, w.packageSources())
	assert.Equal(t, []string{".gitignore", "README.md", "gen/g.go", "lib/x.buzz", "lib/x.go"}, w.markerSources(nil))
	assert.Len(t, w.ignored, asked, "no later scan asked again")

	assert.Equal(t, findDocFiles(root, ""), docs, "the shared walk answers as the standalone scan does")
	assert.Equal(t, findBuzzFiles(root), w.buzzFiles())
	assert.Equal(t, findCommentSources(root), w.commentSources())
	assert.Equal(t, findPackageSources(root), w.packageSources())
}

func TestInputHashPathSeesAbsenceAndChange(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "runtime.json")
	sum := func() string { h := NewInputHash("t"); h.Path(p); return h.Sum() }
	absent := sum()
	require.NoError(t, os.WriteFile(p, []byte("[]"), 0o644))
	present := sum()
	assert.NotEqual(t, absent, present)
	require.NoError(t, os.WriteFile(p, []byte("[1]"), 0o644))
	assert.NotEqual(t, present, sum())
}
