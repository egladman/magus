package guard

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToolCacheUnscoped(t *testing.T) {
	const root = "/work/repo"
	deps := Dependencies{scope: workspaceScope{root: root, dir: root}}
	scoped := "GOLANGCI_LINT_CACHE=/work/repo/.magus/golangci-lint "

	denied := func(command, resolved, why string) ShellVerdict {
		return ShellVerdict{
			Deny: "magus guard denied `" + resolved + "`; [MGS2014] " + why +
				"; run `magus run golangci-lint <project>`, which keeps the cache in /work/repo/.magus/golangci-lint",
			Why:  "A cache shared with other checkouts answers from their results and hides findings this checkout would report: one shared lint cache held 10,202 paths from 5 other checkouts.",
			Rule: denyRule{Name: denyRuleRawTool, Arg: resolved},
		}
	}

	t.Run("unset", func(t *testing.T) {
		t.Setenv("GOLANGCI_LINT_CACHE", "")
		got := Evaluate(deps, ".magus/custom-gcl run ./...")
		want := denied("", "custom-gcl run ./...", "GOLANGCI_LINT_CACHE is unset, so the tool falls back to its shared user cache")
		want.Deny = "magus guard denied `custom-gcl run ./...` (what `.magus/custom-gcl run ./...` resolves to); " + want.Deny[len("magus guard denied `custom-gcl run ./...`; "):]
		assert.Equal(t, want, got)
	})

	t.Run("outside the checkout, inline", func(t *testing.T) {
		got := Evaluate(deps, "GOLANGCI_LINT_CACHE=/Users/x/Library/Caches/golangci-lint custom-gcl run")
		want := denied("", "custom-gcl run", "GOLANGCI_LINT_CACHE=/Users/x/Library/Caches/golangci-lint is outside the checkout /work/repo")
		want.Deny = "magus guard denied `custom-gcl run` (what `GOLANGCI_LINT_CACHE=/Users/x/Library/Caches/golangci-lint custom-gcl run` resolves to); " + want.Deny[len("magus guard denied `custom-gcl run`; "):]
		assert.Equal(t, want, got)
	})

	t.Run("outside the checkout, from the hook environment", func(t *testing.T) {
		t.Setenv("GOLANGCI_LINT_CACHE", "/shared/lint")
		got := Evaluate(deps, "custom-gcl run")
		assert.Equal(t, denied("", "custom-gcl run", "GOLANGCI_LINT_CACHE=/shared/lint is outside the checkout /work/repo"), got)
	})

	t.Run("scoped under the checkout passes", func(t *testing.T) {
		t.Setenv("GOLANGCI_LINT_CACHE", "")
		assert.Equal(t, ShellVerdict{}, Evaluate(deps, scoped+"custom-gcl run ./..."))
		assert.Equal(t, ShellVerdict{}, Evaluate(deps, "env "+scoped+"custom-gcl run"))
	})

	t.Run("a relative cache resolves against the working directory", func(t *testing.T) {
		t.Setenv("GOLANGCI_LINT_CACHE", "")
		assert.Equal(t, ShellVerdict{}, Evaluate(deps, "GOLANGCI_LINT_CACHE=.magus/golangci-lint custom-gcl run"))
		got := Evaluate(deps, "GOLANGCI_LINT_CACHE=../other/lint custom-gcl run")
		assert.Equal(t, "custom-gcl run", got.Rule.Arg)
	})

	t.Run("help and a computed value pass", func(t *testing.T) {
		t.Setenv("GOLANGCI_LINT_CACHE", "")
		assert.Equal(t, ShellVerdict{}, Evaluate(deps, "custom-gcl run --help"))
		assert.Equal(t, ShellVerdict{}, Evaluate(deps, "custom-gcl version"))
		assert.Equal(t, ShellVerdict{}, Evaluate(deps, "GOLANGCI_LINT_CACHE=$(pwd)/.magus/golangci-lint custom-gcl run"))
	})
}
