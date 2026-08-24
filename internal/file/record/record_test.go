package record

import (
	"os"
	"path/filepath"
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

// The contract with whoever is debugging at 2am: one file per field, holding the
// bare value. This is the whole reason records exist rather than a json.Marshal.
func TestWriteIsOneFilePerField(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "lock.owner")
	require.NoError(t, Write(dir, owner{PID: 41221, Command: "magus run ci .", Started: time.Now()}))

	b, err := os.ReadFile(filepath.Join(dir, "pid"))
	require.NoError(t, err)
	assert.Equal(t, "41221\n", string(b), "a bare value with a trailing newline, so cat reads cleanly")

	b, err = os.ReadFile(filepath.Join(dir, "command"))
	require.NoError(t, err)
	assert.Equal(t, "magus run ci .\n", string(b))
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
	dir := filepath.Join(t.TempDir(), "rec")
	require.NoError(t, Write(dir, owner{PID: 1, Skipped: "secret"}))
	assert.NoFileExists(t, filepath.Join(dir, "Skipped"))
	assert.NoFileExists(t, filepath.Join(dir, "skipped"))
}

func TestOmitEmptyLeavesTheFileOut(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rec")
	require.NoError(t, Write(dir, owner{PID: 1}))
	assert.NoFileExists(t, filepath.Join(dir, "invocation"))

	var got owner
	require.NoError(t, Read(dir, &got), "an absent omitempty field is not a missing record")
	assert.Empty(t, got.Inv)
}

// The distinction this turns on: absent means "no record" and a caller may reap it;
// a read ERROR means "leave it alone". Conflating them deletes a live peer's state.
func TestAbsentIsNotFoundButUnreadableIsAnError(t *testing.T) {
	assert.ErrorIs(t, Read(filepath.Join(t.TempDir(), "nope"), &owner{}), ErrNotFound)

	dir := filepath.Join(t.TempDir(), "rec")
	require.NoError(t, Write(dir, owner{PID: 1, Started: time.Now()}))
	require.NoError(t, os.Remove(filepath.Join(dir, "pid")))
	assert.ErrorIs(t, Read(dir, &owner{}), ErrNotFound,
		"a record missing a required field is mid-removal, not corrupt")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "pid"), []byte("not-a-number\n"), 0o644))
	err := Read(dir, &owner{})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound)
}

// Write replaces rather than merges: a stale field from a previous holder would
// otherwise read as belonging to the current one.
func TestWriteReplacesTheWholeRecord(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rec")
	require.NoError(t, Write(dir, owner{PID: 1, Inv: "old", Started: time.Now()}))
	require.NoError(t, Write(dir, owner{PID: 2, Started: time.Now()}))

	var got owner
	require.NoError(t, Read(dir, &got))
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
	assert.False(t, IsPartial(entries[0].Name()))
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
