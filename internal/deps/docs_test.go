package deps

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDocsURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, manager, pkg, version string
		want                        string
		ok                          bool
	}{
		{"go module", "gomod", "golang.org/x/mod", "v0.37.0", "https://pkg.go.dev/golang.org/x/mod@v0.37.0", true},
		{"npm package", "npm", "left-pad", "1.3.0", "https://www.npmjs.com/package/left-pad/v/1.3.0", true},
		{"npm scoped name", "npm", "@connectrpc/connect", "2.1.2", "https://www.npmjs.com/package/@connectrpc/connect/v/2.1.2", true},
		{"pypi project", "python", "pydantic", "2.9.2", "https://pypi.org/project/pydantic/2.9.2/", true},
		{"pypi normalized name", "python", "Zope.Interface", "7.2", "https://pypi.org/project/zope-interface/7.2/", true},
		{"crate", "cargo", "serde", "1.0.210", "https://docs.rs/serde/1.0.210", true},
		{"unknown manager", "maven", "junit", "4.13", "", false},
		{"empty version", "npm", "left-pad", "", "", false},
		{"empty name", "cargo", "", "1.0.0", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := DocsURL(tc.manager, tc.pkg, tc.version)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestDocsURLCoversEveryReader holds DocsURL's switch to the Readers table: a reader
// added without a docs derivation would print package nodes with no docs line.
func TestDocsURLCoversEveryReader(t *testing.T) {
	t.Parallel()
	for manifest, r := range Readers {
		_, ok := DocsURL(r.Manager, "x", "1.0.0")
		assert.True(t, ok, "%s's manager %q has no docs URL", manifest, r.Manager)
	}
}
