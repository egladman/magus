package guard

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/libs/testkit"
)

// The phrasings these came from are messages people actually sent, verbatim where quoted.
func TestAsksArchitectureMatchesRealPhrasings(t *testing.T) {
	t.Parallel()
	for _, prompt := range []string{
		"make sure this respects our current architecture patterns",
		"respecting the current like application boundaries",
		"why do we need a new package called audit package?",
		"the architecture skill should be able to catch all these crazy imports and the sprawl",
		"What do you need to do with the blast radius stuff?",
		"where should the retry helper live?",
		"Where does this belong",
		"is there an import cycle between job and guard",
		"we have a circular dependency somewhere in internal/",
		"fold the helper into the existing package",
		"extract this into its own module",
		"does this fit our conventions",
		"the coupling between cache and graph is too tight",
		"reorganize internal/ so the layering is obvious",
		"that's a god object now",
		"what's the fan-out of this change",
		"co-locate the tests with the code",
		"the ARCHITECTURAL decision record",
	} {
		assert.Truef(t, asksArchitecture(prompt), "%q is a structure question", prompt)
	}
}

// Seen in transcripts as false positives: the same word meaning an instruction set, or a
// figure to redraw.
func TestAsksArchitectureIgnoresCPUsAndDiagrams(t *testing.T) {
	t.Parallel()
	for _, prompt := range []string{
		"build the release for the arm64 architecture",
		"what is the platform architecture of the CI runner?",
		"which architecture is this binary, amd64?",
		"update the architecture diagram in the docs",
		"redraw the architecture diagrams for the release",
		"fix the flaky cache test",
		"this is important: run the gate",
		"respect the deadline",
	} {
		assert.Falsef(t, asksArchitecture(prompt), "%q is not a structure question", prompt)
	}
	assert.True(t, asksArchitecture("update the architecture diagram, and check the imports"),
		"a cut phrase leaves the rest of the message to match on its own terms")
}

// archSession drives one Claude Code session through Judge: messages, Bash calls, writes and
// skill loads, each from the root (agent "") or a subagent.
type archSession struct {
	t        *testing.T
	ctx      context.Context
	deps     Dependencies
	session  string
	observes bool
}

func newArchSession(t *testing.T, deps Dependencies, observes bool) archSession {
	t.Helper()
	testkit.Isolate(t)
	root := inWorkspace(t)
	return archSession{
		t:        t,
		ctx:      WithLocation(t.Context(), t.TempDir(), root, root),
		deps:     deps,
		session:  "8f2c6a1e-3b7d-4c55-9e0a-5d1f2b9c7e41",
		observes: observes,
	}
}

func (s archSession) judge(event string) Verdict {
	return Judge(s.ctx, s.deps, Request{Input: event, Host: "claude-code", Form: "buzz", ReportsSkills: s.observes})
}

func (s archSession) message(text string) Verdict {
	return Judge(s.ctx, s.deps, Request{Input: text, Message: true, Host: "claude-code", Form: "buzz", Session: s.session})
}

func (s archSession) head(agentID string) string {
	head := `{"session_id":"` + s.session + `","hook_event_name":"PreToolUse",`
	if agentID != "" {
		head += `"agent_id":"` + agentID + `",`
	}
	return head
}

func (s archSession) bash(agentID, command string) Verdict {
	return s.judge(s.head(agentID) + `"tool_name":"Bash","tool_input":{"command":"` + command + `"}}`)
}

func (s archSession) write(agentID, path string) Verdict {
	return s.judge(s.head(agentID) + `"tool_name":"Write","tool_input":{"file_path":"` + path + `","content":"package x\n"}}`)
}

func (s archSession) load(agentID, skill string) {
	s.t.Helper()
	require.Equal(s.t, "pass", s.judge(s.head(agentID)+`"tool_name":"Skill","tool_input":{"skill":"`+skill+`"}}`).Decision)
}

// requireArchitectureDeny checks the decision and the rule. A repeat deny in one session is
// shortened to a line and a ref, so the reason's text is checked once, on the first.
func requireArchitectureDeny(t *testing.T, v Verdict, msgAndArgs ...any) {
	t.Helper()
	require.Equal(t, []string{"deny", string(denyArchitectureUnbriefed)}, []string{v.Decision, v.Rule}, msgAndArgs...)
}

func TestArchitectureUnbriefedDeniesAfterAStructureMessage(t *testing.T) {
	s := newArchSession(t, strict(testDependencies()), true)

	assert.NotEqual(t, "deny", s.bash("", "echo hello").Decision, "no structure question, nothing to read first")

	v := s.message("does this respect our current architecture patterns?")
	assert.Equal(t, "pass", v.Decision, "a message is recorded, never judged")
	assert.Empty(t, v.Context, "and nothing is said back")

	v = s.bash("", "echo hello")
	requireArchitectureDeny(t, v, "the next graded call waits on the load")
	assert.Contains(t, v.Reason,
		"acting on an architecture question before the magus-architecture-review skill loaded; load Skill(magus-architecture-review), then retry.")
	requireArchitectureDeny(t, s.write("", "notes.txt"), "a write is a graded call too")

	s.load("", architectureSkill.String())
	assert.NotEqual(t, "deny", s.bash("", "echo hello").Decision, "the load clears the gate")
}

func TestArchitectureUnbriefedIgnoresAnOrdinaryMessage(t *testing.T) {
	s := newArchSession(t, strict(testDependencies()), true)
	s.message("fix the flaky cache test")
	assert.NotEqual(t, "deny", s.bash("", "echo hello").Decision)
}

func TestArchitectureUnbriefedStandsDownWhereLoadsAreNotObserved(t *testing.T) {
	s := newArchSession(t, strict(testDependencies()), false)
	s.message("why do we need a new package called audit package?")
	assert.NotEqual(t, "deny", s.bash("", "echo hello").Decision,
		"a wiring that reports no loads could never clear it")
}

func TestArchitectureUnbriefedClearsOnTheFullTwin(t *testing.T) {
	s := newArchSession(t, strict(testDependencies()), true)
	s.message("check the blast radius of this")
	s.load("", agent.FullTwinName(architectureSkill.String()))
	assert.NotEqual(t, "deny", s.bash("", "echo hello").Decision)
}

// A load counts for the context it was read into. A parent's load leaves its subagent
// unbriefed, and a subagent's load leaves its parent unbriefed.
func TestArchitectureUnbriefedCountsLoadsPerAgent(t *testing.T) {
	s := newArchSession(t, strict(testDependencies()), true)
	s.message("respecting the current like application boundaries")

	s.load("", architectureSkill.String())
	assert.NotEqual(t, "deny", s.bash("", "echo hello").Decision, "the root loaded it")
	requireArchitectureDeny(t, s.bash("a1b2c3", "echo hello"), "the parent's load is not the child's")

	s.load("a1b2c3", architectureSkill.String())
	assert.NotEqual(t, "deny", s.bash("a1b2c3", "echo hello").Decision, "the child loaded it itself")
	requireArchitectureDeny(t, s.bash("d4e5f6", "echo hello"), "a sibling is briefed by its own load only")
}

func TestASubagentLoadDoesNotBriefItsParent(t *testing.T) {
	s := newArchSession(t, strict(testDependencies()), true)
	s.message("the architecture skill should be able to catch all these crazy imports and the sprawl")
	s.load("a1b2c3", architectureSkill.String())
	requireArchitectureDeny(t, s.bash("", "echo hello"))
}

func TestArchitectureUnbriefedGatesANewDirectory(t *testing.T) {
	s := newArchSession(t, strict(testDependencies()), true)

	v := s.write("", filepath.Join("kvdir", "kvdir.go"))
	requireArchitectureDeny(t, v, "creating a directory draws a boundary whatever was asked")
	assert.Contains(t, v.Reason, "creating the new directory `kvdir`")

	assert.NotEqual(t, "deny", s.write("", "main.go").Decision, "a file in an existing directory is not a boundary")

	s.load("", architectureSkill.String())
	v = s.write("", filepath.Join("kvdir", "kvdir.go"))
	assert.Equal(t, "pass", v.Decision, "loaded, and the advisory that sends the reader to the skill has nothing left to say")
}

func TestANewDirectoryStillAdvisesWhereLoadsAreNotObserved(t *testing.T) {
	s := newArchSession(t, strict(testDependencies()), false)
	v := s.write("", filepath.Join("kvdir", "kvdir.go"))
	assert.Equal(t, []string{"advise", string(advisoryNewSourceDir)}, []string{v.Decision, v.Rule})
}

// Shipped default: a workspace that sets nothing is told once, and never refused.
func TestArchitectureUnbriefedAdvisesOnceByDefault(t *testing.T) {
	s := newArchSession(t, testDependencies(), true)
	s.message("where should the retry helper live?")

	v := s.bash("", "echo hello")
	assert.Equal(t, []string{"advise", string(denyArchitectureUnbriefed)}, []string{v.Decision, v.Rule})
	assert.Contains(t, v.Context, "magus-architecture-review")
	assert.NotEqual(t, string(denyArchitectureUnbriefed), s.bash("", "echo hello").Rule, "said once per session")
}

func TestAMessageIsNeverRecordedOnADryRun(t *testing.T) {
	s := newArchSession(t, strict(testDependencies()), true)
	v := Judge(s.ctx, s.deps, Request{Input: "check the blast radius", Message: true, DryRun: true,
		Host: "claude-code", Form: "buzz", Session: s.session})
	assert.Equal(t, "pass", v.Decision)
	assert.NotEqual(t, "deny", s.bash("", "echo hello").Decision)
}

func TestSkillsKeyNestsTheAgentUnderItsSession(t *testing.T) {
	t.Parallel()
	root := hookAttribution{Host: "claude-code", Form: "buzz", Session: "s1"}
	child := root
	child.Agent = "a/1"
	assert.Equal(t, root.factsKey(), root.skillsKey(), "the root's loads are the session's, as they always were")
	assert.Equal(t, "claude-code/s1/agent/a%2F1", child.skillsKey())
	assert.NotEqual(t, child.callerKey(), child.skillsKey())
	assert.Empty(t, hookAttribution{Host: "claude-code", Agent: "a1"}.skillsKey(), "no session keys nothing")
}
