package vm

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestPatMatchBoundsCatastrophicBacktracking is the regression for the
// unbounded-backtracking hang: `(a+)+$` against a run of 'a's followed by a
// character that can never satisfy the trailing '$' drives regexp2's
// backtracking engine into exponential blowup. PatValue must bound this with
// MatchTimeout so the call returns (as an error) instead of wedging the
// calling goroutine forever. Run via `select` + `time.After` per the repo's
// hang-test idiom (pool_test.go TestDispatchRejectsCycleWithMemo) so a
// regression fails the test instead of hanging the suite.
func TestPatMatchBoundsCatastrophicBacktracking(t *testing.T) {
	p, err := PatValue(`(a+)+$`)
	if err != nil {
		t.Fatalf("PatValue: %v", err)
	}
	vm := NewVM(context.Background())
	fn := patMethod(vm, p, "match")
	if fn == nil {
		t.Fatal("patMethod(match) returned nil")
	}

	subject := strings.Repeat("a", 30) + "!"

	done := make(chan struct{})
	go func() {
		defer close(done)
		// The result is expected to be a MatchTimeoutError once bounded; either
		// outcome (match or error) is fine here, only completion is under test.
		_, _ = fn.Fn(context.Background(), []Value{StrValue(subject)})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pat.match on catastrophic-backtracking pattern did not return within bound")
	}
}
