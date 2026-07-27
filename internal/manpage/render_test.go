package manpage

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoffPages(t *testing.T) {
	pages := RoffPages("2026-07-26", "v1.2.3")
	require.Len(t, pages, 1+len(All))

	byName := make(map[string]string, len(pages))
	for _, page := range pages {
		byName[page.Name] = string(page.Content)
	}
	assert.Contains(t, byName["magus.1"], `.TH MAGUS 1 "2026-07-26" "magus v1.2.3"`)
	assert.Contains(t, byName["magus.1"], "magus\\-man")
	assert.Contains(t, byName["magus-run.1"], ".SH OPTIONS")
	assert.Contains(t, byName["magus-man.1"], "embedded section 1 man pages")
}

func TestRoffPagesAreDeterministic(t *testing.T) {
	first := RoffPages("", "")
	second := RoffPages("", "")
	require.Equal(t, first, second)
	for _, page := range first {
		assert.True(t, strings.HasSuffix(page.Name, ".1"))
	}
}
