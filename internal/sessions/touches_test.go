package sessions

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/diff"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type TouchesSuite struct{ suite.Suite }

func TestTouches(t *testing.T) { suite.Run(t, new(TouchesSuite)) }

// record writes one observation the way the guard hook does.
func record(t *testing.T, base string, c recorder.AgentCommand) {
	t.Helper()
	recorder.AppendAgentCommand(context.Background(), base, c)
}

// replayed is replayTrail over a store whose every blob decodes.
func replayed(t *testing.T, root, base string, paths []string, limit int) map[string][]diff.Touch {
	t.Helper()
	got, err := replayTrail(t.Context(), root, base, paths, limit)
	require.NoError(t, err)
	return got
}

// recordUndecodable writes an agent command whose request blob is not JSON.
func recordUndecodable(t *testing.T, base string) string {
	t.Helper()
	request := recorder.WritePayload(t.Context(), base, "agent", []byte("not json"))
	recorder.AppendCall(t.Context(), base, journal.Call{Time: time.UnixMilli(1), Kind: journal.CallKindAgentCommand, Origin: job.Origin{Session: "s1"}, Lease: "l1", Name: recorder.ToolShellCommand, Request: request})
	return request.Ref
}

// A blob that does not decode is named, and the touches the rest give are still returned.
func (s *TouchesSuite) TestReplayReturnsAnUndecodableBlobBesideTheTouches() {
	t := s.T()
	base := t.TempDir()
	record(t, base, recorder.AgentCommand{Session: "s1", Host: "claude-code", Tool: recorder.ToolFileWrite, Path: "magus.go"})
	ref := recordUndecodable(t, base)

	got, err := replayTrail(t.Context(), "", base, []string{"magus.go"}, 100)

	var undecodable *recorder.BlobUndecodableError
	require.ErrorAs(t, err, &undecodable)
	assert.Equal(t, ref, undecodable.Ref)
	assert.Len(t, got["magus.go"], 1)
}

func (s *TouchesSuite) TestReplayThreadsReadsIntoTheWriteThatFollowed() {
	t := s.T()
	base := t.TempDir()
	record(t, base, recorder.AgentCommand{Session: "s1", Host: "claude-code", Transcript: "/tmp/s1.jsonl", Tool: recorder.ToolFileRead, Path: "internal/cache/output.go"})
	record(t, base, recorder.AgentCommand{Session: "s1", Host: "claude-code", Tool: recorder.ToolFileRead, Path: "types/impact.go"})
	record(t, base, recorder.AgentCommand{Session: "s1", Host: "claude-code", Tool: recorder.ToolShellCommand, Command: "go test ./internal/cache/"})
	record(t, base, recorder.AgentCommand{Session: "s1", Host: "claude-code", Transcript: "/tmp/s1.jsonl", Tool: recorder.ToolFileWrite, Path: "magus.go"})

	got := replayed(t, "", base, []string{"magus.go"}, 100)
	touches := got["magus.go"]
	require.Len(t, touches, 1)

	// Read is most recent first: this is the "what was it looking at" ordering. Ran is the
	// PROGRAM, not the command line; see diff.Touch.Ran. The argument list is dropped at
	// ingest, so this asserts the reduction rather than the recorded text.
	assert.Equal(t, diff.Touch{
		Host:       "claude-code",
		Session:    "s1",
		Transcript: "/tmp/s1.jsonl",
		Read:       []string{"types/impact.go", "internal/cache/output.go"},
		Ran:        []string{"go"},
	}, touches[0])
}

// A recorded command must never carry its arguments into the review payload, because that
// payload is served to every MCP client. This is a REGRESSION test for an observed leak: an
// op=state response carried a live server bearer token, in exactly the shape below, because
// the trail stored the command line verbatim.
func (s *TouchesSuite) TestReplayKeepsCredentialsOutOfTheTrail() {
	t := s.T()
	const token = "mgs_liveTokenThatMustNotEscape" // a fixture, not a credential
	base := t.TempDir()
	record(t, base, recorder.AgentCommand{Session: "s1", Tool: recorder.ToolShellCommand, Command: `T=` + token + ` curl -H "Authorization: Bearer $T" http://127.0.0.1:7391/mcp`})
	record(t, base, recorder.AgentCommand{Session: "s1", Tool: recorder.ToolShellCommand, Command: "/usr/local/bin/psql --password=hunter2"})
	record(t, base, recorder.AgentCommand{Session: "s1", Tool: recorder.ToolFileWrite, Path: "magus.go"})

	touches := replayed(t, "", base, []string{"magus.go"}, 100)["magus.go"]
	require.Len(t, touches, 1)

	// Most recent first, and each reduced to its program: the leading VAR=value assignment is
	// skipped rather than reported as the program, since it is where the secret sat.
	assert.Equal(t, []string{"psql", "curl"}, touches[0].Ran)
	for _, ran := range touches[0].Ran {
		assert.NotContains(t, ran, token)
		assert.NotContains(t, ran, "hunter2")
		assert.NotContains(t, ran, "Authorization")
	}
}

// The whole point of walking oldest-first: a path reached AFTER the write must not
// retroactively become its explanation.
func (s *TouchesSuite) TestReplayIgnoresReadsThatCameAfterTheWrite() {
	t := s.T()
	base := t.TempDir()
	record(t, base, recorder.AgentCommand{Session: "s1", Tool: recorder.ToolFileRead, Path: "before.go"})
	record(t, base, recorder.AgentCommand{Session: "s1", Tool: recorder.ToolFileWrite, Path: "target.go"})
	record(t, base, recorder.AgentCommand{Session: "s1", Tool: recorder.ToolFileRead, Path: "after.go"})

	touches := replayed(t, "", base, []string{"target.go"}, 100)["target.go"]
	require.Len(t, touches, 1)
	assert.Equal(t, []string{"before.go"}, touches[0].Read)
	assert.NotContains(t, touches[0].Read, "after.go")
}

// An agent almost always reads a file before editing it; reporting that as context crowds out
// the paths that actually explain the edit.
func (s *TouchesSuite) TestReplayDropsTheWrittenFileFromItsOwnReadList() {
	t := s.T()
	base := t.TempDir()
	record(t, base, recorder.AgentCommand{Session: "s1", Tool: recorder.ToolFileRead, Path: "target.go"})
	record(t, base, recorder.AgentCommand{Session: "s1", Tool: recorder.ToolFileRead, Path: "context.go"})
	record(t, base, recorder.AgentCommand{Session: "s1", Tool: recorder.ToolFileWrite, Path: "target.go"})

	touches := replayed(t, "", base, []string{"target.go"}, 100)["target.go"]
	require.Len(t, touches, 1)
	assert.Equal(t, []string{"context.go"}, touches[0].Read)
}

// A session that edits a file repeatedly is ONE story, not eleven.
func (s *TouchesSuite) TestReplayKeepsOneTouchPerSession() {
	t := s.T()
	base := t.TempDir()
	record(t, base, recorder.AgentCommand{Session: "s1", Tool: recorder.ToolFileRead, Path: "first.go"})
	record(t, base, recorder.AgentCommand{Session: "s1", Tool: recorder.ToolFileWrite, Path: "target.go"})
	record(t, base, recorder.AgentCommand{Session: "s1", Tool: recorder.ToolFileRead, Path: "second.go"})
	record(t, base, recorder.AgentCommand{Session: "s1", Tool: recorder.ToolFileWrite, Path: "target.go"})

	touches := replayed(t, "", base, []string{"target.go"}, 100)["target.go"]
	require.Len(t, touches, 1, "one session is one story")
	// The LAST write wins, so its read list is the one current at that moment.
	assert.Contains(t, touches[0].Read, "second.go")
}

func (s *TouchesSuite) TestReplaySeparatesSessions() {
	t := s.T()
	base := t.TempDir()
	record(t, base, recorder.AgentCommand{Session: "s1", Host: "claude-code", Tool: recorder.ToolFileRead, Path: "a.go"})
	record(t, base, recorder.AgentCommand{Session: "s1", Host: "claude-code", Tool: recorder.ToolFileWrite, Path: "target.go"})
	record(t, base, recorder.AgentCommand{Session: "s2", Host: "codex", Tool: recorder.ToolFileRead, Path: "b.go"})
	record(t, base, recorder.AgentCommand{Session: "s2", Host: "codex", Tool: recorder.ToolFileWrite, Path: "target.go"})

	touches := replayed(t, "", base, []string{"target.go"}, 100)["target.go"]
	require.Len(t, touches, 2)
	hosts := []string{touches[0].Host, touches[1].Host}
	assert.ElementsMatch(t, []string{"claude-code", "codex"}, hosts)
	// One session's reads must not leak into another's story.
	for _, tch := range touches {
		if tch.Host == "claude-code" {
			assert.Equal(t, []string{"a.go"}, tch.Read)
		} else {
			assert.Equal(t, []string{"b.go"}, tch.Read)
		}
	}
}

// A host with no session id produces attributable events that cannot be threaded into a story.
// Collapsing them all into one fictional session would invent a narrative.
func (s *TouchesSuite) TestReplaySkipsEventsWithNoSession() {
	t := s.T()
	base := t.TempDir()
	record(t, base, recorder.AgentCommand{Tool: recorder.ToolFileRead, Path: "a.go"})
	record(t, base, recorder.AgentCommand{Tool: recorder.ToolFileWrite, Path: "target.go"})
	assert.Empty(t, replayed(t, "", base, []string{"target.go"}, 100))
}

func (s *TouchesSuite) TestReplayOnlyReportsRequestedPaths() {
	t := s.T()
	base := t.TempDir()
	record(t, base, recorder.AgentCommand{Session: "s1", Tool: recorder.ToolFileWrite, Path: "wanted.go"})
	record(t, base, recorder.AgentCommand{Session: "s1", Tool: recorder.ToolFileWrite, Path: "unwanted.go"})

	got := replayed(t, "", base, []string{"wanted.go"}, 100)
	assert.Len(t, got, 1)
	assert.Contains(t, got, "wanted.go")
}

// A repeated read must not fill the whole window with one path.
func (s *TouchesSuite) TestReplayDeduplicatesRepeatedReads() {
	t := s.T()
	base := t.TempDir()
	for range 5 {
		record(t, base, recorder.AgentCommand{Session: "s1", Tool: recorder.ToolFileRead, Path: "same.go"})
	}
	record(t, base, recorder.AgentCommand{Session: "s1", Tool: recorder.ToolFileRead, Path: "other.go"})
	record(t, base, recorder.AgentCommand{Session: "s1", Tool: recorder.ToolFileWrite, Path: "target.go"})

	touches := replayed(t, "", base, []string{"target.go"}, 100)["target.go"]
	require.Len(t, touches, 1)
	assert.Equal(t, []string{"other.go", "same.go"}, touches[0].Read)
}

// A workspace whose agents have no guard hook wired is the normal case, and a review has to
// open anyway.
func (s *TouchesSuite) TestReplayOnAnEmptyTrailIsEmptyNotAnError() {
	t := s.T()
	assert.Empty(t, replayed(t, "", t.TempDir(), []string{"anything.go"}, 100))
}

// replayLoaded answers the same question from a loaded transcript: the reads and programs
// that preceded a write, ordered by the host's clock rather than by arrival.
func (s *TouchesSuite) TestReplayLoadedThreadsReadsIntoTheWriteThatFollowed() {
	t := s.T()
	dir := t.TempDir()
	_, err := session.LoadEvents(t.Context(), dir, []session.LoadEvent{
		// Handed out of order: the host clock is what sequences them.
		{Session: "s1", Event: session.AgentEvent{Host: "claude-code", Kind: session.EventFileWrite, Ref: "r3", AtMs: 300, Text: "magus.go", Transcript: "/tmp/s1.jsonl"}},
		{Session: "s1", Event: session.AgentEvent{Host: "claude-code", Kind: session.EventFileRead, Ref: "r1", AtMs: 100, Text: "types/impact.go"}},
		{Session: "s1", Event: session.AgentEvent{Host: "claude-code", Kind: session.EventShellCommand, Ref: "r2", AtMs: 200, Program: "go"}},
		{Session: "s1", Event: session.AgentEvent{Host: "claude-code", Kind: session.EventFileRead, Ref: "r4", AtMs: 400, Text: "late.go"}},
	}, session.InvocationStart{})
	require.NoError(t, err)
	fold, err := session.ReadAll(t.Context(), dir)
	require.NoError(t, err)

	touches := replayLoaded(fold, []string{"magus.go"})["magus.go"]
	require.Len(t, touches, 1)
	// A read after the write does not explain it, so late.go is absent from Read.
	assert.Equal(t, diff.Touch{
		Host:       "claude-code",
		Session:    "s1",
		Transcript: "/tmp/s1.jsonl",
		Read:       []string{"types/impact.go"},
		Ran:        []string{"go"},
	}, touches[0])
}

// AttachTouches merges both stores onto the review: the trail wins for a session both hold,
// and the loaded transcripts add the sessions no hook observed.
func (s *TouchesSuite) TestAttachTouchesMergesTheTrailAndTheLoadedStore() {
	t := s.T()
	testkit.Isolate(t)
	root, base := t.TempDir(), t.TempDir()
	record(t, base, recorder.AgentCommand{Session: "s1", Host: "claude-code", Tool: recorder.ToolFileRead, Path: "from-trail.go"})
	record(t, base, recorder.AgentCommand{Session: "s1", Host: "claude-code", Tool: recorder.ToolFileWrite, Path: "magus.go"})

	dir, err := Dir(root)
	require.NoError(t, err)
	_, err = session.LoadEvents(t.Context(), dir, []session.LoadEvent{
		{Session: "s1", Event: session.AgentEvent{Host: "claude-code", Kind: session.EventFileRead, Ref: "r1", AtMs: 100, Text: "from-load.go"}},
		{Session: "s1", Event: session.AgentEvent{Host: "claude-code", Kind: session.EventFileWrite, Ref: "r2", AtMs: 200, Text: "magus.go"}},
		{Session: "s2", Event: session.AgentEvent{Host: "codex", Kind: session.EventFileWrite, Ref: "r3", AtMs: 300, Text: "magus.go"}},
	}, session.InvocationStart{})
	require.NoError(t, err)

	rev := diff.Diff{Files: []diff.File{{Path: "magus.go"}, {Path: "untouched.go"}}}
	require.NoError(t, AttachTouches(t.Context(), &rev, root, base))

	got := rev.Files[0].Touches
	require.Len(t, got, 2, "one entry per session, whichever store saw it")
	// The trail's account wins for a session both stores hold.
	assert.Equal(t, diff.Touch{Host: "claude-code", Session: "s1", Read: []string{"from-trail.go"}}, got[0])
	assert.Equal(t, diff.Touch{Host: "codex", Session: "s2", Read: []string{}}, got[1])
	assert.Nil(t, rev.Files[1].Touches, "a file nobody wrote keeps no touches")
}

func (s *TouchesSuite) TestAttachTouchesLeavesTheReviewAloneWithNeitherStore() {
	t := s.T()
	testkit.Isolate(t)
	rev := diff.Diff{Files: []diff.File{{Path: "magus.go"}}}
	require.NoError(t, AttachTouches(t.Context(), &rev, t.TempDir(), t.TempDir()))
	assert.Nil(t, rev.Files[0].Touches)
}

// ForSession is the join a transcript reader needs: what the guard saw this host session do
// in this checkout, under which lease, and whom it handed work to.
func (s *TouchesSuite) TestForSessionFoldsOneHostSessionsTrail() {
	t := s.T()
	base := t.TempDir()
	record(t, base, recorder.AgentCommand{Session: "s1", Tool: recorder.ToolShellCommand, Command: "ls", Decision: verdict.Defer, Lease: "fleet/w1"})
	record(t, base, recorder.AgentCommand{Session: "s1", Tool: recorder.ToolShellCommand, Command: "git commit -m x", Decision: verdict.Deny, Lease: "fleet/w1"})
	record(t, base, recorder.AgentCommand{Session: "other", Tool: recorder.ToolShellCommand, Command: "rm -rf", Decision: verdict.Deny})
	recorder.AppendAgentSpawn(context.Background(), base, recorder.AgentSpawn{Session: "s1", Child: "Explore", Context: "lease: fleet/w1-child\nlook around\n"})

	got, err := readActivity(t.Context(), base, "s1", 100)
	require.NoError(t, err)
	require.Len(t, got.Spawns, 1)
	require.Len(t, got.Calls, 2)
	assert.Equal(t, Activity{
		Commands: 2,
		Denied:   1,
		Leases:   []string{"fleet/w1", "fleet/w1-child"},
		Spawns:   []activitySpawn{{Child: "Explore", Lease: "fleet/w1-child", At: got.Spawns[0].At}}, // At is a wall-clock stamp
		Calls: []activityCall{
			{At: got.Calls[0].At, Decision: verdict.Defer, Action: recorder.ToolShellCommand, AgentFacts: types.AgentFacts{Thread: types.AgentThreadUnknown}},
			{At: got.Calls[1].At, Decision: verdict.Deny, Action: recorder.ToolShellCommand, AgentFacts: types.AgentFacts{Thread: types.AgentThreadUnknown}},
		},
	}, got)

	none, err := readActivity(t.Context(), base, "", 100)
	require.NoError(t, err)
	assert.Zero(t, none.Commands, "no session id joins nothing")
	absent, err := readActivity(t.Context(), t.TempDir(), "s1", 100)
	require.NoError(t, err)
	assert.Zero(t, absent.Commands, "an absent store is empty, not an error")
}

// The post-call tool-time row is the same call as the pre-call row before it.
func (s *TouchesSuite) TestForSessionCountsACallOnceWithItsToolTime() {
	t := s.T()
	base := t.TempDir()
	record(t, base, recorder.AgentCommand{Session: "s1", Tool: recorder.ToolShellCommand, Command: "ls", Decision: verdict.Defer})
	record(t, base, recorder.AgentCommand{Session: "s1", Tool: "Bash", ToolMs: 40})

	got, err := readActivity(t.Context(), base, "s1", 100)
	require.NoError(t, err)
	assert.Equal(t, 1, got.Commands)
	assert.Len(t, got.Calls, 2, "both rows still feed the cost and repeat folds")
}

// A response blob written before the defer rename holds "pass", and the read refuses it
// with the store to clear rather than count the call as undecided.
func (s *TouchesSuite) TestForSessionRefusesARetiredDecision() {
	t := s.T()
	base := t.TempDir()
	response := recorder.WritePayload(t.Context(), base, "agent", []byte(`{"api_version":"magus.agent-command/v1alpha1","decision":"pass"}`))
	ref := response.Ref
	recorder.AppendCall(t.Context(), base, journal.Call{Time: time.UnixMilli(1), Kind: journal.CallKindAgentCommand, Origin: job.Origin{Session: "s1"}, Name: recorder.ToolShellCommand, Response: response})

	_, err := readActivity(t.Context(), base, "s1", 100)
	var retired *recorder.RetiredDecisionError
	require.ErrorAs(t, err, &retired)
	assert.Equal(t, recorder.RetiredDecisionError{Store: filepath.Join(base, "activity"), Ref: ref, Decision: "pass"}, *retired, "the store to clear is the activity directory")
}

func (s *TouchesSuite) TestForSessionReturnsEachCallsCostFacts() {
	t := s.T()
	base := t.TempDir()
	shadow := map[types.GuardProfile]verdict.Decision{types.GuardProfileStandard: verdict.Advise}
	advised := types.AgentFacts{
		Thread: types.AgentThreadMain, ThreadFrom: types.ThreadReported, ContextTokens: 300_000, ContextModel: "claude-opus-5-5",
		Billing: types.AgentBillingMetered, Shadow: shadow,
	}
	chars := 5000
	passed := types.AgentFacts{Thread: types.AgentThreadMain, DelegableClass: types.DelegableSearch, ResultChars: &chars}
	record(t, base, recorder.AgentCommand{
		Session: "s1", Tool: recorder.ToolShellCommand, Command: "grep -r x .", Decision: verdict.Advise, Rule: "root-heavy-command", AgentFacts: advised,
	})
	record(t, base, recorder.AgentCommand{Session: "s1", Tool: recorder.ToolShellCommand, Command: "grep -r x .", Decision: verdict.Defer, AgentFacts: passed})
	record(t, base, recorder.AgentCommand{Session: "other", Tool: recorder.ToolShellCommand, Command: "ls", AgentFacts: types.AgentFacts{ContextTokens: 1}})
	recorder.AppendAgentSpawn(t.Context(), base, recorder.AgentSpawn{Session: "s1", Child: "Explore", Context: "look", AgentFacts: types.AgentFacts{ContextTokens: 77_000}})

	got, err := readActivity(t.Context(), base, "s1", 100)
	require.NoError(t, err)
	require.Len(t, got.Calls, 2, "another session's calls are not this one's")
	assert.Equal(t, activityCall{At: got.Calls[0].At, Decision: verdict.Advise, Rule: "root-heavy-command", Action: recorder.ToolShellCommand, AgentFacts: advised}, got.Calls[0])
	assert.Equal(t, activityCall{At: got.Calls[1].At, Decision: verdict.Defer, Action: recorder.ToolShellCommand, AgentFacts: passed}, got.Calls[1])
	require.Len(t, got.Spawns, 1)
	assert.Equal(t, activitySpawn{Child: "Explore", At: got.Spawns[0].At}, got.Spawns[0], "a spawn's cost facts are the caller's, not the child's")
}

type RepeatsSuite struct {
	suite.Suite
	start time.Time
}

func TestRepeatsSuite(t *testing.T) { suite.Run(t, new(RepeatsSuite)) }

func (s *RepeatsSuite) SetupTest() { s.start = time.UnixMilli(1_700_000_000_000) }

// poll is a tool-time row n seconds in.
func (s *RepeatsSuite) poll(n int, worker, call, result string) activityCall {
	return activityCall{
		At: s.start.Add(time.Duration(n) * time.Second), Action: "Bash", Worker: worker, ToolMs: 100,
		AgentFacts: types.AgentFacts{CallDigest: call, ResultDigest: result},
	}
}

func (s *RepeatsSuite) write(n int, worker string) activityCall {
	return activityCall{At: s.start.Add(time.Duration(n) * time.Second), Action: recorder.ToolFileWrite, Worker: worker}
}

func (s *RepeatsSuite) TestOneCallWithOneAnswerFormsARunWithItsTurnsAndToolTime() {
	got := repeats([]activityCall{s.poll(0, "", "c", "r"), s.poll(5, "", "c", "r"), s.poll(9, "", "c", "r")})
	s.Equal([]RepeatRun{{
		Tool: "Bash", CallDigest: "c", ResultDigest: "r", Count: 3,
		First: s.start, Last: s.start.Add(9 * time.Second), ToolMs: 300, SpanMs: 9000,
	}}, got)
}

func (s *RepeatsSuite) TestAChangedAnswerStartsANewRun() {
	got := repeats([]activityCall{s.poll(0, "", "c", "r1"), s.poll(1, "", "c", "r1"), s.poll(2, "", "c", "r2")})
	s.Equal([]RepeatRun{{
		Tool: "Bash", CallDigest: "c", ResultDigest: "r1", Count: 2,
		First: s.start, Last: s.start.Add(time.Second), ToolMs: 200, SpanMs: 1000,
	}}, got)
}

func (s *RepeatsSuite) TestAWriteByTheSameWorkerEndsTheRun() {
	got := repeats([]activityCall{s.poll(0, "w", "c", "r"), s.write(1, "w"), s.poll(2, "w", "c", "r")})
	s.Empty(got)
}

func (s *RepeatsSuite) TestAnotherWorkersWriteLeavesTheRunAlone() {
	got := repeats([]activityCall{s.poll(0, "w1", "c", "r"), s.write(1, "w2"), s.poll(2, "w1", "c", "r")})
	s.Require().Len(got, 1)
	s.Equal("w1", got[0].Worker)
}

func (s *RepeatsSuite) TestTwoWorkersRunningTheSameCallAreNotARepeat() {
	got := repeats([]activityCall{s.poll(0, "w1", "c", "r"), s.poll(1, "w2", "c", "r")})
	s.Empty(got)
}

func (s *RepeatsSuite) TestAnUnobservedDigestJoinsNoRun() {
	noResult := s.poll(0, "", "c", "")
	noCall := s.poll(1, "", "", "r")
	s.Empty(repeats([]activityCall{noResult, noResult, noCall, noCall}))
}

func (s *RepeatsSuite) TestRowsWithoutToolTimeAreNotCounted() {
	pre := s.poll(0, "", "c", "r")
	pre.ToolMs = 0
	s.Empty(repeats([]activityCall{pre, pre}))
}

func (s *RepeatsSuite) TestForSessionCarriesWorkerDigestsAndTheUnobservedNote() {
	base := s.T().TempDir()
	facts := types.AgentFacts{CallDigest: "c", ResultDigest: "r"}
	record(s.T(), base, recorder.AgentCommand{Session: "s1", Agent: "w1", Tool: "Bash", ToolMs: 40, AgentFacts: facts})
	record(s.T(), base, recorder.AgentCommand{Session: "s1", Agent: "w1", Tool: "Bash", ToolMs: 60, AgentFacts: facts})
	record(s.T(), base, recorder.AgentCommand{Session: "s1", Agent: "w1", Tool: "Bash", ToolMs: 5,
		AgentFacts: types.AgentFacts{CallDigest: "c", Unobserved: []string{"result"}}})

	got, err := readActivity(s.T().Context(), base, "s1", 100)
	s.Require().NoError(err)
	s.Require().Len(got.Calls, 3)
	s.Equal("w1", got.Calls[0].Worker)
	s.Equal([]string{"result"}, got.Calls[2].Unobserved)
	s.Require().Len(got.Repeats, 1)
	run := got.Repeats[0]
	run.First, run.Last, run.SpanMs = time.Time{}, time.Time{}, 0
	s.Equal(RepeatRun{Worker: "w1", Thread: "unknown", Tool: "Bash", CallDigest: "c", ResultDigest: "r", Count: 2, ToolMs: 100}, run)
}
