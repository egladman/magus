package recorder

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/types/feedback"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type MarksSuite struct{ suite.Suite }

func TestMarks(t *testing.T) { suite.Run(t, new(MarksSuite)) }

func (s *MarksSuite) TestFeedbackMarksRoundTrip() {
	t := s.T()
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

	marks, err := readMarks(testCtx(t), dir)
	require.NoError(t, err)
	require.Len(t, marks, 2)
	assert.Equal(t, stored, marks[0])
	assert.Equal(t, feedback.VerdictFine, marks[1].Verdict, "a later mark is kept beside the earlier one, never over it")
}

// refused appends m to a fresh store, which must refuse it and write nothing.
func (s *MarksSuite) refused(m feedback.Mark) {
	dir := s.T().TempDir()
	_, err := appendMark(dir, m, time.Now())
	s.Error(err)
	_, err = os.Stat(filepath.Join(dir, marksFile))
	s.ErrorIs(err, os.ErrNotExist, "a refused mark writes nothing")
}

func validMark() feedback.Mark {
	return feedback.Mark{ID: "fb0123456789ab", Section: feedback.SectionRefused, Key: "raw-tool", Verdict: feedback.VerdictWrongDeny}
}

func (s *MarksSuite) TestAppendFeedbackMarkRefusesAPageLabelAsID() {
	m := validMark()
	m.ID = "C3"
	s.refused(m)
}

func (s *MarksSuite) TestAppendFeedbackMarkRefusesAnUnknownSection() {
	m := validMark()
	m.Section = "passed"
	s.refused(m)
}

func (s *MarksSuite) TestAppendFeedbackMarkRefusesAnUnknownVerdict() {
	m := validMark()
	m.Verdict = "bad"
	s.refused(m)
}

func (s *MarksSuite) TestAppendFeedbackMarkRefusesNoKey() {
	m := validMark()
	m.Key = " "
	s.refused(m)
}

func (s *MarksSuite) TestReadFeedbackMarksNamesAnUndecodableLine() {
	t := s.T()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, marksFile), []byte("{\"id\":\"fb0123456789ab\"}\nnot json\n"), 0o644))
	_, err := readMarks(testCtx(t), dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), marksFile+":2")
}

func (s *MarksSuite) TestReadFeedbackMarksOfAnEmptyStore() {
	t := s.T()
	marks, err := readMarks(testCtx(t), t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, marks)
}
