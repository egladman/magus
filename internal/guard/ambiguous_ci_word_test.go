package guard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAsksAmbiguousCIWord(t *testing.T) {
	t.Parallel()
	for _, prompt := range []string{
		"get ci passing",
		"is ci red?",
		"fix CI",
		"why did ci fail",
		"make sure ci is green before you push",
	} {
		assert.Truef(t, asksAmbiguousCIWord(prompt), "%q leaves the word ci ambiguous", prompt)
	}
}

func TestAsksAmbiguousCIWordIgnoresClarified(t *testing.T) {
	t.Parallel()
	for _, prompt := range []string{
		`we need to get "affected ci" passing`,
		"run magus affected ci",
		"the ci target is failing",
		"github actions is red",
		"check the GHA run",
		"gitlab ci failed on main",
		"is the host CI broken?",
		"CI/CD is green",
		"continuous integration failed",
		"look at the PR checks",
		"fix the flaky cache test",
		"this is important: run the gate",
	} {
		assert.Falsef(t, asksAmbiguousCIWord(prompt), "%q is not an ambiguous ci word", prompt)
	}
	assert.True(t, clarifiesCIWord("run the gate"), "the gate is the magus target")
	assert.True(t, clarifiesCIWord("github actions is red"), "github actions is the host CI/CD")
}

func TestAssumesCIWord(t *testing.T) {
	t.Parallel()
	for _, line := range []string{
		"./magus affected ci",
		"magus run ci .",
		"magus --root /ws affected ci",
		"gh run list",
		"gh pr checks",
		"act -l",
		"cat .github/workflows/ci.yaml",
		"rg fail .github/workflows",
	} {
		assert.Truef(t, assumesCIWord(line), "%q picks magus ci or host CI/CD", line)
	}
	for _, line := range []string{
		"./magus affected list",
		"./magus run test .",
		"gh pr view",
		"cat README.md",
		"ls",
	} {
		assert.Falsef(t, assumesCIWord(line), "%q picks neither", line)
	}
}

func TestAmbiguousCIWordDeniesAssumingCall(t *testing.T) {
	s := newArchSession(t, strict(testDependencies()), true)

	assert.NotEqual(t, "deny", s.bash("", "./magus affected ci").Decision, "no ambiguous ci word yet")

	v := s.message("get ci passing")
	assert.Equal(t, "pass", v.Decision, "a message is recorded, never judged")

	v = s.bash("", "./magus affected ci")
	require.Equal(t, []string{"deny", string(denyRuleAmbiguousCIWord)}, []string{v.Decision, v.Rule})
	assert.Contains(t, v.Reason, "ambiguous")

	v = s.bash("", "gh run list")
	require.Equal(t, []string{"deny", string(denyRuleAmbiguousCIWord)}, []string{v.Decision, v.Rule})

	v = s.write("", ".github/workflows/ci.yaml")
	require.Equal(t, []string{"deny", string(denyRuleAmbiguousCIWord)}, []string{v.Decision, v.Rule})

	assert.NotEqual(t, "deny", s.bash("", "./magus affected list").Decision, "a non-ci call is untouched")

	v = s.message("I mean affected ci, the magus gate")
	assert.Equal(t, "pass", v.Decision)

	assert.NotEqual(t, "deny", s.bash("", "./magus affected ci").Decision, "a clarifying message clears the gate")
}

func TestAmbiguousCIWordStaysClearWhenMessageClarifies(t *testing.T) {
	s := newArchSession(t, strict(testDependencies()), true)

	v := s.message(`we need to get "affected ci" passing`)
	assert.Equal(t, "pass", v.Decision)

	assert.NotEqual(t, "deny", s.bash("", "./magus affected ci").Decision,
		"a message that already names the magus target never arms the rule")
}
