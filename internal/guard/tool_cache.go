package guard

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

const lintCacheEnv = "GOLANGCI_LINT_CACHE"

// lintPrograms run golangci-lint's analyzers. custom-gcl is the plugin build, which the
// spell catalog does not render, so the raw-tool rule lets it through.
var lintPrograms = []string{"golangci-lint", "custom-gcl"}

// toolCacheUnscoped finds the first direct lint run on the line whose cache does not live
// under the checkout, and says why. The cache comes from the run's own
// GOLANGCI_LINT_CACHE= prefix or env word, else from the hook's environment, which the
// shell inherits. A value the shell computes cannot be judged here and passes.
func toolCacheUnscoped(scope workspaceScope, command string, d Dialect) (hint.Invocation, string, bool) {
	f, err := parseFile(command, d)
	if err != nil || scope.root == "" {
		return hint.Invocation{}, "", false
	}
	var found hint.Invocation
	var why string
	syntax.Walk(f, func(n syntax.Node) bool {
		call, ok := n.(*syntax.CallExpr)
		if !ok || found.Name != "" {
			return found.Name == ""
		}
		words := literalWords(call.Args)
		for _, inv := range peelWrappers(words, d) {
			if !runsLint(inv) {
				continue
			}
			value, set, known := lintCacheValue(call, words)
			if !known {
				continue
			}
			if reason := unscopedCache(scope, value, set); reason != "" {
				found, why = inv, reason
				return false
			}
		}
		return true
	})
	return found, why, found.Name != ""
}

// runsLint reports whether inv runs the linter over code; a help request does not.
func runsLint(inv hint.Invocation) bool {
	if !slices.Contains(lintPrograms, filepath.Base(inv.Name)) {
		return false
	}
	args := inv.Args
	if i := slices.Index(args, "--"); i >= 0 {
		args = args[:i]
	}
	return slices.Contains(args, "run") && !slices.Contains(args, "--help") && !slices.Contains(args, "-h")
}

// lintCacheValue is the cache variable for one call: its own prefix assignment, then an
// env word, then the hook's environment. known is false when the call sets it to
// something the shell computes.
func lintCacheValue(call *syntax.CallExpr, words []string) (value string, set, known bool) {
	for _, a := range call.Assigns {
		if a.Name.Value != lintCacheEnv {
			continue
		}
		if a.Value == nil {
			return "", false, true
		}
		if !readsAsWritten(a.Value.Parts) {
			return "", false, false
		}
		v := literalWord(a.Value.Parts)
		return v, v != "", true
	}
	for i, w := range words {
		if v, ok := strings.CutPrefix(w, lintCacheEnv+"="); ok {
			if !readsAsWritten(call.Args[i].Parts) {
				return "", false, false
			}
			return v, v != "", true
		}
	}
	v := os.Getenv(lintCacheEnv)
	return v, v != "", true
}

// unscopedCache is why value is not a cache under the checkout, "" when it is.
func unscopedCache(scope workspaceScope, value string, set bool) string {
	if !set {
		return fmt.Sprintf("%s is unset, so the tool falls back to its shared user cache", lintCacheEnv)
	}
	dir := value
	if !filepath.IsAbs(dir) {
		base := scope.dir
		if base == "" {
			base = scope.root
		}
		dir = filepath.Join(base, dir)
	}
	rel, err := filepath.Rel(scope.root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Sprintf("%s=%s is outside the checkout %s", lintCacheEnv, value, scope.root)
	}
	return ""
}

// toolCacheDeny is the verdict for a direct lint run against a cache outside the checkout.
func toolCacheDeny(scope workspaceScope, command string, inv hint.Invocation, why string) ShellVerdict {
	return ShellVerdict{
		Deny: explainDeny(command, inv, fmt.Sprintf("[%s] %s; run `magus run golangci-lint <project>`, which keeps the cache in %s",
			types.ToolCacheUnscoped, why, filepath.Join(scope.root, ".magus", "golangci-lint"))),
		Why:  "A cache shared with other checkouts answers from their results and hides findings this checkout would report: one shared lint cache held 10,202 paths from 5 other checkouts.",
		Rule: denyRule{Name: denyRuleRawTool, Arg: resolvedCommand(inv)},
	}
}
