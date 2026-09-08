package main

import (
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

func TestReadPauseEnvelopeTakesOnlyThePointers(t *testing.T) {
	body := `{"hook_event_name":"Stop","session_id":"01a07dab","transcript_path":"/tmp/rollout.jsonl",` +
		`"last_assistant_message":"the model's closing words"}`

	env := readPauseEnvelope(strings.NewReader(body))

	assert.Equal(t, "01a07dab", env.SessionID)
	assert.Equal(t, "/tmp/rollout.jsonl", env.TranscriptPath)
	assert.Equal(t, "Stop", env.HookEventName)
}

// Anything that is not a host envelope contributes nothing, and is never an error: this
// runs on a hook path where refusing to record would cost the session, not the payload.
func TestReadPauseEnvelopeIgnoresWhatItCannotUse(t *testing.T) {
	bodies := map[string]string{
		"empty":         "",
		"plain text":    "where was I",
		"broken json":   `{"session_id":`,
		"not an object": `["session_id"]`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, hookEnvelope{}, readPauseEnvelope(strings.NewReader(body)))
		})
	}
}

// The hazard this bound exists for: a caller holding an idle pipe. Waiting on it forever
// trades two optional pointers for the record the command was invoked to write.
func TestReadPauseEnvelopeGivesUpOnAReaderThatNeverEnds(t *testing.T) {
	done := make(chan hookEnvelope, 1)
	go func() { done <- readPauseEnvelope(neverEOF{}) }()

	select {
	case env := <-done:
		assert.Equal(t, hookEnvelope{}, env)
	case <-time.After(pauseEnvelopeWait + 5*time.Second):
		t.Fatal("readPauseEnvelope blocked on a reader that never closes")
	}
}

// neverEOF blocks forever without returning data or an error, the way an inherited pipe
// with no writer does.
type neverEOF struct{}

func (neverEOF) Read([]byte) (int, error) { select {} }

func TestPauseLineDistinguishesANewPauseFromAnUnchangedOne(t *testing.T) {
	p := sessions.Pause{
		Workspace: "/repo",
		At:        types.VCSCheckpoint{Revision: "9cd51c88aef0d1e2", Branch: "handoff", Dirty: true},
		Note:      "mid-refactor",
	}

	assert.Equal(t, "paused at 9cd51c88aef0 on handoff, uncommitted changes: mid-refactor", pauseLine(p, true))
	assert.Equal(t, "unchanged since the last pause at 9cd51c88aef0 on handoff, uncommitted changes: mid-refactor", pauseLine(p, false))
}

// A tree before its first commit, or under no VCS, still has a location worth naming.
func TestPausedWhereNamesTheWorkspaceWithoutARevision(t *testing.T) {
	assert.Equal(t, "/repo", pausedWhere(sessions.Pause{Workspace: "/repo"}))
	assert.Equal(t, "codex /repo", pausedWhere(sessions.Pause{Host: "codex", Workspace: "/repo"}))
}

func TestRenderPausedBoundsTheListAndPointsAtTheRest(t *testing.T) {
	at := time.Date(2026, 9, 8, 12, 42, 45, 0, time.UTC)
	var records []sessions.PauseRecord
	for _, branch := range []string{"one", "two", "three", "four", "five"} {
		records = append(records, sessions.PauseRecord{
			Pause: sessions.Pause{Workspace: "/repo", At: types.VCSCheckpoint{Revision: "abcdef012345", Branch: branch}},
			At:    at,
		})
	}

	var b strings.Builder
	renderPaused(&b, records)
	got := b.String()

	assert.Contains(t, got, "on one")
	assert.Contains(t, got, "on three")
	assert.NotContains(t, got, "on four", "only the newest few are shown")
	assert.Contains(t, got, "and 2 more")
	assert.Contains(t, got, "2026-09-08 12:42:45")
}

func TestRenderPausedShowsTheHostPointersWhenThereAreAny(t *testing.T) {
	var b strings.Builder
	renderPaused(&b, []sessions.PauseRecord{{
		Pause: sessions.Pause{
			Host:        "codex",
			HostSession: "01a07dab-240a",
			Transcript:  "/tmp/rollout.jsonl",
			Workspace:   "/repo",
			At:          types.VCSCheckpoint{Revision: "abcdef012345", Branch: "main"},
			Note:        "usage limit",
		},
		At: time.Now(),
	}})
	got := b.String()

	assert.Contains(t, got, "session 01a07dab-240a")
	assert.Contains(t, got, "transcript /tmp/rollout.jsonl")
	assert.Contains(t, got, "usage limit")
}

// Nothing paused prints nothing at all, so the listing keeps its shape for the common
// case of a repository where every session finished what it started.
func TestRenderPausedIsSilentWithNothingPaused(t *testing.T) {
	var b strings.Builder
	renderPaused(&b, nil)
	assert.Empty(t, b.String())
}
