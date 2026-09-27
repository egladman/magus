package knowledge

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPackageDepsFileNamespacesAreSorted(t *testing.T) {
	t.Parallel()

	f := newPrecedentFixture()
	nss := []string{
		precedentNamespace("internal/a", "go"),
		precedentNamespace("internal/b", "go"),
		precedentNamespace("internal/a", "typescript"),
	}
	for _, ns := range nss {
		f.file("internal/a/x.go", ns, "a", "go")
	}
	// Map order decides the append order, so one read in a few would come back unsorted.
	for range 20 {
		assert.Equal(t, map[string][]string{"file:internal/a/x.go": {nss[0], nss[1], nss[2]}}, f.g.fileNamespaces())
	}
}
