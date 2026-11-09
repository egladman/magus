package sessions

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type AdvisorySuite struct {
	suite.Suite
	dir   string
	store AdvisoryStore
	now   time.Time
}

func TestAdvisorySuite(t *testing.T) { suite.Run(t, new(AdvisorySuite)) }

func (s *AdvisorySuite) SetupTest() {
	s.dir = s.T().TempDir()
	s.store = NewAdvisoryStore(s.dir)
	s.now = time.UnixMilli(1_800_000_000_000)
}

func (s *AdvisorySuite) post(text, expect string) Advisory {
	s.now = s.now.Add(time.Second)
	a, err := s.store.Post(context.Background(), text, expect, "eli", s.now)
	s.Require().NoError(err)
	return a
}

func (s *AdvisorySuite) deliver(session string) []Advisory {
	got, err := s.store.Deliver(context.Background(), Showing{Session: session, Host: "claude-code", Event: "Stop"}, s.now)
	s.Require().NoError(err)
	return got
}

func (s *AdvisorySuite) TestAnAbsentStoreHoldsNothing() {
	got, err := s.store.Load()
	s.Require().NoError(err)
	s.Empty(got)
}

func (s *AdvisorySuite) TestADeliveryWithNothingOpenCreatesNoStore() {
	s.store = NewAdvisoryStore(filepath.Join(s.dir, "absent"))
	s.Empty(s.deliver("s1"))
	s.NoDirExists(s.store.Dir())
}

func (s *AdvisorySuite) TestEachSessionIsShownAPostOnce() {
	a := s.post("push what you have; we squash into one PR", "")
	s.Equal([]string{a.ID}, ids(s.deliver("s1")))
	s.Empty(s.deliver("s1"))
	s.Equal([]string{a.ID}, ids(s.deliver("s2")))
}

func (s *AdvisorySuite) TestASessionPostedAfterIsShownItToo() {
	s.post("first", "")
	s.deliver("s1")
	second := s.post("second", "")
	s.Equal([]string{second.ID}, ids(s.deliver("s1")))
}

func (s *AdvisorySuite) TestAnEmptySessionIdIsShownNothing() {
	s.post("anything", "")
	s.Empty(s.deliver(""))
	all, err := s.store.Load()
	s.Require().NoError(err)
	s.Empty(all[0].Delivered)
}

func (s *AdvisorySuite) TestARetractedPostIsShownToNobody() {
	a := s.post("stop pushing", "")
	_, err := s.store.Retract(context.Background(), a.ID[:8], "eli", s.now)
	s.Require().NoError(err)
	s.Empty(s.deliver("s1"))
}

func (s *AdvisorySuite) TestARetractionClosesOnce() {
	a := s.post("stop pushing", "")
	_, err := s.store.Retract(context.Background(), a.ID, "eli", s.now)
	s.Require().NoError(err)
	_, err = s.store.Retract(context.Background(), a.ID, "eli", s.now)
	s.ErrorIs(err, ErrRetracted)
}

func (s *AdvisorySuite) TestARetractionOfAnUnknownIdIsRefused() {
	_, err := s.store.Retract(context.Background(), "adv-nope", "eli", s.now)
	s.ErrorIs(err, ErrNoAdvisory)
}

func (s *AdvisorySuite) TestAnActionCountsOnlyAfterADelivery() {
	a := s.post("push what you have", "git push")
	at := Showing{Session: "s1"}
	s.Require().NoError(s.store.RecordActed(context.Background(), at, "git push origin HEAD", s.now))
	s.deliver("s1")
	s.Require().NoError(s.store.RecordActed(context.Background(), at, "git status", s.now))
	s.Require().NoError(s.store.RecordActed(context.Background(), at, "git push origin HEAD", s.now))
	all, err := s.store.Load()
	s.Require().NoError(err)
	s.Equal(a.ID, all[0].ID)
	s.Contains(all[0].Acted, "s1")
	s.Len(all[0].Acted, 1)
}

func (s *AdvisorySuite) TestAnUndecodableLineFailsTheRead() {
	s.post("anything", "")
	f, err := os.OpenFile(filepath.Join(s.dir, advisoryFile), os.O_APPEND|os.O_WRONLY, 0o644)
	s.Require().NoError(err)
	_, err = f.WriteString(`{"kind":"shout","id":"adv-x"}` + "\n")
	s.Require().NoError(err)
	s.Require().NoError(f.Close())
	_, err = s.store.Load()
	s.Error(err)
}

func (s *AdvisorySuite) TestTheDirectiveSaysWhoseWordsTheyAre() {
	a := s.post("push what you have", "")
	text := Directive([]Advisory{a})
	s.Contains(text, "eli, the person you work for")
	s.Contains(text, "not a relay from another agent")
	s.Contains(text, a.ID)
	s.Contains(text, "push what you have")
}

func (s *AdvisorySuite) TestAnEmptyTextIsRefused() {
	_, err := s.store.Post(context.Background(), "  ", "", "eli", s.now)
	s.Error(err)
}

func ids(as []Advisory) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, a.ID)
	}
	return out
}
