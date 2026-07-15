package knowledge

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testModule = "github.com/egladman/magus"

func TestParseCoverageAggregatesPerFile(t *testing.T) {
	profile := []byte("mode: atomic\n" +
		"github.com/egladman/magus/internal/foo.go:10.2,12.16 3 1\n" +
		"github.com/egladman/magus/internal/foo.go:20.2,20.10 2 0\n" +
		"github.com/egladman/magus/internal/bar.go:5.2,6.3 1 0\n")

	got := ParseCoverage(profile, testModule)

	require.Equal(t, []FileCoverage{
		{
			Path:    "internal/bar.go",
			Covered: 0,
			Total:   1,
			Blocks:  []CoverageBlock{{StartLine: 5, EndLine: 6, NumStmt: 1, Hits: 0}},
		},
		{
			Path:    "internal/foo.go",
			Covered: 3,
			Total:   5,
			Blocks: []CoverageBlock{
				{StartLine: 10, EndLine: 12, NumStmt: 3, Hits: 1},
				{StartLine: 20, EndLine: 20, NumStmt: 2, Hits: 0},
			},
		},
	}, got)
}

func TestParseCoverageDropsForeignAndMalformed(t *testing.T) {
	profile := []byte("mode: set\n" +
		"other.com/pkg/x.go:1.1,2.2 1 1\n" +
		"github.com/egladman/magus/ok.go:1.1,2.2 1 1\n" +
		"garbage line without colon location\n" +
		"github.com/egladman/magus/bad.go:1.1,2.2 notanum 1\n")

	got := ParseCoverage(profile, testModule)

	require.Len(t, got, 1)
	assert.Equal(t, "ok.go", got[0].Path)
}

func TestParseCoverageEmptyModuleYieldsNil(t *testing.T) {
	profile := []byte("mode: atomic\ngithub.com/egladman/magus/x.go:1.1,2.2 1 1\n")
	assert.Nil(t, ParseCoverage(profile, ""))
}

func TestAssembleCoverageFileAndSymbolNodes(t *testing.T) {
	cov := []FileCoverage{{
		Path:    "internal/foo.go",
		Covered: 4,
		Total:   6,
		Blocks: []CoverageBlock{
			{StartLine: 3, EndLine: 4, NumStmt: 1, Hits: 0},
			{StartLine: 12, EndLine: 14, NumStmt: 2, Hits: 1},
			{StartLine: 22, EndLine: 24, NumStmt: 3, Hits: 0},
		},
	}}
	symbols := map[string][]types.KnowledgeSymbol{
		"internal": {
			{Key: "m FuncA#", Source: "internal/foo.go:10"},
			{Key: "m FuncB#", Source: "internal/foo.go:20"},
		},
	}

	sh := assembleCoverage(cov, symbols)

	require.Equal(t, CoverageShardName, sh.Name)
	byID := map[string]map[string]string{}
	for _, n := range sh.Nodes {
		byID[n.ID] = n.Attrs
	}
	assert.Equal(t, map[string]string{
		AttrCoverage:     "0.67",
		AttrCoveredStmts: "4",
		AttrTotalStmts:   "6",
	}, byID[fileID("internal/foo.go")])
	assert.Equal(t, "1.00", byID[symbolID("m FuncA#")][AttrCoverage])
	assert.Equal(t, map[string]string{
		AttrCoverage:     "0.00",
		AttrCoveredStmts: "0",
		AttrTotalStmts:   "3",
	}, byID[symbolID("m FuncB#")])
}

func TestAssembleCoverageWithoutSymbolsIsFileOnly(t *testing.T) {
	cov := []FileCoverage{{
		Path:   "internal/foo.go",
		Total:  2,
		Blocks: []CoverageBlock{{StartLine: 12, EndLine: 14, NumStmt: 2, Hits: 0}},
	}}
	sh := assembleCoverage(cov, nil)
	require.Len(t, sh.Nodes, 1)
	assert.Equal(t, types.KindFile, sh.Nodes[0].Kind)
}

func TestTestRefCount(t *testing.T) {
	refs := []types.KnowledgeSymbolRef{
		{Path: "internal/foo.go"},
		{Path: "internal/foo_test.go"},
		{Path: "internal/bar_test.go"},
	}
	assert.Equal(t, 2, testRefCount(refs))
	assert.Equal(t, 0, testRefCount(nil))
}

func TestEnclosingDef(t *testing.T) {
	defs := []fileSymbolDef{{key: "A", line: 10}, {key: "B", line: 20}}
	assert.Equal(t, -1, enclosingDef(defs, 5))
	assert.Equal(t, 0, enclosingDef(defs, 10))
	assert.Equal(t, 0, enclosingDef(defs, 15))
	assert.Equal(t, 1, enclosingDef(defs, 99))
}
