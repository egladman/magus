package mergequeue

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	hookPlan   = Plan{Base: "main", BaseCommit: strings.Repeat("b", 40)}
	hookChange = Change{ID: "7", Head: strings.Repeat("a", 40)}
)

func init() { retryDelay = time.Millisecond }

func TestGateRunsInTheCandidateWithTheQueuesEnvironment(t *testing.T) {
	dir := t.TempDir()
	var log bytes.Buffer
	g := CommandGate(`echo "$MERGEQUEUE_CHANGE $MERGEQUEUE_ONTO $MERGEQUEUE_CANDIDATE $MERGEQUEUE_BASE $MERGEQUEUE_BASE_COMMIT" > seen; test -f ok`, hookPlan, NewHookLog(&log))

	res, err := g.Validate(context.Background(), Candidate{Commit: "s1", Dir: dir}, "onto1", hookChange)
	require.NoError(t, err)
	assert.False(t, res.Green)
	assert.Contains(t, res.Summary, "exited 1 on the candidate `s1`; the queue log's lines prefixed \"[s1 #7]\" name what failed.")
	seen, err := os.ReadFile(filepath.Join(dir, "seen"))
	require.NoError(t, err)
	assert.Equal(t, "7 onto1 s1 main "+hookPlan.BaseCommit+"\n", string(seen))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "ok"), nil, 0o644))
	res, err = g.Validate(context.Background(), Candidate{Commit: "s1", Dir: dir}, "onto1", hookChange)
	require.NoError(t, err)
	assert.True(t, res.Green)
}

// A gate runs the changes' code; before, it inherited the write token with everything
// else in the queue's environment.
func TestHooksNeverSeeTheQueuesCredentials(t *testing.T) {
	t.Setenv("MERGEQUEUE_TOKEN", "write")
	t.Setenv("GITHUB_TOKEN", "read")
	t.Setenv("KEPT", "yes")
	dir := t.TempDir()
	g := CommandGate(`echo "[$MERGEQUEUE_TOKEN][$GITHUB_TOKEN][$KEPT]" > seen`, hookPlan, nil)
	_, err := g.Validate(context.Background(), Candidate{Commit: "s", Dir: dir}, "o", hookChange)
	require.NoError(t, err)
	seen, err := os.ReadFile(filepath.Join(dir, "seen"))
	require.NoError(t, err)
	assert.Equal(t, "[][][yes]\n", string(seen))
}

func TestGateTagsEveryOutputLineWithItsCandidate(t *testing.T) {
	var log bytes.Buffer
	g := CommandGate(`printf 'one\ntwo\n'; echo three >&2; printf 'no newline'`, hookPlan, NewHookLog(&log))
	_, err := g.Validate(context.Background(), Candidate{Commit: "0123456789abcdef", Dir: t.TempDir()}, "b", hookChange)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"[0123456789ab #7] one", "[0123456789ab #7] two", "[0123456789ab #7] three", "[0123456789ab #7] no newline"},
		strings.Split(strings.TrimSpace(log.String()), "\n"))
}

func TestGateRunsATemporaryFailureAgainRatherThanCallingItRed(t *testing.T) {
	g := CommandGate(`echo x >> tries; test "$(wc -l < tries)" -ge 3 || exit 75`, hookPlan, nil)
	res, err := g.Validate(context.Background(), Candidate{Commit: "s", Dir: t.TempDir()}, "b", hookChange)
	require.NoError(t, err)
	assert.True(t, res.Green)

	_, err = CommandGate(`exit 75`, hookPlan, nil).Validate(context.Background(), Candidate{Commit: "s", Dir: t.TempDir()}, "b", hookChange)
	require.EqualError(t, err, "gate on the candidate `s`: `exit 75` exited 75 (temporary failure) 3 times",
		"a machine that stays busy stops the run instead of kicking the author back")
}

// An OOM kill or a runner shutting down says nothing about the change; before, it was
// a red gate and the author was kicked back.
func TestAGateKilledByASignalIsTheMachinesFailure(t *testing.T) {
	for _, line := range []string{`kill -KILL $$`, `sh -c 'kill -KILL $$'; exit $?`, `exit 143`} {
		res, err := CommandGate(line, hookPlan, nil).Validate(context.Background(), Candidate{Commit: "s", Dir: t.TempDir()}, "b", hookChange)
		require.ErrorContains(t, err, "the machine failed, not the change", line)
		assert.False(t, res.Green)
	}
}

func TestARegenerationThatFailsIsRefusedAndAKilledOneIsAnError(t *testing.T) {
	regen := CommandRegenerate(`cat > got; exit 3`, hookPlan, nil)
	dir := t.TempDir()
	err := regen(context.Background(), dir, "onto", hookChange, []string{"app/gen/a", "app/gen/b"})
	var refused *RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, "`cat > got; exit 3` exited 3 regenerating app/gen/a, app/gen/b; the queue log's lines prefixed \"[regenerate #7]\" name what failed.", refused.Reason)
	got, err := os.ReadFile(filepath.Join(dir, "got"))
	require.NoError(t, err)
	assert.Equal(t, "app/gen/a\napp/gen/b\n", string(got))

	err = CommandRegenerate(`kill -KILL $$`, hookPlan, nil)(context.Background(), t.TempDir(), "onto", hookChange, []string{"x"})
	require.ErrorContains(t, err, "the machine failed")
	assert.NotErrorAs(t, err, &refused)
}

func TestCommandFactsReadTheSetFromRicherOutput(t *testing.T) {
	// magus affected --plan prints a shard plan with affected and unbounded_by beside it.
	facts := CommandFacts(`read p; printf '{"count": 1, "matrix": [], "affected": ["%s"], "unbounded_by": ""}' "${p%%/*}"`, t.TempDir(), nil)
	got, unboundedBy, err := facts.Affected(context.Background(), hookChange, []string{"app/main.go"})
	require.NoError(t, err)
	assert.Equal(t, []string{"app"}, got)
	assert.Empty(t, unboundedBy)
}

func TestCommandFactsWithoutASetAreUnboundedAndAFailureIsAnError(t *testing.T) {
	ctx := context.Background()
	_, unboundedBy, err := CommandFacts(`echo '{}'`, t.TempDir(), nil).Affected(ctx, hookChange, []string{"x"})
	require.NoError(t, err)
	assert.Equal(t, "the affected hook printed no affected set", unboundedBy)

	_, _, err = CommandFacts(`exit 3`, t.TempDir(), nil).Affected(ctx, hookChange, []string{"x"})
	require.EqualError(t, err, "affected hook: exited 3")

	got, unboundedBy, err := CommandFacts(`exit 3`, t.TempDir(), nil).Affected(ctx, hookChange, nil)
	require.NoError(t, err, "a change touching nothing is not asked about")
	assert.Equal(t, []string{}, got)
	assert.Empty(t, unboundedBy)
}
