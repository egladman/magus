package symbols

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeIndex(tb testing.TB, dir, name string, idx *scip.Index) string {
	tb.Helper()
	path := filepath.Join(dir, name)
	require.NoError(tb, os.WriteFile(path, marshalIndex(tb, idx), 0o644))
	return path
}

// A file only one GOOS compiles reaches the merged index, so a host never misses the
// other platforms' symbols; a file every run compiles appears once, as the first run
// recorded it, and an external symbol two runs share is kept once.
func TestMergeSymbolIndexesKeepsEveryOSFile(t *testing.T) {
	dir := t.TempDir()
	meta := &scip.Metadata{ProjectRoot: "file:///ws"}
	shared := func(def string) *scip.Document {
		return &scip.Document{
			RelativePath: "proc.go",
			Symbols:      []*scip.SymbolInformation{{Symbol: def}},
			Occurrences:  []*scip.Occurrence{{Symbol: def, SymbolRoles: int32(scip.SymbolRole_Definition), Range: []int32{0, 5, 9}}},
		}
	}
	external := &scip.SymbolInformation{Symbol: "scip-go gomod std go1.25 os/Getpid()."}
	const windowsOnly = "scip-go gomod example.com/m v0 proc/job()."
	linux := writeIndex(t, dir, "linux.scip", &scip.Index{
		Metadata: meta,
		Documents: []*scip.Document{
			shared("scip-go gomod example.com/m v0 proc/Run()."),
			{RelativePath: "proc_linux.go", Symbols: []*scip.SymbolInformation{{Symbol: "scip-go gomod example.com/m v0 proc/cgroup()."}}},
		},
		ExternalSymbols: []*scip.SymbolInformation{external},
	})
	windows := writeIndex(t, dir, "windows.scip", &scip.Index{
		Metadata: meta,
		Documents: []*scip.Document{
			shared("scip-go gomod example.com/m v0 proc/RunWindows()."),
			{
				RelativePath: "proc_windows.go",
				Symbols:      []*scip.SymbolInformation{{Symbol: windowsOnly}},
				Occurrences:  []*scip.Occurrence{{Symbol: windowsOnly, SymbolRoles: int32(scip.SymbolRole_Definition), Range: []int32{2, 5, 8}}},
			},
		},
		ExternalSymbols: []*scip.SymbolInformation{
			external,
			{Symbol: "scip-go gomod golang.org/x/sys v0.1.0 windows/Handle#"},
		},
	})

	dst := filepath.Join(dir, "index.scip")
	require.NoError(t, MergeIndexes(dst, []string{linux, windows}))

	data, err := os.ReadFile(dst)
	require.NoError(t, err)
	merged, err := DecodeIndex(data)
	require.NoError(t, err)
	assert.Equal(t, "file:///ws", merged.GetMetadata().GetProjectRoot())
	var paths []string
	for _, doc := range merged.GetDocuments() {
		paths = append(paths, doc.GetRelativePath())
	}
	assert.Equal(t, []string{"proc.go", "proc_linux.go", "proc_windows.go"}, paths)
	assert.Equal(t, "scip-go gomod example.com/m v0 proc/Run().", merged.GetDocuments()[0].GetSymbols()[0].GetSymbol(),
		"the first run's copy of a shared file is the one kept")
	var externals []string
	for _, sym := range merged.GetExternalSymbols() {
		externals = append(externals, sym.GetSymbol())
	}
	assert.Equal(t, []string{"scip-go gomod std go1.25 os/Getpid().", "scip-go gomod golang.org/x/sys v0.1.0 windows/Handle#"}, externals)

	syms, err := ParseIndex(t.Context(), data, "", "go")
	require.NoError(t, err)
	sources := map[string]string{}
	for _, s := range syms {
		sources[s.Key] = s.Source
	}
	assert.Equal(t, "proc_windows.go:3", sources["gomod example.com/m proc/job()."], "ingestion reads the windows-only definition")
}

// Runs rooted in different directories name different files by the same relative path,
// so merging them is refused rather than letting one shadow the other.
func TestMergeSymbolIndexesRefusesAnotherRoot(t *testing.T) {
	dir := t.TempDir()
	a := writeIndex(t, dir, "a.scip", &scip.Index{Metadata: &scip.Metadata{ProjectRoot: "file:///a"}})
	b := writeIndex(t, dir, "b.scip", &scip.Index{Metadata: &scip.Metadata{ProjectRoot: "file:///b"}})
	err := MergeIndexes(filepath.Join(dir, "index.scip"), []string{a, b})
	require.ErrorContains(t, err, `project root "file:///b", want "file:///a"`)
	assert.NoFileExists(t, filepath.Join(dir, "index.scip"))
}
