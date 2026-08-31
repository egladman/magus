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

	// Every magus_* token embedded in a line FollowUp can emit resolves to a
	// real tool. Walking every constant through every outcome covers the whole
	// hint surface, ref-chain line included.
	for _, tn := range hint.AllToolNames {
		for _, v := range []string{
			hint.FollowUp(tn.String(), true, ""),
			hint.FollowUp(tn.String(), false, ""),
			hint.FollowUp(tn.String(), false, "out1a2b3c4d"),
		} {
			for _, tok := range toolTokenRe.FindAllString(v, -1) {
				assert.Truef(t, valid[tok], "hint value references tool %q, not a Registry[].Name: %q", tok, v)
			}
		}
	}
}
