package sessions

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// contact is one event to lay down, flattened so a case reads as a table.
type contact struct {
	session string
	kind    string
	path    string
	atMs    int64
	denied  bool
}

// contactStore lays down one store holding the given events, and returns its directory.
//
// Each event gets a distinct Ref: the store dedups on (host, session, kind, ref), so
// events sharing one would collapse and a case about counting would be testing the dedup.
func contactStore(t *testing.T, events ...contact) string {
	t.Helper()
	dir := t.TempDir()
	loaded := make([]LoadEvent, 0, len(events))
	for i, e := range events {
		loaded = append(loaded, LoadEvent{Session: e.session, Event: AgentEvent{
			Host: "test-host", Kind: e.kind, Ref: strconv.Itoa(i),
			AtMs: e.atMs, Text: e.path, Denied: e.denied,
		}})
	}
	_, err := LoadEvents(dir, loaded, SessionStart{Workspace: t.TempDir(), Command: "test"})
	require.NoError(t, err)
	return dir
}

func TestContactForCountsSessionsNotEvents(t *testing.T) {
	t.Parallel()

	now := time.Now().UnixMilli()
	dir := contactStore(t,
		contact{session: "one", kind: EventFileWrite, path: "internal/guard/buzz.go", atMs: now - 3000},
		contact{session: "one", kind: EventFileWrite, path: "internal/guard/buzz.go", atMs: now - 2000},
		contact{session: "two", kind: EventFileRead, path: "internal/guard/buzz.go", atMs: now - 1000},
		contact{session: "two", kind: EventFileWrite, path: "somewhere/else.go", atMs: now},
	)

	assert.Equal(t,
		PathContact{Sessions: 2, Writes: 2, Reads: 1, Last: time.UnixMilli(now - 1000)},
		ContactFor(dir, "internal/guard/buzz.go"),
		"one session saving twice is one session, and Last is the newest contact with THIS path")
}

// TestContactForIgnoresPathlessKinds pins that only file events count. A shell command's
// text is never stored, so anything else matching a path is not contact with the file.
func TestContactForIgnoresPathlessKinds(t *testing.T) {
	t.Parallel()

	dir := contactStore(t,
		contact{session: "one", kind: EventShellCommand, path: "magusfile.buzz", atMs: 5},
		contact{session: "one", kind: EventSpawn, path: "magusfile.buzz", atMs: 6},
		contact{session: "one", kind: EventSkillLoad, path: "magusfile.buzz", atMs: 7},
	)

	assert.Equal(t, PathContact{}, ContactFor(dir, "magusfile.buzz"))
}

func TestContactForRecordsWhatTheHostRefused(t *testing.T) {
	t.Parallel()

	dir := contactStore(t,
		contact{session: "one", kind: EventFileWrite, path: "gen/graph.json", atMs: 10, denied: true},
		contact{session: "one", kind: EventFileWrite, path: "gen/graph.json", atMs: 11},
	)

	assert.Equal(t,
		PathContact{Sessions: 1, Writes: 2, Denials: 1, Last: time.UnixMilli(11)},
		ContactFor(dir, "gen/graph.json"))
}

// TestContactForLeavesAnUndatedContactUndated pins that a host that recorded no time does
// not get one invented. Taken literally a zero is 1970, which is After every real time, so
// one undated event would date the whole contact to the epoch and render as a confident
// "20000 days ago" rather than the missing answer it is.
func TestContactForLeavesAnUndatedContactUndated(t *testing.T) {
	t.Parallel()

	dir := contactStore(t,
		contact{session: "one", kind: EventFileWrite, path: "a.go", atMs: 0},
		contact{session: "one", kind: EventFileRead, path: "a.go", atMs: 0},
	)

	assert.Equal(t, PathContact{Sessions: 1, Writes: 1, Reads: 1}, ContactFor(dir, "a.go"))
}

func TestContactForSaysNothingWhenThereIsNothingToSay(t *testing.T) {
	t.Parallel()

	dir := contactStore(t, contact{session: "one", kind: EventFileWrite, path: "a.go", atMs: 1})

	assert.Equal(t, PathContact{}, ContactFor(dir, "b.go"), "a path no session reached")
	assert.Equal(t, PathContact{}, ContactFor(dir, ""), "a node that is about no file")
	assert.Equal(t, PathContact{}, ContactFor("", "a.go"), "no store")
	assert.Equal(t, PathContact{}, ContactFor(t.TempDir(), "a.go"), "an empty store")
}
