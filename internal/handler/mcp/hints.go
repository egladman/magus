package mcp

// hints.go is the mcp-go side of the follow-up hints: internal/hint owns which
// line a tool+outcome earns (the FollowUp functions and their doctrine), this
// file owns scanning a result for a minted ref and appending the line to the
// CallToolResult without corrupting the JSON payload.

import (
	"strings"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/hint"
)

// decorateResult appends at most one cross-link line to a tool result, chosen by
// the tool name and outcome: an error/empty result gets the recovery hint; a
// success that mints a plan or an output ref gets the matching chain hint; a
// plain success gets nothing. The line is added as its own trailing text block
// so the JSON payload the agent parses is never corrupted. A nil result (the
// marshal-failure path) is a no-op.
//
// This is where the request's tool name, a bare string off the wire, becomes a
// hint.ToolName; everything past it is typed, so a rename cannot silently miss a
// hint entry.
func decorateResult(result *mcplib.CallToolResult, toolName string) {
	if result == nil {
		return
	}
	tool := hint.ToolName(toolName)
	if result.IsError {
		appendHint(result, hint.FollowUpError(tool))
		return
	}
	var ref string
	if hint.MintsRef(tool) {
		ref = firstRef(result)
	}
	appendHint(result, hint.FollowUpSuccess(tool, ref))
}

// appendHint adds s to result as its own text block. A blank hint is a no-op, so
// a tool whose outcome earns no line adds nothing.
func appendHint(result *mcplib.CallToolResult, s string) {
	if s == "" {
		return
	}
	result.Content = append(result.Content, mcplib.NewTextContent(s))
}

// firstRef returns the first output-reference token in the result's text blocks,
// or "" if none. It splits on any non lower-alphanumeric rune so a ref embedded in
// a JSON payload (e.g. "ref":"out1a2b3c") is isolated, then accepts a token only when
// it is a fully-minted ref (cache.IsMintedRef). It deliberately does NOT use the looser
// LooksLikeRef prefix check the `magus query` router uses: over free-text tool output
// that would misfire on short words whose tail is coincidentally hex ("outace", "refed").
func firstRef(result *mcplib.CallToolResult) string {
	if result == nil {
		return ""
	}
	notRefRune := func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	}
	for _, c := range result.Content {
		tc, ok := c.(mcplib.TextContent)
		if !ok {
			continue
		}
		for _, tok := range strings.FieldsFunc(tc.Text, notRefRune) {
			if cache.IsMintedRef(tok) {
				return tok
			}
		}
	}
	return ""
}
