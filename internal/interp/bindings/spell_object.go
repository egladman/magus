package bindings

import (
	"context"

	"github.com/egladman/magus/internal/proc/run"
	ispell "github.com/egladman/magus/internal/spell"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// spellHandleFromMeta builds the MagusSpell handle a workspace-local spell import
// returns. It marshals the resolved spec back as native data so
// magus.project can decode and register the spell by value at bind time, needed
// because the spell is evaluated in a throwaway session whose functions are gone by
// then.
func spellHandleFromMeta(m ispell.Descriptor) vm.Value {
	h := vm.NewMap()
	h.MapSet("name", vm.StrValue(m.Name))
	h.MapSet("needs", strSliceToBuzzList(m.Needs))
	h.MapSet("provides", strSliceToBuzzList(m.Provides))
	h.MapSet("claims", strSliceToBuzzList(m.Claims))
	h.MapSet("version_cmd", strSliceToBuzzList(m.VersionCmd))
	h.MapSet("language", vm.StrValue(m.Language))
	h.MapSet("opaque", vm.BoolValue(m.Opaque))
	h.MapSet("ops", targetsToBuzzMap(m.Ops))
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
func bindBuzzTargetDispatch(h vm.Value, targets map[string]types.SpellOp) {
	h.MapSet("listTargets", vm.DirectValue("spell.listTargets", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		return strSliceToBuzzList(commandTargetNames(targets)), nil
	}))
	for name, tgt := range targets {
		bindBuzzCommandMethod(h, name, tgt)
	}
}

// bindBuzzCommandMethod attaches tgt as a callable method named target on h,
// so spell.<target>(opts?) forks the target.
func bindBuzzCommandMethod(h vm.Value, target string, tgt types.SpellOp) {
	h.MapSet(target, vm.DirectValue("spell."+target, func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		opts := spellOptsFromBuzz(args, 0)
		res, err := runBuzzCommand(ctx, tgt, opts)
		if err != nil {
			return vm.Null, err
		}
		if tgt.Capture {
			return execRecordToBuzz(res.ToMap()), nil
		}
		return vm.Null, nil
	}))
}

// execRecordToBuzz converts the shared {stdout, stderr, code, ok} exec record to
// a Buzz map, marshalled the same way os.exec's record is (see host.AnyVal):
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
func runBuzzCommand(ctx context.Context, tgt types.SpellOp, opts commandOpts) (run.ExecResult, error) {
	if !opts.hasArgs {
		opts.args = project.ExtraArgs(ctx)
	}
	return runCommand(ctx, tgt, opts)
}

// spellOptsFromBuzz reads an optional {cwd=, args=[...], env={...}} options
// table at args[idx], the Buzz analogue of spellOptsFromLua. opts.hasArgs
// reports whether an "args" key was present, so callers know to fall back to
// project.ExtraArgs when it was not.
func spellOptsFromBuzz(args []vm.Value, idx int) (opts commandOpts) {
	if idx >= len(args) || !args[idx].IsMap() {
		return opts
	}
	o := args[idx]
	if cv, ok := o.MapGet("cwd"); ok && cv.IsStr() {
		opts.cwd = cv.AsString()
	}
	if av, ok := o.MapGet("args"); ok {
		opts.args = buzzValToStringSlice(av)
		opts.hasArgs = true
	}
	if ev, ok := o.MapGet("env"); ok && ev.IsMap() {
		opts.env = map[string]string{}
		for _, k := range ev.MapKeys() {
			if v, ok := ev.MapGet(k); ok {
				opts.env[k] = v.AsString()
			}
		}
	}
	if sv, ok := o.MapGet("stdin"); ok && sv.IsStr() {
		opts.stdin = sv.AsString()
	}
	return opts
}

// targetsToBuzzMap marshals resolved targets back to the nested ops map shape
// ispell.Decode reads (a fork target unless it declares fn).
func targetsToBuzzMap(targets map[string]types.SpellOp) vm.Value {
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
// list shape ispell.Decode reads.
func patchOpsToBuzzList(ops []types.PatchOp) vm.Value {
	items := make([]vm.Value, len(ops))
	for i, po := range ops {
		m := vm.NewMap()
		m.MapSet("op", vm.StrValue(po.Op))
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

	spec, ok := ispell.Builtins()[name]
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
