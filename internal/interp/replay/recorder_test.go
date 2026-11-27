package replay

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
)

type RecorderSuite struct {
	suite.Suite
	dir string
	rec *Log
	rcd *Recorder
}

func TestRecorder(t *testing.T) { suite.Run(t, new(RecorderSuite)) }

func (s *RecorderSuite) SetupTest() {
	s.dir = s.T().TempDir()
	rec, err := OpenLog(s.dir, "out1", Header{Script: "run.buzz"})
	s.Require().NoError(err)
	s.rec = rec
	s.rcd = NewRecorder(rec)
}

// calls closes the record and reads it back with the clock zeroed.
func (s *RecorderSuite) calls() []Call {
	s.Require().NoError(s.rec.Close())
	_, calls, err := Read(s.dir, "out1")
	s.Require().NoError(err)
	for i := range calls {
		calls[i].StartedAt = 0
	}
	return calls
}

func (s *RecorderSuite) TestRecorderLogsNestedCalls() {
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded(), buzz.WithCallInterceptor(s.rcd))
	defer func() { s.NoError(sess.Close()) }()
	mod := vm.NewMap()
	mod.MapSet("ping", vm.DirectValue("probe.ping", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		return args[0], nil
	}))
	mod.MapSet("each", vm.DirectValue("probe.each", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		return sess.CallValue(ctx, args[0], nil)
	}))
	sess.SetNativeModule("probe", mod)

	s.Require().NoError(sess.Exec(ctx, `
import "probe";
final a = probe\each(fun () > str => probe\ping("inner"));
final b = probe\ping("after");
`))

	var got []string
	for _, c := range s.calls() {
		got = append(got, fmt.Sprintf("#%d in #%d %s %s %s", c.Seq, c.Parent, c.Name, c.End, c.Result))
	}
	s.Equal([]string{
		`#0 in #-1 probe.each returned "inner"`,
		`#1 in #0 probe.ping returned "inner"`,
		`#2 in #-1 probe.ping returned "after"`,
	}, got, "a call made by a callback is the child of the native that ran it")
}

func (s *RecorderSuite) TestRecorderLeavesTheInterruptedCallOpen() {
	var mid []Call
	_, err := s.rcd.InterceptCall(context.Background(), "proc.exec", []vm.Value{vm.StringValue("make")}, func(context.Context, []vm.Value) (vm.Value, error) {
		_, mid, _ = Read(s.dir, "out1")
		return vm.Null, nil
	})
	s.Require().NoError(err)
	s.Require().Len(mid, 1)
	s.Equal(callOpen, mid[0].End, "the begin line is on disk before the call runs")
}

func (s *RecorderSuite) TestDiedInNamesTheInnermostOpenCall() {
	_, err := s.rcd.InterceptCall(context.Background(), "outer", nil, func(ctx context.Context, _ []vm.Value) (vm.Value, error) {
		return s.rcd.InterceptCall(ctx, "inner", nil, func(context.Context, []vm.Value) (vm.Value, error) {
			c, ok := s.rcd.DiedIn(nil)
			s.True(ok)
			c.StartedAt = 0
			s.Equal(Call{Seq: 1, Parent: 0, Name: "inner", ArgsDigest: argsDigest(nil), End: callOpen}, c)
			return vm.Null, nil
		})
	})
	s.Require().NoError(err)
	_, ok := s.rcd.DiedIn(nil)
	s.False(ok, "nothing is open once both calls end")
}

func (s *RecorderSuite) TestARaisedCallEndsRaisedAndDiedInFindsIt() {
	args := []vm.Value{vm.StringValue("x")}
	_, err := s.rcd.InterceptCall(context.Background(), "fs.read", args, func(context.Context, []vm.Value) (vm.Value, error) {
		return vm.Null, errors.New("no such file")
	})
	s.EqualError(err, "no such file")

	c, ok := s.rcd.DiedIn(errors.New("buzz: native error: no such file"))
	s.True(ok)
	s.Equal("fs.read", c.Name)
	_, ok = s.rcd.DiedIn(errors.New("some other failure"))
	s.False(ok)

	s.Equal([]Call{{
		Seq: 0, Parent: -1, Name: "fs.read", ArgsDigest: argsDigest(args),
		End: callRaised, Error: "no such file",
	}}, s.calls())
}

func (s *RecorderSuite) TestRecorderPassesSuspendThrough() {
	_, err := s.rcd.InterceptCall(context.Background(), "probe.wait", nil, func(context.Context, []vm.Value) (vm.Value, error) {
		return vm.Null, vm.Suspend(vm.StringValue("parked"))
	})
	got, ok := vm.SuspendedValue(err)
	s.Require().True(ok, "the signal reaches the VM unwrapped")
	s.Equal("parked", got.AsString())

	s.Equal([]Call{{
		Seq: 0, Parent: -1, Name: "probe.wait", ArgsDigest: argsDigest(nil), End: callReturned, Result: `"parked"`,
	}}, s.calls())
}

func (s *RecorderSuite) TestRecorderRecordsWhyAResultIsUnencodable() {
	cb := vm.DirectValue("cb", func(context.Context, []vm.Value) (vm.Value, error) { return vm.Null, nil })
	_, err := s.rcd.InterceptCall(context.Background(), "probe.make", nil, func(context.Context, []vm.Value) (vm.Value, error) {
		return cb, nil
	})
	s.Require().NoError(err)

	s.Equal([]Call{{
		Seq: 0, Parent: -1, Name: "probe.make", ArgsDigest: argsDigest(nil), End: callReturned, Unencodable: "direct at $",
	}}, s.calls())
}

func (s *RecorderSuite) TestACallThatFailsWithTheRunCancelledStaysOpen() {
	ctx, cancel := context.WithCancel(context.Background())
	_, err := s.rcd.InterceptCall(ctx, "proc.exec", nil, func(context.Context, []vm.Value) (vm.Value, error) {
		cancel()
		return vm.Null, context.Canceled
	})
	s.ErrorIs(err, context.Canceled)
	c, ok := s.rcd.DiedIn(err)
	s.True(ok)
	s.Equal("proc.exec", c.Name)

	s.Equal([]Call{{Seq: 0, Parent: -1, Name: "proc.exec", ArgsDigest: argsDigest(nil), End: callOpen}}, s.calls())
}

func (s *RecorderSuite) TestARerunAppendsAfterTheEarlierSeqs() {
	ping := func(context.Context, []vm.Value) (vm.Value, error) { return vm.Null, nil }
	_, err := s.rcd.InterceptCall(context.Background(), "probe.ping", nil, ping)
	s.Require().NoError(err)
	s.Require().NoError(s.rec.Close())

	again, err := OpenLog(s.dir, "out1", Header{Script: "run.buzz"})
	s.Require().NoError(err)
	s.rec, s.rcd = again, NewRecorder(again)
	_, err = s.rcd.InterceptCall(context.Background(), "probe.ping", nil, ping)
	s.Require().NoError(err)

	var seqs []int
	for _, c := range s.calls() {
		seqs = append(seqs, c.Seq)
	}
	s.Equal([]int{0, 1}, seqs)
}

func (s *RecorderSuite) TestABeginErrorFailsTheCallUnrun() {
	s.Require().NoError(s.rec.Close())
	ran := false
	_, err := s.rcd.InterceptCall(context.Background(), "probe.ping", nil, func(context.Context, []vm.Value) (vm.Value, error) {
		ran = true
		return vm.Null, nil
	})
	s.ErrorContains(err, "is closed")
	s.False(ran, "a call with no begin line never runs")
	_, ok := s.rcd.DiedIn(nil)
	s.False(ok, "the failed begin leaves nothing open")
}

// The end line's store error wins over the call's own error. The value still returns,
// so the loss is loud and nothing the native computed is dropped.
func (s *RecorderSuite) TestAnEndErrorWinsAndKeepsTheValue() {
	callErr := errors.New("native failed")
	v, err := s.rcd.InterceptCall(context.Background(), "probe.ping", nil, func(context.Context, []vm.Value) (vm.Value, error) {
		s.Require().NoError(s.rec.Close())
		return vm.IntValue(7), callErr
	})
	s.Equal(int64(7), v.AsInt())
	s.ErrorIs(err, callErr)
	s.ErrorContains(err, "is closed")
}
