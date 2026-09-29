package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/spells"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJournalRecordForget(t *testing.T) {
	dir := t.TempDir()
	j, err := NewJournal(dir)
	require.NoError(t, err)

	j.record("abc", spells.Command{Bin: "true"})
	_, err = os.Stat(j.path("abc"))
	require.NoError(t, err, "record wrote a file")

	j.forget("abc")
	_, err = os.Stat(j.path("abc"))
	assert.True(t, os.IsNotExist(err), "forget removed the file")
}

// TestJournalRecordsAnyKey pins that a real registry key, which carries the workspace
// root and a NUL (identity.InstanceKey), is recorded: as a file name it was invalid,
// and the ignored write error left crash reaping with nothing to replay.
func TestJournalRecordsAnyKey(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("needs sh")
	}
	j, err := NewJournal(t.TempDir())
	require.NoError(t, err)
	sentinel := filepath.Join(t.TempDir(), "stopped")

	j.record("/work/space\x00abc123", spells.Command{Bin: "sh", Args: []string{"-c", "touch " + sentinel}})
	assert.Equal(t, SweepResult{Reaped: 1}, j.Sweep(context.Background()))
	assert.FileExists(t, sentinel)
}

func TestJournalSweepRunsStopCommands(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("needs sh")
	}
	dir := t.TempDir()
	j, err := NewJournal(dir)
	require.NoError(t, err)

	sentinel := filepath.Join(t.TempDir(), "stopped")
	// A record whose stop command has an observable effect, as if left by a crashed
	// broker; and one with no stop command (unreapable).
	j.record("svc1", spells.Command{Bin: "sh", Args: []string{"-c", "touch " + sentinel}})
	j.record("svc2", spells.Command{})

	res := j.Sweep(context.Background())
	assert.Equal(t, 1, res.Reaped)
	assert.Equal(t, 1, res.Unreapable)

	_, err = os.Stat(sentinel)
	assert.NoError(t, err, "the recorded stop command ran")

	files, _ := os.ReadDir(dir)
	assert.Empty(t, files, "sweep clears every record")
}

func TestJournalNilSafe(t *testing.T) {
	var j *Journal
	j.record("k", spells.Command{Bin: "true"}) // must not panic
	j.forget("k")
	assert.Equal(t, SweepResult{}, j.Sweep(context.Background()))
}

func TestRegistryRecordsAndForgetsWithJournal(t *testing.T) {
	dir := t.TempDir()
	j, err := NewJournal(dir)
	require.NoError(t, err)
	r := New(&fakeRunner{}, 0, WithJournal(j)) // idle 0: Release stops immediately

	svc := spells.Service{
		Command: spells.Command{Bin: "docker", Args: []string{"run", "postgres:15"}},
		Stop:    spells.Command{Bin: "docker", Args: []string{"stop", "pg"}},
	}
	_, err = r.Acquire(context.Background(), "pg", svc)
	require.NoError(t, err)
	_, err = os.Stat(j.path("pg"))
	require.NoError(t, err, "acquire recorded a journal entry")

	r.Release("pg")
	_, err = os.Stat(j.path("pg"))
	assert.True(t, os.IsNotExist(err), "release forgot the journal entry")
}

// TestRegistryJournalsOnlyOwnedServices pins that an adopted service leaves no journal
// record: a later broker's sweep replays every recorded stop, and stopping a service
// magus never started would take down something another tool owns.
func TestRegistryJournalsOnlyOwnedServices(t *testing.T) {
	dir := t.TempDir()
	j, err := NewJournal(dir)
	require.NoError(t, err)
	r := New(&fakeRunner{adopt: true}, time.Hour, WithJournal(j))

	svc := spells.Service{
		Start:     spells.Command{Bin: "podman", Args: []string{"machine", "start"}},
		Readiness: spells.Command{Bin: "podman", Args: []string{"info"}},
		Stop:      spells.Command{Bin: "podman", Args: []string{"machine", "stop"}},
	}
	h, err := r.Acquire(context.Background(), "machine", svc)
	require.NoError(t, err)
	require.Equal(t, Adopted, h)

	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, files, "an adopted service is not journaled")
}
