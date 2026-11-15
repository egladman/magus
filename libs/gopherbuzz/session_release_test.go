package buzz

import (
	"context"
	"runtime/debug"
	"testing"

	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// SessionReleaseSuite pins that a session's slots are freed by one release, whoever
// asks for it and however often.
type SessionReleaseSuite struct {
	suite.Suite
}

func TestSessionReleaseSuite(t *testing.T) {
	suite.Run(t, new(SessionReleaseSuite))
}

func (s *SessionReleaseSuite) keptSession(code string) (*Session, func()) {
	sess := NewSession(context.Background(), WithEmbedded())
	s.T().Cleanup(func() { _ = sess.Close() })
	require.NoError(s.T(), sess.Exec(context.Background(), code))
	return sess, sess.Keep()
}

func (s *SessionReleaseSuite) TestSecondReleaseFreesNothing() {
	_, release := s.keptSession(`var xs = [1, 2, 3];`)
	before := vm.ReadHeapStats().Objects
	release()
	afterFirst := vm.ReadHeapStats().Objects
	require.Less(s.T(), afterFirst, before, "the first release must free the session's slots")
	release()
	s.Equal(afterFirst, vm.ReadHeapStats().Objects, "the second release freed again")
}

func (s *SessionReleaseSuite) TestCallValueOnAReleasedSessionIsAnError() {
	sess, release := s.keptSession(`fun one() > int { return 1; }`)
	fn := sess.GetGlobal("one")
	got, err := sess.CallValue(context.Background(), fn, nil)
	require.NoError(s.T(), err)
	s.Equal(int64(1), got.AsInt())
	release()
	_, err = sess.CallValue(context.Background(), fn, nil)
	s.Error(err, "CallValue on a released session")
	_, err = sess.CallMethod(context.Background(), fn, "call", nil)
	s.Error(err, "CallMethod on a released session")
}

func (s *SessionReleaseSuite) TestSecondReleaseLeavesAReusedSlotAlone() {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "-race" && setting.Value == "true" {
				s.T().Skip("the race build poisons a released slot instead of reusing it")
			}
		}
	}
	sess, release := s.keptSession(`var xs = [1, 2, 3];`)
	old := sess.GetGlobal("xs")
	release()
	// A host-built value takes the freed slot and is not adopted by any owner yet,
	// which is the state a rule call's request is in.
	var reused vm.Value
	var held []vm.Value
	for range 256 {
		v := vm.ListValue([]vm.Value{vm.IntValue(7)})
		if v == old {
			reused = v
			break
		}
		held = append(held, v)
	}
	require.NotZero(s.T(), reused, "no allocation reused the freed slot")
	release()
	s.Equal([]vm.Value{vm.IntValue(7)}, reused.ListItems(), "the second release freed a slot another value holds")
	_ = held
}
