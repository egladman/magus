package trail

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func TestFeedbackMarksRoundTrip(t *testing.T) {
	dir := t.TempDir()
	at := time.UnixMilli(1790000000000)
	first := types.FeedbackMark{
		ID: "fb0123456789ab", Section: types.FeedbackUnguarded, Key: "grep -nr <arg>",
		Verdict: types.FeedbackShouldDeny, Note: "guard it next time", Session: "s1",
		Examples: []string{"grep -rn a b", "grep -rn c d", "grep -rn e f", "grep -rn g h"},
	}
	stored, err := AppendFeedbackMark(dir, first, at)
	require.NoError(t, err)
	assert.Equal(t, at.UnixMilli(), stored.At)
	assert.Len(t, stored.Examples, 3, "a mark keeps a few examples, not the trail")

	second := first
	second.Verdict, second.Session, second.Examples = types.FeedbackFine, "s2", nil
	_, err = AppendFeedbackMark(dir, second, at.Add(time.Minute))
	require.NoError(t, err)

	marks, err := ReadFeedbackMarks(dir)
	require.NoError(t, err)
	require.Len(t, marks, 2)
	assert.Equal(t, stored, marks[0])
	assert.Equal(t, types.FeedbackFine, marks[1].Verdict, "a later mark is kept beside the earlier one, never over it")
}

func TestAppendFeedbackMarkRefusesAMalformedMark(t *testing.T) {
	dir := t.TempDir()
	valid := types.FeedbackMark{ID: "fb0123456789ab", Section: types.FeedbackRefused, Key: "raw-tool", Verdict: types.FeedbackWrongDeny}
	for name, mutate := range map[string]func(*types.FeedbackMark){
		"page label as id": func(m *types.FeedbackMark) { m.ID = "C3" },
		"unknown section":  func(m *types.FeedbackMark) { m.Section = "passed" },
		"unknown verdict":  func(m *types.FeedbackMark) { m.Verdict = "bad" },
		"no key":           func(m *types.FeedbackMark) { m.Key = " " },
	} {
		m := valid
		mutate(&m)
		_, err := AppendFeedbackMark(dir, m, time.Now())
		assert.Error(t, err, name)
	}
	_, err := os.Stat(filepath.Join(dir, marksFile))
	assert.ErrorIs(t, err, os.ErrNotExist, "a refused mark writes nothing")
}

func TestReadFeedbackMarksNamesAnUndecodableLine(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, marksFile), []byte("{\"id\":\"fb0123456789ab\"}\nnot json\n"), 0o644))
	_, err := ReadFeedbackMarks(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), marksFile+":2")
}

func TestReadFeedbackMarksOfAnEmptyStore(t *testing.T) {
	marks, err := ReadFeedbackMarks(t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, marks)
}
