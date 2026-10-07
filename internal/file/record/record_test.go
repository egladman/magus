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
// previous record and a rename, about 26 syscalls, measured at 670us. This shape is a create
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
	require.NoError(t, Remove(dir), "removing what is already gone is not an error")
}

// benchOwner mirrors the shape lock.go writes on every acquire: a handful of short scalars
// describing a live process.
type benchOwner struct {
	PID     int       `record:"pid"`
	Inv     string    `record:"invocation,omitempty"`
	Command string    `record:"command"`
	Cwd     string    `record:"cwd"`
	Started time.Time `record:"started"`
}

func benchValue() benchOwner {
	return benchOwner{
		PID:     41221,
		Inv:     "inv-0123456789abcdef",
		Command: "magus run ci .",
		Cwd:     "/Users/someone/Repos/magus",
		Started: time.Now(),
	}
}

// BenchmarkWritePublish is the cost of publishing a record where none exists: what the first
// acquire of a lock path pays.
//
// The destination is removed inside the loop rather than left to accumulate: a parent directory
// that grows by one entry per iteration measures its own fan-out, not the write.
func BenchmarkWritePublish(b *testing.B) {
	dir := b.TempDir()
	target := filepath.Join(dir, "owner")
	v := benchValue()
	b.ReportAllocs()
	for b.Loop() {
		if err := Write(target, v); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		if err := os.RemoveAll(target); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
	}
}

// BenchmarkWriteReplace is the steady-state cost: a record is already there, so Write pays the
// RemoveAll of the old one on top of building the new. This is the shape lock.go actually hits,
// since a lock path is acquired over and over.
func BenchmarkWriteReplace(b *testing.B) {
	dir := b.TempDir()
	target := filepath.Join(dir, "owner")
	v := benchValue()
	if err := Write(target, v); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if err := Write(target, v); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRead is the other half of the hot path: reentrantErr reads the owner record on
// every contended acquire.
func BenchmarkRead(b *testing.B) {
	dir := b.TempDir()
	target := filepath.Join(dir, "owner")
	if err := Write(target, benchValue()); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		var got benchOwner
		if err := Read(target, &got); err != nil {
			b.Fatal(err)
		}
	}
}

// The decomposition: Write is MkdirTemp + one WriteFile per field + RemoveAll of the old record
// + Rename. These measure each piece against the same five-field shape, so the 670us of
// BenchmarkWriteReplace can be attributed rather than guessed at.

func BenchmarkPieceMkdirTemp(b *testing.B) {
	dir := b.TempDir()
	b.ReportAllocs()
	for b.Loop() {
		tmp, err := os.MkdirTemp(dir, ".record-tmp-owner-")
		if err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		_ = os.RemoveAll(tmp)
		b.StartTimer()
	}
}

func BenchmarkPieceWriteFields(b *testing.B) {
	dir := b.TempDir()
	fields, err := marshal(benchValue())
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		for k, val := range fields {
			if err := os.WriteFile(filepath.Join(dir, k), []byte(val+"\n"), 0o644); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkPieceRemoveAll(b *testing.B) {
	dir := b.TempDir()
	target := filepath.Join(dir, "owner")
	v := benchValue()
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		if err := Write(target, v); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if err := os.RemoveAll(target); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPieceRename(b *testing.B) {
	dir := b.TempDir()
	a, c := filepath.Join(dir, "a"), filepath.Join(dir, "c")
	if err := os.Mkdir(a, 0o755); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		from, to := a, c
		if i%2 == 1 {
			from, to = c, a
		}
		if err := os.Rename(from, to); err != nil {
			b.Fatal(err)
		}
	}
}
