package buzz_test

import (
	"context"
	"strings"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSession_Diagnostics_Clean(t *testing.T) {
	s := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	got := s.Diagnostics(`fun add(a: int, b: int) > int { return a + b; }`)
	assert.Empty(t, got, "a well-formed program should report no diagnostics")
}

func TestSession_Diagnostics_MultipleTypeErrors(t *testing.T) {
	s := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	// Two independent undefined references: the checker accumulates, so both must
	// come back (the point of Diagnostics over Exec, which stops at the first).
	got := s.Diagnostics("var a: int = missingOne;\nvar b: int = missingTwo;")
	require.Len(t, got, 2, "both undefined references should be reported")

	assert.Equal(t, 1, got[0].Line, "first diagnostic on line 1")
	assert.Contains(t, got[0].Msg, "missingOne")
	assert.NotContains(t, got[0].Msg, "buzz: line", "position prefix should be stripped from Msg")

	assert.Equal(t, 2, got[1].Line, "second diagnostic on line 2")
	assert.Contains(t, got[1].Msg, "missingTwo")
}

func TestSession_Diagnostics_ParseError(t *testing.T) {
	s := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	// A parse failure yields exactly one diagnostic (checking cannot proceed) with
	// a recovered position and the "buzz: line L:C:" prefix stripped.
	got := s.Diagnostics("var x: int = ;")
	require.Len(t, got, 1, "a parse error reports a single diagnostic")
	assert.Equal(t, 1, got[0].Line)
	assert.Positive(t, got[0].Col, "column should be recovered from the parser message")
	assert.NotEmpty(t, got[0].Msg)
	assert.False(t, strings.HasPrefix(got[0].Msg, "buzz: line"), "prefix stripped: %q", got[0].Msg)
}
