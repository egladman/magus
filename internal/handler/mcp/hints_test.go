package mcp

import (
	"context"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/handler/mcp/origin"
)

// resultText joins every text block of a result so a test can assert on the full
// content the agent reads, hint included.
func resultText(r *mcplib.CallToolResult) string {
	var b strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(mcplib.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// Which line a tool+outcome earns is pinned byte-for-byte in internal/hint's
// TestFollowUp; these tests pin the decoration mechanics only.
func TestDecorateResultErrorHint(t *testing.T) {
	t.Parallel()

	t.Run("error result gets the tool's recovery hint as its own block", func(t *testing.T) {
		r := mcplib.NewToolResultError("mcp: no node matches foo")
		decorateResult(r, "magus_explain")
		require.Len(t, r.Content, 2, "hint is appended as its own block")
		assert.Equal(t, "mcp: no node matches foo", r.Content[0].(mcplib.TextContent).Text, "the payload block is untouched")
		assert.Contains(t, resultText(r), "magus_query")
	})

	t.Run("unmapped tool error gets no hint", func(t *testing.T) {
		r := mcplib.NewToolResultError("mcp: boom")
		decorateResult(r, "magus_stats")
		assert.Len(t, r.Content, 1, "no footer for a tool without an error hint")
	})

	t.Run("nil result is a no-op", func(t *testing.T) {
		decorateResult(nil, "magus_explain")
	})
}

func TestDecorateResultNoBlanketFooterOnSuccess(t *testing.T) {
	t.Parallel()

	// A plain success from a read tool must NOT gain a footer - output bytes are
	// the agent's context cost, so silent successes stay lean.
	for _, tool := range []string{"magus_query", "magus_explain", "magus_stats", "magus_describe", "magus_where"} {
		r := mcplib.NewToolResultText(`{"ok":true}`)
		decorateResult(r, tool)
		assert.Len(t, r.Content, 1, "no footer appended to a plain %s success", tool)
	}
}

func TestDecorateResultEmptyQueryHint(t *testing.T) {
	t.Parallel()

	t.Run("a zero-match query success gets the recovery line", func(t *testing.T) {
		r := mcplib.NewToolResultText(`{"query":"guard_shel.go","match_count":0,"matches":[]}`)
		decorateResult(r, "magus_query")
		require.Len(t, r.Content, 2, "hint is appended as its own block")
		assert.Equal(t, `{"query":"guard_shel.go","match_count":0,"matches":[]}`, r.Content[0].(mcplib.TextContent).Text,
			"the JSON payload is untouched")
		assert.Contains(t, resultText(r), "magus_refs")
	})

	t.Run("a query that matched gets nothing", func(t *testing.T) {
		r := mcplib.NewToolResultText(`{"match_count":3,"matches":[]}`)
		decorateResult(r, "magus_query")
		assert.Len(t, r.Content, 1, "a found answer earns no footer")
	})

	t.Run("another tool's empty payload is not decorated", func(t *testing.T) {
		// Only magus_query declares an empty-result line, and the emptiness read is
		// gated on that - so a match_count in someone else's payload changes nothing.
		r := mcplib.NewToolResultText(`{"match_count":0}`)
		decorateResult(r, "magus_stats")
		assert.Len(t, r.Content, 1)
	})
}

func TestMatchedNothing(t *testing.T) {
	t.Parallel()

	assert.True(t, matchedNothing(mcplib.NewToolResultText(`{"match_count":0}`)))
	// Whitespace after the colon is the same answer; a substring scan would miss it.
	assert.True(t, matchedNothing(mcplib.NewToolResultText(`{"match_count": 0}`)))
	assert.False(t, matchedNothing(mcplib.NewToolResultText(`{"match_count":10}`)))
	// A payload with no count is a different shape, not an empty one.
	assert.False(t, matchedNothing(mcplib.NewToolResultText(`{"ok":true}`)))
	assert.False(t, matchedNothing(mcplib.NewToolResultText(`not json at all`)))
}

func TestDecorateResultChainHints(t *testing.T) {
	t.Parallel()

	t.Run("affected_plan success chains into run_affected", func(t *testing.T) {
		r := mcplib.NewToolResultText(`{"count":3,"matrix":[]}`)
		decorateResult(r, "magus_affected_plan")
		require.Len(t, r.Content, 2)
		assert.Equal(t, `{"count":3,"matrix":[]}`, r.Content[0].(mcplib.TextContent).Text, "the JSON payload is untouched")
		assert.Contains(t, resultText(r), "magus_run_affected")
	})

	t.Run("run result carrying a ref chains into magus_output naming the ref", func(t *testing.T) {
		r := mcplib.NewToolResultText(`{"ok":true,"ref":"out1a2b3c4d"}`)
		decorateResult(r, "magus_run_target")
		require.Len(t, r.Content, 2)
		assert.Contains(t, resultText(r), "magus_output (ref=out1a2b3c4d)", "the scanned ref reaches the hint")
	})

	t.Run("run result with no ref gets no chain hint", func(t *testing.T) {
		r := mcplib.NewToolResultText(`{"ok":true,"events":[]}`)
		decorateResult(r, "magus_run_affected")
		assert.Len(t, r.Content, 1, "no ref in the result means no chain hint")
	})
}

func TestFirstRef(t *testing.T) {
	t.Parallel()

	// The id a REAL run prints today: the portable ref, out + 12 hex. This is the
	// shape the agent surface actually receives, so pinning only the shorter attempt
	// form below would let the scanner stop recognizing live refs unnoticed.
	assert.Equal(t, "out9c92fef96e60", firstRef(mcplib.NewToolResultText(`{"ref":"out9c92fef96e60"}`)))
	// An attempt id (out + 8 hex, also every pre-portable ref) stays chainable, so an
	// id from `--attempts` or from old scrollback is still picked up.
	assert.Equal(t, "out1a2b3c4d", firstRef(mcplib.NewToolResultText(`{"ref":"out1a2b3c4d"}`)))
	assert.Empty(t, firstRef(mcplib.NewToolResultText(`{"ok":true}`)))
	// "refactor" has a non-hex tail, so it is a free-text word, not a ref.
	assert.Empty(t, firstRef(mcplib.NewToolResultText(`{"note":"refactor later"}`)))
	// Short English words whose tail is coincidentally all-hex must not be mistaken for a
	// ref: only the exact minted length is accepted, not any hex prefix.
	assert.Empty(t, firstRef(mcplib.NewToolResultText(`please outace the refed panel`)))
	assert.Empty(t, firstRef(nil))
}

func TestWrapAppliesHintsAndCountsTheirBytes(t *testing.T) {
	t.Parallel()

	originFn := func(context.Context) origin.Origin { return origin.Origin{Agent: "test-agent"} }

	t.Run("soft error result gets the hint and its bytes are measured", func(t *testing.T) {
		tel := &fakeTel{}
		// adapt turns an Invoke error into an IsError text result with a nil Go
		// error, mirroring the real dispatch path.
		h := wrap(quietLogger(), originFn, "", noSecrets, tel, func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
			return mcplib.NewToolResultError("mcp: no node matches foo"), nil
		})
		result, err := h(context.Background(), callRequest("magus_explain", map[string]any{"node": "foo"}))
		require.NoError(t, err)
		assert.Contains(t, resultText(result), "magus_query")

		require.Len(t, tel.calls, 1)
		assert.Equal(t, int64(len(allText(result))), tel.calls[0].OutputBytes, "hint bytes count toward output size")
	})

	t.Run("plain success is not decorated", func(t *testing.T) {
		tel := &fakeTel{}
		h := wrap(quietLogger(), originFn, "", noSecrets, tel, func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
			return mcplib.NewToolResultText(`{"ok":true}`), nil
		})
		result, err := h(context.Background(), callRequest("magus_query", map[string]any{"query": "kind:target"}))
		require.NoError(t, err)
		assert.Len(t, result.Content, 1)
	})
}
