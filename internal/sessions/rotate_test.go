package sessions

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/stretchr/testify/suite"
)

const retention = 30 * 24 * time.Hour

// msAgo is a record timestamp age old, in the unix milliseconds a Record carries.
func msAgo(age time.Duration) int64 { return time.Now().Add(-age).UnixMilli() }

// AttentionTieSuite pins the attention exemption: pruning with [AttentionTie] never
// splits a request, and never deletes one nobody answered.
type AttentionTieSuite struct {
	suite.Suite
	dir string
}

func TestAttentionTie(t *testing.T) { suite.Run(t, new(AttentionTieSuite)) }

func (s *AttentionTieSuite) SetupTest() { s.dir = s.T().TempDir() }

// writeAged lays down an invocation file and backdates its modification time to match.
// Pruning DECIDES on the newest record, and only reads a file at all once its
// modification time says it might be old enough to be worth reading.
func (s *AttentionTieSuite) writeAged(invocation string, age time.Duration, records []session.Record) {
	writeRaw(s.T(), s.dir, invocation, records)
	when := time.Now().Add(-age)
	s.Require().NoError(os.Chtimes(filepath.Join(s.dir, invocation+session.FileExt), when, when))
}

func (s *AttentionTieSuite) stored() []string {
	entries, err := os.ReadDir(s.dir)
	s.Require().NoError(err)
	out := []string{}
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), session.FileExt); ok {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

func (s *AttentionTieSuite) read() session.Fold {
	fold, err := session.ReadAll(s.dir)
	s.Require().NoError(err)
	return fold
}

func (s *AttentionTieSuite) prune() {
	s.Require().NoError(session.Prune(s.dir, retention, session.WithTie(AttentionTie())))
}

// Rule 1: a request nobody has answered pins every file naming it, however old. The
// queue belongs to a person, and a retention window that emptied it would be an
// expiry: the one thing this package refuses to have.
func (s *AttentionTieSuite) TestExemptsAFileHoldingAStillOpenRequest() {
	t := s.T()
	age := 200 * 24 * time.Hour
	s.writeAged("blocked", age, []session.Record{
		attRecord(t, "blocked", 1, msAgo(age), KindAttentionOpen, openPayload("att-1", "needs a human")),
	})
	s.writeAged("quiet", age, []session.Record{
		attRecord(t, "quiet", 1, msAgo(age), session.KindTargetResult, session.TargetResult{Target: "build", Outcome: session.OutcomePass}),
	})

	s.prune()

	s.Equal([]string{"blocked"}, s.stored(), "the exemption covers the blocked invocation only; its neighbor ages out normally")
	open := AttentionQueue(s.read())
	s.Require().Len(open, 1)
	s.Equal("att-1", open[0].ID)
}

// Rule 2, the resurrection half. The agent that raised the block is still running, so
// its file is young; the person who disposed of it has not run magus since, so theirs
// is old. Deleting the dispose alone would put an answered request back in the queue.
func (s *AttentionTieSuite) TestWillNotResurrectARequestByDeletingItsDispose() {
	t := s.T()
	s.writeAged("agent", time.Hour, []session.Record{
		attRecord(t, "agent", 1, msAgo(200*24*time.Hour), KindAttentionOpen, openPayload("att-1", "needs a human")),
		attRecord(t, "agent", 2, msAgo(time.Hour), session.KindTargetResult, session.TargetResult{Target: "build", Outcome: session.OutcomePass}),
	})
	s.writeAged("human", 199*24*time.Hour, []session.Record{
		attRecord(t, "human", 1, msAgo(199*24*time.Hour), KindAttentionDispose, AttentionDispose{Request: "att-1", Note: "handled"}),
	})
	s.Require().Empty(AttentionQueue(s.read()), "the request is answered before pruning runs")

	s.prune()

	s.Equal([]string{"agent", "human"}, s.stored())
	s.Empty(AttentionQueue(s.read()), "an answered request must not come back because its answer was garbage-collected")
}

// Rule 2, the other half: once no file naming a request is young enough to spare, the
// whole request goes at once.
func (s *AttentionTieSuite) TestDeletesARequestsFilesTogetherOnceBothAgedOut() {
	t := s.T()
	age := 200 * 24 * time.Hour
	s.writeAged("agent", age, []session.Record{
		attRecord(t, "agent", 1, msAgo(age), KindAttentionOpen, openPayload("att-1", "needs a human")),
	})
	s.writeAged("human", age-time.Hour, []session.Record{
		attRecord(t, "human", 1, msAgo(age-time.Hour), KindAttentionDispose, AttentionDispose{Request: "att-1", Note: "handled"}),
	})

	s.prune()

	s.Empty(s.stored())
	s.Empty(Attention(s.read()))
}

// Exemptions travel: att-open pins the agent file, which pins att-done, which pins the
// human file, and nothing but the pinning would stop att-done's open from outliving its
// dispose.
func (s *AttentionTieSuite) TestExemptionPropagatesThroughASharedFile() {
	t := s.T()
	age := 200 * 24 * time.Hour
	s.writeAged("agent", age, []session.Record{
		attRecord(t, "agent", 1, msAgo(age), KindAttentionOpen, openPayload("att-open", "still waiting")),
		attRecord(t, "agent", 2, msAgo(age), KindAttentionOpen, openPayload("att-done", "was waiting")),
	})
	s.writeAged("human", age, []session.Record{
		attRecord(t, "human", 1, msAgo(age), KindAttentionDispose, AttentionDispose{Request: "att-done", Note: "handled"}),
	})

	s.prune()

	s.Equal([]string{"agent", "human"}, s.stored())
	open := AttentionQueue(s.read())
	s.Require().Len(open, 1, "att-done stays answered; only att-open is still a wait")
	s.Equal("att-open", open[0].ID)
}

// Every magus writer opens with the tie, so the housekeeping an open runs keeps a
// still-open request too.
func (s *AttentionTieSuite) TestAMagusWriterPrunesWithTheTie() {
	t := s.T()
	age := 400 * 24 * time.Hour
	s.writeAged("blocked", age, []session.Record{
		attRecord(t, "blocked", 1, msAgo(age), KindAttentionOpen, openPayload("att-1", "needs a human")),
	})
	s.writeAged("ancient", age, []session.Record{
		attRecord(t, "ancient", 1, msAgo(age), session.KindTargetResult, session.TargetResult{Target: "build", Outcome: session.OutcomePass}),
	})

	s.Require().NoError(RecordGate(s.dir, GateResult{Target: "ci", Scope: GateScopeAffected, Commit: "abc123", Outcome: session.OutcomePass}, session.InvocationStart{}))

	s.NotContains(s.stored(), "ancient")
	s.Contains(s.stored(), "blocked")
}
