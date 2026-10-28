package bindings

import (
	"context"
	"errors"
	"fmt"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/interp/bindings/ffi"
	bindgen "github.com/egladman/magus/internal/interp/bindings/gen"
	"github.com/egladman/magus/internal/parsecache"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// CallOp calls op on drv's Buzz source with target and req. exported is false, err nil,
// when drv does not export op.Name: the caller turns that into its own loud error or skip.
//
// drv must be a [spells.FuncSource] holding source. The source runs in a fresh session per
// call, so a provider's top level must be idempotent. What the op returns decodes strictly:
// a field the response does not declare, or one of the wrong kind, is an error naming it
// and wrapping errMalformedAnswer.
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
	if resp, err = op.Decode(rv); err != nil {
		return resp, true, fmt.Errorf("%w: spell %q: %w", errMalformedAnswer, drv.Name(), err)
	}
	return resp, true, nil
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
	registerMagusModules(ctx, sess)
	if err := interp.TimeExec(ctx, interp.ModeSpell, func() error { return sess.Exec(ctx, src) }); err != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("%s: exec: %w", fn, err)
	}
	return sess, nil
}
