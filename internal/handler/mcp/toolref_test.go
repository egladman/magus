package mcp

import (
	"regexp"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
)

// toolTokenRe matches a magus_* tool token embedded in hint prose. Registry
// names are lowercase-and-underscore, so this captures a whole tool name and
// stops at the surrounding punctuation/space.
var toolTokenRe = regexp.MustCompile(`magus_[a-z_]+`)

// TestMCPToolHintsResolve is the MCP analog of cmd/magus's clihint drift test:
// every tool name that a cross-link hint references - both the map KEYS (the tool
// a hint fires for) and every magus_* token embedded in the assembled hint VALUE
// strings - must resolve to a real Registry[].Name. Building those references
// from the ToolName constants closes the drift at compile time; this test
// re-checks the constants and the assembled strings against Registry, so a tool
// rename that misses either a key or an in-prose reference fails here.
func TestMCPToolHintsResolve(t *testing.T) {
	t.Parallel()

	valid := map[string]bool{}
	for _, d := range Registry {
		valid[d.Name] = true
	}

	// Every declared tool-name constant names a real Registry tool.
	for _, tn := range allToolNames {
		assert.Truef(t, valid[tn.String()], "tool constant %q is not a Registry[].Name", tn)
	}

	// Every hint map key - the tool a hint fires for - is a real tool.
	for key := range errorHints {
		assert.Truef(t, valid[key], "errorHints key %q is not a Registry[].Name", key)
	}
	for key := range staticChainHints {
		assert.Truef(t, valid[key], "staticChainHints key %q is not a Registry[].Name", key)
	}
	for key := range refChainTools {
		assert.Truef(t, valid[key], "refChainTools key %q is not a Registry[].Name", key)
	}

	// Every magus_* token embedded in an assembled hint value resolves to a real
	// tool. Gather the static value strings, plus the ref-chain hint that
	// decorateResult assembles inline, and scan each for tool references.
	values := make([]string, 0, len(errorHints)+len(staticChainHints)+1)
	for _, v := range errorHints {
		values = append(values, v)
	}
	for _, v := range staticChainHints {
		values = append(values, v)
	}
	refResult := mcplib.NewToolResultText(`{"ref":"ref1a2b3c4d"}`)
	decorateResult(refResult, ToolRunTarget.String())
	values = append(values, resultText(refResult))

	for _, v := range values {
		for _, tok := range toolTokenRe.FindAllString(v, -1) {
			assert.Truef(t, valid[tok], "hint value references tool %q, not a Registry[].Name: %q", tok, v)
		}
	}
}
