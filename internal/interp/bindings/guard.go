package bindings

import (
	"context"
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/workspace"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
)

// buildGuard assembles magus\guard for a magusfile. shell() declares one
// additive shell rule the agent guard strengthens with after its compiled
// built-ins:
//
//	magus\guard.shell({
//	    name: "no-curl-prod",
//	    decision: "deny",
//	    program: "curl",
//	    args: ["https://prod.example/health"],
//	    reason: "Do not hit prod from an agent shell.",
//	    dialect: "bash",
//	})
//
// Rules are APPEND-ONLY and cannot disable a built-in. Match criteria are the
// resolved program name plus an optional arg subset, judged by ParseCommands.
//
// spawn() registers the one function the guard calls on every agent spawn and
// continuation, with allow/advise/deny to build its answer and once/count for state
// that lasts the calling session. See registerSpawnRule.
func buildGuard(ctx context.Context, sess *buzz.Session, obs buzz.DirectObserver) vm.Value {
	guardMap := vm.NewMap()
	registerSpawnRule(ctx, sess, obs, guardMap)
	registerShellRule := func(member, obsName, defaultDialect string) {
		guardMap.MapSet(member, directVal(obs, obsName, func(_ context.Context, args []vm.Value) (vm.Value, error) {
			if len(args) == 0 || !args[0].IsMap() {
				return vm.Null, fmt.Errorf(`magus\guard.%s: expected a rule object`, member)
			}
			rule, err := parseShellRule(args[0], member, defaultDialect)
			if err != nil {
				return vm.Null, err
			}
			if reg := workspace.WorkspaceRegistryFromContext(ctx); reg != nil {
				reg.AddShellRule(rule)
			}
			return vm.Null, nil
		}))
	}
	registerShellRule("shell", "magus.guard.shell", "")
	// compat(until: no magusfile still calls guard.bash): observe via magus refs / workspace search for guard.bash
	registerShellRule("bash", "magus.guard.bash", "bash")
	return guardMap
}

func parseShellRule(m vm.Value, apiName, defaultDialect string) (workspace.ShellRule, error) {
	var rule workspace.ShellRule
	for _, k := range m.MapKeys() {
		v, _ := m.MapGet(k)
		switch k {
		case "name":
			if !v.IsStr() || v.AsString() == "" {
				return rule, fmt.Errorf(`magus\guard.%s: "name" must be a non-empty string`, apiName)
			}
			rule.Name = v.AsString()
		case "decision":
			if !v.IsStr() {
				return rule, fmt.Errorf(`magus\guard.%s: "decision" must be "deny" or "advise"`, apiName)
			}
			rule.Decision = v.AsString()
		case "program":
			if !v.IsStr() || v.AsString() == "" {
				return rule, fmt.Errorf(`magus\guard.%s: "program" must be a non-empty string`, apiName)
			}
			rule.Program = v.AsString()
		case "reason":
			if !v.IsStr() || v.AsString() == "" {
				return rule, fmt.Errorf(`magus\guard.%s: "reason" must be a non-empty string`, apiName)
			}
			rule.Reason = v.AsString()
		case "dialect":
			if !v.IsStr() || strings.TrimSpace(v.AsString()) == "" {
				return rule, fmt.Errorf(`magus\guard.%s: "dialect" must be a non-empty string`, apiName)
			}
			if _, err := parseShellDialect(v.AsString()); err != nil {
				return rule, fmt.Errorf(`magus\guard.%s: %w`, apiName, err)
			}
			rule.Dialect = strings.ToLower(strings.TrimSpace(v.AsString()))
		case "args":
			if !v.IsList() {
				return rule, fmt.Errorf(`magus\guard.%s: "args" must be a list of strings`, apiName)
			}
			for i, item := range v.ListItems() {
				if !item.IsStr() {
					return rule, fmt.Errorf(`magus\guard.%s: "args"[%d] must be a string`, apiName, i)
				}
				rule.Args = append(rule.Args, item.AsString())
			}
		default:
			return rule, fmt.Errorf(`magus\guard.%s: unknown field %q`, apiName, k)
		}
	}
	if rule.Name == "" || rule.Program == "" || rule.Reason == "" {
		return rule, fmt.Errorf(`magus\guard.%s: name, program, and reason are required`, apiName)
	}
	switch rule.Decision {
	case "deny", "advise":
	default:
		return rule, fmt.Errorf(`magus\guard.%s: decision must be "deny" or "advise" (got %q)`, apiName, rule.Decision)
	}
	if rule.Dialect == "" && defaultDialect != "" {
		rule.Dialect = defaultDialect
	}
	return rule, nil
}

func parseShellDialect(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "posix", "bash", "mksh", "zsh", "bats":
		return strings.ToLower(strings.TrimSpace(s)), nil
	default:
		return "", fmt.Errorf(`unknown shell dialect %q (want posix, bash, mksh, zsh, or bats)`, s)
	}
}
