package bindings

import (
	"context"
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/internal/spellruntime"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
)

// spellHandleFromMeta builds the MagusSpell handle a workspace-local spell import
// returns. It marshals the resolved spec back as native data so
// magus.project can decode and register the spell by value at bind time, needed
// because the spell is evaluated in a throwaway session whose functions are gone by
// then.
func spellHandleFromMeta(m spells.Descriptor) vm.Value {
	h := vm.NewMap()
	h.MapSet("name", vm.StrValue(m.Name))
	h.MapSet("needs", strSliceToBuzzList(m.Needs))
	h.MapSet("provides", strSliceToBuzzList(m.Provides))
	h.MapSet("claims", strSliceToBuzzList(m.Claims))
	h.MapSet("language", vm.StrValue(m.Language))
	h.MapSet("opaque", vm.BoolValue(m.Opaque))
	h.MapSet("ops", targetsToMap(m.Ops))
	bindBuzzTargetDispatch(h, m.Ops)
	return h
}

// bindBuzzTargetDispatch wires a Buzz spell handle's runnable surface:
//
//   - spell.<target>(opts?): a callable per fork target. This is the way to
//     invoke an op: docker.build({cwd: "..", args: ["-t", tag, "."]}), go.generate().
//   - listTargets(): returns the runnable target names, for introspection.
//
// A method's optional {cwd=, args=[...], env={...}} table appends opts.args to
// the target's base argv and overlays opts.env on the subprocess, so
// flag-carrying and cross-compile invocations need no os.exec. With no opts.args
// the `magus run <t> -- <extra>` args ride along via project.ExtraArgs.
func bindBuzzTargetDispatch(h vm.Value, targets map[string]spells.Op) {
	h.MapSet("listTargets", vm.DirectValue("spell.listTargets", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		return strSliceToBuzzList(commandTargetNames(targets)), nil
	}))
	for name, tgt := range targets {
		bindBuzzCommandMethod(h, name, tgt)
	}
}

// bindBuzzCommandMethod attaches tgt as a callable method named target on h,
// so spell.<target>(opts?) forks the target.
func bindBuzzCommandMethod(h vm.Value, target string, tgt spells.Op) {
	h.MapSet(target, vm.DirectValue("spell."+target, func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		// The leading magus.Context is REQUIRED, not optional. Optional would overload
		// argument one on TYPE - f(), f(ctx), f({args:...}), f(ctx, {args:...}) - which
		// reads fine to the author and badly to everyone else. One shape always, and a
		// grep that finds every op invocation.
		base, consumed := ctxOverridesFromBuzz(args, 0)
		if consumed == 0 {
			return vm.Null, fmt.Errorf(
				"%s: pass the target's context as the first argument, %s(ctx); override env or cwd for one call with ctx.withEnv({...}) / ctx.withCwd(\"..\")",
				target, target)
		}
		opts, err := spellOptsFromBuzz(args, consumed)
		if err != nil {
			return vm.Null, fmt.Errorf("%s: %w", target, err)
		}
		opts.cwd, opts.env = base.cwd, base.env
		res, err := runBuzzCommand(ctx, tgt, opts)
		if err != nil {
			return vm.Null, err
		}
		if tgt.Capture {
			return execRecordToBuzz(res.BuzzObject()), nil
		}
		return vm.Null, nil
	}))
}

// execRecordToBuzz converts the shared {stdout, stderr, code, ok} exec object to
// a Buzz map, marshalled the same way os.exec's object is (see host.AnyVal):
// string/bool direct, int as a Buzz int.
func execRecordToBuzz(rec map[string]any) vm.Value {
	m := vm.NewMap()
	for k, v := range rec {
		switch x := v.(type) {
		case string:
			m.MapSet(k, vm.StrValue(x))
		case bool:
			m.MapSet(k, vm.BoolValue(x))
		case int:
			m.MapSet(k, vm.IntValue(int64(x)))
		}
	}
	return m
}

// runBuzzCommand forks tgt (opts.cwd defaults to the process cwd in
// runCommand). With explicit opts.args it uses them; otherwise it forwards the
// `magus run <t> -- <extra>` args, so a bare go.test() still threads through
// `magus run test -- -run X`.
func runBuzzCommand(ctx context.Context, tgt spells.Op, opts commandOpts) (run.ExecResult, error) {
	if !opts.hasArgs {
		opts.args = project.ExtraArgs(ctx)
	}
	return runCommand(ctx, tgt, opts)
}

// ctxOverridesFromBuzz reads execution overrides off a leading magus.Context and
// reports how many arguments it consumed, so the caller can parse opts from the next
// one. An op invoked as go["go-test"](ctx, {args: [...]}) gets its cwd/env from the
// context; go["go-test"]({args: [...]}) (no context) is the transitional form and
// consumes nothing.
//
// Charms are deliberately NOT readable here. They are run-level - what makes "what did
// this run do" answerable from the invocation alone - and a per-op override would move
// that reasoning from global to local.
// ctxOverridesFromBuzz reads execution overrides off a leading magus.Context and
// reports how many arguments it consumed, so the caller can parse opts from the next
// one. An op invoked as go["go-test"](ctx, {args: [...]}) gets its cwd/env from the
// context; go["go-test"]({args: [...]}) (no context) is the transitional form and
// consumes nothing.
//
// Charms are deliberately NOT readable here. They are run-level - what makes "what did
// this run do" answerable from the invocation alone - and a per-op override would move
// that reasoning from global to local.
func ctxOverridesFromBuzz(args []vm.Value, idx int) (opts commandOpts, consumed int) {
	if idx >= len(args) || !args[idx].IsMap() {
		return opts, 0
	}
	if m, ok := args[idx].MapGet(ctxMarker); !ok || !m.IsBool() || !m.AsBool() {
		return opts, 0
	}
	c := args[idx]
	if cv, ok := c.MapGet("cwd"); ok && cv.IsStr() {
		opts.cwd = cv.AsString()
	}
	if ev, ok := c.MapGet("env"); ok && ev.IsMap() {
		opts.env = map[string]string{}
		for _, k := range ev.MapKeys() {
			if v, ok := ev.MapGet(k); ok && v.IsStr() {
				opts.env[k] = v.AsString()
			}
		}
	}
	return opts, 1
}

// spellOptsFromBuzz reads an optional {cwd=, args=[...], env={...}} options table at
// args[idx]. opts.hasArgs reports whether an "args" key was present, so callers know to
// fall back to project.ExtraArgs when it was not.
func spellOptsFromBuzz(args []vm.Value, idx int) (opts commandOpts, err error) {
	if idx >= len(args) || !args[idx].IsMap() {
		return opts, nil
	}
	o := args[idx]
	// cwd and env are REJECTED here, not read. They are execution context, and the
	// context is the only channel that reaches the cache key: set through the opts
	// table they changed what the tool did while the key said otherwise, and they won
	// over the derived value that WAS in the key. Two doors, one of them unchecked.
	for _, k := range []string{"cwd", "env"} {
		if _, has := o.MapGet(k); has {
			return opts, fmt.Errorf("%q belongs on the context, not the options table: use ctx.with%s%s(...) so it reaches the cache key",
				k, strings.ToUpper(k[:1]), k[1:])
		}
	}
	if av, ok := o.MapGet("args"); ok {
		opts.args = buzzValToStringSlice(av)
		opts.hasArgs = true
	}
	if sv, ok := o.MapGet("stdin"); ok && sv.IsStr() {
		opts.stdin = sv.AsString()
	}
	return opts, nil
}

// targetsToMap marshals resolved targets back to the nested ops map shape
// spellruntime.Decode reads (a fork target unless it declares fn).
func targetsToMap(targets map[string]spells.Op) vm.Value {
	ops := vm.NewMap()
	for name, t := range targets {
		op := vm.NewMap()
		if t.Bin != "" {
			op.MapSet("bin", vm.StrValue(t.Bin))
		}
		if len(t.Args) > 0 {
			op.MapSet("args", strSliceToBuzzList(t.Args))
		}
		if len(t.Charms) > 0 {
			charms := vm.NewMap()
			for cn, c := range t.Charms {
				ce := vm.NewMap()
				ce.MapSet("ops", patchOpsToBuzzList(c.Ops))
				charms.MapSet(cn, ce)
			}
			op.MapSet("charms", charms)
		}
		ops.MapSet(name, op)
	}
	return ops
}

// patchOpsToBuzzList marshals a charm's RFC 6902 ops back to the array-of-records
// list shape spellruntime.Decode reads.
func patchOpsToBuzzList(ops []spells.PatchOp) vm.Value {
	items := make([]vm.Value, len(ops))
	for i, po := range ops {
		m := vm.NewMap()
		m.MapSet("op", vm.StrValue(string(po.Op)))
		m.MapSet("path", vm.StrValue(po.Path))
		if po.Value != "" {
			m.MapSet("value", vm.StrValue(po.Value))
		}
		if po.From != "" {
			m.MapSet("from", vm.StrValue(po.From))
		}
		items[i] = m
	}
	return vm.ListValue(items)
}

// buzzSpellObject returns a spell handle map with the spell's full spec:
// name, needs, claims, provides, plus listTargets() and a callable per target.
func buzzSpellObject(name string) vm.Value {
	m := vm.NewMap()
	m.MapSet("name", vm.StrValue(name))

	spec, ok := spellruntime.Builtins()[name]
	if !ok {
		return m
	}

	m.MapSet("needs", strSliceToBuzzList(spec.Needs))
	m.MapSet("claims", strSliceToBuzzList(spec.Claims))
	m.MapSet("provides", strSliceToBuzzList(spec.Provides))

	// listTargets() + a callable per fork target (go.test(), docker.build()).
	bindBuzzTargetDispatch(m, spec.Ops)

	return m
}
