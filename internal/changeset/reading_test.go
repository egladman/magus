package changeset

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func TestReadingMarkSetLoadClear(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := NewStore(dir)
	at := types.ReviewTarget{ID: "42", Repo: "o/r"}
	start := time.UnixMilli(1_000_000)

	assert.False(t, s.LoadReading().Active(), "a store nobody marked reads as not reading")

	got, err := s.SetReading(ctx, at, start)
	require.NoError(t, err)
	assert.Equal(t, ReadingMark{Review: "42", Repo: "o/r", Since: 1_000_000}, got)

	// A second process (the check-review job) sees the same mark through its own Store.
	assert.Equal(t, got, NewStore(dir).LoadReading())

	require.NoError(t, s.ClearReading(ctx))
	assert.False(t, NewStore(dir).LoadReading().Active())
	require.NoError(t, s.ClearReading(ctx), "clearing an unmarked review is not a mistake")
}

// Pressing the key again must not move the start, or a merge would be measured against the
// second press and the time lost under-reported.
func TestReadingMarkKeepsItsStartForTheSameReview(t *testing.T) {
	ctx := context.Background()
	s := NewStore(t.TempDir())
	at := types.ReviewTarget{ID: "42", Repo: "o/r"}

	_, err := s.SetReading(ctx, at, time.UnixMilli(1_000))
	require.NoError(t, err)
	got, err := s.SetReading(ctx, at, time.UnixMilli(9_000))
	require.NoError(t, err)

	assert.Equal(t, int64(1_000), got.Since)
}

func TestReadingMarkForAnotherReviewReplacesIt(t *testing.T) {
	ctx := context.Background()
	s := NewStore(t.TempDir())

	_, err := s.SetReading(ctx, types.ReviewTarget{ID: "42", Repo: "o/r"}, time.UnixMilli(1_000))
	require.NoError(t, err)
	got, err := s.SetReading(ctx, types.ReviewTarget{ID: "43", Repo: "o/r"}, time.UnixMilli(2_000))
	require.NoError(t, err)

	assert.Equal(t, ReadingMark{Review: "43", Repo: "o/r", Since: 2_000}, got)
}

// A mark past its TTL is a forgotten tab. Marking the same review again starts a new reading
// rather than inheriting a start the reader no longer stands behind.
func TestReadingMarkPastItsTTLStartsOverForTheSameReview(t *testing.T) {
	ctx := context.Background()
	s := NewStore(t.TempDir())
	at := types.ReviewTarget{ID: "42", Repo: "o/r"}
	start := time.UnixMilli(1_000)

	_, err := s.SetReading(ctx, at, start)
	require.NoError(t, err)
	got, err := s.SetReading(ctx, at, start.Add(ReadingTTL+time.Second))
	require.NoError(t, err)

	assert.Equal(t, start.Add(ReadingTTL+time.Second).UnixMilli(), got.Since)
}

func TestReadingMarkExpiresAfterItsTTL(t *testing.T) {
	m := ReadingMark{Review: "42", Since: 1_000}
	start := time.UnixMilli(1_000)

	assert.False(t, m.Expired(start.Add(ReadingTTL)), "exactly the TTL still counts")
	assert.True(t, m.Expired(start.Add(ReadingTTL+time.Millisecond)))
	assert.False(t, m.Expired(start.Add(-time.Hour)), "a clock that stepped back does not expire a mark")
}

func TestReadingMatchesOnlyTheReviewItWasSetAgainst(t *testing.T) {
	m := ReadingMark{Review: "42", Repo: "o/r", Since: 1}

	assert.True(t, m.Matches(types.ReviewTarget{ID: "42", Repo: "o/r"}))
	assert.False(t, m.Matches(types.ReviewTarget{ID: "43", Repo: "o/r"}), "another review")
	assert.False(t, m.Matches(types.ReviewTarget{ID: "42", Repo: "o/other"}), "the same number on another forge repo")
	assert.False(t, ReadingMark{}.Matches(types.ReviewTarget{}), "no mark matches a target with no review")
}

func TestReadingElapsedNeverGoesNegative(t *testing.T) {
	m := ReadingMark{Review: "42", Since: 10_000}

	assert.Equal(t, 5*time.Second, m.Elapsed(time.UnixMilli(15_000)))
	assert.Equal(t, time.Duration(0), m.Elapsed(time.UnixMilli(1_000)), "a clock that stepped back reads as no time")
}

// The job reads the mark, asks the forge, then clears. A mark the reader set on another review in
// between is theirs, not the job's to delete.
func TestClearReadingIfLeavesAMarkThatChangedSinceItWasRead(t *testing.T) {
	ctx := context.Background()
	s := NewStore(t.TempDir())
	read, err := s.SetReading(ctx, types.ReviewTarget{ID: "42", Repo: "o/r"}, time.UnixMilli(1_000))
	require.NoError(t, err)
	replaced, err := s.SetReading(ctx, types.ReviewTarget{ID: "43", Repo: "o/r"}, time.UnixMilli(2_000))
	require.NoError(t, err)

	cleared, err := s.ClearReadingIf(ctx, read)

	require.NoError(t, err)
	assert.False(t, cleared)
	assert.Equal(t, replaced, s.LoadReading(), "the newer mark survives")
}

func TestClearReadingIfClearsTheMarkItWasGiven(t *testing.T) {
	ctx := context.Background()
	s := NewStore(t.TempDir())
	read, err := s.SetReading(ctx, types.ReviewTarget{ID: "42", Repo: "o/r"}, time.UnixMilli(1_000))
	require.NoError(t, err)

	cleared, err := s.ClearReadingIf(ctx, read)
	require.NoError(t, err)
	assert.True(t, cleared)
	assert.False(t, s.LoadReading().Active())

	cleared, err = s.ClearReadingIf(ctx, read)
	require.NoError(t, err)
	assert.False(t, cleared, "a second clear finds nothing, so it reports nothing cleared")
}

// With no state directory there is nowhere to record the mark, and the person must be told: a
// silent success would be a promise of a report that can never be made.
func TestReadingMarkWithoutAStateDirIsAnError(t *testing.T) {
	ctx := context.Background()
	s := NewStore("")

	_, err := s.SetReading(ctx, types.ReviewTarget{ID: "42"}, time.Now())
	require.ErrorIs(t, err, ErrNoStateDir)
	require.ErrorIs(t, s.ClearReading(ctx), ErrNoStateDir)
	assert.False(t, s.LoadReading().Active())
}

func TestReadingMarkCorruptFileReadsAsNoMark(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "review"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "review", "reading.json"), []byte("{not json"), 0o644))

	assert.False(t, NewStore(dir).LoadReading().Active())
}
