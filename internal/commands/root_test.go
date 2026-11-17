package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/clikit/command"
)

// TestAgentHarnessHasNoWritingVerb pins that apply and remove are gone.
// Install, link and verify are the only verbs the agent harness command declares.
func TestAgentHarnessHasNoWritingVerb(t *testing.T) {
	var verbs []string
	command.Walk(Root.Spec.Command, func(path string, c command.Command) {
		if path == "agent harness" {
			for _, child := range c.Children {
				verbs = append(verbs, child.Name)
			}
		}
	})
	require.NotEmpty(t, verbs, "agent harness is declared")
	assert.ElementsMatch(t, []string{"install", "link", "verify"}, verbs)
}
