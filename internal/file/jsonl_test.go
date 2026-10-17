package file

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadLinesKeepsLineNumbersAndFlagsAnUnfinishedLine(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "x.jsonl")
	got, err := ReadLines(path)
	require.NoError(t, err)
	assert.Equal(t, Lines{}, got, "an absent file has no lines")

	require.NoError(t, os.WriteFile(path, []byte("{\"a\":1}\n\n{\"b\":2}\n{\"c\""), 0o644))
	got, err = ReadLines(path)
	require.NoError(t, err)
	assert.Equal(t, Lines{
		Lines:   []Line{{N: 1, Text: []byte(`{"a":1}`)}, {N: 3, Text: []byte(`{"b":2}`)}},
		Partial: true,
	}, got)
}

func TestAppendAndRewriteLines(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dir", "x.jsonl")
	require.NoError(t, AppendLines(path, [][]byte{[]byte("one"), []byte("two")}, 0o644))
	require.NoError(t, AppendLines(path, [][]byte{[]byte("three")}, 0o644, WithoutSync()))
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "one\ntwo\nthree\n", string(raw))

	require.NoError(t, RewriteLines(path, [][]byte{[]byte("only")}, 0o644))
	raw, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "only\n", string(raw))
}

type mark struct {
	Key   string `json:"key"`
	Value int    `json:"value,omitzero"`
	Gone  bool   `json:"gone,omitzero"`
}

func marks(path string) JSONL[mark] {
	return JSONL[mark]{Path: path, Key: func(m mark) string { return m.Key }, Live: func(m mark) bool { return !m.Gone },
		Old: filepath.Join(filepath.Dir(path), "marks.json")}
}

func set(add ...mark) func([]mark) []mark { return func([]mark) []mark { return add } }

// The last line for a key is its entry, a line Live refuses ends the key, and the entries
// keep the order their last lines appear in.
func TestJSONLTakesTheLastLinePerKey(t *testing.T) {
	t.Parallel()

	l := marks(filepath.Join(t.TempDir(), "marks.jsonl"))
	require.NoError(t, l.Update(t.Context(), set(mark{Key: "a", Value: 1}, mark{Key: "b", Value: 1}, mark{Key: "c", Value: 1})))
	require.NoError(t, l.Update(t.Context(), set(mark{Key: "a", Value: 2}, mark{Key: "b", Gone: true})))
	got, err := l.Load()
	require.NoError(t, err)
	assert.Equal(t, []mark{{Key: "c", Value: 1}, {Key: "a", Value: 2}}, got)

	raw, err := os.ReadFile(l.Path)
	require.NoError(t, err)
	assert.Equal(t, 5, strings.Count(string(raw), "\n"), "both updates appended")
}

// An update with nothing to add writes nothing, so it creates no file.
func TestJSONLUpdateAddingNothingWritesNothing(t *testing.T) {
	t.Parallel()

	l := marks(filepath.Join(t.TempDir(), "marks.jsonl"))
	require.NoError(t, l.Update(t.Context(), set()))
	assert.NoFileExists(t, l.Path)
}

// The append that makes a file CompactDue rewrites it to its entries.
func TestJSONLCompactsWhenDue(t *testing.T) {
	t.Parallel()

	l := marks(filepath.Join(t.TempDir(), "marks.jsonl"))
	for i := range 2 + CompactSlack {
		require.NoError(t, l.Update(t.Context(), set(mark{Key: "a", Value: i})))
	}
	read, err := ReadLines(l.Path)
	require.NoError(t, err)
	assert.Len(t, read.Lines, 2+CompactSlack, "not due yet")

	require.NoError(t, l.Update(t.Context(), set(mark{Key: "a", Value: -1})))
	raw, err := os.ReadFile(l.Path)
	require.NoError(t, err)
	assert.Equal(t, `{"key":"a","value":-1}`+"\n", string(raw))
}

// A line no writer finished is left unread, and the next update rewrites the file without
// it rather than gluing its own line onto it.
func TestJSONLRewritesPastAnUnfinishedLine(t *testing.T) {
	t.Parallel()

	l := marks(filepath.Join(t.TempDir(), "marks.jsonl"))
	require.NoError(t, os.WriteFile(l.Path, []byte(`{"key":"a","value":1}`+"\n"+`{"key":"b"`), 0o644))
	got, err := l.Load()
	require.NoError(t, err)
	assert.Equal(t, []mark{{Key: "a", Value: 1}}, got)

	require.NoError(t, l.Update(t.Context(), set(mark{Key: "c", Value: 1})))
	raw, err := os.ReadFile(l.Path)
	require.NoError(t, err)
	assert.Equal(t, `{"key":"a","value":1}`+"\n"+`{"key":"c","value":1}`+"\n", string(raw))
}

// A line that does not decode names the file and its line.
func TestJSONLNamesTheLineThatDoesNotDecode(t *testing.T) {
	t.Parallel()

	l := marks(filepath.Join(t.TempDir(), "marks.jsonl"))
	require.NoError(t, os.WriteFile(l.Path, []byte(`{"key":"a"}`+"\n\n"+`{"key":`+"\n"), 0o644))
	_, err := l.Load()
	require.ErrorContains(t, err, l.Path+":3: ")
}

// While the file an older magus kept the store in exists, neither a read nor a write goes on,
// and the error says how to move it aside.
func TestJSONLRefusesWhileTheOldFileExists(t *testing.T) {
	t.Parallel()

	l := marks(filepath.Join(t.TempDir(), "marks.jsonl"))
	require.NoError(t, os.WriteFile(l.Old, []byte(`[]`), 0o644))
	want := "move it aside with `mv " + l.Old + " " + l.Old + ".old`"
	_, err := l.Load()
	require.ErrorContains(t, err, want)
	require.ErrorContains(t, l.Update(t.Context(), set(mark{Key: "a"})), want)
	assert.NoFileExists(t, l.Path)
}
