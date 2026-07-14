package mcp

// hints.go cross-links the MCP tool surface WITHOUT bloating responses. The
// static per-session map (serverInstructions in transport.go) teaches the flow
// once for free; this file adds the paid, context-sensitive part: at most one
// terse follow-up line, appended only where the tool name plus the call outcome
// make a next step obvious. Two response kinds earn a line - an error/empty
// result (recover with the naming tool) and a response that mints an ID the
// agent will chain. A plain SUCCESS gets nothing: a blanket "related tools"
// footer on every call is pure context tax, and output bytes are the agent's
// measured context cost (see magus.mcp.tool.output.size).

import (
	"strings"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/egladman/magus/internal/cache"
)

// errorHints maps a tool to the one-line recovery step appended ONLY when that
// tool returns an error/empty result. Values name tools, never argument values,
// and stay a single lean line so the failure path costs the agent almost nothing.
// Keys and the tool names embedded in each value are built from the ToolName
// constants (toolref.go), so a tool rename is a compile error rather than a hint
// that silently points at a tool that no longer exists.
var errorHints = map[string]string{
	ToolRunTarget.String():   "next: list valid targets with " + ToolDescribe.String() + " (kind=targets)",
	ToolRunAffected.String(): "next: list valid targets with " + ToolDescribe.String() + " (kind=targets)",
	ToolWhere.String():       "next: list projects with " + ToolDescribe.String() + " (kind=projects)",
	ToolOutput.String():      "next: output refs come from " + ToolRunTarget.String() + " or " + ToolTailLog.String(),
	ToolExplain.String():     "next: locate a node with " + ToolQuery.String() + ", then explain it",
	ToolPath.String():        "next: locate the endpoints with " + ToolQuery.String(),
	ToolRefs.String():        "next: locate a symbol with " + ToolQuery.String(),
}

// staticChainHints maps a tool to a chain hint appended on a SUCCESS that always
// leads somewhere fixed. Only tools whose whole purpose is to feed a follow-up
// tool belong here - never general read tools, which get no footer.
var staticChainHints = map[string]string{
	ToolAffectedPlan.String(): "next: run the affected set with " + ToolRunAffected.String(),
}

// refChainTools mint an output reference the agent chains into magus_output. On
// success their result text is scanned for a ref token; when one is present the
// fetch hint names that exact ref so the agent can pull the captured output
// without re-reading the run's event wall.
var refChainTools = map[string]bool{
	ToolRunTarget.String():   true,
	ToolRunAffected.String(): true,
}

// decorateResult appends at most one cross-link line to a tool result, chosen by
// the tool name and outcome: an error/empty result gets the recovery hint; a
// success that mints a plan or an output ref gets the matching chain hint; a
// plain success gets nothing. The line is added as its own trailing text block
// so the JSON payload the agent parses is never corrupted. A nil result (the
// marshal-failure path) is a no-op.
func decorateResult(result *mcplib.CallToolResult, toolName string) {
	if result == nil {
		return
	}
	if result.IsError {
		appendHint(result, errorHints[toolName])
		return
	}
	if h := staticChainHints[toolName]; h != "" {
		appendHint(result, h)
		return
	}
	if refChainTools[toolName] {
		if ref := firstRef(result); ref != "" {
			appendHint(result, "next: fetch the captured output with "+ToolOutput.String()+" (ref="+ref+")")
		}
	}
}

// appendHint adds s to result as its own text block. A blank hint is a no-op, so
// a tool with no map entry adds nothing.
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
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
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
