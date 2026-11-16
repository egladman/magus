package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentStarterDocPlainASCIISelfContained(t *testing.T) {
	doc := shippedCatalog().StarterDoc(false)
	assert.Contains(t, doc, "# AGENTS.md")
	assert.Contains(t, doc, "## Project")  // a project placeholder to fill in
	assert.Contains(t, doc, "## magus")    // the reproduced magus block
	assert.Contains(t, doc, vcsSafetyRule) // the shared safety rule
	for _, r := range doc {
		require.Less(t, r, rune(128), "sample AGENTS.md must be plain ASCII")
	}
}
