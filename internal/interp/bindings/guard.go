package bindings

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/guard/steprule"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/interp/bindings/ffi"
	bindgen "github.com/egladman/magus/internal/interp/bindings/gen"
	"github.com/egladman/magus/internal/workspace"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/agent"
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
// Shell rules are APPEND-ONLY. Match criteria are the resolved program name plus an
// optional arg subset, judged by ParseCommands.
//
// spawn(), command(), write() and stop() each register the one function the guard calls on
// every agent spawn and continuation, every agent shell command, every agent file write, or
// every agent stop, with allow/advise/deny to build its answer and once/count for state that
// lasts the calling session. step() registers one per kind
// of step an embedding program asks about. See registerEventRules.
//
// builtins() sets compiled rules by name; see registerBuiltins. profile() picks the level
// those settings start from; see registerProfile.
func buildGuard(ctx context.Context, sess *buzz.Session, obs buzz.DirectObserver) vm.Value {
	guardMap := vm.NewMap()
	registerVerdictMembers(ctx, sess, obs, guardMap)
	registerEventRules(ctx, sess, obs, guardMap)
	registerBuiltins(ctx, obs, guardMap)
	registerProfile(ctx, obs, guardMap)
	guardMap.MapSet("shell", directVal(obs, "magus.guard.shell", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) == 0 || !args[0].IsMap() {
			return vm.Null, fmt.Errorf(`magus\guard.shell: expected a rule object`)
		}
		rule, err := parseShellRule(args[0], "shell")
		if err != nil {
			return vm.Null, err
		}
		if reg := workspace.WorkspaceRegistryFromContext(ctx); reg != nil {
			reg.AddShellRule(rule)
		}
		return vm.Null, nil
	}))
	return guardMap
}

// registerBuiltins binds magus\guard.builtins onto guardMap:
//
//	magus\guard.builtins({
//	    "stage-all": "deny",
//	    "raw-tool": "off",
//	    "read-navigation": {"decision": "deny", "lines": 120},
//	});
//
// Each key names a compiled rule and each value sets its decision, off, advise or deny,
// with lines, tokens, calls, chars or markers where the rule takes that parameter. A named rule
// overrides what magus\guard.profile's level sets for it. Every misdeclaration is MGS1045
// and fails the load:
// a rule the workspace believes it set and did not reads as enforced when it is not.
func registerBuiltins(ctx context.Context, obs buzz.DirectObserver, guardMap vm.Value) {
	// Per session, as registerFunctionRule counts.
	registered := false
	guardMap.MapSet("builtins", directVal(obs, "magus.guard.builtins", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) != 1 || !args[0].IsMap() {
			return vm.Null, types.DiagnosticErrorf(types.GuardRuleMisdeclared,
				`magus\guard.builtins: expected one map from rule name to "off", "advise", "deny" or {"decision": ..., "lines": ...}`)
		}
		if path, ok := interp.ProjectPathFromContext(ctx); ok && path != "" && path != "." {
			return vm.Null, types.DiagnosticErrorf(types.GuardRuleMisdeclared,
				`magus\guard.builtins: called from the magusfile of %s; declare the whole workspace's settings in the root magusfile`, path)
		}
		if registered {
			return vm.Null, types.DiagnosticErrorf(types.GuardRuleMisdeclared,
				`magus\guard.builtins: already declared in this magusfile; fold the second map into the first`)
		}
		declared, parseErr := parseBuiltins(args[0])
		_, resolveErr := builtin.Resolve(declared)
		if err := errors.Join(parseErr, resolveErr); err != nil {
			return vm.Null, err
		}
		registered = true
		if reg := workspace.WorkspaceRegistryFromContext(ctx); reg != nil {
			reg.SetBuiltins(declared)
		}
		return vm.Null, nil
	}))
}

// registerProfile binds magus\guard.profile onto guardMap:
//
//	magus\guard.profile("strict");
//
// The level, off, light, standard or strict, is the set of compiled-rule settings the
// workspace starts from before magus\guard.builtins overrides any; unset is light. Every
// misdeclaration is MGS1045 and fails the load, as for builtins.
func registerProfile(ctx context.Context, obs buzz.DirectObserver, guardMap vm.Value) {
	// Per session, as registerFunctionRule counts.
	registered := false
	guardMap.MapSet("profile", directVal(obs, "magus.guard.profile", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		levels := strings.Join(types.GuardProfile("").Values(), ", ")
		if len(args) != 1 || !args[0].IsString() {
			return vm.Null, types.DiagnosticErrorf(types.GuardRuleMisdeclared,
				`magus\guard.profile: expected one level, %s`, levels)
		}
		if path, ok := interp.ProjectPathFromContext(ctx); ok && path != "" && path != "." {
			return vm.Null, types.DiagnosticErrorf(types.GuardRuleMisdeclared,
				`magus\guard.profile: called from the magusfile of %s; declare the whole workspace's level in the root magusfile`, path)
		}
		if registered {
			return vm.Null, types.DiagnosticErrorf(types.GuardRuleMisdeclared,
				`magus\guard.profile: already declared in this magusfile; keep one call`)
		}
		p := types.GuardProfile(args[0].AsString())
		if p == "" || !p.Valid() {
			return vm.Null, types.DiagnosticErrorf(types.GuardRuleMisdeclared,
				`magus\guard.profile: unknown level %q; give one of %s`, string(p), levels)
		}
		registered = true
		if reg := workspace.WorkspaceRegistryFromContext(ctx); reg != nil {
			reg.SetProfile(p)
		}
		return vm.Null, nil
	}))
}

// parseBuiltins decodes the declared map: the settings that decode, and every malformed
// value as one error, so the caller can report those beside what builtin.Resolve finds
// wrong with the rest. A decision name is checked here by builtin.Decision's
// UnmarshalText; rule names and whether a rule takes its decision are left to Resolve.
func parseBuiltins(m vm.Value) (map[string]builtin.Setting, error) {
	out := make(map[string]builtin.Setting, len(m.MapKeys()))
	var errs []error
	for _, name := range m.MapKeys() {
		v, _ := m.MapGet(name)
		s, err := parseBuiltinSetting(v)
		if err != nil {
			errs = append(errs, types.DiagnosticErrorf(types.GuardRuleMisdeclared,
				`magus\guard.builtins: %q: %v`, name, err))
			continue
		}
		out[name] = s
	}
	return out, errors.Join(errs...)
}

func parseBuiltinSetting(v vm.Value) (builtin.Setting, error) {
	if v.IsString() {
		var s builtin.Setting
		err := s.Decision.UnmarshalText([]byte(v.AsString()))
		return s, err
	}
	if !v.IsMap() {
		return builtin.Setting{}, errors.New(`want "off", "advise", "deny" or {"decision": ..., "lines": ...}`)
	}
	var s builtin.Setting
	for _, k := range v.MapKeys() {
		field, _ := v.MapGet(k)
		switch k {
		case "decision":
			if !field.IsString() {
				return s, errors.New(`"decision" must be "off", "advise" or "deny"`)
			}
			if err := s.Decision.UnmarshalText([]byte(field.AsString())); err != nil {
				return s, err
			}
		case "markers":
			// An empty list would reach Resolve as unset and take the rule's own markers,
			// which is not what a workspace declaring none meant.
			if !field.IsList() || len(field.ListItems()) == 0 {
				return s, errors.New(`"markers" must be a list of one or more strings`)
			}
			for i, item := range field.ListItems() {
				if !item.IsString() {
					return s, fmt.Errorf(`"markers"[%d] must be a string`, i)
				}
				s.Markers = append(s.Markers, item.AsString())
			}
		default:
			n := 0
			if field.IsInt() {
				n = int(field.AsInt())
			}
			if !s.SetParam(k, n) {
				return s, fmt.Errorf(`unknown field %q, a setting takes "decision", "lines", "tokens", "calls", "chars" and "markers"`, k)
			}
			// Resolve reads 0 as unset, so a declared 0 must not reach it as one. Resolve
			// owns each parameter's floor and which rules take it.
			if n < 1 {
				return s, fmt.Errorf(`%q must be an int of 1 or more`, k)
			}
		}
	}
	if s.Decision == 0 {
		return s, errors.New(`"decision" is required`)
	}
	return s, nil
}

func parseShellRule(m vm.Value, apiName string) (workspace.ShellRule, error) {
	var rule workspace.ShellRule
	for _, k := range m.MapKeys() {
		v, _ := m.MapGet(k)
		switch k {
		case "name":
			if !v.IsString() || v.AsString() == "" {
				return rule, fmt.Errorf(`magus\guard.%s: "name" must be a non-empty string`, apiName)
			}
			rule.Name = v.AsString()
		case "decision":
			if !v.IsString() {
				return rule, fmt.Errorf(`magus\guard.%s: "decision" must be "deny" or "advise"`, apiName)
			}
			rule.Decision = v.AsString()
		case "program":
			if !v.IsString() || v.AsString() == "" {
				return rule, fmt.Errorf(`magus\guard.%s: "program" must be a non-empty string`, apiName)
			}
			rule.Program = v.AsString()
		case "reason":
			if !v.IsString() || v.AsString() == "" {
				return rule, fmt.Errorf(`magus\guard.%s: "reason" must be a non-empty string`, apiName)
			}
			rule.Reason = v.AsString()
		case "dialect":
			if !v.IsString() || strings.TrimSpace(v.AsString()) == "" {
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
				if !item.IsString() {
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

// registerEventRules binds magus\guard.spawn, command, write and stop onto guardMap. Command
// and write run after the built-in rules; stop runs on each Stop and SubagentStop the
// built-in stop check lets through.
func registerEventRules(ctx context.Context, sess *buzz.Session, obs buzz.DirectObserver, guardMap vm.Value) {
	for _, r := range []functionRule{
		{member: "spawn", signature: "fun(req: SpawnRequest) > GuardVerdict", judges: "every agent spawn",
			set: func(reg *workspace.WorkspaceRegistry, sess *buzz.Session, fn vm.Value) {
				reg.SetSpawnRule(buzzRule(sess, fn, "spawn", bindgen.ObjectSpawnRequest))
			}},
		{member: "command", signature: "fun(req: CommandRequest) > GuardVerdict", judges: "every agent shell command",
			set: func(reg *workspace.WorkspaceRegistry, sess *buzz.Session, fn vm.Value) {
				reg.SetCommandRule(buzzRule(sess, fn, "command", bindgen.ObjectCommandRequest))
			}},
		{member: "write", signature: "fun(req: WriteRequest) > GuardVerdict", judges: "every agent file write",
			set: func(reg *workspace.WorkspaceRegistry, sess *buzz.Session, fn vm.Value) {
				reg.SetWriteRule(buzzRule(sess, fn, "write", bindgen.ObjectWriteRequest))
			}},
		{member: "stop", signature: "fun(req: StopRequest) > GuardVerdict", judges: "every agent stop",
			set: func(reg *workspace.WorkspaceRegistry, sess *buzz.Session, fn vm.Value) {
				reg.SetStopRule(buzzRule(sess, fn, "stop", bindgen.ObjectStopRequest))
			}},
	} {
		registerFunctionRule(ctx, sess, obs, guardMap, r)
	}
	registerStepRule(ctx, sess, obs, guardMap)
}

// registerStepRule binds magus\guard.step(kind, fun) onto guardMap. Each kind
// has one rule, kept on the load's steprule.Set rather than its registry.
// Refusals are MGS1045, as registerFunctionRule's are.
func registerStepRule(ctx context.Context, sess *buzz.Session, obs buzz.DirectObserver, guardMap vm.Value) {
	const name = `magus\guard.step`
	// Per session, as registerFunctionRule counts.
	registered := map[string]bool{}
	guardMap.MapSet("step", directVal(obs, "magus.guard.step", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) != 2 || !args[0].IsString() || !args[1].IsFun() {
			return vm.Null, types.DiagnosticErrorf(types.GuardRuleMisdeclared,
				`%s: expected a kind and one function, step(kind: str, rule: fun(req: StepRequest) > GuardVerdict)`, name)
		}
		kind := args[0].AsString()
		if err := types.ValidateGuardRuleName(kind); err != nil {
			return vm.Null, types.DiagnosticErrorf(types.GuardRuleMisdeclared, `%s: kind: %v`, name, err)
		}
		if path, ok := interp.ProjectPathFromContext(ctx); ok && path != "" && path != "." {
			return vm.Null, types.DiagnosticErrorf(types.GuardRuleMisdeclared,
				`%s: called from the magusfile of %s; register it in the root magusfile, where a rule answering for every %s step in the workspace belongs`, name, path, kind)
		}
		if registered[kind] {
			return vm.Null, types.DiagnosticErrorf(types.GuardRuleMisdeclared,
				`%s: kind %q is already registered in this magusfile, and a workspace has one rule per kind; fold the second one into the first`, name, kind)
		}
		registered[kind] = true
		set, reg := steprule.FromContext(ctx), workspace.WorkspaceRegistryFromContext(ctx)
		if set != nil && reg != nil && sess != nil {
			set.Set(kind, steprule.Rule(buzzRule(sess, args[1], "step", func(req agent.StepRequest, _ ffi.EnumCases) vm.Value {
				return bindgen.ObjectStepRequest(req)
			})))
			// The registry releases the session the rule runs on.
			reg.Keep(sess.Keep())
		}
		return vm.Null, nil
	}))
}

// buzzRule adapts a registered Buzz function to the guard's rule type over requests of
// type R; object renders one as the value the function takes.
func buzzRule[R any](sess *buzz.Session, rule vm.Value, member string, object func(R, ffi.EnumCases) vm.Value) func(context.Context, R, hint.Gate) (types.GuardVerdict, error) {
	return func(ctx context.Context, req R, facts hint.Gate) (types.GuardVerdict, error) {
		return callFunctionRule(ctx, sess, rule, member, object(req, ffi.SessionEnums(ctx, sess, "magus")), facts)
	}
}
