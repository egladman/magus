package guard

import (
	"testing"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every wrapper shape refuses toward the same magus argv bounded by its own --timeout,
// the wrapper's duration carried over and spelled as a flag value.
func TestMagusTimeoutDeniesTowardTheRunsOwnTimeout(t *testing.T) {
	for command, next := range map[string]string{
		"timeout 600 ./magus run test .":                            "./magus run --timeout 10m test .",
		"gtimeout 90 magus run test .":                              "magus run --timeout 1m30s test .",
		"timeout 10m ./magus -s run test . -- -run TestX":           "./magus -s run --timeout 10m test . -- -run TestX",
		"timeout 1.5h magus affected ci":                            "magus affected --timeout 1h30m ci",
		"timeout 2d magus run build .":                              "magus run --timeout 48h build .",
		"timeout 30s /usr/local/bin/magus run lint .":               "/usr/local/bin/magus run --timeout 30s lint .",
		"timeout -s TERM 600 magus run test .":                      "magus run --timeout 10m test .",
		"timeout -sKILL 600 magus run test .":                       "magus run --timeout 10m test .",
		"timeout --signal=INT 600 magus run test .":                 "magus run --timeout 10m test .",
		"timeout -k 5 600 magus run test .":                         "magus run --timeout 10m test .",
		"timeout --kill-after=5s 600 magus run test .":              "magus run --timeout 10m test .",
		"timeout --foreground --preserve-status -v 600 magus run x": "magus run --timeout 10m x",
		"timeout --verbose 600 magus run x":                         "magus run --timeout 10m x",
		"timeout 600 env -i FOO=1 ./magus run x":                    "./magus run --timeout 10m x",
		"timeout 600 nice -n 5 magus run x":                         "magus run --timeout 10m x",
		"timeout 600 command magus run x":                           "magus run --timeout 10m x",
		"env FOO=1 timeout 600 magus run x":                         "magus run --timeout 10m x",
		"nohup timeout 600 magus run x":                             "magus run --timeout 10m x",
		"nice timeout 600 magus run x":                              "magus run --timeout 10m x",
		"true && timeout 600 magus run lint .":                      "magus run --timeout 10m lint .",
		"sh -c 'timeout 600 ./magus run x'":                         "./magus run --timeout 10m x",
		"bash -c \"timeout 600 magus run x\"":                       "magus run --timeout 10m x",
		"eval timeout 600 magus run x":                              "magus run --timeout 10m x",
		"timeout 600 bash -c './magus run x'":                       "./magus run --timeout 10m x",
		"timeout 600 magus --root . run x":                          "magus --root . run --timeout 10m x",
		"timeout 600 magus run --timeout 5m x":                      "magus run --timeout 5m x",
	} {
		v := Evaluate(strict(testDependencies()), command)
		require.NotEmpty(t, v.Deny, command)
		assert.Equal(t, denyRuleMagusTimeout, v.Rule.Name, command)
		require.Len(t, v.Next, 1, command)
		// Argv and Why are the remedy's tokenized form and its prose, which this test does not pin.
		assert.Equal(t, hint.Next{ID: "deny-magus-timeout", Run: next, Argv: v.Next[0].Argv, Why: v.Next[0].Why}, v.Next[0], command)
		for _, flag := range []string{"--timeout <dur>", "--target-timeout <dur>", "--stall-timeout <dur>"} {
			assert.Contains(t, v.Deny, flag, command)
		}
		assert.Contains(t, v.Deny, "run log records no cause", command)
	}
}

// A verb that is not a run ends on its own, and one that finds a lock held refuses at
// once, so the wrapper is dropped rather than traded for a flag.
func TestMagusTimeoutServesAReadBare(t *testing.T) {
	for command, next := range map[string]string{
		"timeout 30 magus status":           "magus status",
		"timeout 30 ./magus query output x": "./magus query output x",
		"gtimeout 5m magus graph build":     "magus graph build",
	} {
		v := Evaluate(strict(testDependencies()), command)
		require.NotEmpty(t, v.Deny, command)
		assert.Equal(t, denyRuleMagusTimeout, v.Rule.Name, command)
		require.Len(t, v.Next, 1, command)
		assert.Equal(t, next, v.Next[0].Run, command)
		assert.Contains(t, v.Deny, "MGS3009", command)
	}
}

// magus buzz has no timeout flag of its own, so the wrapper is its only bound: advised,
// never refused.
func TestMagusTimeoutAdvisesOnABuzzScript(t *testing.T) {
	v, ok := magusTimeoutVerdict("timeout 60 ./magus buzz script.buzz", DialectBash)
	require.True(t, ok)
	assert.Empty(t, v.Deny)
	assert.Equal(t, denyRuleMagusTimeout, v.Rule.Name)
	assert.Contains(t, v.Context, "no timeout of its own")
}

// A dump signal, a verb with no end of its own, and anything that is not magus are
// left alone.
func TestMagusTimeoutLeavesTheRestAlone(t *testing.T) {
	for _, command := range []string{
		"timeout 5 sleep 1",
		"timeout 5 go version",
		"./magus run x",
		"magus run test .",
		"timeout -s QUIT 600 magus run test .",
		"timeout --signal=SIGABRT 600 magus run test .",
		"timeout -s 3 600 magus run test .",
		"timeout 60 magus job watch a/b",
		"timeout 60 magus watch",
		"timeout 60 magus events",
		"timeout 60 magus --version",
		"timeout 0 magus run test .",
		"timeout 600 ./magus-helper run x",
		"echo timeout 600 magus run x",
		"timeout 600 sh -c 'echo magus'",
	} {
		_, fires := magusTimeoutFires(command, DialectBash)
		assert.False(t, fires, command)
		assert.NotEqual(t, denyRuleMagusTimeout, Evaluate(strict(testDependencies()), command).Rule.Name, command)
	}
}

func TestParseTimeoutDuration(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"600": 10 * time.Minute, "90": 90 * time.Second, "10m": 10 * time.Minute, "1.5h": 90 * time.Minute,
		"2d": 48 * time.Hour, "30s": 30 * time.Second, "0.5": time.Second,
	} {
		got, ok := parseTimeoutDuration(in)
		require.True(t, ok, in)
		assert.Equal(t, want, got, in)
	}
	for _, in := range []string{"", "m", "1e3", "inf", "-5", "0x10", "10ms", "1.2.3"} {
		_, ok := parseTimeoutDuration(in)
		assert.False(t, ok, in)
	}
	assert.Equal(t, "10m", formatLimit(10*time.Minute))
	assert.Equal(t, "1m30s", formatLimit(90*time.Second))
	assert.Equal(t, "1h", formatLimit(time.Hour))
	assert.Equal(t, "1h30m", formatLimit(90*time.Minute))
}

func TestMagusTimeoutAdvisesByDefault(t *testing.T) {
	requireAdvisedOnce(t, Evaluate(testDependencies(), "timeout 600 magus run test ."), denyRuleMagusTimeout)
}
