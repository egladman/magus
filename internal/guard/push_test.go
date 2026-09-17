package guard

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runLog writes one run log: a started event carrying argv and the version string, and a
// finished event carrying status. An empty status writes no finished event, which is what
// an interrupted run leaves behind.
func runLog(t *testing.T, dir, name, version, status string, argv ...string) {
	t.Helper()
	args := ""
	for i, a := range argv {
		if i > 0 {
			args += ","
		}
		args += `"` + a + `"`
	}
	body := `{"ts":1,"inv":"i","kind":"started","command":{"arguments":[` + args + `]},"magus_version":"` + version + `"}` + "\n"
	if status != "" {
		body += `{"ts":2,"inv":"i","kind":"finished","status":"` + status + `"}` + "\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
}

func TestGateCoverageReadsTheRunLog(t *testing.T) {
	t.Parallel()

	t.Run("a green gate at this commit covers the push", func(t *testing.T) {
		dir := t.TempDir()
		runLog(t, dir, "a.jsonl", "v0.4.3-97-gabc1234-dirty", "pass", "affected", "ci")
		assert.Equal(t, gatePassed, gateCoverageAt(dir, "abc1234"))
		assert.Empty(t, denyPushWithoutGate(gateCoverageAt(dir, "abc1234")))
	})

	t.Run("a failed gate is not coverage", func(t *testing.T) {
		dir := t.TempDir()
		runLog(t, dir, "a.jsonl", "v0.4.3-97-gabc1234", "fail", "affected", "ci")
		assert.Equal(t, gateFailed, gateCoverageAt(dir, "abc1234"))
	})

	t.Run("an interrupted gate is not coverage", func(t *testing.T) {
		// No finished event, which is what a run killed at the terminal leaves. It is
		// not red and it is not green, and the deny must not call it either.
		dir := t.TempDir()
		runLog(t, dir, "a.jsonl", "v0.4.3-97-gabc1234", "", "run", "ci", ".")
		assert.Equal(t, gateIncomplete, gateCoverageAt(dir, "abc1234"))
		assert.NotContains(t, denyPushWithoutGate(gateCoverageAt(dir, "abc1234")), "RED")
	})

	t.Run("a green gate at a DIFFERENT commit is not coverage", func(t *testing.T) {
		// The case this rule exists for: gate green, commit, push. The work that moved
		// since is exactly what nothing has checked.
		dir := t.TempDir()
		runLog(t, dir, "a.jsonl", "v0.4.3-97-gdeadbee", "pass", "affected", "ci")
		assert.Equal(t, gateAbsent, gateCoverageAt(dir, "abc1234"))
		assert.NotEmpty(t, denyPushWithoutGate(gateCoverageAt(dir, "abc1234")))
	})

	t.Run("a green NON-gate run is not coverage", func(t *testing.T) {
		dir := t.TempDir()
		runLog(t, dir, "a.jsonl", "v0.4.3-97-gabc1234", "pass", "run", "go-build", ".")
		assert.Equal(t, gateAbsent, gateCoverageAt(dir, "abc1234"))
	})

	t.Run("one green gate among many runs is enough", func(t *testing.T) {
		dir := t.TempDir()
		runLog(t, dir, "a.jsonl", "v0.4.3-97-gabc1234", "fail", "affected", "ci")
		runLog(t, dir, "b.jsonl", "v0.4.3-97-gabc1234", "pass", "run", "go-build", ".")
		runLog(t, dir, "c.jsonl", "v0.4.3-97-gabc1234", "pass", "affected", "ci")
		assert.Equal(t, gatePassed, gateCoverageAt(dir, "abc1234"))
	})
}

// TestPushGateStandsDownWithNothingToProve pins that this rule refuses only on evidence.
//
// It is the one deny in its tier, so every case where the question could not be ASKED has
// to pass: a fresh clone with no run log, a host with no VCS to read a revision from, a
// test fixture. Built without this distinction the rule denied all three, because an
// unasked question and a failed one shared the zero value.
func TestPushGateStandsDownWithNothingToProve(t *testing.T) {
	t.Parallel()

	for name, cover := range map[string]gateCoverage{
		"no run log":         gateCoverageAt("", "abc1234"),
		"no commit to match": gateCoverageAt(t.TempDir(), ""),
		"no such directory":  gateCoverageAt(filepath.Join(t.TempDir(), "absent"), "abc1234"),
	} {
		assert.Equal(t, gateUnknown, cover, name)
		assert.Empty(t, denyPushWithoutGate(cover), "%s must not deny", name)
	}
}

// TestBuiltFromMatchesEitherAbbreviation pins that the two sides may abbreviate the commit
// to different lengths: git describe picks its own, and the caller passes whatever the VCS
// layer gave it.
func TestBuiltFromMatchesEitherAbbreviation(t *testing.T) {
	t.Parallel()

	assert.True(t, builtFrom("v0.4.3-97-gabc1234-dirty", "abc1234"))
	assert.True(t, builtFrom("v0.4.3-97-gabc1234", "abc1234567"), "describe abbreviated shorter")
	assert.True(t, builtFrom("v0.4.3-97-gabc1234567", "abc1234"), "caller abbreviated shorter")
	assert.False(t, builtFrom("v0.4.3-97-gabc1234", "def5678"))
	assert.False(t, builtFrom("", "abc1234"))
	assert.False(t, builtFrom("v0.4.3-97-gabc1234", ""))
	assert.False(t, builtFrom("v0.4.3", "abc1234"), "a release build names no commit")
}
