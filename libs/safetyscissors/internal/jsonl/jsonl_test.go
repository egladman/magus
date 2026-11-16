package jsonl

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type mark struct {
	Key   string `json:"key"`
	Value int    `json:"value,omitzero"`
	Gone  bool   `json:"gone,omitzero"`
}

func marks(path string) Store[mark] {
	return Store[mark]{Path: path, Key: func(m mark) string { return m.Key }, Live: func(m mark) bool { return !m.Gone },
		Old: filepath.Join(filepath.Dir(path), "marks.json")}
}

func set(add ...mark) func([]mark) []mark { return func([]mark) []mark { return add } }

type LinesSuite struct{ suite.Suite }

func TestLines(t *testing.T) { suite.Run(t, new(LinesSuite)) }

func (s *LinesSuite) TestReadLinesKeepsLineNumbersAndFlagsAnUnfinishedLine() {
	path := filepath.Join(s.T().TempDir(), "x.jsonl")
	got, err := ReadLines(path)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), Lines{}, got, "an absent file has no lines")

	require.NoError(s.T(), os.WriteFile(path, []byte("{\"a\":1}\n\n{\"b\":2}\n{\"c\""), 0o644))
	got, err = ReadLines(path)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), Lines{
		Lines:   []Line{{N: 1, Text: []byte(`{"a":1}`)}, {N: 3, Text: []byte(`{"b":2}`)}},
		Partial: true,
	}, got)
}

func (s *LinesSuite) TestAppendAndRewriteLines() {
	path := filepath.Join(s.T().TempDir(), "dir", "x.jsonl")
	require.NoError(s.T(), AppendLines(path, [][]byte{[]byte("one"), []byte("two")}, 0o644))
	require.NoError(s.T(), AppendLines(path, [][]byte{[]byte("three")}, 0o644, WithoutSync()))
	raw, err := os.ReadFile(path)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), "one\ntwo\nthree\n", string(raw))

	require.NoError(s.T(), RewriteLines(path, [][]byte{[]byte("only")}, 0o600))
	raw, err = os.ReadFile(path)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), "only\n", string(raw))
	info, err := os.Stat(path)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), os.FileMode(0o600), info.Mode().Perm())
}

type StoreSuite struct{ suite.Suite }

func TestStore(t *testing.T) { suite.Run(t, new(StoreSuite)) }

// The last line for a key is its entry, a line Live refuses ends the key, and the entries
// keep the order their last lines appear in.
func (s *StoreSuite) TestTakesTheLastLinePerKey() {
	l := marks(filepath.Join(s.T().TempDir(), "marks.jsonl"))
	require.NoError(s.T(), l.Update(s.T().Context(), set(mark{Key: "a", Value: 1}, mark{Key: "b", Value: 1}, mark{Key: "c", Value: 1})))
	require.NoError(s.T(), l.Update(s.T().Context(), set(mark{Key: "a", Value: 2}, mark{Key: "b", Gone: true})))
	got, err := l.Load()
	require.NoError(s.T(), err)
	assert.Equal(s.T(), []mark{{Key: "c", Value: 1}, {Key: "a", Value: 2}}, got)

	raw, err := os.ReadFile(l.Path)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), 5, strings.Count(string(raw), "\n"), "both updates appended")
}

// An update with nothing to add writes nothing, so it creates no file.
func (s *StoreSuite) TestUpdateAddingNothingWritesNothing() {
	l := marks(filepath.Join(s.T().TempDir(), "marks.jsonl"))
	require.NoError(s.T(), l.Update(s.T().Context(), set()))
	assert.NoFileExists(s.T(), l.Path)
}

// The append that makes a file CompactDue rewrites it to its entries.
func (s *StoreSuite) TestCompactsWhenDue() {
	l := marks(filepath.Join(s.T().TempDir(), "marks.jsonl"))
	for i := range 2 + CompactSlack {
		require.NoError(s.T(), l.Update(s.T().Context(), set(mark{Key: "a", Value: i})))
	}
	read, err := ReadLines(l.Path)
	require.NoError(s.T(), err)
	assert.Len(s.T(), read.Lines, 2+CompactSlack, "not due yet")

	require.NoError(s.T(), l.Update(s.T().Context(), set(mark{Key: "a", Value: -1})))
	raw, err := os.ReadFile(l.Path)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), `{"key":"a","value":-1}`+"\n", string(raw))
}

// A line no writer finished is left unread, and the next update rewrites the file without
// it rather than gluing its own line onto it, telling the log it was given.
func (s *StoreSuite) TestRewritesPastAnUnfinishedLine() {
	var logged bytes.Buffer
	l := marks(filepath.Join(s.T().TempDir(), "marks.jsonl"))
	l.Log = slog.New(slog.NewTextHandler(&logged, nil))
	require.NoError(s.T(), os.WriteFile(l.Path, []byte(`{"key":"a","value":1}`+"\n"+`{"key":"b"`), 0o644))
	got, err := l.Load()
	require.NoError(s.T(), err)
	assert.Equal(s.T(), []mark{{Key: "a", Value: 1}}, got)

	require.NoError(s.T(), l.Update(s.T().Context(), set(mark{Key: "c", Value: 1})))
	raw, err := os.ReadFile(l.Path)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), `{"key":"a","value":1}`+"\n"+`{"key":"c","value":1}`+"\n", string(raw))
	assert.Contains(s.T(), logged.String(), "store rewritten")
}

// A line that does not decode names the file and its line.
func (s *StoreSuite) TestNamesTheLineThatDoesNotDecode() {
	l := marks(filepath.Join(s.T().TempDir(), "marks.jsonl"))
	require.NoError(s.T(), os.WriteFile(l.Path, []byte(`{"key":"a"}`+"\n\n"+`{"key":`+"\n"), 0o644))
	_, err := l.Load()
	require.ErrorContains(s.T(), err, l.Path+":3: ")
	require.ErrorIs(s.T(), err, ErrLineUndecodable)
}

// While the file an older writer kept the store in exists, neither a read nor a write goes
// on, and the old file is left as it was.
func (s *StoreSuite) TestRefusesWhileTheOldFileExists() {
	l := marks(filepath.Join(s.T().TempDir(), "marks.jsonl"))
	require.NoError(s.T(), os.WriteFile(l.Old, []byte(`[]`), 0o644))
	_, err := l.Load()
	require.ErrorIs(s.T(), err, ErrLegacyStore)
	require.ErrorContains(s.T(), err, "preserve it")
	require.ErrorIs(s.T(), l.Update(s.T().Context(), set(mark{Key: "a"})), ErrLegacyStore)
	raw, err := os.ReadFile(l.Old)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), "[]", string(raw))
	assert.NoFileExists(s.T(), l.Path)
}
