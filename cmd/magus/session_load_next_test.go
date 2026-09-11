package main

import (
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/sessions"
	"github.com/stretchr/testify/assert"
)

// shellAt builds one loaded shell command at ms, for a synthetic stream.
func shellAt(session string, ms int64) sessions.LoadEvent {
	return sessions.LoadEvent{
		Session: session,
		Event:   sessions.AgentEvent{Kind: sessions.EventShellCommand, AtMs: ms, Program: "magus"},
	}
}

// The serving call is the newest one that had already started when the line was
// written; the call that runs the same command inside the lookahead took it up.
func TestJoinServedNextStampsServedAndFollowed(t *testing.T) {
	events := []sessions.LoadEvent{
		shellAt("s1", 1_000),
		shellAt("s1", 2_000),
		shellAt("s1", 3_000),
	}
	commands := []string{"magus query ledger", "cat notes.md", "./magus explain spell:go"}

	joinServedNext(events, commands, []hint.ServedNextEntry{
		{Ts: 1_500, ID: "query-explain", Argv: []string{"magus", "explain", "spell:go"}},
	})

	assert.Equal(t, []string{"query-explain"}, events[0].Event.NextIDs, "the query that printed it served it")
	assert.Empty(t, events[0].Event.NextFollowed)
	assert.Empty(t, events[1].Event.NextIDs)
	assert.Equal(t, []string{"query-explain"}, events[2].Event.NextFollowed,
		"a hint spelled ./magus is still followed when the reader types magus, and the other way round")
}

// Past the lookahead is a rejection, not a delayed follow: the metric has to stay
// comparable with the measurement that set the window.
func TestJoinServedNextStopsAtTheLookahead(t *testing.T) {
	events := []sessions.LoadEvent{shellAt("s1", 1_000)}
	commands := []string{"magus query ledger"}
	for i := range nextLookahead + 1 {
		events = append(events, shellAt("s1", int64(2_000+i)))
		commands = append(commands, "echo nothing")
	}
	events = append(events, shellAt("s1", 9_000))
	commands = append(commands, "magus explain spell:go")

	joinServedNext(events, commands, []hint.ServedNextEntry{
		{Ts: 1_500, ID: "query-explain", Argv: []string{"magus", "explain", "spell:go"}},
	})

	assert.Equal(t, []string{"query-explain"}, events[0].Event.NextIDs)
	assert.Empty(t, events[len(events)-1].Event.NextFollowed)
}

// A line older than every call, or younger than the window, belongs to no call: the
// journal outlives the transcript a load happens to carry.
func TestJoinServedNextLeavesUnattributableLinesAlone(t *testing.T) {
	events := []sessions.LoadEvent{shellAt("s1", 5_000)}
	commands := []string{"magus query ledger"}

	joinServedNext(events, commands, []hint.ServedNextEntry{
		{Ts: 1_000, ID: "query-explain", Argv: []string{"magus", "explain", "spell:go"}},
		{Ts: 5_000 + nextJoinWindowMs + 1, ID: "query-path", Argv: []string{"magus", "path", "a", "b"}},
	})

	assert.Empty(t, events[0].Event.NextIDs)
}

// A follow is credited only inside the session that was served: two hosts running at
// once share a journal and must not share each other's uptake.
func TestJoinServedNextStaysInsideOneSession(t *testing.T) {
	events := []sessions.LoadEvent{shellAt("s1", 1_000), shellAt("s2", 2_000)}
	commands := []string{"magus query ledger", "magus explain spell:go"}

	joinServedNext(events, commands, []hint.ServedNextEntry{
		{Ts: 1_500, ID: "query-explain", Argv: []string{"magus", "explain", "spell:go"}},
	})

	assert.Equal(t, []string{"query-explain"}, events[0].Event.NextIDs)
	assert.Empty(t, events[1].Event.NextFollowed)
}
