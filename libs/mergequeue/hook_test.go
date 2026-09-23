package mergequeue

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommandGateRunsInTheStageWithTheQueuesEnvironment(t *testing.T) {
	dir := t.TempDir()
	var log bytes.Buffer
	g := &CommandGate{Line: `echo "$MERGEQUEUE_CHANGE $MERGEQUEUE_BELOW $MERGEQUEUE_STAGE $MERGEQUEUE_BASE" > seen; test -f ok`,
		Base: "main", BaseSHA: "b0", Log: NewPrefixWriter(&log)}
	c := change("7", "a")

	res, err := g.Validate(context.Background(), Stage{Commit: "s1", Dir: dir}, "below1", c)
	require.NoError(t, err)
	assert.False(t, res.Green)
	assert.Equal(t, "`"+g.Line+"` exited 1 on the staging commit `s1`; the queue log's lines prefixed \"[s1 #7]\" name what failed.", res.Summary)
	seen, err := os.ReadFile(filepath.Join(dir, "seen"))
	require.NoError(t, err)
	assert.Equal(t, "7 below1 s1 main\n", string(seen))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "ok"), nil, 0o644))
	res, err = g.Validate(context.Background(), Stage{Commit: "s1", Dir: dir}, "below1", c)
	require.NoError(t, err)
	assert.True(t, res.Green)
}

func TestCommandGateTagsEveryOutputLineWithItsStage(t *testing.T) {
	var log bytes.Buffer
	g := &CommandGate{Line: `printf 'one\ntwo\n'; echo three >&2`, Log: NewPrefixWriter(&log)}
	_, err := g.Validate(context.Background(), Stage{Commit: "0123456789abcdef", Dir: t.TempDir()}, "b", change("3"))
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"[0123456789ab #3] one", "[0123456789ab #3] two", "[0123456789ab #3] three"},
		splitLines(log.String()))
}

func TestCommandGateRunsATemporaryFailureAgainRatherThanCallingItRed(t *testing.T) {
	dir := t.TempDir()
	// Exits 75 until it has run twice, then passes.
	g := &CommandGate{Line: `echo x >> tries; test "$(wc -l < tries)" -ge 3 || exit 75`, RetryDelay: time.Millisecond}
	res, err := g.Validate(context.Background(), Stage{Commit: "s", Dir: dir}, "b", change("1"))
	require.NoError(t, err)
	assert.True(t, res.Green)

	always := &CommandGate{Line: `exit 75`, RetryDelay: time.Millisecond}
	_, err = always.Validate(context.Background(), Stage{Commit: "s", Dir: t.TempDir()}, "b", change("1"))
	require.EqualError(t, err, "`exit 75` exited 75 (temporary failure) 3 times on the staging commit `s`",
		"a machine that stays busy stops the run instead of kicking the author back")
}

func splitLines(s string) []string {
	var out []string
	for _, l := range bytes.Split(bytes.TrimSpace([]byte(s)), []byte("\n")) {
		out = append(out, string(l))
	}
	return out
}

func TestAffectedCommandReadsTheSetFromRicherOutput(t *testing.T) {
	// magus affected --plan prints a shard plan with affected and unbounded beside it.
	hook := AffectedCommand(`read p; printf '{"count": 1, "matrix": [], "affected": ["%s"], "unbounded": ""}' "${p%%/*}"`, t.TempDir(), nil)
	got, unbounded, err := hook(context.Background(), change("1"), []string{"app/main.go"})
	require.NoError(t, err)
	assert.Equal(t, []string{"app"}, got)
	assert.Empty(t, unbounded)
}

func TestAffectedCommandWithoutASetIsUnboundedAndAFailureIsAnError(t *testing.T) {
	ctx := context.Background()
	_, unbounded, err := AffectedCommand(`echo '{}'`, t.TempDir(), nil)(ctx, change("1"), []string{"x"})
	require.NoError(t, err)
	assert.Equal(t, "the affected hook printed no affected set", unbounded)

	_, _, err = AffectedCommand(`exit 3`, t.TempDir(), nil)(ctx, change("1"), []string{"x"})
	require.ErrorContains(t, err, "affected hook: exit status 3")

	got, unbounded, err := AffectedCommand(`exit 3`, t.TempDir(), nil)(ctx, change("1"), nil)
	require.NoError(t, err, "a change touching nothing is not asked about")
	assert.Equal(t, []string{}, got)
	assert.Empty(t, unbounded)
}
