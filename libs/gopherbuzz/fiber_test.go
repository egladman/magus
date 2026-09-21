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
