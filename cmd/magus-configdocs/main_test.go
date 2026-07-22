package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestConfigDocsUpToDate verifies the checked-in docs/reference/config.md is exactly what
// magus-configdocs would emit today, so the config reference cannot drift from
// schema.Fields (itself generated from internal/config/config.go).
func TestConfigDocsUpToDate(t *testing.T) {
	got, err := os.ReadFile(filepath.Join("..", "..", "docs", "reference", "config.md"))
	if !assert.NoError(t, err, "read docs/reference/config.md") {
		return
	}
	assert.Equal(t, render(), string(got),
		"docs/reference/config.md is out of date; re-run:\n  go run ./cmd/magus-configdocs -out ./docs/reference/config.md")
}
