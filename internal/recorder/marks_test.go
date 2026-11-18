package recorder

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/types/feedback"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFeedbackMarksRoundTrip(t *testing.T) {
	dir := t.TempDir()
	at := time.UnixMilli(1790000000000)
	first := feedback.Mark{
		ID: "fb0123456789ab", Section: feedback.SectionUnguarded, Key: "grep -nr <arg>",
		Verdict: feedback.VerdictShouldDeny, Note: "guard it next time", Session: "s1",
		Examples: []string{"grep -rn a b", "grep -rn c d", "grep -rn e f", "grep -rn g h"},
	}
	stored, err := appendMark(dir, first, at)
	require.NoError(t, err)
	assert.Equal(t, at.UnixMilli(), stored.At)
	assert.Len(t, stored.Examples, 3, "a mark keeps a few examples, not the trail")

	second := first
	second.Verdict, second.Session, second.Examples = feedback.VerdictFine, "s2", nil
	_, err = appendMark(dir, second, at.Add(time.Minute))
	require.NoError(t, err)

	marks, err := readMarks(dir)
	require.NoError(t, err)
	require.Len(t, marks, 2)
	assert.Equal(t, stored, marks[0])
	assert.Equal(t, feedback.VerdictFine, marks[1].Verdict, "a later mark is kept beside the earlier one, never over it")
}

func TestAppendFeedbackMarkRefusesAMalformedMark(t *testing.T) {
	dir := t.TempDir()
	valid := feedback.Mark{ID: "fb0123456789ab", Section: feedback.SectionRefused, Key: "raw-tool", Verdict: feedback.VerdictWrongDeny}
	for name, mutate := range map[string]func(*feedback.Mark){
		"page label as id": func(m *feedback.Mark) { m.ID = "C3" },
		"unknown section":  func(m *feedback.Mark) { m.Section = "passed" },
		"unknown verdict":  func(m *feedback.Mark) { m.Verdict = "bad" },
		"no key":           func(m *feedback.Mark) { m.Key = " " },
	} {
		m := valid
		mutate(&m)
		_, err := appendMark(dir, m, time.Now())
		assert.Error(t, err, name)
	}
	_, err := os.Stat(filepath.Join(dir, marksFile))
	assert.ErrorIs(t, err, os.ErrNotExist, "a refused mark writes nothing")
}

func TestReadFeedbackMarksNamesAnUndecodableLine(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, marksFile), []byte("{\"id\":\"fb0123456789ab\"}\nnot json\n"), 0o644))
	_, err := readMarks(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), marksFile+":2")
}

func TestReadFeedbackMarksOfAnEmptyStore(t *testing.T) {
	marks, err := readMarks(t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, marks)
}
