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

	_, marked := s.LoadReading()
	assert.False(t, marked, "a store nobody marked reads as not reading")

	got, err := s.SetReading(ctx, at, start)
	require.NoError(t, err)
	assert.Equal(t, Reading{Review: "42", Repo: "o/r", Since: 1_000_000}, got)

	// A second process (the check-review job) sees the same mark through its own Store.
	loaded, marked := NewStore(dir).LoadReading()
	require.True(t, marked)
	assert.Equal(t, got, loaded)

	require.NoError(t, s.ClearReading(ctx))
	_, marked = NewStore(dir).LoadReading()
	assert.False(t, marked)
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

	assert.Equal(t, Reading{Review: "43", Repo: "o/r", Since: 2_000}, got)
}

func TestReadingMatchesOnlyTheReviewItWasSetAgainst(t *testing.T) {
	r := Reading{Review: "42", Repo: "o/r", Since: 1}

	assert.True(t, r.Matches(types.ReviewTarget{ID: "42", Repo: "o/r"}))
	assert.False(t, r.Matches(types.ReviewTarget{ID: "43", Repo: "o/r"}), "another review")
	assert.False(t, r.Matches(types.ReviewTarget{ID: "42", Repo: "o/other"}), "the same number on another forge repo")
	assert.False(t, Reading{}.Matches(types.ReviewTarget{}), "no mark matches a target with no review")
}

func TestReadingElapsedNeverGoesNegative(t *testing.T) {
	r := Reading{Review: "42", Since: 10_000}

	assert.Equal(t, 5*time.Second, r.Elapsed(time.UnixMilli(15_000)))
	assert.Equal(t, time.Duration(0), r.Elapsed(time.UnixMilli(1_000)), "a clock that stepped back reads as no time")
}

// With no state directory there is nowhere to record the mark, and the person must be told: a
// silent success would be a promise of a report that can never be made.
func TestReadingMarkWithoutAStateDirIsAnError(t *testing.T) {
	ctx := context.Background()
	s := NewStore("")

	_, err := s.SetReading(ctx, types.ReviewTarget{ID: "42"}, time.Now())
	require.Error(t, err)
	require.Error(t, s.ClearReading(ctx))
	_, marked := s.LoadReading()
	assert.False(t, marked)
}

func TestReadingMarkCorruptFileReadsAsNoMark(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "review"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "review", "reading.json"), []byte("{not json"), 0o644))

	_, marked := NewStore(dir).LoadReading()

	assert.False(t, marked)
}
