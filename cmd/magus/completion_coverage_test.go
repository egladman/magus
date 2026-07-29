package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCompletionCoversEverySubcommand gates the four hand-maintained completion
// scripts against knownSubcommands, the list the dispatcher itself uses.
//
// These scripts are embedded, not generated, so nothing connects them to the CLI
// they describe - and they had already drifted before anyone noticed: agent, memory
// and refs shipped without ever reaching a completion script, and run's
// --no-volatility-retry was still spelled --no-flake-retry from before that rename.
// A subcommand missing here is invisible in the worst way: the user types three
// letters, gets no suggestion, and concludes the command does not exist.
//
// The assertion is a substring match rather than a parse, deliberately: the four
// dialects store their lists four different ways (a bash string, a zsh
// name:description array, a fish printf table, a PowerShell array), and a parser per
// dialect would be more code than the scripts. Coverage is the question here, not
// syntax.
//
// Only SUBCOMMANDS are gated, not flags. A flag gate was tried and removed: flag
// reachability here has at least three forms - bound on a FlagSet, read positionally
// by hasModeFlag, or parsed by a bespoke helper like parseExplainArgs - spread across
// affected.go, bisect.go and others. Every text-scan approximation reported 3+ false
// positives per real finding, which is how a guard teaches people to ignore it. There
// is no canonical flag list to check against, and that absence is exactly why the
// flag drift happened; inventing an unreliable proxy for it does not fix that.
//
// This is a stopgap. The right fix is to generate the lists from one declaration -
// see the note on completionScripts.
func TestCompletionCoversEverySubcommand(t *testing.T) {
	for name, script := range completionScripts() {
		for _, sub := range knownSubcommands {
			assert.Truef(t, strings.Contains(script, sub),
				"%s completion never mentions subcommand %q; add it to cmd/magus/completions/", name, sub)
		}
	}
}

// completionScripts returns each embedded script by shell name.
//
// TODO: these four are hand-maintained and drift silently. The lists they carry
// (subcommands, per-command flags, target verbs, describe nouns, insight lenses) all
// exist in Go already; generating just the LISTS into the scripts - and leaving the
// hand-written completion logic alone, since that part is stable - would remove the
// whole drift class. cmd/magus-utils already generates Go from the config schema, so
// the machinery is there.
func completionScripts() map[string]string {
	return map[string]string{
		"bash":       completionBash,
		"zsh":        completionZsh,
		"fish":       completionFish,
		"powershell": completionPowerShell,
	}
}
