package guard

import (
	"bytes"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
	"github.com/egladman/magus/libs/testkit"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/libs/clikit/output"
)

// verdictDecisionRe finds every decision the guard assigns, in either spelling
// the code uses: the struct-literal `Decision: decisionPass` and the assignment
// `verdict.Decision = decisionDeny`. Each decisionX constant spells its value,
// so the lowercased suffix is the decision.
var verdictDecisionRe = regexp.MustCompile(`Decision(?::|\s*=)\s*decision(\w+)`)

// TestGuardDecisionsCoverEveryVerdictTheHookEmits keeps agent.GuardDecisions
// honest, which is what makes it usable as the contract the host-parity gate
// compares against (see TestHostGluesCoverTheGuardContract in internal/agent/guard_test.go).
//
// Two directions, and both are load-bearing:
//
//   - Every decision the hook can EMIT must be listed. A source scan rather
//     than a behavioral assertion, for the same reason
//     TestEveryCommandBindsDisplayFlags is one: catching an emitted-but-
//     unlisted decision at runtime would mean enumerating every input that
//     produces every verdict, and that enumeration is the thing that goes
//     stale.
//   - Every listed decision must RENDER distinctly. writeGuardVerdict's text
//     arm falls through to "pass" for anything it does not know, so a decision
//     added to the list but not to the renderer would report itself as a pass:
//     the quietest possible wrong answer.
//
// A decision that fails either direction is not a contract a host glue can be
// asked to declare a stance on.
func TestGuardDecisionsCoverEveryVerdictTheHookEmits(t *testing.T) {
	listed := make(map[string]bool)
	for _, d := range agent.GuardDecisions() {
		listed[d] = true
	}

	// Every guard source, not one file: the rules were split out of agent.go and a
	// hard-coded name would silently stop scanning the file the decisions moved to.
	// They now live in internal/guard, not this directory.
	sources, err := filepath.Glob("../../internal/guard/guard*.go")
	require.NoError(t, err)
	require.NotEmpty(t, sources)
	found := false
	for _, src := range sources {
		body, rerr := os.ReadFile(src)
		require.NoError(t, rerr)
		for _, m := range verdictDecisionRe.FindAllStringSubmatch(string(body), -1) {
			found = true
			decision := strings.ToLower(m[1])
			assert.True(t, listed[decision],
				"%s emits the verdict decision %q, which agent.GuardDecisions does not list.\n"+
					"Add it there first: the host-parity gate asks every glue to declare a stance per\n"+
					"decision, and a decision missing from the contract is one no host was asked about.", src, decision)
		}
	}
	require.True(t, found, "found no decision literals in guard*.go; verdictDecisionRe no longer matches how the guard assigns a decision")

	for _, d := range agent.GuardDecisions() {
		var out strings.Builder
		require.NoError(t, testHookEnv().writeVerdict(&out, output.Options{Format: output.Text}, verdict.Judgment{
			APIVersion: agent.GuardAPIVersion,
			Decision:   d,
			Reason:     "why",
			Context:    "why",
		}))
		assert.True(t, strings.HasPrefix(out.String(), d),
			"writeGuardVerdict renders the listed decision %q as %q: its text arm has no case for it and\n"+
				"fell through to the default, so the verdict reads as a pass.", d, strings.TrimSpace(out.String()))
	}
}

// TestEnforceVerdictBlocksOnlyDeny pins the half that makes the guard real. Every rule was
// reachable and correct while the process exited 0, so a host read success and ran the
// command anyway: the guard looked installed and enforced nothing.
func TestEnforceVerdictBlocksOnlyDeny(t *testing.T) {
	text := output.Options{Format: output.Text}
	err := verdictExit(os.Stderr, text, verdict.Judgment{Decision: "deny", Reason: "no"})
	require.Error(t, err, "a deny must exit non-zero or it blocks nothing")
	var silent reportedExit
	require.ErrorAs(t, err, &silent)
	assert.Equal(t, guardDenyExitCode, silent.ExitCode())

	require.NoError(t, verdictExit(os.Stderr, text, verdict.Judgment{Decision: "advise", Context: "fyi"}),
		"advice teaches and must never block")
	require.NoError(t, verdictExit(os.Stderr, text, verdict.Judgment{Decision: "pass"}))

	// The exit code is the enforcement and does not depend on the rendering: a structured
	// consumer that got a zero status would be told the same lie in a different shape.
	require.Error(t, verdictExit(os.Stderr, output.Options{Format: output.JSON}, verdict.Judgment{Decision: "deny", Reason: "no"}))
}

// TestEnforceVerdictBlocksAnAsk pins that an ask stops the call wherever nothing renders the
// host's prompt. A glue that reads only the exit status would otherwise take the person's
// approval as already given. The shipped templates read stdout and exit 0 themselves.
func TestEnforceVerdictBlocksAnAsk(t *testing.T) {
	err := verdictExit(os.Stderr, output.Options{Format: output.Text}, verdict.Judgment{Decision: "ask", Reason: "publish?"})
	var silent reportedExit
	require.ErrorAs(t, err, &silent)
	assert.Equal(t, guardDenyExitCode, silent.ExitCode())

	var stdout bytes.Buffer
	require.NoError(t, testHookEnv().writeVerdict(&stdout, output.Options{Format: output.Text},
		verdict.Judgment{Decision: "ask", Rule: "push-unverified", Reason: "publish?"}))
	assert.Equal(t, "ask [push-unverified]: publish?\n", stdout.String())
}

// TestGuardDenyPrintsItsReasonOnce: text mode already renders the full reason to stdout, and
// every guard template this repo ships reads the verdict from stdout (one discards stderr
// outright), so an unconditional stderr copy reached nobody who lacked another channel and
// simply printed a kilobyte-plus reason twice to a terminal. A structured format renders no
// prose, so there stderr is the only readable channel and keeps it.
func TestGuardDenyPrintsItsReasonOnce(t *testing.T) {
	deny := verdict.Judgment{APIVersion: agent.GuardAPIVersion, Decision: "deny", Reason: "because"}

	var stdout bytes.Buffer
	require.NoError(t, testHookEnv().writeVerdict(&stdout, output.Options{Format: output.Text}, deny))
	assert.Equal(t, 1, strings.Count(stdout.String(), "because"), "stdout renders the reason once")
	assert.Empty(t, captureStderr(t, func() {
		_ = verdictExit(os.Stderr, output.Options{Format: output.Text}, deny)
	}), "and stderr must not repeat what stdout just said")

	assert.Contains(t, captureStderr(t, func() {
		_ = verdictExit(os.Stderr, output.Options{Format: output.JSON}, deny)
	}), "because", "a structured format renders no prose, so stderr carries it")
}

// TestGuardDenyStderrIsOneLine pins the person's view of a blocking hook: the verdict line
// alone, with the next step and the details ref left to stdout.
func TestGuardDenyStderrIsOneLine(t *testing.T) {
	deny := verdict.Judgment{APIVersion: agent.GuardAPIVersion, Decision: "deny",
		Reason: "a text search the graph answers\nnothing ran (2 commands)\ndetails: ./magus query output grd1"}
	assert.Equal(t, "a text search the graph answers\n", captureStderr(t, func() {
		_ = verdictExit(os.Stderr, output.Options{Format: output.JSON}, deny)
	}))
}

// TestReadGuardInputRefusesAnOversizePayload: a truncated payload is exactly the "not the
// command the guard was shown" case, so the overflow is a read failure and hookCmd's
// deny arm turns it into a block.
func TestReadGuardInputRefusesAnOversizePayload(t *testing.T) {
	value, err := readGuardInput(strings.NewReader(strings.Repeat("x", guardInputLimit)))
	require.NoError(t, err)
	assert.Len(t, value, guardInputLimit)

	_, err = readGuardInput(strings.NewReader(strings.Repeat("x", guardInputLimit+1)))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not read whole")
}

// captureStderr is testkit.CaptureStderr with the default logger's records rendered as
// plain lines into the same stream, so a notice reads as it does on a terminal.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	return testkit.CaptureStderr(t, func() {
		prev := slog.Default()
		defer slog.SetDefault(prev)
		slog.SetDefault(slog.New(cache.NewPlainHandler(os.Stderr, slog.LevelInfo)))
		fn()
	})
}
