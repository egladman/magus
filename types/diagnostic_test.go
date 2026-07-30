package types

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEveryDiagnosticCodeHasDocPage enforces that every declared MGS#### code has a
// documentation page under docs/reference/codes/, at exactly the path its URL() resolves to.
// The docs are handwritten (the "Why/Resolution" prose is the value), so this is the
// guard that keeps a new code from shipping without its page and that pins URL()
// routing to a real file.
func TestEveryDiagnosticCodeHasDocPage(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Dir(filepath.Dir(thisFile)) // <root>/types/... -> <root>

	src, err := os.ReadFile(filepath.Join(root, "types", "diagnostic.go"))
	require.NoError(t, err)

	// Pre-existing codes without a doc page, from before this guard existed. They are
	// listed explicitly (not silently skipped) so the debt is visible and the guard
	// still enforces the rule for every other code, including new ones. Shrink this
	// set, never grow it.
	knownUndocumented := map[DiagnosticCode]bool{}

	codes := regexp.MustCompile(`DiagnosticCode = "(MGS\d+)"`).FindAllStringSubmatch(string(src), -1)
	require.NotEmpty(t, codes, "no diagnostic codes found to check")

	for _, m := range codes {
		code := DiagnosticCode(m[1])
		// CodeURL points at the rendered docs SITE, so map it back to the Markdown
		// source it is generated from: <site>/reference/codes/<area>/<CODE>/ is
		// docs/reference/codes/<area>/<CODE>.md in the tree.
		url := CodeURL(code)
		const marker = "/reference/codes/"
		i := strings.Index(url, marker)
		require.GreaterOrEqual(t, i, 0, "%s: CodeURL %q has no reference/codes path", code, url)
		rel := strings.TrimSuffix(url[i+1:], "/")
		path := filepath.Join(root, "docs", filepath.FromSlash(rel)+".md")
		_, statErr := os.Stat(path)
		if knownUndocumented[code] {
			continue
		}
		assert.NoError(t, statErr, "%s: missing doc page at %s", code, path)
	}
}

// TestAllDiagnosticCodesEnumerated guards the hand-maintained allDiagnosticCodes
// slice against the const block: every declared MGS#### code must be enumerated,
// so tooling that lists codes (the knowledge graph builds a node per code) never
// silently drops one. Mirrors the source-scan idiom in diagnostic_doc_test.go.
func TestAllDiagnosticCodesEnumerated(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Dir(filepath.Dir(thisFile))

	src, err := os.ReadFile(filepath.Join(root, "types", "diagnostic.go"))
	require.NoError(t, err)

	declared := regexp.MustCompile(`DiagnosticCode = "(MGS\d+)"`).FindAllStringSubmatch(string(src), -1)
	require.NotEmpty(t, declared)

	enumerated := map[DiagnosticCode]bool{}
	for _, c := range AllDiagnosticCodes() {
		assert.Falsef(t, enumerated[c], "duplicate code %s in AllDiagnosticCodes", c)
		enumerated[c] = true
	}

	for _, m := range declared {
		code := DiagnosticCode(m[1])
		assert.Truef(t, enumerated[code], "%s is declared but missing from allDiagnosticCodes", code)
	}
	assert.Len(t, AllDiagnosticCodes(), len(declared), "allDiagnosticCodes has entries not in the const block")
}
