package recorder

import (
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ConsultSuite struct{ suite.Suite }

func TestConsult(t *testing.T) { suite.Run(t, new(ConsultSuite)) }

// record writes one observation the way the guard hook does.
func record(t *testing.T, base string, c AgentCommand) {
	t.Helper()
	AppendAgentCommand(t.Context(), base, c)
}

func (s *ConsultSuite) TestConsultedJoinsQuestionsToTheWriteThroughTheLease() {
	t := s.T()
	base := t.TempDir()
	record(t, base, AgentCommand{Session: "s1", Lease: "wave2/trail", Tool: ToolShellCommand, Command: `magus query "kind=target project=internal/trail"`})
	record(t, base, AgentCommand{Session: "s1", Lease: "wave2/trail", Tool: ToolShellCommand, Command: "magus explain internal/trail"})
	record(t, base, AgentCommand{Session: "s1", Lease: "wave2/trail", Tool: ToolShellCommand, Command: "magus explain internal/trail"})
	// Neither of these is a consultation: one runs work, the other is not magus at all.
	record(t, base, AgentCommand{Session: "s1", Lease: "wave2/trail", Tool: ToolShellCommand, Command: "magus run go-build ."})
	record(t, base, AgentCommand{Session: "s1", Lease: "wave2/trail", Tool: ToolShellCommand, Command: "grep -rn explain internal/trail"})
	record(t, base, AgentCommand{Session: "s1", Lease: "wave2/trail", Tool: ToolFileWrite, Path: "internal/trail/consult.go"})

	got, gap, _ := Consulted(t.Context(), "", base, []string{"internal/trail/consult.go"}, 100)

	// Most asked first, and the subject is kept where Touch.Ran drops every argument.
	assert.Equal(t, []Consultation{
		{Verb: "explain", Subject: "internal/trail", Count: 2},
		{Verb: "query", Subject: "kind=target project=internal/trail", Count: 1},
	}, got)
	assert.Equal(t, ConsultGapNone, gap)
}

// A work unit that fans out has one lease per worker. The reviewer is asking what backs the
// changeset, so every lease that wrote into it contributes to one merged list.
func (s *ConsultSuite) TestConsultedMergesEveryLeaseThatWroteTheChangeset() {
	t := s.T()
	base := t.TempDir()
	record(t, base, AgentCommand{Session: "boss", Lease: "wave/orchestrator", Tool: ToolShellCommand, Command: "magus explain internal/trail"})
	record(t, base, AgentCommand{Session: "boss", Lease: "wave/orchestrator", Tool: ToolFileWrite, Path: "a.go"})
	record(t, base, AgentCommand{Session: "w1", Lease: "wave/worker-1", Tool: ToolShellCommand, Command: "magus explain internal/trail"})
	record(t, base, AgentCommand{Session: "w1", Lease: "wave/worker-1", Tool: ToolShellCommand, Command: "magus refs Consulted"})
	record(t, base, AgentCommand{Session: "w1", Lease: "wave/worker-1", Tool: ToolFileWrite, Path: "b.go"})

	got, gap, _ := Consulted(t.Context(), "", base, []string{"a.go", "b.go"}, 100)

	// The subject both workers asked about counts twice; the list is keyed by subject and not
	// by who asked.
	assert.Equal(t, []Consultation{
		{Verb: "explain", Subject: "internal/trail", Count: 2},
		{Verb: "refs", Subject: "Consulted", Count: 1},
	}, got)
	assert.Equal(t, ConsultGapNone, gap)
}

// The four silences a reviewer must be able to tell apart. Three are facts about the record and
// one is a fact about the change, and rendering them alike reports a fresh clone as unresearched
// work.
func (s *ConsultSuite) TestConsultedNamesNothingObservedInThisCheckout() {
	_, gap, _ := Consulted(s.T().Context(), "", s.T().TempDir(), []string{"a.go"}, 100)
	s.Equal(ConsultGapUnobserved, gap)
}

func (s *ConsultSuite) TestConsultedNamesObservedButUnleased() {
	t := s.T()
	t.Setenv(EnvBaggage, "")
	base := t.TempDir()
	record(t, base, AgentCommand{Session: "s1", Tool: ToolShellCommand, Command: "magus explain internal/trail"})
	record(t, base, AgentCommand{Session: "s1", Tool: ToolFileWrite, Path: "a.go"})

	_, gap, _ := Consulted(t.Context(), "", base, []string{"a.go"}, 100)
	s.Equal(ConsultGapUnleased, gap)
}

func (s *ConsultSuite) TestConsultedNamesLeasedWorkThatWroteOtherFiles() {
	t := s.T()
	base := t.TempDir()
	record(t, base, AgentCommand{Session: "s1", Lease: "other", Tool: ToolShellCommand, Command: "magus explain internal/trail"})
	record(t, base, AgentCommand{Session: "s1", Lease: "other", Tool: ToolFileWrite, Path: "elsewhere.go"})

	_, gap, _ := Consulted(t.Context(), "", base, []string{"a.go"}, 100)
	s.Equal(ConsultGapUnmatched, gap)
}

func (s *ConsultSuite) TestConsultedNamesAuthorsWhoAskedNothing() {
	t := s.T()
	base := t.TempDir()
	record(t, base, AgentCommand{Session: "s1", Lease: "mine", Tool: ToolShellCommand, Command: "go doc net/http"})
	record(t, base, AgentCommand{Session: "s1", Lease: "mine", Tool: ToolFileWrite, Path: "a.go"})

	_, gap, _ := Consulted(t.Context(), "", base, []string{"a.go"}, 100)
	s.Equal(ConsultGapNoQuestions, gap)
}

// A consultation that cannot be tied to a lease is not evidence about any changeset, so it is
// dropped rather than attributed to whoever happens to have written nearby.
func (s *ConsultSuite) TestConsultedReportsNothingWithoutALease() {
	t := s.T()
	// Explicit, because AppendAgentCommand falls back to this channel and these tests may
	// themselves be run by an agent working under a lease.
	t.Setenv(EnvBaggage, "")
	base := t.TempDir()
	record(t, base, AgentCommand{Session: "s1", Tool: ToolShellCommand, Command: "magus explain internal/trail"})
	record(t, base, AgentCommand{Session: "s1", Tool: ToolFileWrite, Path: "internal/trail/consult.go"})

	got, gap, _ := Consulted(t.Context(), "", base, []string{"internal/trail/consult.go"}, 100)
	assert.Empty(t, got)
	assert.Equal(t, ConsultGapUnleased, gap)
}

// A lease that wrote somewhere else explains a different change, and its questions must not
// travel to this one.
func (s *ConsultSuite) TestConsultedIgnoresALeaseThatWroteNothingInTheChangeset() {
	t := s.T()
	base := t.TempDir()
	record(t, base, AgentCommand{Session: "s1", Lease: "mine", Tool: ToolShellCommand, Command: "magus refs AppendAgentCommand"})
	record(t, base, AgentCommand{Session: "s1", Lease: "mine", Tool: ToolFileWrite, Path: "internal/trail/consult.go"})
	record(t, base, AgentCommand{Session: "s2", Lease: "theirs", Tool: ToolShellCommand, Command: "magus explain a"})
	record(t, base, AgentCommand{Session: "s2", Lease: "theirs", Tool: ToolFileWrite, Path: "console/src/app.ts"})

	got, _, _ := Consulted(t.Context(), "", base, []string{"internal/trail/consult.go"}, 100)
	require.Len(t, got, 1)
	assert.Equal(t, Consultation{Verb: "refs", Subject: "AppendAgentCommand", Count: 1}, got[0])
}

// The recorded path is absolute for every host observed so far, and the changeset speaks
// workspace-relative. Replay closes the same vocabulary gap.
func (s *ConsultSuite) TestConsultedRelativizesTheWrittenPath() {
	t := s.T()
	base := t.TempDir()
	record(t, base, AgentCommand{Session: "s1", Lease: "l", Tool: ToolShellCommand, Command: "magus describe target ci ."})
	record(t, base, AgentCommand{Session: "s1", Lease: "l", Tool: ToolFileWrite, Path: "/repo/magusfile.buzz"})

	got, _, _ := Consulted(t.Context(), "/repo", base, []string{"magusfile.buzz"}, 100)
	require.Len(t, got, 1)
	assert.Equal(t, Consultation{Verb: "describe", Subject: "target ci .", Count: 1}, got[0])
}

// consultation is parseConsultation over cmd, which must read as one.
func (s *ConsultSuite) consultation(cmd string) [2]string {
	verb, subject, ok := parseConsultation(cmd)
	s.Require().True(ok, cmd)
	return [2]string{verb, subject}
}

func (s *ConsultSuite) TestConsultationOfReadsTheVerbAndItsSubject() {
	s.Equal([2]string{"query", "kind=spell"}, s.consultation("magus query kind=spell"))
	s.Equal([2]string{"explain", "internal/cache"}, s.consultation("./magus explain internal/cache"))
	s.Equal([2]string{"refs", "Open"}, s.consultation("/usr/local/bin/magus refs Open"))
	s.Equal([2]string{"where", "api gateway"}, s.consultation("magus where api gateway"))
	s.Equal([2]string{"explain", "a b"}, s.consultation("MAGUS_ROOT=/repo magus explain a b"))
}

// A flag that takes a separate value must not hand the value to the verb slot, and a
// trailing flag is not part of what was asked.
func (s *ConsultSuite) TestConsultationOfSkipsFlags() {
	s.Equal([2]string{"query", "kind=op"}, s.consultation("magus --root /repo -o json query kind=op -s"))
}

func (s *ConsultSuite) TestConsultationOfRefusesWhatIsNotAConsultation() {
	for _, cmd := range []string{
		"magus graph stats",           // rule 1: shared with `graph build`, which writes
		"magus notes get spawn-chain", // rule 3: the subject sits in a second token
		"magus run test .",
		"magus",
		"magus --root", // a value-taking flag with nothing after it
		"rg query internal",
		"",
	} {
		verb, subject, ok := parseConsultation(cmd)
		s.False(ok, cmd)
		s.Empty(verb, cmd)
		s.Empty(subject, cmd)
	}
}

// A subject is one line of a report, so an odd invocation cannot push an unbounded string into
// it. The cut lands on a rune boundary.
func (s *ConsultSuite) TestConsultationOfBoundsTheSubject() {
	t := s.T()
	long := ""
	for len(long) < maxSubjectLen*2 {
		long += "ünïcödé"
	}
	_, subject, ok := parseConsultation("magus explain " + long)
	require.True(t, ok)
	assert.LessOrEqual(t, len(subject), maxSubjectLen)
	assert.True(t, utf8.ValidString(subject))
}

// A blob that does not decode is named beside the consultations the rest give.
func (s *ConsultSuite) TestConsultedReturnsAnUndecodableBlob() {
	t := s.T()
	base := t.TempDir()
	ref := recordUndecodable(t, base)

	_, _, err := Consulted(t.Context(), "", base, []string{"a.go"}, 100)

	var undecodable *BlobUndecodableError
	require.ErrorAs(t, err, &undecodable)
	assert.Equal(t, ref, undecodable.Ref)
}
