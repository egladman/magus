package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

func TestReadCheckpointEnvelopeTakesOnlyThePointers(t *testing.T) {
	body := `{"hook_event_name":"Stop","session_id":"01a07dab","transcript_path":"/tmp/rollout.jsonl",` +
		`"last_assistant_message":"the model's closing words"}`

	env := readCheckpointEnvelope(strings.NewReader(body))

	assert.Equal(t, "01a07dab", env.SessionID)
	assert.Equal(t, "/tmp/rollout.jsonl", env.TranscriptPath)
	assert.Equal(t, "Stop", env.HookEventName)
}

// Anything that is not a host envelope contributes nothing, and is never an error: this
// runs on a hook path where refusing to record would cost the session, not the payload.
func TestReadCheckpointEnvelopeIgnoresWhatItCannotUse(t *testing.T) {
	bodies := map[string]string{
		"empty":         "",
		"plain text":    "where was I",
		"broken json":   `{"session_id":`,
		"not an object": `["session_id"]`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, hookEnvelope{}, readCheckpointEnvelope(strings.NewReader(body)))
		})
	}
}

// The hazard this bound exists for: a caller holding an idle pipe. Waiting on it forever
// trades two optional pointers for the record the command was invoked to write.
func TestReadCheckpointEnvelopeGivesUpOnAReaderThatNeverEnds(t *testing.T) {
	done := make(chan hookEnvelope, 1)
	go func() { done <- readCheckpointEnvelope(neverEOF{}) }()

	select {
	case env := <-done:
		assert.Equal(t, hookEnvelope{}, env)
	case <-time.After(checkpointEnvelopeWait + 5*time.Second):
		t.Fatal("readCheckpointEnvelope blocked on a reader that never closes")
	}
}

// neverEOF blocks forever without returning data or an error, the way an inherited pipe
// with no writer does.
type neverEOF struct{}

func (neverEOF) Read([]byte) (int, error) { select {} }

func TestCheckpointRecordedLineDistinguishesANewOneFromAnUnchangedOne(t *testing.T) {
	c := sessions.Checkpoint{
		Workspace: "/repo",
		Tree:      types.VCSCheckpoint{Revision: "9cd51c88aef0d1e2", Branch: "handoff", Dirty: true},
		Note:      "mid-refactor",
	}

	assert.Equal(t, "checkpoint at 9cd51c88aef0 on handoff, uncommitted changes: mid-refactor",
		checkpointRecordedLine(c, true))
	assert.Equal(t, "unchanged since the last checkpoint at 9cd51c88aef0 on handoff, uncommitted changes: mid-refactor",
		checkpointRecordedLine(c, false))
}

// A tree before its first commit, or under no VCS, still has a location worth naming.
func TestCheckpointWhereNamesTheWorkspaceWithoutARevision(t *testing.T) {
	assert.Equal(t, "/repo", checkpointWhere(sessions.Checkpoint{Workspace: "/repo"}))
	assert.Equal(t, "codex /repo", checkpointWhere(sessions.Checkpoint{Host: "codex", Workspace: "/repo"}))
}

func TestRenderCheckpointsBoundsTheListAndPointsAtTheRest(t *testing.T) {
	at := time.Date(2026, 9, 8, 12, 42, 45, 0, time.UTC)
	var records []sessions.CheckpointRecord
	for _, branch := range []string{"one", "two", "three", "four", "five"} {
		records = append(records, sessions.CheckpointRecord{
			Checkpoint: sessions.Checkpoint{Workspace: "/repo", Tree: types.VCSCheckpoint{Revision: "abcdef012345", Branch: branch}},
			At:         at,
		})
	}

	var b strings.Builder
	renderCheckpoints(context.Background(), "", &b, records)
	got := b.String()

	assert.Contains(t, got, "on one")
	assert.Contains(t, got, "on three")
	assert.NotContains(t, got, "on four", "only the newest few are shown")
	assert.Contains(t, got, "and 2 more")
	assert.Contains(t, got, "2026-09-08 12:42:45")
}

func TestRenderCheckpointsShowsTheHostPointersWhenThereAreAny(t *testing.T) {
	var b strings.Builder
	renderCheckpoints(context.Background(), "", &b, []sessions.CheckpointRecord{{
		Checkpoint: sessions.Checkpoint{
			Host:        "codex",
			HostSession: "01a07dab-240a",
			Transcript:  "/tmp/rollout.jsonl",
			Workspace:   "/repo",
			Tree:        types.VCSCheckpoint{Revision: "abcdef012345", Branch: "main"},
			Note:        "usage limit",
		},
		At: time.Now(),
	}})
	got := b.String()

	assert.Contains(t, got, "session 01a07dab-240a")
	assert.Contains(t, got, "transcript /tmp/rollout.jsonl")
	assert.Contains(t, got, "usage limit")
}

// Nothing recorded prints nothing at all, so the listing keeps its shape for the common
// case of a repository where every session finished what it started.
func TestRenderCheckpointsIsSilentWithNothingRecorded(t *testing.T) {
	var b strings.Builder
	renderCheckpoints(context.Background(), "", &b, nil)
	assert.Empty(t, b.String())
}
