package bindings

import (
	"context"
	"errors"
	"fmt"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/interp/bindings/ffi"
	bindgen "github.com/egladman/magus/internal/interp/bindings/gen"
	"github.com/egladman/magus/internal/parsecache"
	"github.com/egladman/magus/libs/diagnostics"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// CallOp calls op on drv's Buzz source with target and req. exported is false, err nil,
// when drv does not export op.Name: the caller turns that into its own loud error or skip.
//
// drv must be a [spells.FuncSource] holding source. Each call runs it in a
// fresh session, so a provider's top level must be idempotent.
//
// An op that can fail returns result\Result::<Resp>. A failed one is the
// error, worded and coded as its Diagnostic. The answer decodes strictly: an
// undeclared or wrong-kind field wraps errMalformedAnswer.
func CallOp[Req, Resp any](ctx context.Context, drv spells.Driver, op bindgen.ProviderOp[Req, Resp], target types.Target, req Req) (resp Resp, exported bool, err error) {
	fs, ok := drv.(spells.FuncSource)
	if !ok || fs.FuncSource() == "" {
		return resp, false, fmt.Errorf("spell %q: %s provider op %s: the spell has no Buzz source to call", drv.Name(), op.Family, op.Name)
	}
	sess, err := execSpellSource(ctx, fs.FuncSource(), op.Name)
	if err != nil {
		return resp, false, fmt.Errorf("spell %q: %w", drv.Name(), err)
	}
	defer sess.Close()
	fn, ok := sess.Exports()[op.Name]
	if !ok {
		return resp, false, nil
	}
	if !fn.IsFun() {
		return resp, true, fmt.Errorf("spell %q: %s is exported but is not a function", drv.Name(), op.Name)
	}
	rv, err := interp.TimeCall(ctx, interp.ModeSpell, func() (vm.Value, error) {
		return sess.CallValue(ctx, fn, []vm.Value{bindgen.ObjectTarget(target, ffi.SessionEnums(ctx, sess, "magus/spell")), op.Encode(req)})
	})
	if err != nil {
		return resp, true, fmt.Errorf("spell %q: %s: %w", drv.Name(), op.Name, err)
	}
	answer, failure, err := readResult(rv)
	if err != nil {
		return resp, true, fmt.Errorf("%w: spell %q: %s: %w", errMalformedAnswer, drv.Name(), op.Name, err)
	}
	if failure != nil {
		return resp, true, fmt.Errorf("spell %q: %s: %w", drv.Name(), op.Name, failure)
	}
	if resp, err = op.Decode(answer); err != nil {
		return resp, true, fmt.Errorf("%w: spell %q: %w", errMalformedAnswer, drv.Name(), err)
	}
	return resp, true, nil
}

// readResult opens rv when it is a result\Result.
// Any other rv is the answer of an op that cannot fail.
func readResult(rv vm.Value) (answer vm.Value, failure error, err error) {
	if rv.ObjectTypeName() != "Result" {
		return rv, nil, nil
	}
	fields, ok := rv.MapView()
	if !ok {
		return vm.Null, nil, errors.New("a Result that is not an object")
	}
	if errValue, _ := fields.MapGet("error"); !errValue.IsNull() {
		d, err := readDiagnostic(errValue)
		if err != nil {
			return vm.Null, nil, err
		}
		return vm.Null, diagnosticError(d), nil
	}
	value, _ := fields.MapGet("value")
	if value.IsNull() {
		return vm.Null, nil, errors.New("the Result carries neither a value nor an error")
	}
	return value, nil, nil
}

// readDiagnostic reads a Result's error.
// A failure with no message would print as an empty error.
func readDiagnostic(v vm.Value) (types.Diagnostic, error) {
	var d types.Diagnostic
	fields, ok := v.MapView()
	if !ok {
		return d, fmt.Errorf("error: want a Diagnostic, got %s", v.TypeName())
	}
	for _, key := range fields.MapKeys() {
		item, _ := fields.MapGet(key)
		if !item.IsString() {
			return d, fmt.Errorf("error.%s: want a str, got %s", key, item.TypeName())
		}
		switch key {
		case "code":
			d.Code = item.AsString()
		case "message":
			d.Message = item.AsString()
		case "url":
			d.URL = item.AsString()
		case "fix":
			d.Fix = item.AsString()
		default:
			return d, fmt.Errorf("error: Diagnostic has no member %q", key)
		}
	}
	if d.Message == "" {
		return d, errors.New("error: a failed Result carries no message")
	}
	return d, nil
}

// diagnosticError is the error a raise of d's fields became.
// A coded one renders "[code] message" and matches errors.Is on the code.
func diagnosticError(d types.Diagnostic) error {
	if d.Code == "" {
		return errors.New(d.Message)
	}
	e := diagnostics.New(func(diagnostics.Code) string { return d.URL }).Errorf(diagnostics.Code(d.Code), "%s", d.Message)
	if d.Fix != "" {
		e = e.WithFix(d.Fix)
	}
	return e
}

// errMalformedAnswer marks a CallOp error as the op answering with the wrong shape, which
// a caller that reads a raised op as an unreachable host must not mistake for one.
var errMalformedAnswer = errors.New("malformed answer")

// providerTarget is the Target a provider op receives: the op's own name, the directory it
// answers for ("" when it answers for none) and the run's charms, which ride on ctx.
func providerTarget(ctx context.Context, name, dir string) types.Target {
	return types.Target{Name: name, Path: dir, Charms: types.CharmsFromContext(ctx)}
}

// execSpellSource runs src in a fresh session with the host modules registered, in the
// script mode resolveWithModules gives a spell's top level, so an op body and that top
// level see one `magus` namespace. fn names the function the session is for, in errors.
func execSpellSource(ctx context.Context, src, fn string) (*buzz.Session, error) {
	sess := buzz.NewSession(ctx, buzz.WithEmbedded(), buzz.WithParseCache(parsecache.Shared()))
	interp.AttachSessionObservers(ctx, sess, interp.ModeSpell)
	RegisterMagusNamespace(ctx, sess)
	if err := registerMagusModules(ctx, sess); err != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("%s: %w", fn, err)
	}
	if err := interp.TimeExec(ctx, interp.ModeSpell, func() error { return sess.Exec(ctx, src) }); err != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("%s: exec: %w", fn, err)
	}
	return sess, nil
}
