package stream

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type FollowerSuite struct{ suite.Suite }

func TestFollower(t *testing.T) { suite.Run(t, new(FollowerSuite)) }

// writeLog appends raw JSONL to one invocation's run log, standing in for a
// concurrently running magus process.
func (s *FollowerSuite) writeLog(dir, inv, content string) {
	s.Require().NoError(os.MkdirAll(dir, 0o755))
	f, err := os.OpenFile(filepath.Join(dir, inv+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	s.Require().NoError(err)
	_, err = f.WriteString(content)
	s.Require().NoError(err)
	s.Require().NoError(f.Close())
}

// collect drains events into a slice via the emit callback the Follower takes.
func collect(got *[]Event) func(Event) error {
	return func(e Event) error {
		*got = append(*got, e)
		return nil
	}
}

func (s *FollowerSuite) TestReplaysRetainedRuns() {
	dir := s.T().TempDir()
	s.writeLog(dir, "inv1", `{"ts":1,"inv":"inv1","kind":"started","command":{"arguments":["run","build"],"trigger":"run"}}
{"ts":2,"inv":"inv1","kind":"result","project":"api","target":"build","status":"pass","ref":"out_1"}
{"ts":3,"inv":"inv1","kind":"finished","status":"pass"}
`)

	var got []Event
	f := NewFollower(dir, "/repo")
	s.Require().NoError(f.Replay(s.T().Context(), 0, collect(&got)))

	s.Require().Len(got, 3)
	s.Equal(RunStarted, got[0].Body.EventType())
	s.Equal(Target{Project: "api", Target: "build", Status: "ok", Ref: "out_1"}, got[1].Body)
	s.Equal(RunFinished, got[2].Body.EventType())
	s.Equal("/repo", got[0].Workspace)
}

// TestReplayDoesNotRepeatOnFollow: Replay leaves the offset at end of
// file, so attaching a follow after it must deliver only what is new.
func (s *FollowerSuite) TestReplayDoesNotRepeatOnFollow() {
	dir := s.T().TempDir()
	s.writeLog(dir, "inv1", `{"ts":1,"inv":"inv1","kind":"finished","status":"pass"}`+"\n")

	var replayed []Event
	f := NewFollower(dir, "/repo")
	s.Require().NoError(f.Replay(s.T().Context(), 0, collect(&replayed)))
	s.Require().Len(replayed, 1)

	var fresh []Event
	s.Require().NoError(f.drain(s.T().Context(), "inv1.jsonl", collect(&fresh)))
	s.Empty(fresh)
}

// TestSkipsPartialLines: an output-triggered page flush can split a line,
// and half a JSON object never reaches a subscriber nor is lost once the
// rest of it lands.
func (s *FollowerSuite) TestSkipsPartialLines() {
	dir := s.T().TempDir()
	s.writeLog(dir, "inv1", `{"ts":1,"inv":"inv1","kind":"finished","status":"pass"}`+"\n"+`{"ts":2,"inv":"inv1","kind":"res`)

	var got []Event
	f := NewFollower(dir, "/repo")
	s.Require().NoError(f.Replay(s.T().Context(), 0, collect(&got)))
	s.Require().Len(got, 1, "the torn line must not be emitted")

	s.writeLog(dir, "inv1", `ult","project":"api","target":"build","status":"pass"}`+"\n")
	s.Require().NoError(f.drain(s.T().Context(), "inv1.jsonl", collect(&got)))
	s.Require().Len(got, 2, "the completed line must be picked up, not lost")
	s.Equal(TargetResult, got[1].Body.EventType())
}

// TestReplayLimitKeepsNewest: attaching to a workspace with a long
// history must not deliver all of it.
func (s *FollowerSuite) TestReplayLimitKeepsNewest() {
	dir := s.T().TempDir()
	for _, inv := range []string{"inv1", "inv2", "inv3"} {
		s.writeLog(dir, inv, fmt.Sprintf(`{"ts":1,"inv":"%s","kind":"finished","status":"pass"}`+"\n", inv))
		// Distinct mtimes: ordering is by modification time, and a same-tick write
		// would make the assertion depend on filesystem timestamp resolution.
		time.Sleep(10 * time.Millisecond)
	}

	var got []Event
	s.Require().NoError(NewFollower(dir, "/repo").Replay(s.T().Context(), 2, collect(&got)))
	s.Require().Len(got, 2)
	s.Equal("inv2", got[0].Inv)
	s.Equal("inv3", got[1].Inv)
}

// TestReplayLimitSurvivesTheFirstFollowTick: Follow re-lists the directory,
// so a log the window excluded must not drain in full on the first tick.
func (s *FollowerSuite) TestReplayLimitSurvivesTheFirstFollowTick() {
	dir := s.T().TempDir()
	for _, inv := range []string{"inv1", "inv2", "inv3"} {
		s.writeLog(dir, inv, fmt.Sprintf(`{"ts":1,"inv":"%s","kind":"finished","status":"pass"}`+"\n", inv))
		time.Sleep(10 * time.Millisecond)
	}

	f := NewFollower(dir, "/repo")
	var replayed []Event
	s.Require().NoError(f.Replay(s.T().Context(), 1, collect(&replayed)))
	s.Require().Len(replayed, 1)

	var followed []Event
	ctx, cancel := context.WithTimeout(s.T().Context(), 300*time.Millisecond)
	defer cancel()
	s.Require().NoError(f.Follow(ctx, 10*time.Millisecond, collect(&followed)))
	s.Empty(followed, "the excluded logs must stay excluded once Follow starts")
}

// TestSkipDeliversOnlyWhatHappensNext covers plain `--follow`: history
// is positioned past, not replayed.
func (s *FollowerSuite) TestSkipDeliversOnlyWhatHappensNext() {
	dir := s.T().TempDir()
	s.writeLog(dir, "inv1", `{"ts":1,"inv":"inv1","kind":"finished","status":"pass"}`+"\n")

	f := NewFollower(dir, "/repo")
	s.Require().NoError(f.Skip())

	var got []Event
	s.Require().NoError(f.drain(s.T().Context(), "inv1.jsonl", collect(&got)))
	s.Empty(got)

	s.writeLog(dir, "inv1", `{"ts":2,"inv":"inv1","kind":"result","project":"api","target":"test","status":"fail"}`+"\n")
	s.Require().NoError(f.drain(s.T().Context(), "inv1.jsonl", collect(&got)))
	s.Require().Len(got, 1)
	s.Equal(Target{Project: "api", Target: "test", Status: "failed"}, got[0].Body)
}

// TestMissingDirectoryIsNotAnError: a workspace that has never run
// anything is a normal state for an editor attaching at startup.
func (s *FollowerSuite) TestMissingDirectoryIsNotAnError() {
	f := NewFollower(filepath.Join(s.T().TempDir(), "absent"), "/repo")
	var got []Event
	s.Require().NoError(f.Replay(s.T().Context(), 0, collect(&got)))
	s.Empty(got)
}

func (s *FollowerSuite) TestFollowRefusesANonPositiveInterval() {
	f := NewFollower(s.T().TempDir(), "/repo")
	s.ErrorContains(f.Follow(s.T().Context(), 0, collect(new([]Event))), "must be positive")
}

func (s *FollowerSuite) TestFollowReturnsALogThatDoesNotRead() {
	dir := s.T().TempDir()
	f := NewFollower(dir, "/repo")
	s.Require().NoError(f.Skip())
	s.writeLog(dir, "inv1", `{"ts":1,"inv":"inv1","kind":"finished","status":"pass"}`+"\n")
	s.Require().NoError(os.Chmod(filepath.Join(dir, "inv1.jsonl"), 0))

	ctx, cancel := context.WithTimeout(s.T().Context(), 3*time.Second)
	defer cancel()
	s.ErrorIs(f.Follow(ctx, 10*time.Millisecond, collect(new([]Event))), fs.ErrPermission)
}

// TestDrainForgetsARemovedLog: rotation removes logs between a listing and
// its read.
func (s *FollowerSuite) TestDrainForgetsARemovedLog() {
	dir := s.T().TempDir()
	s.writeLog(dir, "inv1", `{"ts":1,"inv":"inv1","kind":"finished","status":"pass"}`+"\n")
	f := NewFollower(dir, "/repo")
	s.Require().NoError(f.Skip())
	s.Require().NoError(os.Remove(filepath.Join(dir, "inv1.jsonl")))

	s.Require().NoError(f.drain(s.T().Context(), "inv1.jsonl", collect(new([]Event))))
	s.NotContains(f.offsets, "inv1.jsonl")
}

// TestFollowPicksUpANewInvocation: a run started by ANOTHER process reaches
// a follower that was already attached.
func (s *FollowerSuite) TestFollowPicksUpANewInvocation() {
	dir := s.T().TempDir()
	s.Require().NoError(os.MkdirAll(dir, 0o755))

	f := NewFollower(dir, "/repo")
	s.Require().NoError(f.Skip())

	ctx, cancel := context.WithCancel(s.T().Context())
	defer cancel()

	seen := make(chan Event, 4)
	done := make(chan error, 1)
	go func() {
		done <- f.Follow(ctx, 10*time.Millisecond, func(e Event) error {
			seen <- e
			return nil
		})
	}()

	s.writeLog(dir, "inv9", `{"ts":1,"inv":"inv9","kind":"result","project":"api","target":"build","status":"fail"}`+"\n")

	select {
	case got := <-seen:
		s.Equal(Target{Project: "api", Target: "build", Status: "failed"}, got.Body)
		s.Equal("inv9", got.Inv)
	case <-time.After(3 * time.Second):
		s.Fail("a run started elsewhere never reached the follower")
	}

	cancel()
	s.Require().NoError(<-done, "cancellation is not a failure")
}
