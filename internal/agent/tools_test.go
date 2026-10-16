package agent

import (
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func writeToolFiles(t *testing.T, root string, files []HarnessAgentFile) {
	t.Helper()
	for _, f := range files {
		path := filepath.Join(root, f.Path)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(f.Content), 0o644))
	}
}

// The directory is one file per capability that has a row, a tool name a line in sorted
// order, and the version, which marks the directory installed.
func TestHarnessToolsFilesAreOneFilePerCapability(t *testing.T) {
	tools := HarnessTools{Dir: "host/magus/tools", Rows: []HarnessTool{
		{Name: "Shell", Capability: ToolRuns},
		{Name: "Read", Capability: ToolReads},
		{Name: "Glob", Capability: ToolReads},
		{Name: "Todo", Capability: ToolInert},
	}}
	assert.Equal(t, []HarnessAgentFile{
		{Path: "host/magus/tools/runs", Content: "Shell\n"},
		{Path: "host/magus/tools/reads", Content: "Glob\nRead\n"},
		{Path: "host/magus/tools/inert", Content: "Todo\n"},
		{Path: "host/magus/tools/version", Content: "1\n"},
	}, tools.Files())
}

func TestReadToolRowsReadsBackWhatFilesWrote(t *testing.T) {
	root := t.TempDir()
	writeToolFiles(t, root, HarnessTools{Dir: "tools", Rows: []HarnessTool{
		{Name: "Shell", Capability: ToolRuns},
		{Name: "Browse", Capability: ToolNetwork},
	}}.Files())

	rows := ReadToolRows(root, "tools")
	require.NoError(t, rows.Err)
	assert.Equal(t, ToolRuns, rows.Capability("Shell"))
	assert.Equal(t, ToolNetwork, rows.Capability("Browse"))
	assert.Empty(t, rows.Capability("Stranger"))
}

func testModelFacts(t testing.TB) *ModelFacts {
	t.Helper()
	tiers, err := types.NewModelTierSet(types.ModelTierDeclaration{
		Enum: "Tier", Site: "magusfile.buzz:7", Cases: []string{"economy", "standard"}, Default: 1,
		Bounds: []types.ModelTierBounds{{MaxLive: 4}, {MaxContext: 150_000}},
		Skills: []types.SkillVariant{types.SkillVariantShort, types.SkillVariantFull},
		Hosts:  map[types.HarnessKey][]string{{ID: "host"}: {"small", "large"}},
	})
	require.NoError(t, err)
	return &ModelFacts{Tiers: tiers, Rows: []types.ModelPricing{
		{ID: "small-1", Tier: "economy", Window: 200_000, Input: 1, Output: 5},
		{ID: "large-1", Tier: "standard", Window: 1_000_000, Input: 3, CacheWrite5m: 3.75, CacheWrite1h: 6, CacheRead: 0.3, Output: 15,
			LongContext: &types.PriceStep{Above: 200_000, Multiplier: 2}},
	}}
}

func TestToolRowsModelsReadBackWhatFilesWrote(t *testing.T) {
	root := t.TempDir()
	want := testModelFacts(t)
	writeToolFiles(t, root, HarnessTools{Dir: "tools", Rows: []HarnessTool{{Name: "Shell", Capability: ToolRuns}}, Models: want}.Files())

	got, err := ReadToolRows(root, "tools").Models()
	require.NoError(t, err)
	assert.Equal(t, want.Rows, got.Rows)
	require.NotNil(t, got.Tiers)
	assert.Equal(t, encodeTiers(want.Tiers), encodeTiers(got.Tiers), "every field of the declaration survives")
	_, err = got.Tiers.Parse("premium")
	assert.EqualError(t, err, `"premium" is not a model tier: enum Tier at magusfile.buzz:7 declares economy, standard`)
}

// A workspace that declares no tiers writes an empty tiers file, which reads back as none
// rather than as a missing file.
func TestToolRowsModelsWithoutTiersReadAsNone(t *testing.T) {
	root := t.TempDir()
	writeToolFiles(t, root, HarnessTools{Dir: "tools", Models: &ModelFacts{}}.Files())

	got, err := ReadToolRows(root, "tools").Models()
	require.NoError(t, err)
	assert.Equal(t, ModelFacts{}, got)
}

// Every way to have no model facts says why and names the install that rewrites them.
func TestToolRowsModelsSayWhyThereAreNone(t *testing.T) {
	root := t.TempDir()
	writeToolFiles(t, root, HarnessTools{Dir: "bare", Rows: []HarnessTool{{Name: "Shell", Capability: ToolRuns}}}.Files())
	broken := HarnessTools{Dir: "broken", Models: testModelFacts(t)}.Files()
	for i, f := range broken {
		if path.Base(f.Path) == tierFile {
			broken[i].Content = "{\n{\n"
		}
	}
	writeToolFiles(t, root, broken)

	for name, tc := range map[string]struct{ dir, says string }{
		"no rows":          {"missing", "no tool rows at missing"},
		"written before":   {"bare", "no model rows at bare"},
		"a malformed file": {"broken", "the model tiers at broken do not read"},
	} {
		_, err := ReadToolRows(root, tc.dir).Models()
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), tc.says, name)
		assert.Contains(t, err.Error(), "magus agent harness install", name)
	}
	_, err := ToolRows{}.Models()
	assert.EqualError(t, err, "no tool rows were read")
}

// BenchmarkToolRowsModels is what a hook pays for the model facts once a judgment asks for
// them: the two files install writes, read and decoded, in place of loading the workspace
// to ask each harness spell for its rows and the magusfile for its tiers.
func BenchmarkToolRowsModels(b *testing.B) {
	root := b.TempDir()
	for _, f := range (HarnessTools{Dir: "tools", Rows: []HarnessTool{{Name: "Shell", Capability: ToolRuns}}, Models: testModelFacts(b)}).Files() {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(f.Path)), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, f.Path), []byte(f.Content), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	rows := ReadToolRows(root, "tools")
	b.ReportAllocs()
	for b.Loop() {
		if _, err := rows.Models(); err != nil {
			b.Fatal(err)
		}
	}
}

// Every way to have no table names the install that writes one, and knows no tool.
func TestReadToolRowsSaysWhyThereAreNone(t *testing.T) {
	root := t.TempDir()
	stale := HarnessTools{Dir: "stale", Rows: []HarnessTool{{Name: "Shell", Capability: ToolRuns}}}.Files()
	stale[len(stale)-1].Content = "0\n"
	writeToolFiles(t, root, stale)

	for name, tc := range map[string]struct{ dir, says string }{
		"no directory named": {"", "names no --tool-rows directory"},
		"never installed":    {"missing", "no tool rows at missing"},
		"another layout":     {"stale", "are version 0 and this magus reads version 1"},
	} {
		rows := ReadToolRows(root, tc.dir)
		require.Error(t, rows.Err, name)
		assert.Contains(t, rows.Err.Error(), tc.says, name)
		assert.Contains(t, rows.Err.Error(), "magus agent harness install", name)
		assert.Empty(t, rows.Capability("Shell"), name)
	}
}

func TestValidateHarnessToolsRefusesWhatTheGuardCannotRead(t *testing.T) {
	for name, tc := range map[string]struct {
		tools HarnessTools
		err   string
	}{
		"absolute dir":       {HarnessTools{Dir: "/abs"}, "dir must be a workspace-relative path"},
		"unknown capability": {HarnessTools{Dir: "d", Rows: []HarnessTool{{Name: "X", Capability: "flies"}}}, `row 1 (X): capability "flies" is none of runs, reads, writes, spawns, network, inert`},
		"two rows":           {HarnessTools{Dir: "d", Rows: []HarnessTool{{Name: "X", Capability: ToolRuns}, {Name: "X", Capability: ToolInert}}}, "row 2: X is declared twice"},
		"a name with a line": {HarnessTools{Dir: "d", Rows: []HarnessTool{{Name: "X\nY", Capability: ToolRuns}}}, "row 1: name must be one non-empty line"},
	} {
		assert.EqualError(t, validateHarnessTools(tc.tools), tc.err, name)
	}
	assert.NoError(t, validateHarnessTools(HarnessTools{Dir: "d", Rows: []HarnessTool{{Name: "X", Capability: ToolRuns}}}))
}
