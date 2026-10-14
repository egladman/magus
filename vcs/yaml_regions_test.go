package vcs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A YAML line is named by its top-level key and the second-level key under it, which is
// what a `magus.yaml#jobs.stale_after` claim names.
func TestPlaceYAMLKeys(t *testing.T) {
	var funcnames []string
	for _, f := range gitFuncnames {
		funcnames = append(funcnames, "-c", f.key()+"="+f.pattern)
	}
	body := "# config\nversion: 1\njobs:\n  stale_after: 2h\n  nested:\n    deep: 1\nlist:\n  - a\nblock: |\n  text: here\n"
	got, err := gitPlaceLines(t.Context(), t.TempDir(), "yaml", funcnames, []byte(body))
	require.NoError(t, err)
	assert.Equal(t, []string{
		"", "version", "jobs", "jobs.stale_after", "jobs.nested", "jobs.nested", "list", "list", "block", "block",
	}, got)
}
