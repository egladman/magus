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

	"github.com/egladman/magus/libs/mergequeue/types"
)

var (
	hookPlan   = types.Plan{Base: "main", BaseCommit: strings.Repeat("b", 40)}
	hookChange = types.Change{ID: "7", Head: strings.Repeat("a", 40)}
)

func init() { retryDelay = time.Millisecond }

func TestGateRunsInTheCandidateWithTheQueuesEnvironment(t *testing.T) {
	dir := t.TempDir()
	var log bytes.Buffer
	g := CommandGate(`echo "$MERGEQUEUE_CHANGE $MERGEQUEUE_ONTO $MERGEQUEUE_CANDIDATE $MERGEQUEUE_BASE $MERGEQUEUE_BASE_COMMIT $MERGEQUEUE_SCRATCH" > seen; test -f ok`, hookPlan, NewHookLog(&log))

	cand := types.Candidate{Commit: "s1", Dir: dir, Scratch: "/private/s1"}
	res, err := g.Validate(context.Background(), cand, "onto1", hookChange)
	require.NoError(t, err)
	assert.False(t, res.Green)
	assert.Contains(t, res.Summary, "exited 1 on the candidate `s1`; the queue log's lines prefixed \"[s1 #7]\" name what failed.")
	seen, err := os.ReadFile(filepath.Join(dir, "seen"))
	require.NoError(t, err)
	assert.Equal(t, "7 onto1 s1 main "+hookPlan.BaseCommit+" /private/s1\n", string(seen))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "ok"), nil, 0o644))
	res, err = g.Validate(context.Background(), cand, "onto1", hookChange)
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
	_, err := g.Validate(context.Background(), types.Candidate{Commit: "s", Dir: dir}, "o", hookChange)
	require.NoError(t, err)
	seen, err := os.ReadFile(filepath.Join(dir, "seen"))
	require.NoError(t, err)
	assert.Equal(t, "[][][yes]\n", string(seen))
}

func TestGateTagsEveryOutputLineWithItsCandidate(t *testing.T) {
	var log bytes.Buffer
	g := CommandGate(`printf 'one\ntwo\n'; echo three >&2; printf 'no newline'`, hookPlan, NewHookLog(&log))
	_, err := g.Validate(context.Background(), types.Candidate{Commit: "0123456789abcdef", Dir: t.TempDir()}, "b", hookChange)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"[0123456789ab #7] one", "[0123456789ab #7] two", "[0123456789ab #7] three", "[0123456789ab #7] no newline"},
		strings.Split(strings.TrimSpace(log.String()), "\n"))
}

func TestGateRunsATemporaryFailureAgainAndThenCallsItRed(t *testing.T) {
	g := CommandGate(`echo x >> tries; test "$(wc -l < tries)" -ge 3 || exit 75`, hookPlan, nil)
	res, err := g.Validate(context.Background(), types.Candidate{Commit: "s", Dir: t.TempDir()}, "b", hookChange)
	require.NoError(t, err)
	assert.True(t, res.Green)

	res, err = CommandGate(`exit 75`, hookPlan, nil).Validate(context.Background(), types.Candidate{Commit: "s", Dir: t.TempDir()}, "b", hookChange)
	require.NoError(t, err, "the change's processes chose the exit status, so it proves nothing about the machine")
	assert.False(t, res.Green)
	assert.Contains(t, res.Summary, "`exit 75` exited 75 (temporary failure) 3 times")
}

// A change's own OOM kill, or a death by any other signal, is its red verdict. Before, it
// was the machine's failure, and a change dying that way stopped its partition every run.
func TestAGateKilledByASignalIsTheChangesRedVerdict(t *testing.T) {
	for line, why := range map[string]string{
		`kill -KILL $$`:                  "was killed (signal: killed)",
		`sh -c 'kill -KILL $$'; exit $?`: "exited 137",
		`exit 143`:                       "exited 143",
	} {
		res, err := CommandGate(line, hookPlan, nil).Validate(context.Background(), types.Candidate{Commit: "s", Dir: t.TempDir()}, "b", hookChange)
		require.NoError(t, err, line)
		assert.False(t, res.Green)
		assert.Contains(t, res.Summary, why, line)
	}
}

// Nothing a hook started outlives it: before, a background process kept running after
// the gate returned, into the next candidate and past the verdict it led to.
func TestAHooksProcessesDoNotOutliveIt(t *testing.T) {
	dir := t.TempDir()
	res, err := CommandGate(`(sleep 0.3; touch late) & echo started`, hookPlan, nil).Validate(context.Background(), types.Candidate{Commit: "s", Dir: dir}, "b", hookChange)
	require.NoError(t, err)
	assert.True(t, res.Green)
	time.Sleep(600 * time.Millisecond)
	assert.NoFileExists(t, filepath.Join(dir, "late"))
}

func TestARegenerationThatFailsOrIsKilledIsRefused(t *testing.T) {
	regen := CommandRegenerate(`cat > got; echo "$MERGEQUEUE_UNITS" > units; exit 3`, hookPlan, nil)
	dir := t.TempDir()
	err := regen(context.Background(), types.Regeneration{Dir: dir, Onto: "onto", Change: hookChange, Paths: []string{"app/gen/a", "app/gen/b"}, Units: []string{"app", "lib"}})
	var refused *types.RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, "`cat > got; echo \"$MERGEQUEUE_UNITS\" > units; exit 3` exited 3 regenerating app/gen/a, app/gen/b", refused.Reason)
	assert.Equal(t, "The queue log's lines prefixed \"[regenerate #7]\" name what failed.", refused.Remedy)
	got, err := os.ReadFile(filepath.Join(dir, "got"))
	require.NoError(t, err)
	assert.Equal(t, "app/gen/a\napp/gen/b\n", string(got))
	units, err := os.ReadFile(filepath.Join(dir, "units"))
	require.NoError(t, err)
	assert.Equal(t, "app lib\n", string(units))

	err = CommandRegenerate(`kill -KILL $$`, hookPlan, nil)(context.Background(), types.Regeneration{Dir: t.TempDir(), Change: hookChange, Paths: []string{"x"}})
	require.ErrorAs(t, err, &refused)
	assert.Contains(t, refused.Reason, "was killed")
}

func TestAHookThatCannotStartIsTheMachinesFailure(t *testing.T) {
	_, err := CommandGate(`true`, hookPlan, nil).Validate(context.Background(), types.Candidate{Commit: "s", Dir: filepath.Join(t.TempDir(), "gone")}, "b", hookChange)
	require.ErrorContains(t, err, "gate on the candidate `s`")
}

func TestCommandFactsReadTheSetFromRicherOutput(t *testing.T) {
	// magus affected --plan prints a shard plan with affected and unbounded_by beside it.
	facts := CommandFacts(`test "$MERGEQUEUE_QUERY" = affected || exit 9; read p; printf '{"count": 1, "matrix": [], "affected": ["%s"], "unbounded_by": ""}' "${p%%/*}"`, t.TempDir(), nil)
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

// A facts command that answers only "outputs" declares no update and maintains nothing.
func TestCommandFactsReadAnOutputsOnlyAnswer(t *testing.T) {
	writes, err := CommandFacts(`echo '{"outputs": ["gen/a"]}'`, t.TempDir(), nil).Classify(context.Background(), []string{"gen/a", ".gitattributes"})
	require.NoError(t, err)
	assert.Equal(t, map[string]types.Writes{"gen/a": {Output: true}}, writes)
}

func TestCommandFactsAnswerWritesAndGeneration(t *testing.T) {
	ctx := context.Background()
	line := `case "$MERGEQUEUE_QUERY" in
	outputs) echo '{"outputs": ["gen/a", "elsewhere"], "updated": ["docs/b.md", "gen/a"], "maintained": [".gitattributes"]}';;
	generation) cat > asked; echo '{"units": ["app"], "code": ["app/gen.go"], "unbounded": ""}';;
	*) exit 9;;
	esac`
	dir := t.TempDir()
	facts := CommandFacts(line, dir, nil)
	writes, err := facts.Classify(ctx, []string{"gen/a", "src/b", "docs/b.md", ".gitattributes"})
	require.NoError(t, err)
	assert.Equal(t, map[string]types.Writes{"gen/a": {Output: true, Updated: true}, "docs/b.md": {Updated: true}, ".gitattributes": {Maintained: true}}, writes,
		"only what was asked about")

	g, err := facts.Generation(ctx, []string{"gen/a"}, []string{"app/gen.go", "docs/x.md"})
	require.NoError(t, err)
	assert.Equal(t, types.Generation{Units: []string{"app"}, Code: []string{"app/gen.go"}}, g)
	assert.False(t, regenerationProven(g))
	asked, err := os.ReadFile(filepath.Join(dir, "asked"))
	require.NoError(t, err)
	assert.JSONEq(t, `{"outputs": ["gen/a"], "changed": ["app/gen.go", "docs/x.md"]}`, string(asked))
}
