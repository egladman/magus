package guard

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestGuardDeniesMisconfiguredMagusEnv: a retired or misspelled MAGUS_* name is a setting
// that silently never took effect. The day MAGUS_NO_WAIT was removed, 462 commands carried
// it. Every spelling that hands a command's environment the name is refused; one that
// only mentions it, sets a shell variable no child sees, or names a variable magus cannot
// prove wrong, is not.
func TestGuardDeniesMisconfiguredMagusEnv(t *testing.T) {
	for _, cmd := range []string{
		"MAGUS_NO_WAIT=1 ./magus run test .",
		"MAGUS_NO_WAIT= ./magus run test .",
		"env MAGUS_NO_WAIT=1 magus run lint .",
		"env -u MAGUS_NO_WAIT ./magus run lint .",
		"env -uMAGUS_NO_WAIT ./magus run lint .",
		"env --unset=MAGUS_NO_WAIT ./magus run lint .",
		"timeout 60 env MAGUS_NO_WAIT=0 magus run lint .",
		"export MAGUS_NO_WAIT=1",
		"export MAGUS_NO_WAIT",
		"bash -c 'MAGUS_NO_WAIT=1 magus run lint .'",
		"ls && MAGUS_NO_WAIT=1 go version",
	} {
		v := Evaluate(testDependencies(), cmd)
		assert.Equal(t, denyRule{Name: denyRuleUnknownEnv, Arg: "MAGUS_NO_WAIT"}, v.Rule, cmd)
		assert.Contains(t, v.Deny, "MGS1046", cmd)
		assert.Contains(t, v.Deny, "config view -h", "a deny routes to the list of names that do exist: %s", cmd)
	}

	for _, cmd := range []string{
		"MAGUS_CACHE_DIR=/tmp/c magus run lint .",
		"env -u MAGUS_CACHE_DIR magus run lint .",
		"export MAGUS_LOG_LEVEL=debug",
		// A documented placeholder names a family.
		"MAGUS_VCS_GIT_BASE_REF=origin/main magus affected ci",
		// Unknown, but near no registered name: a newer magus or the repository's tooling
		// may read it, so nothing proves it wrong.
		"MAGUS_QUEUE_APP_PRIVATE_KEY=x ./tools/release.sh",
		// A bare assignment sets a shell variable no child process sees.
		"MAGUS_NO_WAIT=1",
		// Mentioning a name is not setting it.
		"echo MAGUS_NO_WAIT=1",
		"grep -rn MAGUS_NO_WAIT docs/",
		"FOO=1 magus run lint .",
	} {
		assert.NotEqual(t, denyRuleUnknownEnv, Evaluate(testDependencies(), cmd).Rule.Name, cmd)
	}
}

// A misspelling names the variable the caller most likely meant.
func TestMisconfiguredMagusEnvNamesTheClosestKnownName(t *testing.T) {
	v := Evaluate(testDependencies(), "MAGUS_CACHE_DRI=/tmp/c magus run lint .")
	assert.Equal(t, denyRule{Name: denyRuleUnknownEnv, Arg: "MAGUS_CACHE_DRI"}, v.Rule)
	assert.Contains(t, v.Deny, "did you mean MAGUS_CACHE_DIR")
}
