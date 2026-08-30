package record

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type owner struct {
	PID     int       `record:"pid"`
	Command string    `record:"command"`
	Started time.Time `record:"started"`
	Inv     string    `record:"invocation,omitempty"`
	Skipped string    // untagged: never persisted
}

// The contract with whoever is debugging at 2am: one cat shows the whole record, as
// name-then-value lines. This is the whole reason records exist rather than a json.Marshal.
//
// It used to be one FILE per field, which cost a mkdir, a create per field, a RemoveAll of the
// previous record and a rename - about 26 syscalls, measured at 670us. This shape is a create
// and a rename. The per-field cat became a whole-record cat, which is the better one to have
// when something is stuck: every field at once rather than five reads to assemble them.
func TestARecordIsOneCattableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock.owner")
	require.NoError(t, Write(path, owner{PID: 41221, Command: "magus run ci .", Started: time.Now()}))

	b, err := os.ReadFile(path)
	require.NoError(t, err)
	got := string(b)
	assert.Contains(t, got, "pid\t41221\n")
	assert.Contains(t, got, "command\tmagus run ci .\n")
	// Sorted, so an unchanged record rewrites to identical bytes rather than to a new
	// permutation that reads as a change to anyone diffing or watching it.
	assert.Less(t, strings.Index(got, "command\t"), strings.Index(got, "pid\t"))
}

// A value holding a newline or a tab is what would otherwise be read back as a different field
// or a truncated one. A command line is one of these fields and nothing stops an argument
// containing either.
func TestValuesSurviveNewlinesAndTabs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rec")
	want := owner{PID: 1, Command: "magus run x\t-flag\nsecond line", Started: time.Now()}
	require.NoError(t, Write(path, want))

	var got owner
	require.NoError(t, Read(path, &got))
	assert.Equal(t, want.Command, got.Command)
}

func TestRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rec")
	started := time.Now().Truncate(time.Second)
	want := owner{PID: 7, Command: "magus lint .", Started: started, Inv: "inv-1"}
	require.NoError(t, Write(dir, want))

	var got owner
	require.NoError(t, Read(dir, &got))
	assert.Equal(t, want.PID, got.PID)
	assert.Equal(t, want.Command, got.Command)
	assert.Equal(t, want.Inv, got.Inv)
	assert.True(t, want.Started.Equal(got.Started), "started: want %s got %s", want.Started, got.Started)
}

// An untagged field never reaches disk, so renaming a Go field cannot silently
// rename a file other processes are reading.
func TestUntaggedFieldsAreNotPersisted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rec")
	require.NoError(t, Write(path, owner{PID: 1, Skipped: "secret"}))
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "secret")
	assert.NotContains(t, string(b), "kipped")
}

func TestOmitEmptyLeavesTheLineOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rec")
	require.NoError(t, Write(path, owner{PID: 1}))
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "invocation")

	var got owner
	require.NoError(t, Read(path, &got), "an absent omitempty field is not a missing record")
	assert.Empty(t, got.Inv)
}

// The distinction this turns on: absent means "no record" and a caller may reap it;
// a read ERROR means "leave it alone". Conflating them deletes a live peer's state.
func TestAbsentIsNotFoundButUnreadableIsAnError(t *testing.T) {
	assert.ErrorIs(t, Read(filepath.Join(t.TempDir(), "nope"), &owner{}), ErrNotFound)

	path := filepath.Join(t.TempDir(), "rec")
	require.NoError(t, Write(path, owner{PID: 1, Started: time.Now()}))
	require.NoError(t, os.WriteFile(path, []byte("command\tx\n"), 0o644))
	assert.ErrorIs(t, Read(path, &owner{}), ErrNotFound,
		"a record missing a required field is incomplete, not corrupt")

	require.NoError(t, os.WriteFile(path, []byte("pid\tnot-a-number\ncommand\tx\nstarted\t2026-01-01T00:00:00Z\n"), 0o644))
	err := Read(path, &owner{})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound)
}

// Write replaces rather than merges: a stale field from a previous holder would
// otherwise read as belonging to the current one.
func TestWriteReplacesTheWholeRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rec")
	require.NoError(t, Write(path, owner{PID: 1, Inv: "old", Started: time.Now()}))
	require.NoError(t, Write(path, owner{PID: 2, Started: time.Now()}))

	var got owner
	require.NoError(t, Read(path, &got))
	assert.Equal(t, 2, got.PID)
	assert.Empty(t, got.Inv)
}

func TestWriteLeavesNoTemporaryBehind(t *testing.T) {
	parent := t.TempDir()
	require.NoError(t, Write(filepath.Join(parent, "rec"), owner{PID: 1}))

	entries, err := os.ReadDir(parent)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "rec", entries[0].Name())
}

// An unsupported field type is an error, never a silent skip.
func TestUnsupportedFieldTypeIsRejected(t *testing.T) {
	type bad struct {
		Ratio float64 `record:"ratio"`
	}
	assert.Error(t, Write(filepath.Join(t.TempDir(), "rec"), bad{Ratio: 1.5}))
}

func TestReadRejectsANonPointer(t *testing.T) {
	assert.Error(t, Read(t.TempDir(), owner{}))
	assert.Error(t, Read(t.TempDir(), (*owner)(nil)))
}

func TestRemove(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rec")
	require.NoError(t, Write(dir, owner{PID: 1}))
	require.NoError(t, Remove(dir))
	assert.NoDirExists(t, dir)
	assert.NoError(t, Remove(dir), "removing what is already gone is not an error")
}
