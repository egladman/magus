package review

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// TestReceiptCarriesNoIdentity pins the standing refusal in the package doc: a receipt names
// no reader, so a read count cannot be attributed to a person and therefore cannot become
// somebody's performance metric.
//
// Structural rather than behavioural on purpose. "Nothing reports this to a second person"
// is a property of every future caller, which no test can assert; that no field exists to
// report is a property of the type, and it is the one that makes the rest hard to undo by
// accident. Adding a field here should be a decision somebody argues for, not a diff that
// slips through.
func TestReceiptCarriesNoIdentity(t *testing.T) {
	var fields []string
	rt := reflect.TypeOf(Receipt{})
	for i := range rt.NumField() {
		fields = append(fields, rt.Field(i).Name)
	}
	assert.Equal(t, []string{"Path", "Digest", "At", "Source", "Reason"}, fields,
		"a receipt names no reader; see the package doc before adding a field")

	// Source is a type this package does not own, so the list above stops being sufficient:
	// a person added to VCSCheckpoint for some unrelated caller would arrive here silently
	// and hand the store the attributable field the package doc spent itself refusing.
	// Argued for and admitted because a checkpoint answers "which revision", never "whose".
	var checkpoint []string
	ct := reflect.TypeOf(types.VCSCheckpoint{})
	for i := range ct.NumField() {
		checkpoint = append(checkpoint, ct.Field(i).Name)
	}
	assert.Equal(t, []string{"Revision", "Branch", "Dirty", "PatchDigest", "VCS"}, checkpoint,
		"a receipt embeds this: it must keep naming a revision and never a person")
}

func TestLoadMissingStoreIsEmptyNotAnError(t *testing.T) {
	s, err := Load(t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, s)
}

func TestRecordMerges(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	require.NoError(t, Record(dir, []Receipt{{Path: "a.go", Digest: "aaa", At: now}}))
	require.NoError(t, Record(dir, []Receipt{{Path: "b.go", Digest: "bbb", At: now}}))

	s, err := Load(dir)
	require.NoError(t, err)
	// Acknowledging b must not withdraw a: a reviewer working through a large change in
	// several sittings is the normal case.
	assert.True(t, s.Covers("a.go", "aaa"))
	assert.True(t, s.Covers("b.go", "bbb"))
}

func TestRecordReplacesTheSamePath(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	require.NoError(t, Record(dir, []Receipt{{Path: "a.go", Digest: "old", At: now}}))
	require.NoError(t, Record(dir, []Receipt{{Path: "a.go", Digest: "new", At: now}}))

	s, err := Load(dir)
	require.NoError(t, err)
	require.Len(t, s, 1)
	assert.True(t, s.Covers("a.go", "new"))
	assert.False(t, s.Covers("a.go", "old"))
}

// The receipt is a claim about CONTENT, not about a path. Without this the first
// acknowledgement of a file would silently cover every later edit to it, which is the one
// way this feature could actively mislead a reader.
func TestCoversVoidsWhenTheContentMoves(t *testing.T) {
	s := Store{"a.go": {Path: "a.go", Digest: "aaa"}}

	assert.True(t, s.Covers("a.go", "aaa"))
	assert.False(t, s.Covers("a.go", "bbb"))
	assert.False(t, s.Covers("never-read.go", "aaa"))
}

// An unreadable file digests to "", and "" must never satisfy a receipt: otherwise a
// deleted file would read as acknowledged forever.
func TestCoversRejectsAnEmptyDigest(t *testing.T) {
	s := Store{"a.go": {Path: "a.go", Digest: ""}}
	assert.False(t, s.Covers("a.go", ""))
}

func TestDigestFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte("package a\n\nfunc F() {}\n"), 0o644))

	first := DigestFile(path)
	assert.NotEmpty(t, first)

	require.NoError(t, os.WriteFile(path, []byte("package a\n\nfunc G() {}\n"), 0o644))
	assert.NotEqual(t, first, DigestFile(path))

	assert.Empty(t, DigestFile(filepath.Join(dir, "gone.go")))
}

// TestDigestFileIsByteExact is the property a receipt turns on, and it is the opposite of
// what the notes store wants.
//
// A note asks whether prose still describes code, so reformatting must not fire. A receipt
// asks whether a person saw these bytes - and in Python, YAML, or a Makefile, whitespace IS
// the change. An earlier version reused the notes digest and a receipt survived every one
// of the edits below, attesting to content nobody had seen.
func TestDigestFileIsByteExact(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.py")
	write := func(body string) string {
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
		return DigestFile(path)
	}

	base := write("def f():\n    return 1\n")
	for name, body := range map[string]string{
		"a trailing newline":      "def f():\n    return 1\n\n",
		"a trailing space":        "def f():\n    return 1 \n",
		"a changed indent":        "def f():\n        return 1\n",
		"a tab instead of spaces": "def f():\n\treturn 1\n",
	} {
		assert.NotEqual(t, base, write(body), "%s must void the receipt", name)
	}
}

// A re-ack must not silently drop the note explaining why a file was covered in bulk.
// Losing it turns a stamped receipt into one that reads as though somebody sat down with
// the file - the exact conflation the reason exists to prevent.
func TestRecordKeepsAnEarlierReasonWhenTheNewOneIsBlank(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	require.NoError(t, Record(dir, []Receipt{{Path: "a.go", Digest: "one", At: now, Reason: "codemod output"}}))
	require.NoError(t, Record(dir, []Receipt{{Path: "a.go", Digest: "two", At: now}}))

	s, err := Load(dir)
	require.NoError(t, err)
	assert.Equal(t, "codemod output", s["a.go"].Reason)
	assert.True(t, s.Covers("a.go", "two"), "the digest still moves to the new content")
}

// ...but an explicit new reason replaces the old one: the reader said something newer.
func TestRecordReplacesAnEarlierReasonWhenGivenOne(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	require.NoError(t, Record(dir, []Receipt{{Path: "a.go", Digest: "one", At: now, Reason: "codemod output"}}))
	require.NoError(t, Record(dir, []Receipt{{Path: "a.go", Digest: "two", At: now, Reason: "read it properly"}}))

	s, err := Load(dir)
	require.NoError(t, err)
	assert.Equal(t, "read it properly", s["a.go"].Reason)
}

func TestReviewedAt(t *testing.T) {
	dir := t.TempDir()
	early := time.Now().Add(-2 * time.Hour)
	late := time.Now()
	require.NoError(t, Record(dir, []Receipt{
		{Path: "a.go", Digest: "a1", At: late, Source: types.VCSCheckpoint{Revision: "rev-new", VCS: "git"}},
		{Path: "b.go", Digest: "b1", At: early, Source: types.VCSCheckpoint{Revision: "rev-old", VCS: "git"}},
		// A working-tree receipt names no revision and cannot answer the question.
		{Path: "c.go", Digest: "c1", At: late},
	}))
	s, err := Load(dir)
	require.NoError(t, err)

	t.Run("the oldest pass wins, so nothing read earlier is hidden", func(t *testing.T) {
		at, covered := s.ReviewedAt([]string{"a.go", "b.go", "c.go"})

		assert.Equal(t, "rev-old", at.Revision)
		assert.Equal(t, 2, covered, "c.go carries no revision and is not covered")
	})

	t.Run("paths nobody reviewed have no earlier pass to subtract", func(t *testing.T) {
		at, covered := s.ReviewedAt([]string{"never.go"})

		assert.Zero(t, at)
		assert.Zero(t, covered)
	})

	t.Run("a working-tree-only review names no revision", func(t *testing.T) {
		at, covered := s.ReviewedAt([]string{"c.go"})

		assert.Zero(t, at, "a working tree has no revision to diff from")
		assert.Zero(t, covered)
	})
}
