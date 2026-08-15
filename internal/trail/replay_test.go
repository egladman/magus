package trail

import (
	"context"
	"testing"

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

	got := Replay("", base, []string{"magus.go"}, 100)
	touches := got["magus.go"]
	require.Len(t, touches, 1)

	assert.Equal(t, "claude-code", touches[0].Host)
	assert.Equal(t, "/tmp/s1.jsonl", touches[0].Transcript)
	// Most recent read first: this is the "what was it looking at" ordering.
	assert.Equal(t, []string{"types/impact.go", "internal/cache/output.go"}, touches[0].Read)
	assert.Equal(t, []string{"go test ./internal/cache/"}, touches[0].Ran)
}

// The whole point of walking oldest-first: a path reached AFTER the write must not
// retroactively become its explanation.
func TestReplayIgnoresReadsThatCameAfterTheWrite(t *testing.T) {
	base := t.TempDir()
	record(t, base, AgentCommand{Session: "s1", Tool: toolRead, Path: "before.go"})
	record(t, base, AgentCommand{Session: "s1", Tool: toolWrite, Path: "target.go"})
	record(t, base, AgentCommand{Session: "s1", Tool: toolRead, Path: "after.go"})

	touches := Replay("", base, []string{"target.go"}, 100)["target.go"]
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

	touches := Replay("", base, []string{"target.go"}, 100)["target.go"]
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

	touches := Replay("", base, []string{"target.go"}, 100)["target.go"]
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

	touches := Replay("", base, []string{"target.go"}, 100)["target.go"]
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
	assert.Empty(t, Replay("", base, []string{"target.go"}, 100))
}

func TestReplayOnlyReportsRequestedPaths(t *testing.T) {
	base := t.TempDir()
	record(t, base, AgentCommand{Session: "s1", Tool: toolWrite, Path: "wanted.go"})
	record(t, base, AgentCommand{Session: "s1", Tool: toolWrite, Path: "unwanted.go"})

	got := Replay("", base, []string{"wanted.go"}, 100)
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

	touches := Replay("", base, []string{"target.go"}, 100)["target.go"]
	require.Len(t, touches, 1)
	assert.Equal(t, []string{"other.go", "same.go"}, touches[0].Read)
}

// A workspace whose agents have no guard hook wired is the normal case, and a review has to
// open anyway.
func TestReplayOnAnEmptyTrailIsEmptyNotAnError(t *testing.T) {
	assert.Empty(t, Replay("", t.TempDir(), []string{"anything.go"}, 100))
}
