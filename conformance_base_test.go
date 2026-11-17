package magus

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/internal/symbols"
	"github.com/egladman/magus/internal/symbols/buzzindex"
	"github.com/egladman/magus/internal/symbols/goindex"
	"github.com/egladman/magus/internal/symbols/shindex"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

type ConformanceBaseSuite struct {
	suite.Suite
	plan baseIndexPlan
}

func TestConformanceBaseSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(ConformanceBaseSuite))
}

func (s *ConformanceBaseSuite) SetupTest() {
	s.plan = baseIndexPlan{external: []baseExternal{
		{dir: "console", language: "typescript", extensions: []string{".ts", ".tsx"}},
	}}
}

func (s *ConformanceBaseSuite) TestAFileOfAnExternalLanguageIsUnmeasured() {
	lang, ok := s.plan.unmeasured("console/src/app.ts")
	s.True(ok)
	s.Equal("typescript", lang)
}

func (s *ConformanceBaseSuite) TestAnotherLanguageInTheSameProjectIsMeasured() {
	_, ok := s.plan.unmeasured("console/magusfile.buzz")
	s.False(ok)
}

func (s *ConformanceBaseSuite) TestAFileOutsideTheProjectIsMeasured() {
	_, ok := s.plan.unmeasured("consoles/app.ts")
	s.False(ok)
}

func (s *ConformanceBaseSuite) TestTheRootProjectHoldsEveryFile() {
	s.plan.external[0].dir = "."
	_, ok := s.plan.unmeasured("deep/x.tsx")
	s.True(ok)
}

func (s *ConformanceBaseSuite) TestAProjectTheBaseLacksIsNamedAndNotIndexed() {
	tree := s.T().TempDir()
	s.Require().NoError(os.MkdirAll(filepath.Join(tree, "libs", "old"), 0o700))
	s.plan.indexes = []baseIndex{{project: "libs/old", language: "go"}, {project: "libs/new", language: "go"}}

	present, absent := s.plan.splitAbsent(tree)

	s.Equal(baseIndexPlan{indexes: []baseIndex{{project: "libs/old", language: "go"}}, external: s.plan.external}, present)
	s.Equal([]types.ConformanceUnmeasured{{File: "libs/new", Language: "go", Reason: projectNewAtHead}}, absent)
}

func (s *ConformanceBaseSuite) TestAFileInAnAbsentProjectReportsItsReason() {
	absent := []types.ConformanceUnmeasured{{File: "libs/new", Language: "go", Reason: projectNewAtHead}}

	project, ok := insideAbsentProject(absent, "libs/new/a.go")

	s.True(ok)
	s.Equal(projectNewAtHead, project.Reason)
}

func (s *ConformanceBaseSuite) TestAFileBesideAnAbsentProjectIsJudged() {
	absent := []types.ConformanceUnmeasured{{File: "libs/new", Language: "go", Reason: projectNewAtHead}}

	_, ok := insideAbsentProject(absent, "libs/newer/a.go")

	s.False(ok)
}

func (s *ConformanceBaseSuite) TestGoRunsInProcessWithItsPatterns() {
	ix := inProcessIndexer(&spells.SymbolIndexer{Format: spells.SymbolFormatGoPackages}, "/t/p", "/t")
	got, ok := ix.(*goindex.Indexer)
	s.Require().True(ok)
	s.Equal("/t/p", got.Dir)
	s.Equal([]string{"."}, got.Patterns)
}

func (s *ConformanceBaseSuite) TestGoIsHandedTheTreeAsATrustedMiseConfig() {
	env := trustRoot([]string{"A=1", "MISE_TRUSTED_CONFIG_PATHS=/ws"}, "/t")

	s.Equal([]string{"A=1", "MISE_TRUSTED_CONFIG_PATHS=/ws" + string(os.PathListSeparator) + "/t"}, env)
}

func (s *ConformanceBaseSuite) TestGoIndexerEnvTrustsTheTree() {
	ix := inProcessIndexer(&spells.SymbolIndexer{Format: spells.SymbolFormatGoPackages}, "/t/p", "/t")

	var trusted []string
	for _, kv := range ix.(*goindex.Indexer).Env {
		if strings.HasPrefix(kv, "MISE_TRUSTED_CONFIG_PATHS=") {
			trusted = append(trusted, kv)
		}
	}
	s.Require().Len(trusted, 1)
	s.True(strings.HasSuffix(trusted[0], "/t"))
}

func (s *ConformanceBaseSuite) TestTheTreeIsTrustedAloneWhenNothingIsInherited() {
	s.Equal([]string{"A=1", "MISE_TRUSTED_CONFIG_PATHS=/t"}, trustRoot([]string{"A=1"}, "/t"))
}

func (s *ConformanceBaseSuite) TestBuzzAndShellRunInProcessUnderTheBaseRoot() {
	s.Equal(&buzzindex.Indexer{Dir: "/t/p", WorkspaceRoot: "/t"},
		inProcessIndexer(&spells.SymbolIndexer{Format: spells.SymbolFormatBuzzModule}, "/t/p", "/t"))
	s.Equal(&shindex.Indexer{Dir: "/t/p", WorkspaceRoot: "/t"},
		inProcessIndexer(&spells.SymbolIndexer{Format: spells.SymbolFormatShellScript}, "/t/p", "/t"))
}

func (s *ConformanceBaseSuite) TestAnExternalFormatHasNoInProcessIndexer() {
	s.Nil(inProcessIndexer(&spells.SymbolIndexer{Format: "scip"}, "/t/p", "/t"))
}

func (s *ConformanceBaseSuite) TestTheBaseTreeIsMadeInThePoliciesPrivateTempDir() {
	private := s.T().TempDir()
	ctx := sandbox.WithPolicy(s.T().Context(), &sandbox.Policy{TempDir: private})

	tree, err := conformanceTree(ctx)
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = os.RemoveAll(tree) })

	s.Equal(private, filepath.Dir(tree))
}

// versioned is an indexer known only by its version.
type versioned string

func (v versioned) Version() string { return string(v) }

func (versioned) Units(context.Context) ([]symbols.Unit, error) { return nil, nil }

func (versioned) Index(context.Context, []symbols.Unit) ([]symbols.Shard, error) {
	return nil, nil
}

// seed hands seedPack a base pack holding body, written by an indexer at base,
// for head's indexer at headVersion. It returns where head keeps the index.
func (s *ConformanceBaseSuite) seed(base, headVersion, body string, readOnly bool) (string, bool, error) {
	tree := s.T().TempDir()
	pack := filepath.Join(tree, "0.pack")
	s.Require().NoError(os.WriteFile(pack, []byte(body), 0o600))
	dest := filepath.Join(s.T().TempDir(), "symbols", "p", "scip.pack")
	b := baseIndex{project: "p", op: "scip", ix: versioned(base)}
	head := baseIndexPlan{indexes: []baseIndex{{project: "p", op: "scip", ix: versioned(headVersion)}}}
	seeded, err := seedPack(tree, pack, dest, b, head, readOnly)
	return dest, seeded, err
}

func (s *ConformanceBaseSuite) TestASeedLandsTheBasePacksBytes() {
	dest, seeded, err := s.seed("goindex 4", "goindex 4", "pack", false)
	s.Require().NoError(err)
	s.True(seeded)
	got, err := os.ReadFile(dest)
	s.Require().NoError(err)
	s.Equal("pack", string(got))
}

func (s *ConformanceBaseSuite) TestASeedLeavesNoTemporaryFileBehind() {
	dest, _, err := s.seed("goindex 4", "goindex 4", "pack", false)
	s.Require().NoError(err)
	entries, err := os.ReadDir(filepath.Dir(dest))
	s.Require().NoError(err)
	s.Len(entries, 1)
}

func (s *ConformanceBaseSuite) TestAPackOfAnotherIndexerVersionIsNotSeeded() {
	dest, seeded, err := s.seed("goindex 4 go1.25", "goindex 4 go1.26", "pack", false)
	s.Require().ErrorContains(err, `the base tree's indexer is "goindex 4 go1.25" and head's is "goindex 4 go1.26"`)
	s.False(seeded)
	s.NoFileExists(dest)
}

func (s *ConformanceBaseSuite) TestAReadOnlyCacheIsNotSeeded() {
	dest, seeded, err := s.seed("goindex 4", "goindex 4", "pack", true)
	s.Require().ErrorContains(err, "read-only")
	s.False(seeded)
	s.NoFileExists(dest)
}

func (s *ConformanceBaseSuite) TestAnIndexHeadLacksIsNotSeeded() {
	tree := s.T().TempDir()
	b := baseIndex{project: "p", op: "scip", ix: versioned("v")}
	_, err := seedPack(tree, filepath.Join(tree, "0.pack"), filepath.Join(tree, "dest.pack"), b, baseIndexPlan{}, false)
	s.ErrorContains(err, "head declares no scip index of p")
}

// A shard naming the base tree points readers at a removed directory.
func (s *ConformanceBaseSuite) TestAPackNamingTheBaseTreeIsNotSeeded() {
	tree := s.T().TempDir()
	pack := filepath.Join(tree, "0.pack")
	s.Require().NoError(os.WriteFile(pack, []byte("doc "+filepath.Join(tree, "a.go")), 0o600))
	dest := filepath.Join(s.T().TempDir(), "scip.pack")
	b := baseIndex{project: "p", op: "scip", ix: versioned("v")}
	head := baseIndexPlan{indexes: []baseIndex{b}}

	seeded, err := seedPack(tree, pack, dest, b, head, false)

	s.Require().ErrorContains(err, "its shards name the base tree's path")
	s.False(seeded)
	s.NoFileExists(dest)
}

// Head's indexer may write its pack while the base is measured.
func (s *ConformanceBaseSuite) TestASeedNeverReplacesAnIndexHeadWrote() {
	tree := s.T().TempDir()
	pack := filepath.Join(tree, "0.pack")
	s.Require().NoError(os.WriteFile(pack, []byte("base"), 0o600))
	dest := filepath.Join(s.T().TempDir(), "scip.pack")
	s.Require().NoError(os.WriteFile(dest, []byte("head"), 0o600))
	b := baseIndex{project: "p", op: "scip", ix: versioned("v")}

	seeded, err := seedPack(tree, pack, dest, b, baseIndexPlan{indexes: []baseIndex{b}}, false)

	s.Require().NoError(err)
	s.False(seeded)
	got, err := os.ReadFile(dest)
	s.Require().NoError(err)
	s.Equal("head", string(got))
}

// keep writes measure as the line kept under the want key.
func (s *ConformanceBaseSuite) keep(measure string) string {
	name := s.want().file()
	entry := filepath.Join(s.T().TempDir(), name)
	line := fmt.Sprintf("{\"name\":%q,\"measure\":%s}\n", name, measure)
	s.Require().NoError(os.WriteFile(entry, []byte(line), 0o600))
	return entry
}

func (s *ConformanceBaseSuite) TestAnEntryKeptUnderTheOldKeyIsRefused() {
	entry := s.keep(`{"rows":[],"answer":{"verdict":"found"}}`)

	_, ok, err := readConformanceEntry(entry)

	s.False(ok)
	s.Require().Error(err)
	s.Contains(err.Error(), entry)
	s.Contains(err.Error(), "magus run conformance-base .")
}

func (s *ConformanceBaseSuite) TestAnEntryWithNeitherKeyIsRefused() {
	entry := s.keep(`{"answer":{"verdict":"absent"}}`)

	_, ok, err := readConformanceEntry(entry)

	s.False(ok)
	s.Require().Error(err)
	s.Contains(err.Error(), entry)
	s.Contains(err.Error(), "magus run conformance-base .")
}

func (s *ConformanceBaseSuite) TestAnEntryWithDistributionsIsRead() {
	entry := s.keep(`{"distributions":[{"key":{"metric":"file-split"},"n":1}],"answer":{"verdict":"found"}}`)

	got, ok, err := readConformanceEntry(entry)

	s.Require().NoError(err)
	s.True(ok)
	s.Len(got.Distributions, 1)
}

// A cache restore can bring another commit's measure under this one's name.
func (s *ConformanceBaseSuite) TestAnEntryNamingAnotherKeyIsRefused() {
	entry := s.keep(`{"distributions":[{"key":{"metric":"file-split"},"n":1}],"answer":{"verdict":"found"}}`)
	other := filepath.Join(filepath.Dir(entry), conformanceKey{commit: "def456", version: "8", policy: "p1", indexers: "i1"}.file())
	s.Require().NoError(os.Rename(entry, other))

	_, ok, err := readConformanceEntry(other)

	s.False(ok)
	s.Require().Error(err)
	s.Contains(err.Error(), s.want().file())
	s.Contains(err.Error(), "magus run conformance-base .")
}

func (s *ConformanceBaseSuite) TestAnEntryBeforeTheNameWasKeptIsRefused() {
	entry := filepath.Join(s.T().TempDir(), s.want().file())
	s.Require().NoError(os.WriteFile(entry, []byte(`{"distributions":[{"n":1}],"answer":{"verdict":"found"}}`+"\n"), 0o600))

	_, ok, err := readConformanceEntry(entry)

	s.False(ok)
	s.Require().Error(err)
	s.Contains(err.Error(), entry)
}

func (s *ConformanceBaseSuite) TestAWrittenEntryReadsBack() {
	entry := filepath.Join(s.T().TempDir(), "conformance", s.want().file())
	want := types.Conformance{
		Distributions: []types.ConformanceDistribution{{N: 1}},
		Answer:        types.KnowledgeAnswer{Verdict: types.VerdictFound},
	}
	s.Require().NoError(writeConformanceEntry(entry, want))

	got, ok, err := readConformanceEntry(entry)

	s.Require().NoError(err)
	s.True(ok)
	s.Equal(want, got)
}

// oldLayoutName is a measure named by one digest of its whole key.
const oldLayoutName = "abc123-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.jsonl"

func (s *ConformanceBaseSuite) TestAKeyReadsBackFromItsFileName() {
	key := conformanceKey{commit: "abc123", version: "7", policy: "p1", indexers: "i1"}

	got, ok := parseConformanceKey(key.file())

	s.True(ok)
	s.Equal(key, got)
}

func (s *ConformanceBaseSuite) TestAnOldLayoutNameIsNoKey() {
	_, ok := parseConformanceKey(oldLayoutName)

	s.False(ok)
}

// remeasure is whyRemeasure's answer for want with names kept beside it.
func (s *ConformanceBaseSuite) remeasure(want conformanceKey, names ...string) string {
	dir := s.T().TempDir()
	for _, name := range names {
		s.Require().NoError(os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o600))
	}
	return whyRemeasure(filepath.Join(dir, want.file()))
}

func (s *ConformanceBaseSuite) want() conformanceKey {
	return conformanceKey{commit: "abc123", version: "7", policy: "p1", indexers: "i1"}
}

func (s *ConformanceBaseSuite) TestNothingKeptSaysSo() {
	other := conformanceKey{commit: "def456", version: "7", policy: "p1", indexers: "i1"}

	s.Equal("no measure of it is kept", s.remeasure(s.want(), other.file()))
}

func (s *ConformanceBaseSuite) TestAnotherMeasureVersionIsNamed() {
	old := s.want()
	old.version = "6"

	s.Equal("the kept measure of it is measure version 6, and this magus measures version 7",
		s.remeasure(s.want(), old.file()))
}

func (s *ConformanceBaseSuite) TestAnotherPolicyIsNamed() {
	other := s.want()
	other.policy = "p2"

	s.Contains(s.remeasure(s.want(), other.file()), "another knowledge.conformance policy")
}

func (s *ConformanceBaseSuite) TestOtherIndexerVersionsAreNamed() {
	other := s.want()
	other.indexers = "i2"

	s.Contains(s.remeasure(s.want(), other.file()), "other indexer versions")
}

func (s *ConformanceBaseSuite) TestAnOlderLayoutIsNamed() {
	s.Contains(s.remeasure(s.want(), oldLayoutName), "older key layout")
}

func (s *ConformanceBaseSuite) TestEachDistinctMismatchIsNamed() {
	policy, indexers := s.want(), s.want()
	policy.policy, indexers.indexers = "p2", "i2"

	got := s.remeasure(s.want(), policy.file(), indexers.file())

	s.Contains(got, "policy")
	s.Contains(got, "indexer versions")
}

type ConformanceBaseBoxSuite struct {
	suite.Suite
}

func TestConformanceBaseBoxSuite(t *testing.T) {
	suite.Run(t, new(ConformanceBaseBoxSuite))
}

const boxMagusfile = "import \"magus\";\nexport fun build(ctx: magus\\Context, args: [str]) > void {}\n"

func (s *ConformanceBaseBoxSuite) inspect(root string, cfg config.Config) *Magus {
	return inspectBuzzWorkspace(s.T(), root, cfg)
}

// inspectBuzzWorkspace is root as one Buzz project, inspected with no build
// cache.
func inspectBuzzWorkspace(t *testing.T, root string, cfg config.Config) *Magus {
	t.Helper()
	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell("buzz"))
	inspected, err := Inspect(t.Context(), root, WithWorkspaceRegistry(reg), WithSpawnRule(tier.Ceiling), workspace.WithLoadedConfig(cfg))
	require.NoError(t, err)
	m := inspected.(*Magus)
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func (s *ConformanceBaseBoxSuite) TestMeasureWritesOnlyInsideTheBox() {
	denied := filepath.Join(s.T().TempDir(), "denied")
	s.Require().NoError(os.Mkdir(denied, 0o500))
	s.T().Setenv("XDG_CACHE_HOME", denied)
	s.T().Setenv("HOME", denied)
	granted := filepath.Join(s.T().TempDir(), "granted")
	cfg := config.Defaults()
	cfg.Cache.Dir = granted

	repo := testkit.NewRepository(s.T(), map[string]string{"magusfile.buzz": boxMagusfile})
	m := s.inspect(repo.Root(), cfg)
	commit, err := m.MeasureConformanceBase(s.T().Context(), "HEAD")
	s.Require().NoError(err)

	kept, err := filepath.Glob(filepath.Join(granted, "conformance", commit+"-*.jsonl"))
	s.Require().NoError(err)
	s.Len(kept, 1)
	s.NoDirExists(filepath.Join(denied, "magus"))
}

func (s *ConformanceBaseBoxSuite) TestCheckoutsOfOneWorkspaceReadOneMeasure() {
	s.T().Setenv("XDG_CACHE_HOME", s.T().TempDir())
	repo := testkit.NewRepository(s.T(), map[string]string{"magusfile.buzz": boxMagusfile})
	sibling := repo.AddWorktree("sibling")

	first := s.inspect(repo.Root(), config.Defaults())
	second := s.inspect(sibling.Root(), config.Defaults())
	s.NotEqual(first.CacheDir(), second.CacheDir())

	commit, err := first.MeasureConformanceBase(s.T().Context(), "HEAD")
	s.Require().NoError(err)

	plan, err := second.baseIndexPlan(s.T().Context(), second.Root())
	s.Require().NoError(err)
	entry, err := second.conformanceEntry(commit, plan)
	s.Require().NoError(err)
	_, kept, err := readConformanceEntry(entry)
	s.Require().NoError(err)
	s.True(kept, "the sibling checkout finds the measure the first one kept")
}

// CI's gate checks out at a new path in each job.
// The cache dir is the one path the jobs share.
func (s *ConformanceBaseBoxSuite) TestACheckoutAtAnotherPathReusesTheCacheDirsMeasure() {
	cfg := config.Defaults()
	cfg.Cache.Dir = s.T().TempDir()
	repo := testkit.NewRepository(s.T(), map[string]string{"magusfile.buzz": boxMagusfile})
	elsewhere := repo.AddWorktree("elsewhere")
	commit, err := s.inspect(repo.Root(), cfg).MeasureConformanceBase(s.T().Context(), "HEAD")
	s.Require().NoError(err)
	kept, err := filepath.Glob(filepath.Join(cfg.Cache.Dir, "conformance", commit+"-*.jsonl"))
	s.Require().NoError(err)
	s.Require().Len(kept, 1)
	stamp := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	s.Require().NoError(os.Chtimes(kept[0], stamp, stamp))

	again, err := s.inspect(elsewhere.Root(), cfg).MeasureConformanceBase(s.T().Context(), "HEAD")
	s.Require().NoError(err)

	s.Equal(commit, again)
	info, err := os.Stat(kept[0])
	s.Require().NoError(err)
	s.True(info.ModTime().Equal(stamp), "the second checkout reused the kept measure rather than measuring again")
}

const (
	boxGreet = "export fun greet() > str {\n    return \"hi\";\n}\n"
	boxOther = "export fun other() > int {\n    return 1;\n}\n"
)

// measureEditedRepository measures the base of a repository whose head
// edited one of its Buzz files. It returns head's index and where head keeps
// its pack.
func (s *ConformanceBaseBoxSuite) measureEditedRepository(before func(head baseIndex, pack string)) (baseIndex, string) {
	cfg := config.Defaults()
	cfg.Cache.Dir = s.T().TempDir()
	repo := testkit.NewRepository(s.T(), map[string]string{
		"magusfile.buzz": boxMagusfile, "lib/greet.buzz": boxGreet, "lib/other.buzz": boxOther,
	})
	repo.Commit("edit", map[string]string{"lib/greet.buzz": strings.Replace(boxGreet, "hi", "hello", 1)})
	m := s.inspect(repo.Root(), cfg)
	plan, err := m.baseIndexPlan(s.T().Context(), m.Root())
	s.Require().NoError(err)
	s.Require().Len(plan.indexes, 1)
	head := plan.indexes[0]
	pack := symbols.IndexPath(m.CacheDir(), head.project, head.op)
	before(head, pack)

	_, err = m.MeasureConformanceBase(s.T().Context(), "HEAD~1")
	s.Require().NoError(err)
	return head, pack
}

func (s *ConformanceBaseBoxSuite) read(path string) []byte {
	data, err := os.ReadFile(path)
	s.Require().NoError(err)
	return data
}

// The seed is the base's pack, so head's freshen re-indexes the edited file
// alone and writes what a cold index of head writes.
func (s *ConformanceBaseBoxSuite) TestASeededIndexFreshensToTheColdIndex() {
	head, seeded := s.measureEditedRepository(func(baseIndex, string) {})
	s.Require().FileExists(seeded)

	res, err := symbols.UpdatePack(s.T().Context(), seeded, head.ix)
	s.Require().NoError(err)
	cold := filepath.Join(s.T().TempDir(), "cold.pack")
	_, err = symbols.UpdatePack(s.T().Context(), cold, head.ix)
	s.Require().NoError(err)

	s.False(res.Rebuilt)
	s.Equal([]string{"lib/greet.buzz"}, res.Indexed)
	s.Equal(s.read(cold), s.read(seeded))
}

func (s *ConformanceBaseBoxSuite) TestHeadsOwnIndexIsNotSeeded() {
	_, pack := s.measureEditedRepository(func(_ baseIndex, pack string) {
		s.Require().NoError(os.MkdirAll(filepath.Dir(pack), 0o755))
		s.Require().NoError(os.WriteFile(pack, []byte("head's own"), 0o600))
	})
	s.Equal("head's own", string(s.read(pack)))
}

type ConformanceBaseRemoteSuite struct {
	suite.Suite
	backend    *cache.FSRemoteBackend
	backendDir string
	pub        ed25519.PublicKey
	seed       []byte
	root       string
	logs       *bytes.Buffer
}

// TestConformanceBaseRemoteSuite is serial: it swaps the default logger.
func TestConformanceBaseRemoteSuite(t *testing.T) {
	suite.Run(t, new(ConformanceBaseRemoteSuite))
}

func (s *ConformanceBaseRemoteSuite) SetupTest() {
	s.backendDir = s.T().TempDir()
	backend, err := cache.NewFSRemoteBackend(s.backendDir)
	s.Require().NoError(err)
	s.backend = backend
	s.pub, s.seed = s.keypair()
	s.root = testkit.NewRepository(s.T(), map[string]string{
		"magusfile.buzz": boxMagusfile, "lib/greet.buzz": boxGreet, "lib/other.buzz": boxOther,
	}).Root()
	s.logs = &bytes.Buffer{}
	prior := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(s.logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	s.T().Cleanup(func() { slog.SetDefault(prior) })
}

func (s *ConformanceBaseRemoteSuite) keypair() (ed25519.PublicKey, []byte) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	s.Require().NoError(err)
	return pub, priv.Seed()
}

// workspace is the repository with a fresh local store, its build cache
// opened with opts, or none when opts is nil.
func (s *ConformanceBaseRemoteSuite) workspace(opts ...cache.Option) *Magus {
	cfg := config.Defaults()
	cfg.Cache.Dir = s.T().TempDir()
	m := inspectBuzzWorkspace(s.T(), s.root, cfg)
	if opts != nil {
		c, err := cache.Open(s.T().Context(), filepath.Join(s.T().TempDir(), ".magus"), opts...)
		s.Require().NoError(err)
		m.cache = c
	}
	return m
}

// reader may read the remote tier, and write nothing to it.
func (s *ConformanceBaseRemoteSuite) reader() *Magus {
	return s.workspace(cache.WithRemoteBackend(s.backend), cache.WithTrustedKeys([][]byte{s.pub}))
}

// writer may write the remote tier, signing with seed.
func (s *ConformanceBaseRemoteSuite) writer(pub ed25519.PublicKey, seed []byte) *Magus {
	return s.workspace(cache.WithRemoteBackend(s.backend), cache.WithTrustedKeys([][]byte{pub}), cache.WithSigningKey(seed))
}

// entry is where m keeps the measure of HEAD.
func (s *ConformanceBaseRemoteSuite) entry(m *Magus) string {
	_, commit, err := m.conformanceBaseCommit(s.T().Context(), "HEAD")
	s.Require().NoError(err)
	plan, err := m.baseIndexPlan(s.T().Context(), m.Root())
	s.Require().NoError(err)
	entry, err := m.conformanceEntry(commit, plan)
	s.Require().NoError(err)
	return entry
}

// planted is a measure line no cold measure of the repository gives.
func (s *ConformanceBaseRemoteSuite) planted(entry string) []byte {
	kept := filepath.Join(s.T().TempDir(), filepath.Base(entry))
	s.Require().NoError(writeConformanceEntry(kept, types.Conformance{
		Distributions: []types.ConformanceDistribution{{N: 4242}},
		Answer:        types.KnowledgeAnswer{Verdict: types.VerdictFound},
	}))
	data, err := os.ReadFile(kept)
	s.Require().NoError(err)
	return data
}

// plant stores data as m's measure of HEAD through a writer signing with seed.
func (s *ConformanceBaseRemoteSuite) plant(entry string, pub ed25519.PublicKey, seed []byte, data []byte) {
	w := s.writer(pub, seed)
	ns := w.cache.RemoteNamespace(conformanceRemoteNamespace)
	s.Require().NoError(ns.Put(s.T().Context(), filepath.Base(entry), bytes.NewReader(data)))
}

// measure runs MeasureConformanceBase on m and returns the measure it kept.
func (s *ConformanceBaseRemoteSuite) measure(m *Magus) types.Conformance {
	_, err := m.MeasureConformanceBase(s.T().Context(), "HEAD")
	s.Require().NoError(err)
	got, ok, err := readConformanceEntry(s.entry(m))
	s.Require().NoError(err)
	s.Require().True(ok)
	return got
}

// isPlanted reports whether c is the measure planted gives.
func isPlanted(c types.Conformance) bool {
	return len(c.Distributions) == 1 && c.Distributions[0].N == 4242
}

// pair is a gzip tar of members, the layout the remote tier signs.
func (s *ConformanceBaseRemoteSuite) pair(members ...[2]string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, m := range members {
		s.Require().NoError(tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: m[0], Size: int64(len(m[1])), Mode: 0o644}))
		_, err := tw.Write([]byte(m[1]))
		s.Require().NoError(err)
	}
	s.Require().NoError(tw.Close())
	s.Require().NoError(gz.Close())
	return buf.Bytes()
}

// members reads the gzip tar stored under key.
func (s *ConformanceBaseRemoteSuite) members(key string) [][2]string {
	rc, err := s.backend.GetArtifact(s.T().Context(), conformanceRemoteNamespace, key)
	s.Require().NoError(err)
	defer rc.Close()
	gz, err := gzip.NewReader(rc)
	s.Require().NoError(err)
	tr := tar.NewReader(gz)
	var out [][2]string
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		s.Require().NoError(err)
		data, err := io.ReadAll(tr)
		s.Require().NoError(err)
		out = append(out, [2]string{h.Name, string(data)})
	}
}

// overwrite replaces what the backend stores under key, which its Put
// refuses to do.
func (s *ConformanceBaseRemoteSuite) overwrite(key string, data []byte) {
	found, err := filepath.Glob(filepath.Join(s.backendDir, "*", key+".tar.gz"))
	s.Require().NoError(err)
	s.Require().Len(found, 1)
	s.Require().NoError(os.WriteFile(found[0], data, 0o600))
}

func (s *ConformanceBaseRemoteSuite) TestASignedMeasureIsUsed() {
	m := s.reader()
	entry := s.entry(m)
	s.plant(entry, s.pub, s.seed, s.planted(entry))

	got := s.measure(m)

	s.True(isPlanted(got), "the signed measure was kept, not a cold one")
	s.Contains(s.logs.String(), "reusing the signed conformance measure of base")
}

func (s *ConformanceBaseRemoteSuite) TestASignedMeasureSeedsHeadsPacks() {
	s.measure(s.writer(s.pub, s.seed))
	m := s.reader()
	plan, err := m.baseIndexPlan(s.T().Context(), m.Root())
	s.Require().NoError(err)
	s.Require().Len(plan.indexes, 1)

	s.measure(m)

	s.FileExists(symbols.IndexPath(m.CacheDir(), plan.indexes[0].project, plan.indexes[0].op))
	s.Contains(s.logs.String(), "seeded head's symbol index from the remote tier's signed pack")
}

func (s *ConformanceBaseRemoteSuite) TestAnUnsignedMeasureIsMeasuredAgainAndSaysSo() {
	m := s.reader()
	entry := s.entry(m)
	key := filepath.Base(entry)
	meta := fmt.Sprintf(`{"namespace":%q,"key":%q}`, conformanceRemoteNamespace, key)
	unsigned := s.pair([2]string{"object.json", meta}, [2]string{"object", string(s.planted(entry))})
	s.Require().NoError(s.backend.PutArtifact(s.T().Context(), conformanceRemoteNamespace, key, bytes.NewReader(unsigned)))

	got := s.measure(m)

	s.False(isPlanted(got), "the unsigned measure was used")
	s.Contains(s.logs.String(), "is refused, so measuring cold")
	s.Contains(s.logs.String(), entry)
}

func (s *ConformanceBaseRemoteSuite) TestAMeasureSignedByAnUntrustedKeyIsMeasuredAgainAndSaysSo() {
	m := s.reader()
	entry := s.entry(m)
	pub, seed := s.keypair()
	s.plant(entry, pub, seed, s.planted(entry))

	got := s.measure(m)

	s.False(isPlanted(got), "a measure no trusted key signed was used")
	s.Contains(s.logs.String(), "is refused, so measuring cold")
	s.Contains(s.logs.String(), entry)
}

func (s *ConformanceBaseRemoteSuite) TestATamperedMeasureIsMeasuredAgainAndSaysSo() {
	m := s.reader()
	entry := s.entry(m)
	key := filepath.Base(entry)
	s.plant(entry, s.pub, s.seed, s.planted(entry))
	members := s.members(key)
	for i, member := range members {
		if member[0] == "object" {
			members[i][1] = strings.Replace(member[1], "4242", "4243", 1)
		}
	}
	s.overwrite(key, s.pair(members...))

	got := s.measure(m)

	s.NotContains(fmt.Sprint(got.Distributions), "4243", "the tampered measure was used")
	s.Contains(s.logs.String(), "is refused, so measuring cold")
}

func (s *ConformanceBaseRemoteSuite) TestNoRemoteTierIsMeasuredAgainAndSaysSo() {
	s.measure(s.workspace())

	s.Contains(s.logs.String(), "no remote tier is wired")
}

func (s *ConformanceBaseRemoteSuite) TestAnInactiveBackendIsMeasuredAgainAndStoresNothing() {
	m := s.workspace(cache.WithRemoteBackend(inactiveBackend{s.backend}), cache.WithTrustedKeys([][]byte{s.pub}), cache.WithSigningKey(s.seed))

	s.measure(m)

	s.Contains(s.logs.String(), "no remote tier is wired here: its fs backend is not active in this environment")
	s.NotContains(s.logs.String(), "stored the conformance measure")
}

func (s *ConformanceBaseRemoteSuite) TestAColdMeasureIsSignedAndStored() {
	w := s.writer(s.pub, s.seed)
	entry := s.entry(w)

	s.measure(w)

	s.Contains(s.logs.String(), "stored the conformance measure of base")
	data, err := getRemote(s.T().Context(), s.reader().cache.RemoteNamespace(conformanceRemoteNamespace), filepath.Base(entry))
	s.Require().NoError(err)
	kept, err := os.ReadFile(entry)
	s.Require().NoError(err)
	s.Equal(kept, data)
}

func (s *ConformanceBaseRemoteSuite) TestARunThatMayNotWriteStoresNothingAndSaysSo() {
	m := s.reader()

	s.measure(m)

	s.Contains(s.logs.String(), "stored nothing on the remote tier for conformance base")
	ok, err := s.backend.HasArtifact(s.T().Context(), conformanceRemoteNamespace, filepath.Base(s.entry(m)))
	s.Require().NoError(err)
	s.False(ok)
}

// inactiveBackend is a backend its environment turned off.
type inactiveBackend struct{ types.CacheBackend }

func (inactiveBackend) Active(context.Context) bool { return false }
