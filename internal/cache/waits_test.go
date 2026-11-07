package cache

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/termkit/screen"
	"github.com/egladman/magus/types"
)

type WaitBoardSuite struct {
	suite.Suite
	start time.Time
	board waitBoard
}

func TestWaitBoardSuite(t *testing.T) { suite.Run(t, new(WaitBoardSuite)) }

func (s *WaitBoardSuite) SetupTest() {
	s.start = time.Unix(0, 0)
	s.board = waitBoard{}
}

func (s *WaitBoardSuite) later() time.Time { return s.start.Add(time.Minute) }

// chain plans a <- b <- c and starts a.
func (s *WaitBoardSuite) chain() {
	a, b, c := ref("a", "build"), ref("b", "build"), ref("c", "build")
	s.board.plan(s.start, []PlannedStep{
		{Ref: a}, {Ref: b, Upstreams: []types.TargetRef{a}}, {Ref: c, Upstreams: []types.TargetRef{b}},
	})
	s.board.set(s.start, a, StepRunning)
}

func (s *WaitBoardSuite) TestTreeIsNilBeforeAWaitIsOldEnough() {
	s.chain()
	s.Nil(s.board.tree(s.start.Add(waitShowAfter - time.Millisecond)))
}

func (s *WaitBoardSuite) TestTreeHangsTheChainUnderTheRunningStep() {
	s.chain()
	roots := s.board.tree(s.later())
	s.Require().Len(roots, 1)
	c := &waitNode{ref: ref("c", "build"), state: StepWaiting, age: time.Minute}
	b := &waitNode{ref: ref("b", "build"), state: StepWaiting, age: time.Minute, holds: 1, children: []*waitNode{c}}
	s.Equal([]*waitNode{{ref: ref("a", "build"), state: StepRunning, age: time.Minute, holds: 2, children: []*waitNode{b}}}, roots)
}

func (s *WaitBoardSuite) TestTreePutsTheBottleneckFirst() {
	x, y := ref("x", "test"), ref("y", "test")
	s.board.plan(s.start, []PlannedStep{
		{Ref: x}, {Ref: y},
		{Ref: ref("w1", "test"), Upstreams: []types.TargetRef{x}},
		{Ref: ref("w2", "test"), Upstreams: []types.TargetRef{y}},
		{Ref: ref("w3", "test"), Upstreams: []types.TargetRef{y}},
	})
	s.board.set(s.start, x, StepRunning)
	s.board.set(s.start, y, StepRunning)
	roots := s.board.tree(s.later())
	s.Require().Len(roots, 2)
	s.Equal(&waitNode{ref: y, state: StepRunning, age: time.Minute, holds: 2, children: []*waitNode{
		{ref: ref("w2", "test"), state: StepWaiting, age: time.Minute},
		{ref: ref("w3", "test"), state: StepWaiting, age: time.Minute},
	}}, roots[0])
}

func (s *WaitBoardSuite) TestWaiterOnTwoUpstreamsIsDrawnOnceUnderTheRunningOne() {
	run, wait := ref("run", "build"), ref("queued", "build")
	w := ref("w", "build")
	s.board.plan(s.start, []PlannedStep{
		{Ref: run}, {Ref: wait}, {Ref: w, Upstreams: []types.TargetRef{wait, run}},
	})
	s.board.set(s.start, run, StepRunning)
	s.board.set(s.start, wait, StepQueued)
	roots := s.board.tree(s.later())
	s.Require().Len(roots, 1)
	s.Equal([]*waitNode{{ref: run, state: StepRunning, age: time.Minute, holds: 1, children: []*waitNode{
		{ref: w, state: StepWaiting, age: time.Minute, others: 1},
	}}}, roots)
}

func (s *WaitBoardSuite) TestDoneUpstreamReleasesItsWaiter() {
	s.chain()
	s.board.set(s.start, ref("a", "build"), StepDone)
	s.board.set(s.start, ref("b", "build"), StepRunning)
	roots := s.board.tree(s.later())
	s.Require().Len(roots, 1)
	s.Equal([]*waitNode{{ref: ref("b", "build"), state: StepRunning, age: time.Minute, holds: 1, children: []*waitNode{
		{ref: ref("c", "build"), state: StepWaiting, age: time.Minute},
	}}}, roots)
}

func (s *WaitBoardSuite) TestBoardIsIdleOnceEveryStepIsDone() {
	s.chain()
	for _, p := range []string{"a", "b", "c"} {
		s.board.set(s.start, ref(p, "build"), StepDone)
	}
	s.False(s.board.inFlight())
}

func (s *WaitBoardSuite) TestUpstreamsLeaveOutStepsOutsideTheBatch() {
	ups := inScopeUpstreams([]Step{
		{ProjectPath: "a", Target: "build"},
		{ProjectPath: "b", Target: "build", DependsOn: []string{"a", "gone"}},
	})
	s.Equal([]types.TargetRef{ref("a", "build")}, ups[ref("b", "build")])
}

// waitContext is the context waitForUpstream logs console build's wait with.
func waitContext() context.Context {
	edge := WaitEdge{Waiter: ref("console", "build"), Upstream: ref("docs", "build")}
	return context.WithValue(context.Background(), waitEdgeKey{}, edge)
}

type WaitBandSuite struct {
	suite.Suite
	screen *screen.Terminal
	h      *PrettyHandler
	now    time.Time
}

func TestWaitBandSuite(t *testing.T) { suite.Run(t, new(WaitBandSuite)) }

func (s *WaitBandSuite) SetupTest() {
	s.T().Setenv("TERM", "xterm-256color")
	s.screen = screen.New(120, 30)
	s.h = newPrettyHandler(s.screen, slog.LevelInfo, terminalProbe{})
	s.h.tickEvery = 0
	s.now = time.Unix(0, 0)
	s.h.now = func() time.Time { return s.now }
}

func (s *WaitBandSuite) handle(msg string, attrs ...slog.Attr) {
	s.Require().NoError(s.h.Handle(context.Background(), buildRecord(msg, attrs...)))
}

func (s *WaitBandSuite) step(project, target string, state StepState) {
	s.handle(stepEvent, slog.String("project", project), slog.String("target", target),
		slog.String("state", string(state)))
}

// plan feeds docs <- console, plus n waiters on test.
func (s *WaitBandSuite) plan(n int) {
	steps := []PlannedStep{
		{Ref: ref("docs", "build")},
		{Ref: ref("console", "build"), Upstreams: []types.TargetRef{ref("docs", "build")}},
		{Ref: ref("", "test")},
	}
	for i := range n {
		steps = append(steps, PlannedStep{
			Ref:       ref(fmt.Sprintf("w%d", i), "lint"),
			Upstreams: []types.TargetRef{ref("", "test")},
		})
	}
	s.handle(planEvent, slog.Any("steps", steps))
	s.step("docs", "build", StepRunning)
	s.step("", "test", StepRunning)
	s.now = s.now.Add(8 * time.Minute)
	s.handle("cache.pool", slog.Int("capacity", 4), slog.Int("running", 2))
}

func (s *WaitBandSuite) TestBandDrawsTheBottleneckAndItsWaiters() {
	s.plan(2)
	row := s.screen.FindRow("(root) test")
	s.Require().NotZero(row)
	s.Contains(s.screen.Row(row), "8m0s · bottleneck · holds 2")
	s.NotZero(s.screen.FindRow("w0 lint"))
	s.Contains(s.screen.Row(s.screen.FindRow("console build")), "waiting 8m0s")
}

func (s *WaitBandSuite) TestBandCountsTheWaitersPastItsBudget() {
	s.plan(8)
	s.NotZero(s.screen.FindRow("more waiting"))
}

func (s *WaitBandSuite) TestWaitLineIsDroppedWhileTheBoardShowsIt() {
	s.plan(1)
	r := buildRecord("console build is waiting for docs build to finish (8m0s so far)")
	s.Require().NoError(s.h.Handle(waitContext(), r))
	s.Zero(s.screen.FindRow("is waiting for"))
}

func (s *WaitBandSuite) TestBoardClearsAtTheEndOfTheRun() {
	s.plan(1)
	s.Require().NoError(s.h.EndRun(context.Background(), ""))
	s.Zero(s.screen.FindRow("bottleneck"))
}

func (s *WaitBandSuite) TestRunAllFeedsTheBoard() {
	s.Contains(s.boardDuringRunAll(), "holds 1", "the running upstream heads the board")
}

// An earlier phase of the same command ends its run and gives the band back
// before RunAll starts.
func (s *WaitBandSuite) TestRunAllFeedsTheBoardAfterAnEarlierRunEnded() {
	s.handle("cache.pool", slog.Int("capacity", 4), slog.Int("running", 1))
	s.Require().NoError(s.h.EndRun(context.Background(), ""))
	s.Require().False(s.h.RendersBand())
	s.Contains(s.boardDuringRunAll(), "holds 1")
}

// boardDuringRunAll runs A <- B and returns A's band row while B waits.
func (s *WaitBandSuite) boardDuringRunAll() string {
	_, c := openCache(s.T(), WithLogger(slog.New(s.h)))
	started, release := make(chan struct{}), make(chan struct{})
	steps := []Step{
		{ProjectPath: "A", Target: "build", NoCache: true},
		{ProjectPath: "B", Target: "build", NoCache: true, DependsOn: []string{"A"}},
	}
	done := make(chan error, 1)
	go func() {
		_, err := c.RunAll(s.T().Context(), steps, func(_ context.Context, st Step) error {
			if st.ProjectPath == "A" {
				close(started)
				<-release
			}
			return nil
		})
		done <- err
	}()
	<-started
	s.now = s.now.Add(time.Minute)
	s.handle("cache.pool", slog.Int("capacity", 4), slog.Int("running", 1))
	var drawn string
	if row := s.screen.FindRow("A build"); row != 0 {
		drawn = s.screen.Row(row)
	}
	close(release)
	s.Require().NoError(<-done)
	return drawn
}

func TestWaitLinePrintsOffATerminal(t *testing.T) {
	var buf bytes.Buffer
	h := NewPlainHandler(&buf, slog.LevelInfo)
	r := buildRecord("console build is waiting for docs build to finish (8m0s so far)")
	if err := h.Handle(waitContext(), r); err != nil {
		t.Fatal(err)
	}
	want := "[info] console build is waiting for docs build to finish (8m0s so far)\n"
	if got := buf.String(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
