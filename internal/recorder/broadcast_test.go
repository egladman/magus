package recorder

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/safetyscissors/journal"
)

type BroadcasterSuite struct{ suite.Suite }

func TestBroadcaster(t *testing.T) { suite.Run(t, new(BroadcasterSuite)) }

// emit sends one record to bc through Emit, the real capture path.
func emit(bc *Broadcaster, text string) {
	Emit(WithSinks(backgroundContext(), bc.Handle), journal.Output{Text: text})
}

func text(r journal.Record) string {
	out, _ := r.Output()
	return out.Text
}

func (s *BroadcasterSuite) TestABacklogThenLiveRecords() {
	bc := NewBroadcaster()
	emit(bc, "before-1")
	emit(bc, "before-2")

	backlog, ch, cancel := bc.Subscribe()
	defer cancel()
	s.Require().Len(backlog, 2)
	s.Equal("before-1", text(backlog[0]))

	emit(bc, "after-1")
	s.Equal("after-1", text(<-ch))

	select {
	case <-bc.Done():
		s.Fail("Done should not be closed before Close")
	default:
	}
	bc.Close()
	<-bc.Done()
}

func (s *BroadcasterSuite) TestARecordAfterCloseIsDropped() {
	bc := NewBroadcaster()
	emit(bc, "one")
	bc.Close()
	bc.Close()
	emit(bc, "dropped")

	backlog, _, cancel := bc.Subscribe()
	defer cancel()
	s.Require().Len(backlog, 1)
	s.Equal("one", text(backlog[0]))
}

func (s *BroadcasterSuite) TestEverySubscriberReceivesARecord() {
	bc := NewBroadcaster()
	_, ch1, cancel1 := bc.Subscribe()
	_, ch2, cancel2 := bc.Subscribe()
	defer cancel2()

	emit(bc, "hi")
	s.Equal("hi", text(<-ch1))
	s.Equal("hi", text(<-ch2))

	cancel1()
	emit(bc, "again")
	s.Equal("again", text(<-ch2))
}
