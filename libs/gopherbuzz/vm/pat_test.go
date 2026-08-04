package vm

// Internal (package vm) so the tests can drive patMethod directly: the pat
// methods are only reachable through method dispatch from outside, which would
// put a whole session between the assertion and the matcher under test.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// catastrophicPat and catastrophicSubject are the classic exponential-backtracking
// pair: nested quantifiers over the same character, and a subject that matches the
// inner group all the way to the end and then fails the anchor, so regexp2 must try
// every way of splitting the run of 'a's before it can say no.
const (
	catastrophicPat     = `(a+)+$`
	catastrophicSubject = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaab"
)

// callPatMethod invokes one pat method the way OpInvoke would.
func callPatMethod(t *testing.T, po Value, name string, args ...Value) (Value, error) {
	t.Helper()
	m := patMethod(NewVM(context.Background()), po, name)
	require.False(t, m.IsNull(), "pat has no method %q", name)
	return m.asDirect().Fn(context.Background(), args)
}

// TestPatMatchTimesOutOnCatastrophicBacktracking is the regression test for the
// ReDoS hang: regexp2 backtracks, took no deadline, and had no ctx to cancel
// against, so this pattern ran past a 30s SIGKILL. It must now come back as an
// ordinary buzz error.
//
// The deadline goroutine is the point of the test, so a regression fails fast
// here rather than hanging the package until `go test` gives up at 10 minutes.
func TestPatMatchTimesOutOnCatastrophicBacktracking(t *testing.T) {
	t.Parallel() // it costs patMatchTimeout in wall time; overlap it with the sibling
	p, err := PatValue(catastrophicPat)
	require.NoError(t, err, "compile")

	type result struct {
		v   Value
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := callPatMethod(t, p, "match", StrValue(catastrophicSubject))
		done <- result{v, err}
	}()

	// Generous next to patMatchTimeout (3s): the fastclock ticks on a period, so
	// the bound is approximate, and a loaded machine can add a second on top.
	select {
	case got := <-done:
		require.Error(t, got.err, "match must fail, not return %v", got.v)
		require.Contains(t, got.err.Error(), "pat.match:", "buzz-level error")
		require.Contains(t, got.err.Error(), "backtracks catastrophically", "timeout reported as such")
		// The subject must NOT be echoed: regexp2's own timeout message inlines the
		// whole input, which is why runErr replaces it.
		require.NotContains(t, got.err.Error(), catastrophicSubject, "subject echoed into the error")
	case <-time.After(patMatchTimeout + 10*time.Second):
		t.Fatal("pat.match did not return: the match timeout is not in effect")
	}
}

// TestPatReplaceTimesOutOnCatastrophicBacktracking covers replace separately
// because it enters regexp2 through replace.go rather than the match path, and
// the deadline has to reach that side too. The remaining methods (matchAll,
// matchAgainst, matchAllAgainst, replaceAll) share one of these two entry points
// with the same compiled Regexp, so they are covered by construction.
func TestPatReplaceTimesOutOnCatastrophicBacktracking(t *testing.T) {
	t.Parallel()
	p, err := PatValue(catastrophicPat)
	require.NoError(t, err, "compile")

	done := make(chan error, 1)
	go func() {
		_, err := callPatMethod(t, p, "replace", StrValue(catastrophicSubject), StrValue("x"))
		done <- err
	}()
	select {
	case err := <-done:
		require.Error(t, err, "replace must fail")
		require.True(t, strings.HasPrefix(err.Error(), "pat.replace:"), "error names the method: %v", err)
		require.Contains(t, err.Error(), "backtracks catastrophically", "timeout reported as such")
	case <-time.After(patMatchTimeout + 10*time.Second):
		t.Fatal("pat.replace did not return: the match timeout is not in effect")
	}
}

// TestPatMatchStillWorks pins that the deadline did not break ordinary matching -
// a bound short enough to fire on honest patterns would be its own bug.
func TestPatMatchStillWorks(t *testing.T) {
	p, err := PatValue(`(\d+)-(\d+)`)
	require.NoError(t, err, "compile")
	v, err := callPatMethod(t, p, "match", StrValue("order 12-34 shipped"))
	require.NoError(t, err, "match")
	items := v.asList().Items
	require.Len(t, items, 3, "whole match plus two groups")
	require.Equal(t, "12-34", items[0].AsString())
	require.Equal(t, "12", items[1].AsString())
	require.Equal(t, "34", items[2].AsString())
}
