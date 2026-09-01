package mcp

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/internal/hint"
)

// toolTokenRe matches a magus_* tool token embedded in hint prose. Registry
// names are lowercase-and-underscore, so this captures a whole tool name and
// stops at the surrounding punctuation/space.
var toolTokenRe = regexp.MustCompile(`magus_[a-z_]+`)

// TestMCPToolHintsResolve is the MCP analog of cmd/magus's CLI-command drift test:
// every tool name the follow-up hints reference must resolve to a real
// Registry[].Name. hint builds its map keys and in-prose references from the
// hint.ToolName constants, which closes the drift at compile time; this test
// re-checks the constants and every line FollowUp can emit against Registry,
// so a tool rename that misses either side fails here.
func TestMCPToolHintsResolve(t *testing.T) {
	t.Parallel()

	valid := map[string]bool{}
	for _, d := range Registry {
		valid[d.Name] = true
	}

	// Every declared tool-name constant names a real Registry tool.
	for _, tn := range hint.AllToolNames {
		assert.Truef(t, valid[tn.String()], "tool constant %q is not a Registry[].Name", tn)
	}

	// And the reverse, which is what makes hint/mcptool.go's "every Registry[].Name
	// is bound to one of these" true rather than aspirational: a Name() returning a
	// string literal satisfies the interface, ships unhinted, and passes every
	// assertion above, because those only walk the constants.
	declared := map[string]bool{}
	for _, tn := range hint.AllToolNames {
		declared[tn.String()] = true
	}
	for _, d := range Registry {
		assert.Truef(t, declared[d.Name], "Registry tool %q is not a declared hint.ToolName", d.Name)
	}

	// Every magus_* token embedded in a line the hints can emit resolves to a
	// real tool. Walking every constant through every outcome covers the whole
	// hint surface, ref-chain line included.
	for _, tn := range hint.AllToolNames {
		for _, v := range []string{
			hint.FollowUpError(tn),
			hint.FollowUpSuccess(tn, ""),
			hint.FollowUpSuccess(tn, "out1a2b3c4d"),
		} {
			for _, tok := range toolTokenRe.FindAllString(v, -1) {
				assert.Truef(t, valid[tok], "hint value references tool %q, not a Registry[].Name: %q", tok, v)
			}
		}
	}
}
