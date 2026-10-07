package scipbuzz

import (
	"strings"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"github.com/stretchr/testify/require"
)

// magusKey is the node key magus derives from a moniker (internal/symbols/scip.go
// parseMoniker): manager and package name, then the descriptors, version dropped.
// A moniker with no Package is skipped there, so a nil Package would make every
// symbol this indexer emits invisible to `magus refs`.
func magusKey(t *testing.T, symbol string) string {
	t.Helper()
	sym, err := scip.ParseSymbol(symbol)
	require.NoError(t, err)
	require.NotNil(t, sym.Package, "%s parses with no Package", symbol)
	pkg := strings.TrimSpace(sym.Package.Manager + " " + sym.Package.Name)
	return strings.TrimSpace(pkg + " " + scip.DescriptorOnlyFormatter.FormatSymbol(sym))
}

func TestWorkspaceSymbolParsesWithAPackage(t *testing.T) {
	sym := declSymbol("hack/magusfile/drift.buzz", kindFun, "check")
	require.Equal(t, "scip-buzz buzz . . `hack/magusfile/drift.buzz`/check().", sym)

	parsed, err := scip.ParseSymbol(sym)
	require.NoError(t, err)
	require.Equal(t, "scip-buzz", parsed.Scheme)
	require.Equal(t, &scip.Package{Manager: "buzz"}, parsed.Package, "`.` parses to an empty name and version")
	require.Equal(t, []*scip.Descriptor{
		{Name: "hack/magusfile/drift.buzz", Suffix: scip.Descriptor_Namespace},
		{Name: "check", Suffix: scip.Descriptor_Method},
	}, parsed.Descriptors)
	require.Equal(t, "buzz `hack/magusfile/drift.buzz`/check().", magusKey(t, sym))
}

func TestDeclSymbolSuffixes(t *testing.T) {
	cases := map[declKind]string{
		kindFun:      "scip-buzz buzz . . `a.buzz`/x().",
		kindExtern:   "scip-buzz buzz . . `a.buzz`/x().",
		kindFinal:    "scip-buzz buzz . . `a.buzz`/x.",
		kindVar:      "scip-buzz buzz . . `a.buzz`/x.",
		kindObject:   "scip-buzz buzz . . `a.buzz`/x#",
		kindProtocol: "scip-buzz buzz . . `a.buzz`/x#",
		kindEnum:     "scip-buzz buzz . . `a.buzz`/x#",
	}
	for kind, want := range cases {
		require.Equal(t, want, declSymbol("a.buzz", kind, "x"))
	}
}

func TestHostSymbolParsesWithAPackage(t *testing.T) {
	fn, kind := hostSymbol("fs", "readFile")
	require.Equal(t, "scip-buzz buzz host . fs/readFile().", fn)
	require.Equal(t, scip.SymbolInformation_Function, kind)
	require.Equal(t, "buzz host fs/readFile().", magusKey(t, fn))

	typ, kind := hostSymbol("magus/spell", "Command")
	require.Equal(t, "scip-buzz buzz host . `magus/spell`/Command#", typ)
	require.Equal(t, scip.SymbolInformation_UnspecifiedKind, kind)
	require.Equal(t, "buzz host `magus/spell`/Command#", magusKey(t, typ))
}

// TestEmittedSymbolsRoundTrip parses and re-formats every global symbol the corpus
// produces; a symbol that changes is one `scip lint` reports as non-canonical.
func TestEmittedSymbolsRoundTrip(t *testing.T) {
	idx, _, err := indexDir(snapshotInput)
	require.NoError(t, err)
	var symbols []string
	for _, doc := range idx.Documents {
		for _, info := range doc.Symbols {
			symbols = append(symbols, info.Symbol)
		}
		for _, occ := range doc.Occurrences {
			symbols = append(symbols, occ.Symbol)
		}
	}
	for _, info := range idx.ExternalSymbols {
		symbols = append(symbols, info.Symbol)
	}
	require.NotEmpty(t, symbols)
	for _, sym := range symbols {
		if scip.IsLocalSymbol(sym) {
			_, err := scip.ParseSymbol(sym)
			require.NoError(t, err, sym)
			continue
		}
		parsed, err := scip.ParseSymbol(sym)
		require.NoError(t, err, sym)
		require.NotNil(t, parsed.Package, sym)
		require.Equal(t, sym, scip.VerboseSymbolFormatter.FormatSymbol(parsed))
	}
}
