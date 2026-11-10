package bindgen

import (
	"context"
	"errors"
	"testing"

	"github.com/egladman/magus/internal/interp/bindings/ffi"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

// CallbacksSuite runs the generated adapters against real Buzz function values.
type CallbacksSuite struct {
	suite.Suite
	sess *buzz.Session
}

func TestCallbacks(t *testing.T) {
	suite.Run(t, new(CallbacksSuite))
}

func (s *CallbacksSuite) SetupTest() {
	s.sess = buzz.NewSession(s.T().Context(), buzz.WithEmbedded())
	s.T().Cleanup(func() { _ = s.sess.Close() })
}

// fun runs src and returns the global it binds as the only argument a
// trampoline would pass.
func (s *CallbacksSuite) fun(src, name string) []vm.Value {
	s.Require().NoError(s.sess.Exec(s.T().Context(), src))
	return []vm.Value{s.sess.GetGlobal(name)}
}

func (s *CallbacksSuite) TestAnAbsentArgumentIsANilFunc() {
	s.Nil(callbackVoid(s.sess, nil, 0))
	s.Nil(callbackVoid(s.sess, []vm.Value{vm.StringValue("not a function")}, 0))
}

func (s *CallbacksSuite) TestAnyResultCrossesAsAGoValue() {
	cb := callbackReturnsAny(s.sess, s.fun(`final f = fun() > str { return "payload"; };`, "f"), 0)
	got, err := cb(s.T().Context())
	s.Require().NoError(err)
	s.Equal("payload", got)
}

func (s *CallbacksSuite) TestArgumentsCrossByPosition() {
	cb := callbackStringBoolReturnsWalkStep(s.sess, s.fun(`
final f = fun(path: str, isDir: bool) > str {
    if (isDir and path == "sub") { return "skipDir"; }
    return "next";
};`, "f"), 0)
	step, err := cb(s.T().Context(), "sub", true)
	s.Require().NoError(err)
	s.Equal(types.WalkSkipDir, step)
}

func (s *CallbacksSuite) TestAWalkStepOutsideItsCasesIsRefused() {
	cb := callbackStringBoolReturnsWalkStep(s.sess, s.fun(`final f = fun(path: str, isDir: bool) > str { return "onward"; };`, "f"), 0)
	_, err := cb(s.T().Context(), "a.txt", false)
	s.ErrorContains(err, "want a WalkStep: next, skipDir, skipAll")
}

func (s *CallbacksSuite) TestABoolResultMustBeABool() {
	cb := callbackStringReturnsBool(s.sess, s.fun(`final f = fun(s: str) > any { return "yes"; };`, "f"), 0)
	_, err := cb(s.T().Context(), "a")
	s.ErrorContains(err, "want bool")
}

func (s *CallbacksSuite) TestABoolResultCrosses() {
	cb := callbackStringSliceReturnsBool(s.sess, s.fun(`final f = fun(paths: [str]) > bool { return paths.len() == 2; };`, "f"), 0)
	done, err := cb(s.T().Context(), []string{"a", "b"})
	s.Require().NoError(err)
	s.True(done)
}

// TestRetryErrorsKeepTheirClass: a host error raised in the callback keeps
// its chain across the VM, so a non-retryable one ends os\retry at once. A
// thrown string carries no type, so every attempt runs.
func (s *CallbacksSuite) TestRetryErrorsKeepTheirClass() {
	refused := types.NonRetryable(errors.New("hook is not a command"), types.RetryAfterConfig, "edit the hook")
	calls := 0
	s.sess.SetGlobal("refuse", vm.DirectValue("refuse", func(context.Context, []vm.Value) (vm.Value, error) {
		calls++
		return vm.Null, ffi.Error(refused)
	}))
	s.sess.SetGlobal("tick", vm.DirectValue("tick", func(context.Context, []vm.Value) (vm.Value, error) {
		calls++
		return vm.Null, nil
	}))
	s.Require().NoError(s.sess.Exec(s.T().Context(), `
final viaHost = fun() > void !> any { refuse(); };
final thrown = fun() > void !> any { tick(); throw "flaky"; };
`))
	callback := func(name string) func(context.Context) (any, error) {
		return callbackReturnsAny(s.sess, []vm.Value{s.sess.GetGlobal(name)}, 0)
	}
	fast := types.RetryOptions{BackoffMs: new(0.0)}

	_, err := std.OsRetry(s.T().Context(), 3, callback("viaHost"), fast)
	var typed *types.NonRetryableError
	s.Require().ErrorAs(err, &typed)
	s.Equal(types.RetryAfterConfig, types.ClassifyRetry(err).Change)
	s.Equal(1, calls)

	calls = 0
	_, err = std.OsRetry(s.T().Context(), 3, callback("thrown"), fast)
	s.Require().ErrorContains(err, "flaky")
	s.True(types.ClassifyRetry(err).Retryable)
	s.Equal(3, calls)
}
