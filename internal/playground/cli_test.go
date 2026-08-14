package playground

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCompletableCommandsAreUnique pins the dedup. ls, graph, run and version
// exist both as playground verbs and as real subcommands, and a duplicate makes
// the completer treat a name as ambiguous against itself - `ru` stopped
// completing to `run ` when this regressed.
func TestCompletableCommandsAreUnique(t *testing.T) {
	seen := map[string]int{}
	for _, c := range completableCommands() {
		seen[c]++
	}
	for name, n := range seen {
		assert.Equal(t, 1, n, "command %q appears %d times in the completion set", name, n)
	}
}

// TestCompletableCommandsCoverTheRealCLI: the set is read from the registry, so
// a subcommand added there is completable here without touching this package.
func TestCompletableCommandsCoverTheRealCLI(t *testing.T) {
	got := map[string]bool{}
	for _, c := range completableCommands() {
		got[c] = true
	}
	for _, c := range cliCommands() {
		assert.True(t, got[c], "real subcommand %q is not completable", c)
	}
}

// TestCompleteCLIOffersDeclaredFlags: flags come from the registry, and only
// flags - a positional would be a guess at a path this page cannot see.
func TestCompleteCLIOffersDeclaredFlags(t *testing.T) {
	got := completeCLI("run", "--no-")
	require.NotEmpty(t, got, "run declares --no-cache and --no-default-charms")
	for _, f := range got {
		assert.Contains(t, f, "--no-")
	}
	assert.Nil(t, completeCLI("run", "buil"), "a positional is not completed")
}

// TestExplainCLIDescribesARealSubcommand covers the path that replaced handing
// an unknown word to the Buzz evaluator.
func TestExplainCLIDescribesARealSubcommand(t *testing.T) {
	rows := explainCLI("doctor")
	require.NotEmpty(t, rows, "doctor is a real subcommand")
	assert.Contains(t, rows[0].HTML, "doctor")
	assert.Contains(t, rows[len(rows)-1].HTML, "cannot run it")
	assert.Nil(t, explainCLI("nonesuch"), "an unknown word is left to the evaluator")
}
