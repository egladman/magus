package codec

import (
	"bytes"
	"context"
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

func marks(t *testing.T, path string, opts ...JSONLOption[mark]) JSONL[mark] {
	return marksPerm(t, path, 0o644, opts...)
}

func marksPerm(t *testing.T, path string, perm os.FileMode, opts ...JSONLOption[mark]) JSONL[mark] {
	t.Helper()
	l, err := NewJSONL(JSONLConfig[mark]{Path: path, Perm: perm, Key: markKey, Live: func(m mark) bool { return !m.Gone },
		Old: oldMarks(path)}, opts...)
	require.NoError(t, err)
	return l
}

func markKey(m mark) string { return m.Key }

func oldMarks(path string) string { return filepath.Join(filepath.Dir(path), "marks.json") }

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
		Lines:       []Line{{N: 1, Text: []byte(`{"a":1}`)}, {N: 3, Text: []byte(`{"b":2}`)}},
		Partial:     true,
		PartialLine: 4,
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
	l := marks(s.T(), filepath.Join(s.T().TempDir(), "marks.jsonl"))
	require.NoError(s.T(), l.Update(s.T().Context(), set(mark{Key: "a", Value: 1}, mark{Key: "b", Value: 1}, mark{Key: "c", Value: 1})))
	require.NoError(s.T(), l.Update(s.T().Context(), set(mark{Key: "a", Value: 2}, mark{Key: "b", Gone: true})))
	got, err := l.Load()
	require.NoError(s.T(), err)
	assert.Equal(s.T(), []mark{{Key: "c", Value: 1}, {Key: "a", Value: 2}}, got)

	raw, err := os.ReadFile(l.Path())
	require.NoError(s.T(), err)
	assert.Equal(s.T(), 5, strings.Count(string(raw), "\n"), "both updates appended")
}

// An update with nothing to add writes nothing, so it creates no file.
func (s *StoreSuite) TestUpdateAddingNothingWritesNothing() {
	l := marks(s.T(), filepath.Join(s.T().TempDir(), "marks.jsonl"))
	require.NoError(s.T(), l.Update(s.T().Context(), set()))
	assert.NoFileExists(s.T(), l.Path())
}

// The append that makes a file CompactDue rewrites it to its entries.
func (s *StoreSuite) TestCompactsWhenDue() {
	l := marks(s.T(), filepath.Join(s.T().TempDir(), "marks.jsonl"))
	for i := range 2 + CompactSlack {
		require.NoError(s.T(), l.Update(s.T().Context(), set(mark{Key: "a", Value: i})))
	}
	read, err := ReadLines(l.Path())
	require.NoError(s.T(), err)
	assert.Len(s.T(), read.Lines, 2+CompactSlack, "not due yet")

	require.NoError(s.T(), l.Update(s.T().Context(), set(mark{Key: "a", Value: -1})))
	raw, err := os.ReadFile(l.Path())
	require.NoError(s.T(), err)
	assert.Equal(s.T(), `{"key":"a","value":-1}`+"\n", string(raw))
}

// A line no writer finished is left unread, and the next update rewrites the file without
// it rather than gluing its own line onto it, telling the log it was given.
func (s *StoreSuite) TestRewritesPastAnUnfinishedLine() {
	var logged bytes.Buffer
	l := marks(s.T(), filepath.Join(s.T().TempDir(), "marks.jsonl"), WithLog[mark](slog.New(slog.NewTextHandler(&logged, nil))))
	require.NoError(s.T(), os.WriteFile(l.Path(), []byte(`{"key":"a","value":1}`+"\n"+`{"key":"b"`), 0o644))
	got, err := l.Load()
	require.NoError(s.T(), err)
	assert.Equal(s.T(), []mark{{Key: "a", Value: 1}}, got)

	require.NoError(s.T(), l.Update(s.T().Context(), set(mark{Key: "c", Value: 1})))
	raw, err := os.ReadFile(l.Path())
	require.NoError(s.T(), err)
	assert.Equal(s.T(), `{"key":"a","value":1}`+"\n"+`{"key":"c","value":1}`+"\n", string(raw))
	assert.Contains(s.T(), logged.String(), "store rewritten")
}

// OnDrop alone is told of the line a rewrite drops, by path and line number.
func (s *StoreSuite) TestOnDropIsToldOfTheDroppedLine() {
	var dropped []Dropped
	l := marks(s.T(), filepath.Join(s.T().TempDir(), "marks.jsonl"),
		WithOnDrop[mark](func(_ context.Context, d Dropped) { dropped = append(dropped, d) }))
	require.NoError(s.T(), os.WriteFile(l.Path(), []byte(`{"key":"a","value":1}`+"\n\n"+`{"key":"b"`), 0o644))
	require.NoError(s.T(), l.Update(s.T().Context(), set(mark{Key: "c", Value: 1})))
	assert.Equal(s.T(), []Dropped{{Path: l.Path(), Line: 3}}, dropped)
	raw, err := os.ReadFile(l.Path())
	require.NoError(s.T(), err)
	assert.Equal(s.T(), `{"key":"a","value":1}`+"\n"+`{"key":"c","value":1}`+"\n", string(raw))
}

// With nothing to tell, the rewrite that would drop the line is refused and the file is
// left as it was.
func (s *StoreSuite) TestRefusesToDropALineUnsaid() {
	l := marks(s.T(), filepath.Join(s.T().TempDir(), "marks.jsonl"))
	torn := `{"key":"a","value":1}` + "\n" + `{"key":"b"`
	require.NoError(s.T(), os.WriteFile(l.Path(), []byte(torn), 0o644))
	err := l.Update(s.T().Context(), set(mark{Key: "c", Value: 1}))
	require.ErrorIs(s.T(), err, ErrUnfinishedLine)
	var unfinished *UnfinishedLineError
	require.ErrorAs(s.T(), err, &unfinished)
	assert.Equal(s.T(), UnfinishedLineError{Path: l.Path(), Line: 2}, *unfinished)
	require.ErrorContains(s.T(), err, l.Path()+":2: ")
	raw, err := os.ReadFile(l.Path())
	require.NoError(s.T(), err)
	assert.Equal(s.T(), torn, string(raw))
}

// The directory a store creates is searchable by whoever may read its file: a private
// store's directory is private too.
func (s *StoreSuite) TestTheDirectoryModeFollowsPerm() {
	root := s.T().TempDir()
	private := marksPerm(s.T(), filepath.Join(root, "private", "marks.jsonl"), 0o600)
	require.NoError(s.T(), private.Update(s.T().Context(), set(mark{Key: "a"})))
	info, err := os.Stat(filepath.Dir(private.Path()))
	require.NoError(s.T(), err)
	assert.Equal(s.T(), os.FileMode(0o700), info.Mode().Perm())
	info, err = os.Stat(private.Path())
	require.NoError(s.T(), err)
	assert.Equal(s.T(), os.FileMode(0o600), info.Mode().Perm())

	shared := marks(s.T(), filepath.Join(root, "shared", "marks.jsonl"))
	require.NoError(s.T(), shared.Update(s.T().Context(), set(mark{Key: "a"})))
	info, err = os.Stat(filepath.Dir(shared.Path()))
	require.NoError(s.T(), err)
	assert.Equal(s.T(), os.FileMode(0o700), info.Mode().Perm()&0o700, "a 0644 store's directory is the owner's to search")
}

// A line that does not decode names the file and its line.
func (s *StoreSuite) TestNamesTheLineThatDoesNotDecode() {
	l := marks(s.T(), filepath.Join(s.T().TempDir(), "marks.jsonl"))
	require.NoError(s.T(), os.WriteFile(l.Path(), []byte(`{"key":"a"}`+"\n\n"+`{"key":`+"\n"), 0o644))
	_, err := l.Load()
	require.ErrorContains(s.T(), err, l.Path()+":3: ")
	require.ErrorIs(s.T(), err, ErrLineUndecodable)
	var line *LineError
	require.ErrorAs(s.T(), err, &line)
	assert.Equal(s.T(), 3, line.Line)
	var syntax *SyntaxError
	assert.ErrorAs(s.T(), err, &syntax, "the line's own fault stays reachable")
}

// While the file an older writer kept the store in exists, neither a read nor a write goes
// on, and the old file is left as it was.
func (s *StoreSuite) TestRefusesWhileTheOldFileExists() {
	l := marks(s.T(), filepath.Join(s.T().TempDir(), "marks.jsonl"))
	old := oldMarks(l.Path())
	require.NoError(s.T(), os.WriteFile(old, []byte(`[]`), 0o644))
	_, err := l.Load()
	require.ErrorIs(s.T(), err, ErrLegacyStore)
	var legacy *LegacyStoreError
	require.ErrorAs(s.T(), err, &legacy)
	assert.Equal(s.T(), old, legacy.Path)
	require.ErrorContains(s.T(), err, "preserve it")
	require.ErrorIs(s.T(), l.Update(s.T().Context(), set(mark{Key: "a"})), ErrLegacyStore)
	raw, err := os.ReadFile(old)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), "[]", string(raw))
	assert.NoFileExists(s.T(), l.Path())
}

type ConfigSuite struct{ suite.Suite }

func TestConfig(t *testing.T) { suite.Run(t, new(ConfigSuite)) }

func (s *ConfigSuite) valid() JSONLConfig[mark] {
	return JSONLConfig[mark]{Path: filepath.Join(s.T().TempDir(), "marks.jsonl"), Perm: 0o644, Key: markKey}
}

func (s *ConfigSuite) TestRefusesAnEmptyPath() {
	cfg := s.valid()
	cfg.Path = ""
	_, err := NewJSONL(cfg)
	var got *JSONLConfigError
	require.ErrorAs(s.T(), err, &got)
	assert.Equal(s.T(), JSONLConfigError{Field: "Path", Problem: "empty"}, *got)
	assert.EqualError(s.T(), err, "codec: JSONL Path: empty")
}

func (s *ConfigSuite) TestRefusesAZeroPerm() {
	cfg := s.valid()
	cfg.Perm = 0
	_, err := NewJSONL(cfg)
	var got *JSONLConfigError
	require.ErrorAs(s.T(), err, &got)
	assert.Equal(s.T(), "Perm", got.Field)
}

func (s *ConfigSuite) TestRefusesPermBitsOutsideThePermissionMask() {
	cfg := s.valid()
	cfg.Perm = os.ModeDir | 0o644
	_, err := NewJSONL(cfg)
	var got *JSONLConfigError
	require.ErrorAs(s.T(), err, &got)
	assert.Equal(s.T(), "Perm", got.Field)
}

func (s *ConfigSuite) TestRefusesANilKey() {
	cfg := s.valid()
	cfg.Key = nil
	_, err := NewJSONL(cfg)
	var got *JSONLConfigError
	require.ErrorAs(s.T(), err, &got)
	assert.Equal(s.T(), "Key", got.Field)
}

func (s *ConfigSuite) TestRefusesANilLive() {
	_, err := NewJSONL(s.valid(), WithLive[mark](nil))
	var got *OptionError
	require.ErrorAs(s.T(), err, &got)
	assert.Equal(s.T(), OptionError{Option: "WithLive", Problem: "nil function"}, *got)
	assert.EqualError(s.T(), err, "codec: option WithLive: nil function")
}

func (s *ConfigSuite) TestRefusesAnEmptyOld() {
	_, err := NewJSONL(s.valid(), WithOld[mark](""))
	var got *OptionError
	require.ErrorAs(s.T(), err, &got)
	assert.Equal(s.T(), "WithOld", got.Option)
}

func (s *ConfigSuite) TestRefusesANilLogger() {
	_, err := NewJSONL(s.valid(), WithLog[mark](nil))
	var got *OptionError
	require.ErrorAs(s.T(), err, &got)
	assert.Equal(s.T(), "WithLog", got.Option)
}

func (s *ConfigSuite) TestRefusesANilOnDrop() {
	_, err := NewJSONL(s.valid(), WithOnDrop[mark](nil))
	var got *OptionError
	require.ErrorAs(s.T(), err, &got)
	assert.Equal(s.T(), "WithOnDrop", got.Option)
}

func (s *ConfigSuite) TestReportsEveryProblemAtOnce() {
	_, err := NewJSONL(JSONLConfig[mark]{}, WithLog[mark](nil))
	var opt *OptionError
	var cfg *JSONLConfigError
	require.ErrorAs(s.T(), err, &opt)
	require.ErrorAs(s.T(), err, &cfg)
	for _, field := range []string{"Path", "Perm", "Key"} {
		assert.ErrorContains(s.T(), err, "codec: JSONL "+field+": ")
	}
}

func (s *ConfigSuite) TestOptionsSetTheirParts() {
	old := filepath.Join(s.T().TempDir(), "old.json")
	l, err := NewJSONL(s.valid(), WithOld[mark](old), WithLive[mark](func(m mark) bool { return !m.Gone }))
	require.NoError(s.T(), err)
	require.NoError(s.T(), os.WriteFile(old, []byte(`[]`), 0o644))
	_, err = l.Load()
	require.ErrorIs(s.T(), err, ErrLegacyStore)
}

// A store nobody built has no key to fold by and no file to write, so it fails rather than
// panicking on the nil Key.
func (s *ConfigSuite) TestTheZeroStoreIsRefused() {
	var l JSONL[mark]
	_, err := l.Load()
	var got *JSONLConfigError
	require.ErrorAs(s.T(), err, &got)
	assert.Equal(s.T(), JSONLConfigError{Field: "JSONL", Problem: "not built by NewJSONL"}, *got)
	require.ErrorAs(s.T(), l.Update(s.T().Context(), set(mark{Key: "a"})), &got)
}
