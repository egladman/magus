package buzz

import (
	"context"
	"sync"
	"testing"
)

// TestFiberBasic covers the generator pattern: a fiber yields a sequence of
// values and then returns a final one. resume returns each yielded value, then
// null on completion; resolve returns the cached return value.
func TestFiberBasic(t *testing.T) {
	sess := newSession(context.Background())
	src := `
fun gen() int {
    yield 1;
    yield 2;
    return 3;
}
final f = &gen();
final a = resume f;
final b = resume f;
final c = resume f;
final r = resolve f;
`
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]int64{"a": 1, "b": 2} {
		if got := sess.GetGlobal(name).AsInt(); got != want {
			t.Errorf("%s = %d, want %d", name, got, want)
		}
	}
	if !sess.GetGlobal("c").IsNull() {
		t.Error("resume on fiber completion should return null")
	}
	if got := sess.GetGlobal("r").AsInt(); got != 3 {
		t.Errorf("resolve r = %d, want 3", got)
	}
}

// TestFiberArgsAndClosure checks that &fn(args) forwards arguments and that
// local state (loop counter) survives across yield/resume boundaries.
func TestFiberArgsAndClosure(t *testing.T) {
	sess := newSession(context.Background())
	src := `
fun counter(start) int {
    var i = start;
    while (i < start + 3) {
        yield i;
        i = i + 1;
    }
    return -1;
}
final f = &counter(10);
final a = resume f;
final b = resume f;
final c = resume f;
final d = resume f;
final r = resolve f;
`
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]int64{"a": 10, "b": 11, "c": 12} {
		if got := sess.GetGlobal(name).AsInt(); got != want {
			t.Errorf("%s = %d, want %d", name, got, want)
		}
	}
	if !sess.GetGlobal("d").IsNull() {
		t.Error("resume on fiber completion should return null")
	}
	if got := sess.GetGlobal("r").AsInt(); got != -1 {
		t.Errorf("resolve r = %d, want -1", got)
	}
}

// TestFiberCancellation verifies a non-terminating fiber observes context
// cancellation when resumed (the loop's back-edge polls ctx).
func TestFiberCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sess := newSession(ctx)
	if err := sess.Exec(context.Background(),
		`fun spin() int { var i = 0; while (i >= 0) { i = i + 1; } return i; }
final f = &spin();`); err != nil {
		t.Fatal(err)
	}
	if err := sess.Exec(ctx, `final r = resume f;`); err == nil {
		t.Fatal("resume of infinite-loop fiber under cancelled ctx did not error")
	}
}

// TestFiberRecursiveResumeGuard checks that resuming a fiber from within its own
// (still running) execution is rejected rather than corrupting the VM.
func TestFiberRecursiveResumeGuard(t *testing.T) {
	sess := newSession(context.Background())
	src := `
var f = null;
f = &(fun() int { return resume f; })();
final r = resume f;
`
	if err := sess.Exec(context.Background(), src); err == nil {
		t.Fatal("recursive resume of a running fiber should error")
	}
}

// TestFiberResumeDoneReturnsNull checks that resuming a completed fiber returns
// null (upstream parity: resume on a done fiber is not an error).
func TestFiberResumeDoneReturnsNull(t *testing.T) {
	sess := newSession(context.Background())
	src := `
fun gen() int { return 1; }
final f = &gen();
final a = resume f;
final b = resume f;
`
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	if !sess.GetGlobal("a").IsNull() {
		t.Error("first resume (completes gen) should return null")
	}
	if !sess.GetGlobal("b").IsNull() {
		t.Error("resume of a done fiber should return null")
	}
}

// TestFiberErrorResurfaced checks that a fiber whose VM errors caches the error
// so a *later* resume/resolve re-surfaces it, rather than swallowing it and
// returning null (the fiber is FiberDone after the failure, and the done-branch
// must report the cached error, not a zero return value).
func TestFiberErrorResurfaced(t *testing.T) {
	ctx := context.Background()
	t.Run("resolve then resolve", func(t *testing.T) {
		sess := newSession(ctx)
		if err := sess.Exec(ctx, `fun boom() { throw "boom"; } final f = &boom();`); err != nil {
			t.Fatal(err)
		}
		if err := sess.Exec(ctx, `final a = resolve f;`); err == nil {
			t.Fatal("first resolve of a throwing fiber should error")
		}
		if err := sess.Exec(ctx, `final b = resolve f;`); err == nil {
			t.Fatal("re-resolve of an errored fiber should re-surface the error, not return null")
		}
	})
	t.Run("resume then resolve", func(t *testing.T) {
		sess := newSession(ctx)
		if err := sess.Exec(ctx, `fun boom() { throw "boom"; } final f = &boom();`); err != nil {
			t.Fatal(err)
		}
		if err := sess.Exec(ctx, `final a = resume f;`); err == nil {
			t.Fatal("resume of a throwing fiber should error")
		}
		if err := sess.Exec(ctx, `final b = resolve f;`); err == nil {
			t.Fatal("resolve after a resume that errored should re-surface the error, not return null")
		}
	})
}

// TestYieldOutsideFiberDismissed checks that a top-level yield (no enclosing
// fiber) is silently dismissed and not an error (upstream parity).
func TestYieldOutsideFiberDismissed(t *testing.T) {
	sess := newSession(context.Background())
	if err := sess.Exec(context.Background(), `yield 1;`); err != nil {
		t.Fatalf("yield outside a fiber should not error, got: %v", err)
	}
}

// TestFiberDirectRejected checks that wrapping a direct (Go) callable with &
// is rejected: direct callables have no Buzz bytecode and cannot yield.
func TestFiberDirectRejected(t *testing.T) {
	sess := newSession(context.Background())
	sess.SetGlobal("nat", DirectValue("nat", func(_ context.Context, _ []Value) (Value, error) {
		return IntValue(42), nil
	}))
	if err := sess.Exec(context.Background(), `final f = &nat();`); err == nil {
		t.Fatal("& on a direct callable should error")
	}
}

// TestFiberDebugIntrospectsFiberStack verifies that Frames()/CallDepth() report
// the *fiber's* call stack (not the outer VM's) while a direct callable is executing
// inside a resumed fiber. Before the fix, curVM still pointed at the suspended
// outer VM so the debugger saw the wrong frames.
func TestFiberDebugIntrospectsFiberStack(t *testing.T) {
	sess := newSession(context.Background())

	type snapshot struct {
		depth  int
		frames []DebugFrame
	}
	var got snapshot

	// inner() is called from inside the fiber body so the fiber stack is at
	// least 2 frames deep (top-level + gen + inner). probe() captures the
	// session's view of the live stack at that moment.
	sess.SetGlobal("probe", DirectValue("probe", func(_ context.Context, _ []Value) (Value, error) {
		got.depth = sess.CallDepth()
		got.frames = sess.Frames()
		return Null, nil
	}))

	src := `
fun inner() int { probe(); return 1; }
fun gen() int {
    yield inner();
    return 0;
}
final f = &gen();
final v = resume f;
`
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatal(err)
	}

	// probe() fires inside inner(), which is called from gen() running inside
	// the fiber. Expect at least 2 frames (inner + gen) and the innermost frame
	// to be named "inner".
	if got.depth < 2 {
		t.Errorf("CallDepth = %d, want >= 2 (fiber stack not visible)", got.depth)
	}
	if len(got.frames) == 0 {
		t.Fatal("Frames() returned empty inside fiber")
	}
	if got.frames[0].Name != "inner" {
		t.Errorf("innermost frame = %q, want %q", got.frames[0].Name, "inner")
	}
}

// TestFiberConcurrentSessions mirrors the magus pool model: N goroutines, each
// owning its own Session, each running a fiber generator. Sessions share no
// mutable state, so this must be race-free under -race.
func TestFiberConcurrentSessions(t *testing.T) {
	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	got := make([]int64, n)
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			sess := newSession(context.Background())
			src := `
fun gen() int { yield 1; yield 2; return 7; }
final f = &gen();
var sum = 0;
sum = sum + resume f;
sum = sum + resume f;
resume f;
`
			if err := sess.Exec(context.Background(), src); err != nil {
				errs[g] = err
				return
			}
			got[g] = sess.GetGlobal("sum").AsInt()
		}(g)
	}
	wg.Wait()
	for g := 0; g < n; g++ {
		if errs[g] != nil {
			t.Fatalf("g%d: %v", g, errs[g])
		}
		if got[g] != 3 {
			t.Errorf("g%d sum = %d, want 3", g, got[g])
		}
	}
}
