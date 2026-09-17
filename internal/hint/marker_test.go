package hint

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testKindA, testKindB and testKindC are opaque MarkerKind values used only to prove
// the gate's mechanics (one family's firing must not spend another's). The guard
// package owns the real vocabulary; this package tests the mechanism, not the words.
const (
	testKindA MarkerKind = "kind-a"
	testKindB MarkerKind = "kind-b"
	testKindC MarkerKind = "kind-c"
)

// testGate stands up a gate over a temporary cache dir, so a test never marks the
// checkout's real advisory state.
func testGate(t *testing.T, session string) Gate {
	t.Helper()
	return NewGate(t.TempDir(), session)
}

// TestAdvisoryGateHoldsAnEnrolledKindToOneFiring is the whole rule in one assertion pair:
// the same fact, twice in one session, said once.
func TestAdvisoryGateHoldsAnEnrolledKindToOneFiring(t *testing.T) {
	g := testGate(t, "session-1")
	assert.Equal(t, "the notice", g.Once(testKindA, "the notice"))
	assert.Empty(t, g.Once(testKindA, "the notice"), "the second firing of one kind is the one nobody reads")

	// Per KIND, not per session: silencing one advisory must not silence the rest.
	assert.Equal(t, "other", g.Once(testKindB, "other"))
}

// TestAdvisoryGateSpeaksAgainInAFreshSession pins the other half. A rule held forever is
// a rule the next session never learns.
func TestAdvisoryGateSpeaksAgainInAFreshSession(t *testing.T) {
	base := t.TempDir()
	assert.Equal(t, "n", NewGate(base, "session-1").Once(testKindA, "n"))
	assert.Empty(t, NewGate(base, "session-1").Once(testKindA, "n"),
		"a hook is a short-lived process, so the marker on disk is the only thing that carries the session")
	assert.Equal(t, "n", NewGate(base, "session-2").Once(testKindA, "n"))
}

// TestAdvisoryGateHoldsOneFiringAcrossConcurrentCallers pins the gate against the access
// pattern it actually meets. A host runs one hook process per tool call and an agent
// issues tool calls in parallel, so a stat-then-write check-and-set lets every racing
// caller believe it is the first and the reader gets one notice N times.
func TestAdvisoryGateHoldsOneFiringAcrossConcurrentCallers(t *testing.T) {
	base := t.TempDir()
	const callers = 16

	var wg sync.WaitGroup
	spoke := make([]string, callers)
	start := make(chan struct{})
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			spoke[i] = NewGate(base, "session-1").Once(testKindA, "the notice")
		}()
	}
	close(start)
	wg.Wait()

	said := 0
	for _, s := range spoke {
		if s != "" {
			said++
		}
	}
	assert.Equal(t, 1, said, "exactly one of %d racing callers may spend the firing", callers)
}

// TestAdvisoryGateExpiresTheAnonymousMarker covers the surface with no session identity.
// A host that reports none leaves nothing to tell this run from the next, so that marker
// expires on a clock instead; otherwise the first session on such a host would silence
// every session after it, permanently.
func TestAdvisoryGateExpiresTheAnonymousMarker(t *testing.T) {
	base := t.TempDir()
	g := NewGate(base, "")
	require.Equal(t, "n", g.Once(testKindA, "n"))
	require.Empty(t, g.Once(testKindA, "n"))

	marker := g.markerPath(testKindA)
	aged := time.Now().Add(-anonWindow - time.Minute)
	require.NoError(t, os.Chtimes(marker, aged, aged))
	assert.Equal(t, "n", g.Once(testKindA, "n"), "a marker past its window is a session that ended")
	assert.Empty(t, g.Once(testKindA, "n"), "and the firing that reported it starts the window again")
}

// TestAdvisoryGateSpeaksWhenItCannotRemember pins the fail-open direction. State magus
// cannot write is not a reason to go quiet: a notice repeated is a smaller failure than a
// notice nobody ever gets.
func TestAdvisoryGateSpeaksWhenItCannotRemember(t *testing.T) {
	g := NewGate("", "session-1")
	assert.Equal(t, "n", g.Once(testKindA, "n"))
	assert.Equal(t, "n", g.Once(testKindA, "n"))
}

// TestAdvisoryGateSpendsNothingOnSilence: a rule with nothing to say has not used up the
// one time it may speak. Without this, an advisory whose condition was not met on the
// first write would be suppressed on the write where it finally applied.
func TestAdvisoryGateSpendsNothingOnSilence(t *testing.T) {
	g := testGate(t, "session-1")
	require.Empty(t, g.Once(testKindA, ""))
	assert.Equal(t, "n", g.Once(testKindA, "n"))
}

// TestAdvisoryGateLeavesUnenrolledKindsAlone. An empty kind MEANS "speaks every time";
// reading it as a key would give every such advisory one shared marker and silence all of
// them the moment any one of them fired.
func TestAdvisoryGateLeavesUnenrolledKindsAlone(t *testing.T) {
	g := testGate(t, "session-1")
	assert.Equal(t, "n", g.Once("", "n"))
	assert.Equal(t, "n", g.Once("", "n"))
}

// The families that carry a command degrade rather than go silent, and the ones
// reporting a condition still go quiet: an empty brief is how a kind says so.
func TestAdvisoryGateDegradesToTheBrief(t *testing.T) {
	g := testGate(t, "session-1")

	assert.Equal(t, "full text", g.OnceOrBrief(testKindA, "full text", "run this"))
	assert.Equal(t, "run this", g.OnceOrBrief(testKindA, "full text", "run this"))
	assert.Equal(t, "run this", g.OnceOrBrief(testKindA, "full text", "run this"))

	assert.Equal(t, "condition", g.OnceOrBrief(testKindB, "condition", ""))
	assert.Empty(t, g.OnceOrBrief(testKindB, "condition", ""),
		"a notice with no command to re-offer has nothing to say twice")

	assert.Equal(t, "other full", g.OnceOrBrief(testKindC, "other full", "other brief"),
		"one family's firing must not spend another's")
}

// A Claude Code subagent shares its orchestrator's session id, so a worker reaches an
// already-spent family and is owed the brief. Measured against this checkout's live
// marker directory: one session key covered an 11-hour session in which both an
// orchestrator and a subagent ran tool calls, and no second key ever appeared.
//
// The worker is a SEPARATE PROCESS, so this builds a second gate over the same base
// rather than reusing the first.
func TestAdvisoryGateGivesAWorkerTheBrief(t *testing.T) {
	base := t.TempDir()
	const full, brief = "the whole routing table", "run this instead"

	assert.Equal(t, full, NewGate(base, "shared").OnceOrBrief(testKindA, full, brief),
		"the orchestrator meets the family first and is owed the full text")
	assert.Equal(t, brief, NewGate(base, "shared").OnceOrBrief(testKindA, full, brief),
		"a worker in the same session gets the brief, which has to carry a command on its own")
}

// Concurrent first-firings must degrade to duplicate text, never to silence. A lost
// advisory is a verdict the reader never gets; a duplicated one costs bytes, and the
// check-then-write below cannot produce the first.
func TestAdvisoryGateRaceCostsBytesNotVerdicts(t *testing.T) {
	base := t.TempDir()
	const full, brief = "full", "brief"

	const workers = 16
	results := make(chan string, workers)
	start := make(chan struct{})
	for range workers {
		go func() {
			<-start
			results <- NewGate(base, "shared").OnceOrBrief(testKindA, full, brief)
		}()
	}
	close(start)

	fulls := 0
	for range workers {
		got := <-results
		require.NotEmpty(t, got, "a raced firing must never come back silent")
		if got == full {
			fulls++
		}
	}
	assert.GreaterOrEqual(t, fulls, 1, "somebody has to be first")
	assert.LessOrEqual(t, fulls, workers, "the race can duplicate the full text, which is the safe direction")
}

// TestSessionFromTerminalPrefersTheWindow pins what a person's session IS. They pass no
// --session, and keying them on a clock made an advisory go quiet on a second deliberate
// look and come back unbidden the next morning. A terminal window is what they mean.
func TestSessionFromTerminalPrefersTheWindow(t *testing.T) {
	t.Parallel()

	env := func(vals map[string]string) func(string) string {
		return func(k string) string { return vals[k] }
	}

	// The window outlives a shell restart inside it, so it beats the pid.
	assert.Equal(t, "tty:abc-123",
		SessionFromTerminal(env(map[string]string{"TERM_SESSION_ID": "abc-123"}), 4242, true))
	assert.Equal(t, "tty:0x1400003",
		SessionFromTerminal(env(map[string]string{"WINDOWID": "0x1400003"}), 4242, true))

	// No window variable: the invoking shell is the next best thing.
	assert.Equal(t, "ppid:4242", SessionFromTerminal(env(nil), 4242, true))

	// Whitespace is an unset variable spelled differently.
	assert.Equal(t, "ppid:4242",
		SessionFromTerminal(env(map[string]string{"TERM_SESSION_ID": "  "}), 4242, true))
}

// TestSessionFromTerminalAbstainsWithoutATerminal keeps the derivation off the pipeline
// and CI path. There the caller is not a person, nothing distinguishes one run from the
// next, and anonWindow is the honest fallback rather than a pid that changes every run.
func TestSessionFromTerminalAbstainsWithoutATerminal(t *testing.T) {
	t.Parallel()
	env := func(string) string { return "session-from-env" }

	assert.Empty(t, SessionFromTerminal(env, 4242, false), "no terminal, no derived session")
	// A reparented process (ppid 1) names nothing a later run would share.
	assert.Empty(t, SessionFromTerminal(func(string) string { return "" }, 1, true))
}

// TestDerivedSessionsDoNotCollide is the property the whole change rests on: two terminal
// windows are two sessions, so an advisory spent in one still fires in the other.
func TestDerivedSessionsDoNotCollide(t *testing.T) {
	t.Parallel()
	base := t.TempDir()

	first := NewGate(base, "ppid:1001")
	second := NewGate(base, "ppid:1002")

	assert.Equal(t, "full", first.Once(testKindA, "full"), "the first window is told")
	assert.Empty(t, first.Once(testKindA, "full"), "and held on its repeat")
	assert.Equal(t, "full", second.Once(testKindA, "full"), "a second window is a second session")
}
