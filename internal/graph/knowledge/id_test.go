package knowledge

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

// TestToolID pins the tool node ID format: "tool:<program>", the workspace-scoped node
// an op (and its spell) uses for the program it runs.
func TestToolID(t *testing.T) {
	assert.Equal(t, "tool:go", toolID("go"))
	assert.Equal(t, types.KindTool+":sh", toolID("sh"))
}

// TestSanitizeTruncatesOnRuneBoundary pins that a byte-limit cut lands between runes: ten
// 3-byte runes capped at 8 bytes yield two whole runes, not a split rune.
func TestSanitizeTruncatesOnRuneBoundary(t *testing.T) {
	s := sanitize(strings.Repeat("世", 10), 8)
	assert.True(t, utf8.ValidString(s), "truncation split a rune: %q", s)
	assert.Equal(t, "世世", s)
}

// TestSanitizeDocKeepsLines pins what a symbol doc keeps that a label loses: newlines and
// tabs survive, a carriage return and any other control rune are dropped, and only a doc
// past maxSymbolDocLen is cut.
func TestSanitizeDocKeepsLines(t *testing.T) {
	assert.Equal(t, "Doc opens.\n\n\tcode()\n- item", sanitizeDoc("Doc opens.\r\n\n\tcode()\n- item\x07"))

	long := strings.Repeat("word ", 300)
	assert.Equal(t, strings.TrimSpace(long), sanitizeDoc(long), "a doc past maxLabelLen stays whole")
	assert.Len(t, sanitizeDoc(strings.Repeat("w", maxSymbolDocLen+10)), maxSymbolDocLen)
}
