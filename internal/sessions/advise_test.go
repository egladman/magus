package sessions

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type DirectiveSuite struct {
	suite.Suite
	dir   string
	store DirectiveStore
	now   time.Time
}

func TestDirectiveSuite(t *testing.T) { suite.Run(t, new(DirectiveSuite)) }

func (s *DirectiveSuite) SetupTest() {
	s.dir = s.T().TempDir()
	s.store = newDirectiveStore(s.dir)
	s.now = time.UnixMilli(1_800_000_000_000)
}

func (s *DirectiveSuite) post(text, expect string) Directive {
	s.now = s.now.Add(time.Second)
	a, err := s.store.Post(context.Background(), text, expect, "eli", s.now)
	s.Require().NoError(err)
	return a
}

func (s *DirectiveSuite) deliver(session string) []Directive {
	got, err := s.store.Deliver(context.Background(), Recipient{Session: session, Host: "claude-code", Event: "Stop"}, s.now)
	s.Require().NoError(err)
	return got
}

func (s *DirectiveSuite) TestAnAbsentStoreHoldsNothing() {
	got, err := s.store.Load()
	s.Require().NoError(err)
	s.Empty(got)
}

func (s *DirectiveSuite) TestADeliveryWithNothingOpenCreatesNoStore() {
	s.store = newDirectiveStore(filepath.Join(s.dir, "absent"))
	s.Empty(s.deliver("s1"))
	s.NoDirExists(s.store.Dir())
}

func (s *DirectiveSuite) TestEachSessionIsShownAPostOnce() {
	a := s.post("push what you have; we squash into one PR", "")
	s.Equal([]string{a.ID}, ids(s.deliver("s1")))
	s.Empty(s.deliver("s1"))
	s.Equal([]string{a.ID}, ids(s.deliver("s2")))
}

func (s *DirectiveSuite) TestASessionPostedAfterIsShownItToo() {
	s.post("first", "")
	s.deliver("s1")
	second := s.post("second", "")
	s.Equal([]string{second.ID}, ids(s.deliver("s1")))
}

func (s *DirectiveSuite) TestAnEmptySessionIdIsShownNothing() {
	s.post("anything", "")
	s.Empty(s.deliver(""))
	all, err := s.store.Load()
	s.Require().NoError(err)
	s.Empty(all[0].Delivered)
}

func (s *DirectiveSuite) TestARetractedPostIsShownToNobody() {
	a := s.post("stop pushing", "")
	_, err := s.store.Retract(context.Background(), a.ID[:8], "eli", s.now)
	s.Require().NoError(err)
	s.Empty(s.deliver("s1"))
}

func (s *DirectiveSuite) TestARetractionClosesOnce() {
	a := s.post("stop pushing", "")
	_, err := s.store.Retract(context.Background(), a.ID, "eli", s.now)
	s.Require().NoError(err)
	_, err = s.store.Retract(context.Background(), a.ID, "eli", s.now)
	s.ErrorIs(err, errRetracted)
}

func (s *DirectiveSuite) TestAnAmbiguousRetractionNamesEveryMatch() {
	first := s.post("first", "")
	second := s.post("second", "")
	_, err := s.store.Retract(context.Background(), "adv-", "eli", s.now)
	s.ErrorContains(err, first.ID)
	s.ErrorContains(err, second.ID)
}

func (s *DirectiveSuite) TestARetractionOfAnUnknownIdIsRefused() {
	_, err := s.store.Retract(context.Background(), "adv-nope", "eli", s.now)
	s.ErrorIs(err, ErrNoDirective)
}

func (s *DirectiveSuite) TestAnActionCountsOnlyAfterADelivery() {
	a := s.post("push what you have", "git push")
	at := Recipient{Session: "s1"}
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

func (s *DirectiveSuite) TestAnUndecodableLineFailsTheRead() {
	s.post("anything", "")
	f, err := os.OpenFile(filepath.Join(s.dir, directiveFile), os.O_APPEND|os.O_WRONLY, 0o644)
	s.Require().NoError(err)
	_, err = f.WriteString(`{"kind":"shout","id":"adv-x"}` + "\n")
	s.Require().NoError(err)
	s.Require().NoError(f.Close())
	_, err = s.store.Load()
	s.Error(err)
}

func (s *DirectiveSuite) TestTheDirectiveSaysWhoseWordsTheyAre() {
	a := s.post("push what you have", "")
	text := DirectiveText([]Directive{a})
	s.Contains(text, "eli, the person you work for")
	s.Contains(text, "not a relay from another agent")
	s.Contains(text, a.ID)
	s.Contains(text, "push what you have")
}

func (s *DirectiveSuite) TestAnEmptyTextIsRefused() {
	_, err := s.store.Post(context.Background(), "  ", "", "eli", s.now)
	s.Error(err)
}

func ids(as []Directive) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, a.ID)
	}
	return out
}
