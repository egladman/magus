package trail

import (
	"context"
	"testing"
	"time"

	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// record writes one observation the way the guard hook does.
func record(t *testing.T, base string, c AgentCommand) {
	t.Helper()
	AppendAgentCommand(context.Background(), base, c)
}

func TestReplayThreadsReadsIntoTheWriteThatFollowed(t *testing.T) {
	base := t.TempDir()
	record(t, base, AgentCommand{Session: "s1", Host: "claude-code", Transcript: "/tmp/s1.jsonl", Tool: toolRead, Path: "internal/cache/output.go"})
	record(t, base, AgentCommand{Session: "s1", Host: "claude-code", Tool: toolRead, Path: "types/impact.go"})
	record(t, base, AgentCommand{Session: "s1", Host: "claude-code", Tool: toolShell, Command: "go test ./internal/cache/"})
	record(t, base, AgentCommand{Session: "s1", Host: "claude-code", Transcript: "/tmp/s1.jsonl", Tool: toolWrite, Path: "magus.go"})

	got := replayTrail("", base, []string{"magus.go"}, 100)
	touches := got["magus.go"]
	require.Len(t, touches, 1)

	assert.Equal(t, "claude-code", touches[0].Host)
	assert.Equal(t, "/tmp/s1.jsonl", touches[0].Transcript)
	// Most recent read first: this is the "what was it looking at" ordering.
	assert.Equal(t, []string{"types/impact.go", "internal/cache/output.go"}, touches[0].Read)
	// The PROGRAM, not the command line; see types.DiffTouch.Ran. The argument list is dropped at
	// ingest, so this asserts the reduction rather than the recorded text.
	assert.Equal(t, []string{"go"}, touches[0].Ran)
}

// A recorded command must never carry its arguments into the review payload, because that
// payload is served to every MCP client. This is a REGRESSION test for an observed leak: an
// op=state response carried a live daemon bearer token, in exactly the shape below, because
// the trail stored the command line verbatim.
func TestReplayKeepsCredentialsOutOfTheTrail(t *testing.T) {
	const token = "mgs_liveTokenThatMustNotEscape" // a fixture, not a credential
	base := t.TempDir()
	record(t, base, AgentCommand{Session: "s1", Tool: toolShell, Command: `T=` + token + ` curl -H "Authorization: Bearer $T" http://127.0.0.1:7391/mcp`})
	record(t, base, AgentCommand{Session: "s1", Tool: toolShell, Command: "/usr/local/bin/psql --password=hunter2"})
	record(t, base, AgentCommand{Session: "s1", Tool: toolWrite, Path: "magus.go"})

	touches := replayTrail("", base, []string{"magus.go"}, 100)["magus.go"]
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

func TestCommandProgramReducesToTheProgram(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"go test ./internal/cache/", "go"},
		{"./magus run build .", "magus"},
		{"/usr/local/bin/psql --password=hunter2", "psql"},
		{`SECRET=abc TOKEN=def curl -H "Authorization: Bearer $TOKEN"`, "curl"},
		{`sh -c "curl -H 'Authorization: Bearer x'"`, "sh"},
		// A path that merely contains "=" is a program, not an assignment.
		{"/opt/we=ird/tool --flag", "tool"},
		// Nothing parseable reduces to empty and is dropped rather than guessed at.
		{"", ""},
		{"ONLY=assignments HERE=too", ""},
	} {
		assert.Equal(t, tc.want, CommandProgram(tc.in), "input %q", tc.in)
	}
}

// The whole point of walking oldest-first: a path reached AFTER the write must not
// retroactively become its explanation.
func TestReplayIgnoresReadsThatCameAfterTheWrite(t *testing.T) {
	base := t.TempDir()
	record(t, base, AgentCommand{Session: "s1", Tool: toolRead, Path: "before.go"})
	record(t, base, AgentCommand{Session: "s1", Tool: toolWrite, Path: "target.go"})
	record(t, base, AgentCommand{Session: "s1", Tool: toolRead, Path: "after.go"})

	touches := replayTrail("", base, []string{"target.go"}, 100)["target.go"]
	require.Len(t, touches, 1)
	assert.Equal(t, []string{"before.go"}, touches[0].Read)
	assert.NotContains(t, touches[0].Read, "after.go")
}

// An agent almost always reads a file before editing it; reporting that as context crowds out
// the paths that actually explain the edit.
func TestReplayDropsTheWrittenFileFromItsOwnReadList(t *testing.T) {
	base := t.TempDir()
	record(t, base, AgentCommand{Session: "s1", Tool: toolRead, Path: "target.go"})
	record(t, base, AgentCommand{Session: "s1", Tool: toolRead, Path: "context.go"})
	record(t, base, AgentCommand{Session: "s1", Tool: toolWrite, Path: "target.go"})

	touches := replayTrail("", base, []string{"target.go"}, 100)["target.go"]
	require.Len(t, touches, 1)
	assert.Equal(t, []string{"context.go"}, touches[0].Read)
}

// A session that edits a file repeatedly is ONE story, not eleven.
func TestReplayKeepsOneTouchPerSession(t *testing.T) {
	base := t.TempDir()
	record(t, base, AgentCommand{Session: "s1", Tool: toolRead, Path: "first.go"})
	record(t, base, AgentCommand{Session: "s1", Tool: toolWrite, Path: "target.go"})
	record(t, base, AgentCommand{Session: "s1", Tool: toolRead, Path: "second.go"})
	record(t, base, AgentCommand{Session: "s1", Tool: toolWrite, Path: "target.go"})

	touches := replayTrail("", base, []string{"target.go"}, 100)["target.go"]
	require.Len(t, touches, 1, "one session is one story")
	// The LAST write wins, so its read list is the one current at that moment.
	assert.Contains(t, touches[0].Read, "second.go")
}

func TestReplaySeparatesSessions(t *testing.T) {
	base := t.TempDir()
	record(t, base, AgentCommand{Session: "s1", Host: "claude-code", Tool: toolRead, Path: "a.go"})
	record(t, base, AgentCommand{Session: "s1", Host: "claude-code", Tool: toolWrite, Path: "target.go"})
	record(t, base, AgentCommand{Session: "s2", Host: "codex", Tool: toolRead, Path: "b.go"})
	record(t, base, AgentCommand{Session: "s2", Host: "codex", Tool: toolWrite, Path: "target.go"})

	touches := replayTrail("", base, []string{"target.go"}, 100)["target.go"]
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
func TestReplaySkipsEventsWithNoSession(t *testing.T) {
	base := t.TempDir()
	record(t, base, AgentCommand{Tool: toolRead, Path: "a.go"})
	record(t, base, AgentCommand{Tool: toolWrite, Path: "target.go"})
	assert.Empty(t, replayTrail("", base, []string{"target.go"}, 100))
}

func TestReplayOnlyReportsRequestedPaths(t *testing.T) {
	base := t.TempDir()
	record(t, base, AgentCommand{Session: "s1", Tool: toolWrite, Path: "wanted.go"})
	record(t, base, AgentCommand{Session: "s1", Tool: toolWrite, Path: "unwanted.go"})

	got := replayTrail("", base, []string{"wanted.go"}, 100)
	assert.Len(t, got, 1)
	assert.Contains(t, got, "wanted.go")
}

// A repeated read must not fill the whole window with one path.
func TestReplayDeduplicatesRepeatedReads(t *testing.T) {
	base := t.TempDir()
	for range 5 {
		record(t, base, AgentCommand{Session: "s1", Tool: toolRead, Path: "same.go"})
	}
	record(t, base, AgentCommand{Session: "s1", Tool: toolRead, Path: "other.go"})
	record(t, base, AgentCommand{Session: "s1", Tool: toolWrite, Path: "target.go"})

	touches := replayTrail("", base, []string{"target.go"}, 100)["target.go"]
	require.Len(t, touches, 1)
	assert.Equal(t, []string{"other.go", "same.go"}, touches[0].Read)
}

// A workspace whose agents have no guard hook wired is the normal case, and a review has to
// open anyway.
func TestReplayOnAnEmptyTrailIsEmptyNotAnError(t *testing.T) {
	assert.Empty(t, replayTrail("", t.TempDir(), []string{"anything.go"}, 100))
}

// replayLoaded answers the same question from a loaded transcript: the reads and programs
// that preceded a write, ordered by the host's clock rather than by arrival.
func TestReplayLoadedThreadsReadsIntoTheWriteThatFollowed(t *testing.T) {
	dir := t.TempDir()
	_, err := sessions.LoadEvents(dir, []sessions.LoadEvent{
		// Handed out of order: the host clock is what sequences them.
		{Session: "s1", Event: sessions.AgentEvent{Host: "claude-code", Kind: sessions.EventFileWrite, Ref: "r3", AtMs: 300, Text: "magus.go", Transcript: "/tmp/s1.jsonl"}},
		{Session: "s1", Event: sessions.AgentEvent{Host: "claude-code", Kind: sessions.EventFileRead, Ref: "r1", AtMs: 100, Text: "types/impact.go"}},
		{Session: "s1", Event: sessions.AgentEvent{Host: "claude-code", Kind: sessions.EventShellCommand, Ref: "r2", AtMs: 200, Program: "go"}},
		{Session: "s1", Event: sessions.AgentEvent{Host: "claude-code", Kind: sessions.EventFileRead, Ref: "r4", AtMs: 400, Text: "late.go"}},
	}, sessions.SessionStart{})
	require.NoError(t, err)
	fold, err := sessions.ReadAll(dir)
	require.NoError(t, err)

	touches := replayLoaded(fold, []string{"magus.go"})["magus.go"]
	require.Len(t, touches, 1)
	assert.Equal(t, "claude-code", touches[0].Host)
	assert.Equal(t, "/tmp/s1.jsonl", touches[0].Transcript)
	assert.Equal(t, []string{"types/impact.go"}, touches[0].Read, "a read after the write does not explain it")
	assert.Equal(t, []string{"go"}, touches[0].Ran)
}

// AttachTouches merges both stores onto the review: the trail wins for a session both hold,
// and the loaded transcripts add the sessions no hook observed.
func TestAttachTouchesMergesTheTrailAndTheLoadedStore(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root, base := t.TempDir(), t.TempDir()
	record(t, base, AgentCommand{Session: "s1", Host: "claude-code", Tool: toolRead, Path: "from-trail.go"})
	record(t, base, AgentCommand{Session: "s1", Host: "claude-code", Tool: toolWrite, Path: "magus.go"})

	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	_, err = sessions.LoadEvents(dir, []sessions.LoadEvent{
		{Session: "s1", Event: sessions.AgentEvent{Host: "claude-code", Kind: sessions.EventFileRead, Ref: "r1", AtMs: 100, Text: "from-load.go"}},
		{Session: "s1", Event: sessions.AgentEvent{Host: "claude-code", Kind: sessions.EventFileWrite, Ref: "r2", AtMs: 200, Text: "magus.go"}},
		{Session: "s2", Event: sessions.AgentEvent{Host: "codex", Kind: sessions.EventFileWrite, Ref: "r3", AtMs: 300, Text: "magus.go"}},
	}, sessions.SessionStart{})
	require.NoError(t, err)

	rev := types.Diff{Files: []types.DiffFile{{Path: "magus.go"}, {Path: "untouched.go"}}}
	AttachTouches(&rev, root, base)

	got := rev.Files[0].Touches
	require.Len(t, got, 2, "one entry per session, whichever store saw it")
	assert.Equal(t, "s1", got[0].Session)
	assert.Equal(t, []string{"from-trail.go"}, got[0].Read, "the trail's account wins for a session both stores hold")
	assert.Equal(t, "s2", got[1].Session)
	assert.Equal(t, "codex", got[1].Host)
	assert.Nil(t, rev.Files[1].Touches, "a file nobody wrote keeps no touches")
}

func TestAttachTouchesLeavesTheReviewAloneWithNeitherStore(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	rev := types.Diff{Files: []types.DiffFile{{Path: "magus.go"}}}
	AttachTouches(&rev, t.TempDir(), t.TempDir())
	assert.Nil(t, rev.Files[0].Touches)
}

// ForSession is the join a transcript reader needs: what the guard saw this host session do
// in this checkout, under which lease, and whom it handed work to.
func TestForSessionFoldsOneHostSessionsTrail(t *testing.T) {
	base := t.TempDir()
	record(t, base, AgentCommand{Session: "s1", Tool: toolShell, Command: "ls", Decision: "pass", Lease: "fleet/w1"})
	record(t, base, AgentCommand{Session: "s1", Tool: toolShell, Command: "git commit -m x", Decision: "deny", Lease: "fleet/w1"})
	record(t, base, AgentCommand{Session: "other", Tool: toolShell, Command: "rm -rf", Decision: "deny"})
	AppendAgentSpawn(context.Background(), base, AgentSpawn{Session: "s1", Child: "Explore", Context: "lease: fleet/w1-child\nlook around\n"})

	got := ForSession(base, "s1", 100)
	assert.Equal(t, 2, got.Commands)
	assert.Equal(t, 1, got.Denied)
	assert.Equal(t, []string{"fleet/w1", "fleet/w1-child"}, got.Leases)
	require.Len(t, got.Spawns, 1)
	assert.Equal(t, "Explore", got.Spawns[0].Child)
	assert.Equal(t, "fleet/w1-child", got.Spawns[0].Lease)

	assert.Zero(t, ForSession(base, "", 100).Commands, "no session id joins nothing")
	assert.Zero(t, ForSession(t.TempDir(), "s1", 100).Commands, "an absent trail is empty, not an error")
}

func TestLastAgentActivityReadsTheNewestObservation(t *testing.T) {
	base := t.TempDir()
	_, ok := LastAgentActivity(base, 100)
	assert.False(t, ok, "an empty trail has no activity to report")

	record(t, base, AgentCommand{Session: "s1", Host: "opencode", Tool: toolShell, Command: "ls"})
	record(t, base, AgentCommand{Session: "s2", Host: "opencode", Tool: toolRead, Path: "magus.go"})

	got, ok := LastAgentActivity(base, 100)
	require.True(t, ok)
	assert.Equal(t, "s2", got.Session)
	assert.Equal(t, "opencode", got.Host)
	assert.WithinDuration(t, time.Now(), got.At, time.Minute)

	_, ok = LastAgentActivity(t.TempDir(), 100)
	assert.False(t, ok, "an absent trail is no activity, not an error")
}
