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
var errorHints = map[string]string{
	"magus_run_target":   "next: list valid targets with magus_describe (kind=targets)",
	"magus_run_affected": "next: list valid targets with magus_describe (kind=targets)",
	"magus_where":        "next: list projects with magus_describe (kind=projects)",
	"magus_output":       "next: output refs come from magus_run_target or magus_tail_log",
	"magus_explain":      "next: locate a node with magus_query, then explain it",
	"magus_path":         "next: locate the endpoints with magus_query",
	"magus_refs":         "next: locate a symbol with magus_query",
}

// staticChainHints maps a tool to a chain hint appended on a SUCCESS that always
// leads somewhere fixed. Only tools whose whole purpose is to feed a follow-up
// tool belong here - never general read tools, which get no footer.
var staticChainHints = map[string]string{
	"magus_affected_plan": "next: run the affected set with magus_run_affected",
}

// refChainTools mint an output reference the agent chains into magus_output. On
// success their result text is scanned for a ref token; when one is present the
// fetch hint names that exact ref so the agent can pull the captured output
// without re-reading the run's event wall.
var refChainTools = map[string]bool{
	"magus_run_target":   true,
	"magus_run_affected": true,
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
			appendHint(result, "next: fetch the captured output with magus_output (ref="+ref+")")
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
// or "" if none. It splits on any non lower-hex-alpha rune so a ref embedded in
// a JSON payload (e.g. "ref":"ref1a2b3c") is isolated, then reuses the cache
// package's own ref-shape check - the same discriminator `magus query` uses.
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
			if cache.LooksLikeRef(tok) {
				return tok
			}
		}
	}
	return ""
}
