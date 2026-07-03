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
// documentation page under docs/codes/, at exactly the path its URL() resolves to.
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
		url := code.URL()
		i := strings.Index(url, "docs/codes/")
		require.GreaterOrEqual(t, i, 0, "%s: URL() %q has no docs/codes path", code, url)
		path := filepath.Join(root, filepath.FromSlash(url[i:]))
		_, statErr := os.Stat(path)
		if knownUndocumented[code] {
			continue
		}
		assert.NoError(t, statErr, "%s: missing doc page at %s", code, path)
	}
}
