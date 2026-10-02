package magus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/readlog"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fastStampsOver computes the domain fast stamp of a tree with VCS history disabled, the
// configuration a fast read of a repository with no git would see.
func fastStampsOver(t *testing.T, root, cacheDir string, reads readlog.Reads, known bool) string {
	t.Helper()
	cfg := config.Config{}
	stamps := knowledgeFastStamps(t.Context(), cfg, root, cacheDir, knowledge.WalkTree(root), knowledge.DefaultClasses, reads, known)
	require.NotEmpty(t, stamps[knowledge.ClassDomain])
	return stamps[knowledge.ClassDomain]
}

// TestKnowledgeFastStampsFoldWhatTheEvaluationRead pins the contract that makes a
// fast-settled read exact: every input the last evaluation consulted beyond the tree
// moves the fast stamp, and nothing else does.
func TestKnowledgeFastStampsFoldWhatTheEvaluationRead(t *testing.T) {
	// No VCS: the stamp's VCS inputs are pinned by TestKnowledgeStampsInvalidateExactlyTheirClasses,
	// and a temp dir no repository claims would otherwise ask git about one.
	t.Setenv("MAGUS_VCS_ENABLED", "false")
	root := t.TempDir()
	cacheDir := filepath.Join(t.TempDir(), "cache")
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("// empty\n"), 0o644))
	outside := filepath.Join(t.TempDir(), "site.toml")
	require.NoError(t, os.WriteFile(outside, []byte("region = \"eu\"\n"), 0o644))
	settled := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(outside, settled, settled))

	const name = "MAGUS_TEST_FAST_STAMP_REGION"
	t.Setenv(name, "eu")
	reads := readlog.Reads{Env: []string{name}, Files: []string{outside}}

	base := fastStampsOver(t, root, cacheDir, reads, true)
	assert.Equal(t, base, fastStampsOver(t, root, cacheDir, reads, true), "unchanged inputs stamp alike")

	t.Setenv("MAGUS_TEST_FAST_STAMP_OTHER", "x")
	assert.Equal(t, base, fastStampsOver(t, root, cacheDir, reads, true), "a variable the evaluation never read does not move it")

	t.Setenv(name, "us")
	moved := fastStampsOver(t, root, cacheDir, reads, true)
	assert.NotEqual(t, base, moved, "a variable the evaluation read moves it")
	t.Setenv(name, "eu")
	assert.Equal(t, base, fastStampsOver(t, root, cacheDir, reads, true), "and moving it back restores it")

	require.NoError(t, os.Unsetenv(name))
	assert.NotEqual(t, base, fastStampsOver(t, root, cacheDir, reads, true), "unset is a value of its own")
	t.Setenv(name, "eu")

	require.NoError(t, os.WriteFile(outside, []byte("region = \"us\"\n"), 0o644))
	assert.NotEqual(t, base, fastStampsOver(t, root, cacheDir, reads, true), "a file outside the tree the evaluation read moves it")

	assert.NotEqual(t, fastStampsOver(t, root, cacheDir, readlog.Reads{}, true), fastStampsOver(t, root, cacheDir, readlog.Reads{}, false),
		"an evaluation known to have read nothing and a manifest that never recorded its reads stamp apart")

	all := readlog.Reads{EnvAll: true}
	whole := fastStampsOver(t, root, cacheDir, all, true)
	t.Setenv("MAGUS_TEST_FAST_STAMP_ANY", "1")
	assert.NotEqual(t, whole, fastStampsOver(t, root, cacheDir, all, true), "after a read of the whole environment, every variable moves it")
}

// TestRecordedStaleIndexesAnswerWhileTheirEvidenceHolds pins the other half: a read that
// never opens the workspace reports the stale indexes the last evaluation judged, exactly
// while the sources and the judged index files are unchanged, and refuses to answer the
// moment either moved.
func TestRecordedStaleIndexesAnswerWhileTheirEvidenceHolds(t *testing.T) {
	t.Setenv("MAGUS_VCS_ENABLED", "false") // see TestKnowledgeFastStampsFoldWhatTheEvaluationRead
	root := t.TempDir()
	cacheDir := filepath.Join(t.TempDir(), "cache")
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("// empty\n"), 0o644))
	settled := time.Now().Add(-time.Hour)
	index := filepath.Join(cacheDir, "symbols", "a", "index.scip")
	require.NoError(t, os.MkdirAll(filepath.Dir(index), 0o755))
	require.NoError(t, os.WriteFile(index, []byte("scip"), 0o644))
	require.NoError(t, os.Chtimes(index, settled, settled))
	info, err := os.Stat(index)
	require.NoError(t, err)

	// The read resolves the cache dir from the configuration, as the CLI does, so the
	// configuration must name the store this test builds.
	cfg := config.Config{Cache: config.Cache{Dir: cacheDir}}
	reads := readlog.Reads{}
	decls := []knowledge.SymbolIndexDeclaration{
		{Project: "pkg/a", Dir: filepath.Join(root, "pkg/a"), Language: "go", Path: index,
			Freshness: string(types.SymbolIndexStale), Size: info.Size(), ModTime: info.ModTime().UnixNano()},
		{Project: "pkg/b", Dir: filepath.Join(root, "pkg/b"), Language: "go", Path: filepath.Join(cacheDir, "symbols", "b", "index.scip"),
			Freshness: string(types.SymbolIndexNotBuilt)},
	}
	// The sync an evaluating read performs, with what it records: the declarations with
	// their verdicts, the reads, and fast stamps over them.
	ctx := context.Background()
	in := knowledge.Inputs{
		Graph:   types.TargetGraphOutput{Projects: []types.TargetGraphProject{{Path: "pkg/a"}, {Path: "pkg/b"}}},
		Root:    root,
		Indexes: decls,
		Reads:   &reads,
	}
	_, err = knowledge.Ensure(ctx, cacheDir, knowledge.BuildOptions{
		Root: root,
		FastStampsFunc: func(ctx context.Context, reads readlog.Reads, known bool) knowledge.Stamps {
			return knowledgeFastStamps(ctx, cfg, root, cacheDir, knowledge.WalkTree(root), knowledge.DefaultClasses, reads, known)
		},
		StampsFunc: func(context.Context) (knowledge.Stamps, error) { return knowledge.Stamps{knowledge.ClassDomain: "v1", knowledge.ClassRuntime: "v1"}, nil },
	}, knowledge.DefaultClasses, func([]knowledge.ShardClass) (knowledge.Inputs, error) { return in, nil }, nil)
	require.NoError(t, err)

	opens := 0
	lw := NewLazyWorkspace(root, func(context.Context) (*Magus, error) {
		opens++
		return nil, errors.New("a fast read must not open the workspace")
	})
	stale, ok := RecordedStaleIndexes(ctx, lw, cfg)
	require.True(t, ok, "the recorded verdicts hold: sources and index files are as they were")
	assert.Equal(t, []string{"pkg/a"}, stale)
	assert.Equal(t, 0, opens)

	// The judged index was rebuilt: the verdict is no longer evidence.
	require.NoError(t, os.WriteFile(index, []byte("scip, rebuilt"), 0o644))
	_, ok = RecordedStaleIndexes(ctx, lw, cfg)
	assert.False(t, ok, "a changed index file sends the read back to the workspace")
	require.NoError(t, os.Chtimes(index, settled, settled))
	require.NoError(t, os.WriteFile(index, []byte("scip"), 0o644))
	require.NoError(t, os.Chtimes(index, settled, settled))
	_, ok = RecordedStaleIndexes(ctx, lw, cfg)
	require.True(t, ok, "restored, the verdict holds again")

	// The sources moved: the domain fast stamp no longer matches, so nothing recorded is
	// vouched for. A LazyWorkspace is one read's view of the tree (it walks once), so the
	// next read, like every CLI read, holds a new one.
	before := fastStampsOver(t, root, cacheDir, reads, true)
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("// edited\n"), 0o644))
	require.NotEqual(t, before, fastStampsOver(t, root, cacheDir, reads, true), "an edited source moves the domain fast stamp")
	next := NewLazyWorkspace(root, func(context.Context) (*Magus, error) {
		opens++
		return nil, errors.New("a fast read must not open the workspace")
	})
	_, ok = RecordedStaleIndexes(ctx, next, cfg)
	assert.False(t, ok, "changed sources send the read back to the workspace")
	assert.Equal(t, 0, opens, "RecordedStaleIndexes itself never opens; the caller decides")
}
