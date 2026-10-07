package scipbuzz

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"github.com/scip-code/scip/bindings/go/scip/testutil"
	"github.com/stretchr/testify/require"
)

const (
	snapshotInput = "testdata/snapshots/input"
	snapshotGen   = "testdata/snapshots/gen"
)

// indexDir indexes dir as both project and workspace root, returning the index
// and the warnings Index reported.
func indexDir(dir string) (*scip.Index, []string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, nil, err
	}
	var warnings []string
	idx, err := Index(context.Background(), Options{
		ProjectRoot:   abs,
		WorkspaceRoot: abs,
		Warn:          func(msg string) { warnings = append(warnings, msg) },
	})
	return idx, warnings, err
}

// snapshotCases returns the case directories under testdata/snapshots/input.
func snapshotCases(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(snapshotInput)
	require.NoError(t, err)
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(snapshotInput, e.Name()))
		}
	}
	require.NotEmpty(t, dirs)
	return dirs
}

// TestSnapshots compares each case's index, rendered by the SCIP bindings' own
// snapshot formatter, with its golden under testdata/snapshots/gen. Refresh the
// goldens with the snapshots-generate target.
func TestSnapshots(t *testing.T) {
	testutil.SnapshotTestDirectories(t, snapshotInput, snapshotGen, func(in, _ string, _ []*scip.SourceFile) []*scip.SourceFile {
		idx, warnings, err := indexDir(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			return nil
		}
		for _, w := range warnings {
			t.Errorf("%s: %s", in, w)
		}
		abs, _ := filepath.Abs(in)
		files, err := testutil.FormatSnapshots(idx, "//", scip.VerboseSymbolFormatter, abs)
		if err != nil {
			t.Errorf("%s: %v", in, err)
		}
		return commentedExternals(files)
	})
}

// commentedExternals turns the formatter's external_symbols.txt into a Buzz file of
// comments, so every golden under gen/ is a .buzz file that still parses.
func commentedExternals(files []*scip.SourceFile) []*scip.SourceFile {
	for _, f := range files {
		if f.RelativePath != "external_symbols.txt" {
			continue
		}
		lines := strings.Split(strings.TrimSuffix(f.Text, "\n"), "\n")
		for i, line := range lines {
			lines[i] = strings.TrimRight("// "+line, " ")
		}
		f.RelativePath = "external_symbols.buzz"
		f.Text = strings.Join(lines, "\n") + "\n"
	}
	return files
}

// TestOccurrenceRangesHoldTheirNames is the check magus's rename runs before it
// edits a file: the bytes under every occurrence spell the symbol's name, the last
// descriptor of a global and the display name of a local. One wrong range would
// mark the file stale and refuse the rename.
func TestOccurrenceRangesHoldTheirNames(t *testing.T) {
	for _, dir := range snapshotCases(t) {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			idx, _, err := indexDir(dir)
			require.NoError(t, err)
			count := 0
			for _, doc := range idx.Documents {
				data, err := os.ReadFile(filepath.Join(dir, doc.RelativePath))
				require.NoError(t, err)
				lines := strings.Split(string(data), "\n")
				locals := map[string]string{}
				for _, info := range doc.Symbols {
					if scip.IsLocalSymbol(info.Symbol) {
						locals[info.Symbol] = info.DisplayName
					}
				}
				for _, occ := range doc.Occurrences {
					r, ok := occ.SourceRange()
					require.True(t, ok)
					require.True(t, r.IsSingleLine(), "%s: %v spans lines", doc.RelativePath, r)
					line := lines[r.Start.Line]
					require.LessOrEqual(t, int(r.End.Character), len(line), "%s: %v past the line", doc.RelativePath, r)
					got := line[r.Start.Character:r.End.Character]
					require.Equal(t, occurrenceName(t, occ.Symbol, locals), got, "%s:%d: %s", doc.RelativePath, r.Start.Line+1, occ.Symbol)
					count++
				}
			}
			require.NotZero(t, count, "a case with no occurrences proves nothing")
		})
	}
}

func occurrenceName(t *testing.T, symbol string, locals map[string]string) string {
	t.Helper()
	if scip.IsLocalSymbol(symbol) {
		name, ok := locals[symbol]
		require.True(t, ok, "local %s has no SymbolInformation", symbol)
		return name
	}
	parsed, err := scip.ParseSymbol(symbol)
	require.NoError(t, err)
	return parsed.Descriptors[len(parsed.Descriptors)-1].Name
}

// TestIndexIsDeterministic indexes the whole corpus twice: the index is published
// and cached by content, so equal input must give equal bytes.
func TestIndexIsDeterministic(t *testing.T) {
	encode := func() []byte {
		idx, _, err := indexDir(snapshotInput)
		require.NoError(t, err)
		var buf bytes.Buffer
		require.NoError(t, Write(&buf, idx))
		return buf.Bytes()
	}
	first := encode()
	require.NotEmpty(t, first)
	for range 5 {
		require.True(t, bytes.Equal(first, encode()), "a second index of the same tree differs")
	}
}

// TestIndexIsLintClean holds the corpus to what `scip lint` reports: every
// occurrence's symbol has SymbolInformation, in a document or in external_symbols
// but not both; no occurrence repeats; a forward definition is never also a
// definition; and every symbol is in canonical form.
func TestIndexIsLintClean(t *testing.T) {
	for _, dir := range snapshotCases(t) {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			idx, _, err := indexDir(dir)
			require.NoError(t, err)
			defined := map[string]bool{}
			for _, doc := range idx.Documents {
				for _, info := range doc.Symbols {
					require.NotEmpty(t, info.Symbol)
					defined[info.Symbol] = true
				}
			}
			globals := map[string]bool{}
			for _, info := range idx.ExternalSymbols {
				require.False(t, globals[info.Symbol], "external %s twice", info.Symbol)
				require.False(t, defined[info.Symbol], "%s is both a document symbol and an external one", info.Symbol)
				globals[info.Symbol] = true
			}
			for sym := range defined {
				if !scip.IsLocalSymbol(sym) {
					globals[sym] = true
				}
			}
			for _, doc := range idx.Documents {
				docLocals := map[string]bool{}
				for _, info := range doc.Symbols {
					docLocals[info.Symbol] = true
				}
				seen := map[string]bool{}
				for _, occ := range doc.Occurrences {
					if scip.IsLocalSymbol(occ.Symbol) {
						require.True(t, docLocals[occ.Symbol], "%s: %s has no SymbolInformation", doc.RelativePath, occ.Symbol)
					} else {
						require.True(t, globals[occ.Symbol], "%s: %s has no SymbolInformation", doc.RelativePath, occ.Symbol)
					}
					roles := scip.SymbolRole(occ.SymbolRoles)
					require.False(t, roles&scip.SymbolRole_ForwardDefinition != 0 && roles&scip.SymbolRole_Definition != 0,
						"%s: %s is a forward definition marked as a definition", doc.RelativePath, occ.Symbol)
					r, _ := occ.SourceRange()
					key := fmt.Sprint(occ.Symbol, r, occ.SymbolRoles)
					require.False(t, seen[key], "%s: duplicate occurrence %s", doc.RelativePath, key)
					seen[key] = true
				}
			}
			for sym := range globals {
				require.Equal(t, sym, canonical(t, sym))
			}
		})
	}
}

func canonical(t *testing.T, symbol string) string {
	t.Helper()
	formatted, err := scip.VerboseSymbolFormatter.Format(symbol)
	require.NoError(t, err)
	return formatted
}

// TestIndexSkipsAFileThatDoesNotParse keeps one broken file from costing the rest
// of the project its index.
func TestIndexSkipsAFileThatDoesNotParse(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "good.buzz"), []byte("fun ok() > int {\n    return 1;\n}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.buzz"), []byte("fun (\n"), 0o644))
	idx, warnings, err := indexDir(dir)
	require.NoError(t, err)
	require.Len(t, idx.Documents, 1)
	require.Equal(t, "good.buzz", idx.Documents[0].RelativePath)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "bad.buzz")
}

// TestIndexMetadata pins the document fields consumers read.
func TestIndexMetadata(t *testing.T) {
	idx, _, err := indexDir(filepath.Join(snapshotInput, "toplevel"))
	require.NoError(t, err)
	require.Equal(t, "scip-buzz", idx.Metadata.ToolInfo.Name)
	require.Equal(t, Version, idx.Metadata.ToolInfo.Version)
	require.Empty(t, idx.Metadata.ProjectRoot, "an absolute root would differ per checkout")
	require.Len(t, idx.Documents, 1)
	doc := idx.Documents[0]
	require.Equal(t, "buzz", doc.Language)
	require.Equal(t, "main.buzz", doc.RelativePath)
	require.Equal(t, scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart, doc.PositionEncoding)
}

// TestDocumentPathsAreProjectRelative indexes a project below the workspace root:
// documents name files from the project, symbols from the workspace.
func TestDocumentPathsAreProjectRelative(t *testing.T) {
	ws := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(ws, "magus.yaml"), nil, 0o644))
	project := filepath.Join(ws, "libs", "app")
	require.NoError(t, os.MkdirAll(project, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(project, "app.buzz"), []byte("export fun run() > void {}\n"), 0o644))
	idx, err := Index(context.Background(), Options{ProjectRoot: project})
	require.NoError(t, err)
	require.Len(t, idx.Documents, 1)
	require.Equal(t, "app.buzz", idx.Documents[0].RelativePath)
	require.Equal(t, "scip-buzz buzz . . `libs/app/app.buzz`/run().", idx.Documents[0].Symbols[0].Symbol)
}

// TestIndexRequiresAProjectRoot keeps a caller that forgot the root from indexing
// whatever directory the process happens to run in.
func TestIndexRequiresAProjectRoot(t *testing.T) {
	_, err := Index(context.Background(), Options{})
	require.ErrorContains(t, err, "no project root")
}

// TestIndexBytesDoNotDependOnTheCheckoutPath indexes one tree from two checkouts.
// The index is cached and published by content, so where the tree lives must not
// reach its bytes.
func TestIndexBytesDoNotDependOnTheCheckoutPath(t *testing.T) {
	encode := func(ws string) []byte {
		project := filepath.Join(ws, "libs", "app")
		require.NoError(t, os.MkdirAll(project, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(ws, "magus.yaml"), nil, 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(ws, "shared.buzz"), []byte("export fun one() > int {\n    return 1;\n}\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(project, "app.buzz"), []byte("import \"shared\";\n\nexport fun run() > int {\n    return shared\\one();\n}\n"), 0o644))
		idx, err := Index(context.Background(), Options{ProjectRoot: project})
		require.NoError(t, err)
		var buf bytes.Buffer
		require.NoError(t, Write(&buf, idx))
		return buf.Bytes()
	}
	first := encode(t.TempDir())
	second := encode(filepath.Join(t.TempDir(), "another", "checkout"))
	require.Equal(t, first, second)
}
