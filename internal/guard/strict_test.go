package guard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/guard/builtin"
)

// recoverableRules are the compiled rules that refused before they advised by default.
var recoverableRules = []string{
	"brief-command", "busy-wait", "buzz-unbriefed", "chained-run", "exit-status-echo",
	"filter-without-input", "grep-reader", "interpreter-rewrite", "magus-timeout",
	"output-pipe", "output-redirect", "process-poll", "raw-tool", "read-navigation",
	"scripted-rewrite", "search-translation", "sed-in-place", "sibling-checkout",
	"spawn-unbriefed", "stage-all", "symbol-search", "throwaway-copy", "unknown-env",
}

// strict returns deps with every refusing rule set to deny, so a test pins a denial a
// workspace has to ask for. Advisories keep their defaults: raising them would turn the
// advisory rows of the same tables into denies.
func strict(deps Dependencies) Dependencies {
	deps.Builtins = make(map[string]builtin.Setting)
	for name, d := range builtin.Defaults() {
		deps.Builtins[name] = builtin.Setting{Decision: d}
	}
	for _, name := range recoverableRules {
		deps.Builtins[name] = builtin.Setting{Decision: builtin.Deny}
	}
	return deps
}

func TestStrictDeniesEveryRecoverableRule(t *testing.T) {
	t.Parallel()
	defaults := builtin.Defaults()
	deps := strict(Dependencies{})

	for _, name := range recoverableRules {
		require.Contains(t, defaults, name, "a recoverable rule the table does not carry")
		assert.Equal(t, builtin.Advise, defaults[name], name)
		assert.Equal(t, builtin.Deny, deps.Builtins[name].Decision, name)
	}
	assert.Len(t, deps.Builtins, len(defaults))

	const sed = `sed -i 's/a/b/' f.go`
	assert.Empty(t, Evaluate(Dependencies{}, sed).Deny, "the default advises")
	assert.Equal(t, denyRuleSedInPlace, Evaluate(deps, sed).Rule.Name)
}
