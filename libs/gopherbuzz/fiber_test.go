package buzz_test //nolint:testlayout // in-package would close a cycle: gopherbuzz/std imports gopherbuzz

import (
	"context"
	"errors"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The acquire/yield/release shape a scoped-resource helper is written in. `held`
// records whether the release half ran, which is the only thing these tests ask.
const guardSource = `
var held = false;
var entered = false;

fun guarded() > void *> int {
    held = true;
    entered = true;
    _ = yield 1;
    held = false;
}
`

func newGuardSession(t *testing.T) *buzz.Session {
	t.Helper()
	s := buzz.NewSession(t.Context(), buzz.WithEmbedded())
	require.NoError(t, s.Exec(t.Context(), guardSource), "exec guard source")
	return s
}

func held(t *testing.T, s *buzz.Session) bool {
	t.Helper()
	v, ok := s.Globals()["held"]
	require.True(t, ok, "global 'held' missing")
	return v.AsBool()
}

// The baseline this whole prototype exists to beat: Buzz's own foreach abandons a
// fiber when the body throws, so the code after the yield never runs and a lock
// taken before it leaks for the rest of the invocation.
func TestForeachAbandonsAFiberWhenTheBodyThrows(t *testing.T) {
	s := newGuardSession(t)
	err := s.Exec(t.Context(), `
try {
    foreach (_ in &guarded()) { throw "body failed"; }
} catch (e) { _ = e; }
`)
	require.NoError(t, err)
	assert.True(t, held(t, s), "foreach was expected to leak; if this fails, Buzz gained finalization and the host driver is unnecessary")
}

// The prototype: the HOST drives the fiber, so finalization is a Go defer and runs
// whatever the region body did. This is the capability foreach cannot offer.
func TestHostDrivenFiberFinalizesAfterAFailedRegion(t *testing.T) {
	s := newGuardSession(t)

	fiber, err := s.Eval(t.Context(), `return &guarded()`)
	require.NoError(t, err)

	regionErr := func() (err error) {
		// Finalization: drive past the yield no matter how the region exited.
		// Idempotent, so a region that completed normally costs nothing.
		defer func() {
			if _, ferr := s.ResolveFiber(t.Context(), fiber); ferr != nil && err == nil {
				err = ferr
			}
		}()
		if _, err := s.ResumeFiber(t.Context(), fiber); err != nil {
			return err
		}
		assert.True(t, held(t, s), "the resource should be held between the yield and finalization")
		return errors.New("region body failed")
	}()

	require.EqualError(t, regionErr, "region body failed", "the body's own error must survive finalization")
	assert.False(t, held(t, s), "the host driver did not finalize the fiber; the release never ran")
}

// The ordinary path still works, and finalizing twice is not an error: a region
// that ran to completion must not turn a success into a failure.
func TestHostDrivenFiberFinalizationIsIdempotent(t *testing.T) {
	s := newGuardSession(t)
	fiber, err := s.Eval(t.Context(), `return &guarded()`)
	require.NoError(t, err)

	_, err = s.ResumeFiber(t.Context(), fiber)
	require.NoError(t, err)
	_, err = s.ResolveFiber(t.Context(), fiber)
	require.NoError(t, err)
	assert.False(t, held(t, s))
	fib, ok := vm.AsFiber(fiber)
	require.True(t, ok)
	assert.Equal(t, vm.FiberDone, fib.Status(), "a resolved fiber reports done")

	_, err = s.ResolveFiber(t.Context(), fiber)
	assert.NoError(t, err, "finalizing a completed fiber is a no-op")
}

// A region that is never entered must still be safe to finalize: the host takes the
// defer before the first resume, so the failure path runs with a fresh fiber.
func TestHostDrivenFiberFinalizesOneThatNeverStarted(t *testing.T) {
	s := newGuardSession(t)
	fiber, err := s.Eval(t.Context(), `return &guarded()`)
	require.NoError(t, err)

	_, err = s.ResolveFiber(t.Context(), fiber)
	require.NoError(t, err)
	assert.False(t, held(t, s), "a fiber resolved without a prior resume still runs its release")

	entered, ok := s.Globals()["entered"]
	require.True(t, ok)
	assert.True(t, entered.AsBool(), "resolve runs the acquire half too, so the body did execute")
}

// Cancellation has to reach a parked region, or a sibling's failure leaves the
// invocation waiting on a resource nobody will release.
func TestHostDrivenFiberFinalizationHonorsCancellation(t *testing.T) {
	s := newGuardSession(t)
	fiber, err := s.Eval(t.Context(), `return &guarded()`)
	require.NoError(t, err)
	_, err = s.ResumeFiber(t.Context(), fiber)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = s.ResolveFiber(ctx, fiber)
	assert.ErrorIs(t, err, context.Canceled, "a cancelled finalization reports the cause")
}

// The cancellation path is the one that matters for cleanup, and it is the one a naive
// `defer ResolveFiber(ctx, f)` gets wrong: ResolveFiber checks ctx.Err() first, so a
// cancelled run skips the release half entirely. Finalizing needs a live context.
func TestHostDrivenFiberFinalizesUnderCancellationOnlyWithoutCancel(t *testing.T) {
	t.Run("a cancelled context skips the release", func(t *testing.T) {
		s := newGuardSession(t)
		fiber, err := s.Eval(t.Context(), `return &guarded()`)
		require.NoError(t, err)
		_, err = s.ResumeFiber(t.Context(), fiber)
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, _ = s.ResolveFiber(ctx, fiber)
		assert.True(t, held(t, s), "documents the hazard: the release did NOT run")
	})

	t.Run("WithoutCancel runs it", func(t *testing.T) {
		s := newGuardSession(t)
		fiber, err := s.Eval(t.Context(), `return &guarded()`)
		require.NoError(t, err)
		_, err = s.ResumeFiber(t.Context(), fiber)
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err = s.ResolveFiber(context.WithoutCancel(ctx), fiber)
		require.NoError(t, err)
		assert.False(t, held(t, s), "the release must run even though the run was cancelled")
	})
}

// The mechanism ctx.needs would ride: a host NATIVE suspends the fiber it is running
// inside, so a body waiting on something slow hands control back instead of pinning
// its session and its slot for the duration.
//
// This is the load-bearing claim for driving target bodies as fibers. If it fails, a
// blocking native cannot become a suspending one and the whole approach is dead.
func TestNativeCanSuspendTheFiberItRunsInside(t *testing.T) {
	s := buzz.NewSession(t.Context(), buzz.WithEmbedded())

	resumes := 0
	mod := vm.NewMap()
	mod.MapSet("wait", vm.DirectValue("host.wait", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		// Parks the body and hands the host what it is waiting on. The same value is
		// what the call evaluates to once resumed, so the native answers the body here
		// and runs exactly once.
		resumes++
		return vm.Null, vm.Suspend(vm.StrValue("dependencies"))
	}))
	s.SetNativeModule("host", mod)

	require.NoError(t, s.Exec(t.Context(), `
import "host";
var reached = "no";
fun body() > void *> str !> any {
    final answer = host.wait();
    reached = answer;
}
`))
	fiber, err := s.Eval(t.Context(), `return &body()`)
	require.NoError(t, err)

	// First resume runs until the native parks it, and reports what it is waiting on.
	waitingOn, err := s.ResumeFiber(t.Context(), fiber)
	require.NoError(t, err)
	require.True(t, waitingOn.IsStr(), "the native's suspend value must reach the driver")
	assert.Equal(t, "dependencies", waitingOn.AsString())

	reached, ok := s.Globals()["reached"]
	require.True(t, ok)
	assert.Equal(t, "no", reached.AsString(), "the body must be parked, not finished")

	// The host would run the dependencies here, then resume.
	_, err = s.ResumeFiber(t.Context(), fiber)
	require.NoError(t, err)

	reached, ok = s.Globals()["reached"]
	require.True(t, ok)
	require.Equalf(t, 1, resumes, "the native must run ONCE; ran %d time(s)", resumes)
	require.Truef(t, reached.IsStr(), "after resume the body assigned a %s", reached.Kind())
	assert.Equal(t, "dependencies", reached.AsString(),
		"the call evaluates to the suspended value, exactly as `yield v` evaluates to v")
}

// ctx.needs is never called at the top of a fiber: a target body calls it, and that body
// may be several Buzz frames deep. The suspend has to unwind and re-enter through all of
// them, or the mechanism only works in the toy case.
func TestNativeSuspendSurvivesNestedFrames(t *testing.T) {
	s := buzz.NewSession(t.Context(), buzz.WithEmbedded())

	calls := 0
	mod := vm.NewMap()
	mod.MapSet("wait", vm.DirectValue("host.wait", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		calls++
		return vm.Null, vm.Suspend(vm.StrValue("answered"))
	}))
	s.SetNativeModule("host", mod)

	// `landed`, not `out`: out is a Buzz keyword.
	require.NoError(t, s.Exec(t.Context(), `
import "host";
var landed = "none";
fun inner() > str !> any { return host.wait(); }
fun middle() > str !> any { return inner(); }
fun body() > void *> str !> any { landed = middle(); }
`))
	fiber, err := s.Eval(t.Context(), `return &body()`)
	require.NoError(t, err)

	parked, err := s.ResumeFiber(t.Context(), fiber)
	require.NoError(t, err)
	require.True(t, parked.IsStr())
	assert.Equal(t, "answered", parked.AsString(), "the suspend must cross three frames to reach the driver")

	_, err = s.ResumeFiber(t.Context(), fiber)
	require.NoError(t, err)

	landed, ok := s.Globals()["landed"]
	require.True(t, ok)
	require.Truef(t, landed.IsStr(), "after resume the nested call assigned a %s", landed.Kind())
	assert.Equal(t, "answered", landed.AsString(), "execution must resume inside the innermost frame")
	assert.Equal(t, 1, calls, "the native runs once; the suspend value is its answer")
}

// A suspending native must not corrupt the stack when the fiber is driven to completion
// by resolve rather than stepped by resume: resolve dismisses yields, so it drives the
// re-execution itself and must land on the same answer.
func TestNativeSuspendUnderResolve(t *testing.T) {
	s := buzz.NewSession(t.Context(), buzz.WithEmbedded())

	calls := 0
	mod := vm.NewMap()
	mod.MapSet("wait", vm.DirectValue("host.wait", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		calls++
		return vm.Null, vm.Suspend(vm.StrValue("resolved"))
	}))
	s.SetNativeModule("host", mod)

	require.NoError(t, s.Exec(t.Context(), `
import "host";
var landed = "none";
fun body() > void *> str !> any { landed = host.wait(); }
`))
	fiber, err := s.Eval(t.Context(), `return &body()`)
	require.NoError(t, err)

	_, err = s.ResolveFiber(t.Context(), fiber)
	require.NoError(t, err)

	landed, ok := s.Globals()["landed"]
	require.True(t, ok)
	require.Truef(t, landed.IsStr(), "resolve left a %s behind", landed.Kind())
	assert.Equal(t, "resolved", landed.AsString())
}
