package mcp

import (
	"context"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestDecorateResultErrorHint(t *testing.T) {
	t.Parallel()

	t.Run("error result gets the tool's terse recovery hint", func(t *testing.T) {
		r := mcplib.NewToolResultError("mcp: no node matches foo")
		decorateResult(r, "magus_explain")
		require.Len(t, r.Content, 2, "hint is appended as its own block")
		assert.Contains(t, resultText(r), "magus_query")
	})

	t.Run("run error points at magus_describe", func(t *testing.T) {
		r := mcplib.NewToolResultError("mcp: no targets resolved for bogus")
		decorateResult(r, "magus_run_target")
		assert.Contains(t, resultText(r), "magus_describe (kind=targets)")
	})

	t.Run("output error explains where refs come from", func(t *testing.T) {
		r := mcplib.NewToolResultError("mcp: no stored output for ref")
		decorateResult(r, "magus_output")
		assert.Contains(t, resultText(r), "magus_run_target")
		assert.Contains(t, resultText(r), "magus_tail_log")
	})

	t.Run("unmapped tool error gets no hint", func(t *testing.T) {
		r := mcplib.NewToolResultError("mcp: boom")
		decorateResult(r, "magus_stats")
		assert.Len(t, r.Content, 1, "no footer for a tool without an error hint")
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

func TestDecorateResultChainHints(t *testing.T) {
	t.Parallel()

	t.Run("affected_plan success chains into run_affected", func(t *testing.T) {
		r := mcplib.NewToolResultText(`{"count":3,"matrix":[]}`)
		decorateResult(r, "magus_affected_plan")
		require.Len(t, r.Content, 2)
		assert.Contains(t, resultText(r), "magus_run_affected")
	})

	t.Run("run result carrying a ref chains into magus_output naming the ref", func(t *testing.T) {
		r := mcplib.NewToolResultText(`{"ok":true,"ref":"ref1a2b3c4d"}`)
		decorateResult(r, "magus_run_target")
		require.Len(t, r.Content, 2)
		assert.Contains(t, resultText(r), "magus_output (ref=ref1a2b3c4d)")
	})

	t.Run("run result with no ref gets no chain hint", func(t *testing.T) {
		r := mcplib.NewToolResultText(`{"ok":true,"events":[]}`)
		decorateResult(r, "magus_run_affected")
		assert.Len(t, r.Content, 1, "no ref in the result means no chain hint")
	})
}

func TestFirstRef(t *testing.T) {
	t.Parallel()

	// A fully-minted ref (ref + 8 hex) is isolated from the JSON payload.
	assert.Equal(t, "ref1a2b3c4d", firstRef(mcplib.NewToolResultText(`{"ref":"ref1a2b3c4d"}`)))
	assert.Empty(t, firstRef(mcplib.NewToolResultText(`{"ok":true}`)))
	// "refactor" has a non-hex tail, so it is a free-text word, not a ref.
	assert.Empty(t, firstRef(mcplib.NewToolResultText(`{"note":"refactor later"}`)))
	// Short English words whose tail is coincidentally all-hex must not be mistaken for a
	// ref: only the exact minted length is accepted, not any hex prefix.
	assert.Empty(t, firstRef(mcplib.NewToolResultText(`please reface the refed panel`)))
	assert.Empty(t, firstRef(nil))
}

func TestWrapAppliesHintsAndCountsTheirBytes(t *testing.T) {
	t.Parallel()

	agentFn := func(context.Context) string { return "test-agent" }

	t.Run("soft error result gets the hint and its bytes are measured", func(t *testing.T) {
		tel := &fakeTel{}
		// adapt turns an Invoke error into an IsError text result with a nil Go
		// error, mirroring the real dispatch path.
		h := wrap(quietLogger(), agentFn, nil, tel, func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
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
		h := wrap(quietLogger(), agentFn, nil, tel, func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
			return mcplib.NewToolResultText(`{"ok":true}`), nil
		})
		result, err := h(context.Background(), callRequest("magus_query", map[string]any{"query": "kind:target"}))
		require.NoError(t, err)
		assert.Len(t, result.Content, 1)
	})
}
