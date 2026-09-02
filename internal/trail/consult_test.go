package trail

import (
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConsultedJoinsQuestionsToTheWriteThroughTheLease(t *testing.T) {
	base := t.TempDir()
	record(t, base, AgentCommand{Session: "s1", Lease: "wave2/trail", Tool: toolShell, Command: `magus query "kind=target project=internal/trail"`})
	record(t, base, AgentCommand{Session: "s1", Lease: "wave2/trail", Tool: toolShell, Command: "magus explain internal/trail"})
	record(t, base, AgentCommand{Session: "s1", Lease: "wave2/trail", Tool: toolShell, Command: "magus explain internal/trail"})
	// Neither of these is a consultation: one runs work, the other is not magus at all.
	record(t, base, AgentCommand{Session: "s1", Lease: "wave2/trail", Tool: toolShell, Command: "magus run go-build ."})
	record(t, base, AgentCommand{Session: "s1", Lease: "wave2/trail", Tool: toolShell, Command: "grep -rn explain internal/trail"})
	record(t, base, AgentCommand{Session: "s1", Lease: "wave2/trail", Tool: toolWrite, Path: "internal/trail/consult.go"})

	got := Consulted("", base, []string{"internal/trail/consult.go"}, 100)

	// Most asked first, and the subject is kept where Touch.Ran drops every argument.
	assert.Equal(t, []Consultation{
		{Verb: "explain", Subject: "internal/trail", Count: 2},
		{Verb: "query", Subject: "kind=target project=internal/trail", Count: 1},
	}, got)
}

// A consultation that cannot be tied to a lease is not evidence about any changeset, so it is
// dropped rather than attributed to whoever happens to have written nearby.
func TestConsultedReportsNothingWithoutALease(t *testing.T) {
	// Explicit, because AppendAgentCommand falls back to this channel and these tests may
	// themselves be run by an agent working under a lease.
	t.Setenv(EnvBaggage, "")
	base := t.TempDir()
	record(t, base, AgentCommand{Session: "s1", Tool: toolShell, Command: "magus explain internal/trail"})
	record(t, base, AgentCommand{Session: "s1", Tool: toolWrite, Path: "internal/trail/consult.go"})

	assert.Empty(t, Consulted("", base, []string{"internal/trail/consult.go"}, 100))
}

// A lease that wrote somewhere else explains a different change, and its questions must not
// travel to this one.
func TestConsultedIgnoresALeaseThatWroteNothingInTheChangeset(t *testing.T) {
	base := t.TempDir()
	record(t, base, AgentCommand{Session: "s1", Lease: "mine", Tool: toolShell, Command: "magus refs AppendAgentCommand"})
	record(t, base, AgentCommand{Session: "s1", Lease: "mine", Tool: toolWrite, Path: "internal/trail/consult.go"})
	record(t, base, AgentCommand{Session: "s2", Lease: "theirs", Tool: toolShell, Command: "magus path a b"})
	record(t, base, AgentCommand{Session: "s2", Lease: "theirs", Tool: toolWrite, Path: "console/src/app.ts"})

	got := Consulted("", base, []string{"internal/trail/consult.go"}, 100)
	require.Len(t, got, 1)
	assert.Equal(t, Consultation{Verb: "refs", Subject: "AppendAgentCommand", Count: 1}, got[0])
}

// The recorded path is absolute for every host observed so far, and the changeset speaks
// workspace-relative. Replay closes the same vocabulary gap.
func TestConsultedRelativizesTheWrittenPath(t *testing.T) {
	base := t.TempDir()
	record(t, base, AgentCommand{Session: "s1", Lease: "l", Tool: toolShell, Command: "magus describe target ci ."})
	record(t, base, AgentCommand{Session: "s1", Lease: "l", Tool: toolWrite, Path: "/repo/magusfile.buzz"})

	got := Consulted("/repo", base, []string{"magusfile.buzz"}, 100)
	require.Len(t, got, 1)
	assert.Equal(t, "describe", got[0].Verb)
	assert.Equal(t, "target ci .", got[0].Subject)
}

func TestConsultationOfReadsTheVerbAndItsSubject(t *testing.T) {
	for _, tc := range []struct {
		cmd, verb, subject string
		ok                 bool
	}{
		{cmd: "magus query kind=spell", verb: "query", subject: "kind=spell", ok: true},
		{cmd: "./magus explain internal/cache", verb: "explain", subject: "internal/cache", ok: true},
		{cmd: "/usr/local/bin/magus refs Open", verb: "refs", subject: "Open", ok: true},
		// A flag that takes a separate value must not hand the value to the verb slot, and a
		// trailing flag is not part of what was asked.
		{cmd: "magus --root /repo -o json query kind=op -s", verb: "query", subject: "kind=op", ok: true},
		{cmd: "MAGUS_ROOT=/repo magus path a b", verb: "path", subject: "a b", ok: true},
		{cmd: "magus run test .", ok: false},
		{cmd: "magus", ok: false},
		// A value-taking flag with nothing after it walks the scan past the end of the line.
		{cmd: "magus --root", ok: false},
		{cmd: "rg query internal", ok: false},
		{cmd: "", ok: false},
	} {
		verb, subject, ok := consultationOf(tc.cmd)
		assert.Equal(t, tc.ok, ok, tc.cmd)
		assert.Equal(t, tc.verb, verb, tc.cmd)
		assert.Equal(t, tc.subject, subject, tc.cmd)
	}
}

// A subject is one line of a report, so an odd invocation cannot push an unbounded string into
// it. The cut lands on a rune boundary.
func TestConsultationOfBoundsTheSubject(t *testing.T) {
	long := ""
	for len(long) < maxSubjectLen*2 {
		long += "ünïcödé"
	}
	_, subject, ok := consultationOf("magus explain " + long)
	require.True(t, ok)
	assert.LessOrEqual(t, len(subject), maxSubjectLen)
	assert.True(t, utf8.ValidString(subject))
}
