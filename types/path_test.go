package types

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPathResolveAndRelativeTo(t *testing.T) {
	p := Path{Value: "src/main.go", IsDir: false}
	resolved := p.Resolve("/repo")
	assert.Equal(t, filepath.Join("/repo", "src/main.go"), resolved.Value)
	assert.False(t, resolved.IsDir)

	rel, err := resolved.RelativeTo("/repo")
	require.NoError(t, err)
	assert.Equal(t, "src/main.go", rel.Value)
}

func TestPathResolveEmptyPreservesOptionalPath(t *testing.T) {
	assert.Equal(t, Path{IsDir: true}, (Path{IsDir: true}).Resolve("/repo"))
}
