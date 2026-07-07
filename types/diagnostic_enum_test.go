package types

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
