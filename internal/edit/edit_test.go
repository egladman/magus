package edit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
	}
	return root
}

func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	require.NoError(t, err)
	return string(body)
}

func set(edits ...types.EditSite) types.EditSet {
	return types.EditSet{SchemaVersion: types.EditSetSchemaVersion, Edits: edits}
}

// dirEntries lists root's tree, so a test can assert no staged temp file survived.
func dirEntries(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	require.NoError(t, filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, filepath.ToSlash(rel))
		return nil
	}))
	return out
}

func TestEditRefusedSetWritesNothing(t *testing.T) {
	t.Parallel()

	files := map[string]string{"a.go": "package a\n\nvar x = 1\n", "b/b.go": "package b\n\nvar y = 2\n"}
	root := writeFiles(t, files)

	p := Resolve(root, set(
		types.EditSite{Path: "a.go", Anchor: types.EditAnchorText, Old: "x = 1", New: "x = 10"},
		types.EditSite{Path: "b/b.go", Anchor: types.EditAnchorLines, Lines: []int{3, 3}, Old: "var y = 3\n", New: "var y = 20\n"},
	))
	got, err := p.Apply(func(types.EditReceipt) error {
		t.Fatal("a refused plan must not record a receipt")
		return nil
	})

	require.ErrorIs(t, err, ErrRefused)
	assert.Equal(t, types.EditReceipt{
		SchemaVersion: types.EditSetSchemaVersion,
		Refused:       []types.EditRefusal{{Edit: 1, Path: "b/b.go", Reason: "old does not match the bytes at lines [3, 3]"}},
	}, got)
	for rel, body := range files {
		assert.Equal(t, body, readFile(t, root, rel), rel)
	}
	assert.ElementsMatch(t, []string{"a.go", "b/b.go"}, dirEntries(t, root))
}

func TestEditApplyThenUndoRestoresDigests(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"a.go":   "package a\n\nfunc f() { g(); g() }\n\nvar v = 1\n",
		"b/b.go": "package b\n\nvar y = 2\nvar z = 3\n",
	}
	root := writeFiles(t, files)
	before := map[string]string{}
	for rel, body := range files {
		before[rel] = Digest([]byte(body))
	}
	receipts := t.TempDir()

	p := Resolve(root, set(
		types.EditSite{Path: "a.go", Anchor: types.EditAnchorText, Old: "g()", New: "h()", All: true, Digest: before["a.go"]},
		types.EditSite{Path: "a.go", Anchor: types.EditAnchorLines, Lines: []int{5, 5}, New: "var v = 2\n"},
		types.EditSite{Path: "./b/b.go", Anchor: types.EditAnchorLines, Lines: []int{3, 4}, Old: "var y = 2\nvar z = 3\n", New: "var y = 20\n"},
	), WithCheckpoint(func() string { return "rev" }))
	require.Empty(t, p.Refused())
	applied, err := p.Apply(func(r types.EditReceipt) error { return SaveReceipt(receipts, r) })
	require.NoError(t, err)

	assert.Equal(t, "package a\n\nfunc f() { h(); h() }\n\nvar v = 2\n", readFile(t, root, "a.go"))
	assert.Equal(t, "package b\n\nvar y = 20\n", readFile(t, root, "b/b.go"))
	assert.True(t, applied.Applied)
	assert.Equal(t, "rev", applied.CheckpointBefore)
	assert.Equal(t, "rev", applied.CheckpointAfter)
	assert.Equal(t, []types.EditedFile{
		{Path: "a.go", DigestBefore: before["a.go"], DigestAfter: Digest([]byte(readFile(t, root, "a.go")))},
		{Path: "b/b.go", DigestBefore: before["b/b.go"], DigestAfter: Digest([]byte(readFile(t, root, "b/b.go")))},
	}, applied.Files)
	assert.Equal(t, []types.EditSpan{
		{Edit: 0, Path: "a.go", Start: types.EditPosition{Line: 3, Col: 12}, End: types.EditPosition{Line: 3, Col: 15}},
		{Edit: 0, Path: "a.go", Start: types.EditPosition{Line: 3, Col: 17}, End: types.EditPosition{Line: 3, Col: 20}},
		{Edit: 1, Path: "a.go", Start: types.EditPosition{Line: 5, Col: 1}, End: types.EditPosition{Line: 6, Col: 1}},
		{Edit: 2, Path: "b/b.go", Start: types.EditPosition{Line: 3, Col: 1}, End: types.EditPosition{Line: 5, Col: 1}},
	}, applied.Spans)

	stored, err := LoadReceipt(receipts, applied.ID)
	require.NoError(t, err)
	assert.Equal(t, applied.Undo, stored.Undo)

	undone, err := Resolve(root, *stored.Undo).Apply(nil)
	require.NoError(t, err)
	for rel, body := range files {
		assert.Equal(t, body, readFile(t, root, rel), rel)
	}
	for _, f := range undone.Files {
		assert.Equal(t, before[f.Path], f.DigestAfter, f.Path)
	}
	assert.ElementsMatch(t, []string{"a.go", "b/b.go"}, dirEntries(t, root))
}

func TestEditRenameFailureRestoresEarlierFiles(t *testing.T) {
	t.Parallel()

	files := map[string]string{"a.go": "one\n", "b.go": "two\n"}
	root := writeFiles(t, files)
	p := Resolve(root, set(
		types.EditSite{Path: "a.go", Anchor: types.EditAnchorText, Old: "one", New: "ONE"},
		types.EditSite{Path: "b.go", Anchor: types.EditAnchorText, Old: "two", New: "TWO"},
	))
	require.Empty(t, p.Refused())
	var landed []string
	p.opts.rename = func(oldpath, newpath string) error {
		if filepath.Base(newpath) == "b.go" {
			return errors.New("injected")
		}
		landed = append(landed, filepath.Base(newpath)+"="+readFileAbs(t, oldpath))
		return os.Rename(oldpath, newpath)
	}

	_, err := p.Apply(nil)

	require.EqualError(t, err, "edit: write b.go: injected; every file written before it was restored")
	assert.Equal(t, []string{"a.go=ONE\n", "a.go=one\n"}, landed, "a.go lands, then its original lands back")
	for rel, body := range files {
		assert.Equal(t, body, readFile(t, root, rel), rel)
	}
	assert.ElementsMatch(t, []string{"a.go", "b.go"}, dirEntries(t, root))
}

func TestEditRestoreFailureIsNamed(t *testing.T) {
	t.Parallel()

	root := writeFiles(t, map[string]string{"a.go": "one\n", "b.go": "two\n"})
	p := Resolve(root, set(
		types.EditSite{Path: "a.go", Anchor: types.EditAnchorText, Old: "one", New: "ONE"},
		types.EditSite{Path: "b.go", Anchor: types.EditAnchorText, Old: "two", New: "TWO"},
	))
	calls := 0
	p.opts.rename = func(oldpath, newpath string) error {
		if calls++; calls == 1 {
			return os.Rename(oldpath, newpath)
		}
		return errors.New("injected")
	}

	_, err := p.Apply(nil)

	require.ErrorIs(t, err, ErrRestoreFailed)
	assert.Equal(t, "edit: write b.go: injected\nedit: restore failed: a.go: injected", err.Error())
	assert.Equal(t, "ONE\n", readFile(t, root, "a.go"))
	assert.ElementsMatch(t, []string{"a.go", "b.go"}, dirEntries(t, root))
}

func readFileAbs(t *testing.T, abs string) string {
	t.Helper()
	body, err := os.ReadFile(abs)
	require.NoError(t, err)
	return string(body)
}

func TestEditFileChangedAfterResolveIsNotOverwritten(t *testing.T) {
	t.Parallel()

	root := writeFiles(t, map[string]string{"a.go": "one\n", "b.go": "two\n"})
	p := Resolve(root, set(
		types.EditSite{Path: "a.go", Anchor: types.EditAnchorText, Old: "one", New: "ONE"},
		types.EditSite{Path: "b.go", Anchor: types.EditAnchorText, Old: "two", New: "TWO"},
	))
	require.NoError(t, os.WriteFile(filepath.Join(root, "b.go"), []byte("two, and someone else's line\n"), 0o644))

	_, err := p.Apply(nil)

	require.ErrorContains(t, err, "edit: write b.go: changed on disk since it was read")
	assert.Equal(t, "one\n", readFile(t, root, "a.go"))
	assert.Equal(t, "two, and someone else's line\n", readFile(t, root, "b.go"))
}

func TestEditRecordFailureWritesNothing(t *testing.T) {
	t.Parallel()

	root := writeFiles(t, map[string]string{"a.go": "one\n"})
	_, err := Resolve(root, set(types.EditSite{Path: "a.go", Anchor: types.EditAnchorText, Old: "one", New: "ONE"})).
		Apply(func(types.EditReceipt) error { return errors.New("disk full") })

	require.EqualError(t, err, "edit: record the receipt: disk full, nothing written")
	assert.Equal(t, "one\n", readFile(t, root, "a.go"))
	assert.Equal(t, []string{"a.go"}, dirEntries(t, root))
}

func TestEditPreservesMode(t *testing.T) {
	t.Parallel()

	root := writeFiles(t, map[string]string{"run.sh": "echo one\n"})
	require.NoError(t, os.Chmod(filepath.Join(root, "run.sh"), 0o755))

	_, err := Resolve(root, set(types.EditSite{Path: "run.sh", Anchor: types.EditAnchorText, Old: "one", New: "two"})).Apply(nil)

	require.NoError(t, err)
	info, err := os.Stat(filepath.Join(root, "run.sh"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

// Every case applies, lands on after, and then its own undo set lands back on before.
func TestEditUndoRoundTrips(t *testing.T) {
	t.Parallel()

	lines := func(start, end int, old, repl string) types.EditSite {
		return types.EditSite{Path: "f", Anchor: types.EditAnchorLines, Lines: []int{start, end}, Old: old, New: repl}
	}
	text := func(old, repl string, all bool) types.EditSite {
		return types.EditSite{Path: "f", Anchor: types.EditAnchorText, Old: old, New: repl, All: all}
	}
	for _, tc := range []struct {
		name, before, after string
		edits               []types.EditSite
	}{
		{"replace one line", "a\nb\nc\n", "a\nB\nc\n", []types.EditSite{lines(2, 2, "b\n", "B\n")}},
		{"delete the unterminated last line", "a\nb", "a\n", []types.EditSite{text("b", "", false)}},
		{"delete mid-line", "ab\n", "a\n", []types.EditSite{text("b", "", false)}},
		{"delete a whole line", "a\nb\nc\n", "a\nc\n", []types.EditSite{lines(2, 2, "b\n", "")}},
		{"insert into an empty file", "", "x\n", []types.EditSite{lines(1, 0, "", "x\n")}},
		{"append after the last line", "a\n", "a\nb\n", []types.EditSite{lines(2, 1, "", "b\n")}},
		{"insert before line 1", "a\n", "z\na\n", []types.EditSite{lines(1, 0, "", "z\n")}},
		{"empty the file", "a\nb\n", "", []types.EditSite{lines(1, 2, "a\nb\n", "")}},
		{"two sites on one line", "f(x, x)\n", "f(y, y)\n", []types.EditSite{text("x", "y", true)}},
		{"adjacent lines", "a\nb\nc\n", "A\nB\nc\n", []types.EditSite{lines(1, 1, "", "A\n"), lines(2, 2, "", "B\n")}},
		{"replace then insert at the next line", "a\nb\n", "A\nnew\nb\n", []types.EditSite{lines(1, 1, "", "A\n"), lines(2, 1, "", "new\n")}},
		{"grow and shrink", "one\ntwo\nthree\nfour\n", "1\n2\n2\nthree\n", []types.EditSite{text("one", "1", false), text("two", "2\n2", false), lines(4, 4, "four\n", "")}},
		{"no trailing newline, multi-line new", "a\nb", "a\nb\nc", []types.EditSite{text("b", "b\nc", false)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := writeFiles(t, map[string]string{"f": tc.before})

			applied, err := Resolve(root, set(tc.edits...)).Apply(nil)
			require.NoError(t, err)
			require.Equal(t, tc.after, readFile(t, root, "f"))

			undo := Resolve(root, *applied.Undo)
			require.Empty(t, undo.Refused())
			_, err = undo.Apply(nil)
			require.NoError(t, err)
			assert.Equal(t, tc.before, readFile(t, root, "f"))
		})
	}
}

func TestEditUndoRefusesAFileEditedSince(t *testing.T) {
	t.Parallel()

	root := writeFiles(t, map[string]string{"f": "a\n"})
	applied, err := Resolve(root, set(types.EditSite{Path: "f", Anchor: types.EditAnchorText, Old: "a", New: "b"})).Apply(nil)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "f"), []byte("b\nmore\n"), 0o644))

	refused := Resolve(root, *applied.Undo).Refused()

	require.Len(t, refused, 1)
	assert.Contains(t, refused[0].Reason, "the file changed since it was read")
}

func TestEditResolveRefusals(t *testing.T) {
	t.Parallel()

	root := writeFiles(t, map[string]string{"a.go": "x x\ny\n", "gen/out.go": "generated\n", "dir/keep": ""})
	require.NoError(t, os.Symlink("a.go", filepath.Join(root, "link.go")))
	outside := writeFiles(t, map[string]string{"secret.go": "x\n"})
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "away")))
	declared := WithRefusal(func(path string) string {
		if strings.HasPrefix(path, "gen/") {
			return "a declared output of .: regenerate it, never hand-edit"
		}
		return ""
	})

	for _, tc := range []struct {
		name string
		site types.EditSite
		want types.EditRefusal
	}{
		{"missing file", types.EditSite{Path: "nope.go", Anchor: types.EditAnchorText, Old: "x"}, types.EditRefusal{Path: "nope.go", Reason: "no such file"}},
		{"escapes the root", types.EditSite{Path: "../a.go", Anchor: types.EditAnchorText, Old: "x"}, types.EditRefusal{Path: "../a.go", Reason: "not a path inside the workspace"}},
		{"absolute", types.EditSite{Path: "/etc/hosts", Anchor: types.EditAnchorText, Old: "x"}, types.EditRefusal{Path: "/etc/hosts", Reason: "not a path inside the workspace"}},
		{"declared output", types.EditSite{Path: "gen/out.go", Anchor: types.EditAnchorText, Old: "generated"}, types.EditRefusal{Path: "gen/out.go", Reason: "a declared output of .: regenerate it, never hand-edit"}},
		{"directory", types.EditSite{Path: "dir", Anchor: types.EditAnchorText, Old: "x"}, types.EditRefusal{Path: "dir", Reason: "not a regular file (d---------)"}},
		{"symlink", types.EditSite{Path: "link.go", Anchor: types.EditAnchorText, Old: "x"}, types.EditRefusal{Path: "link.go", Reason: "not a regular file (L---------)"}},
		{"through a symlinked dir", types.EditSite{Path: "away/secret.go", Anchor: types.EditAnchorText, Old: "x"}, types.EditRefusal{Path: "away/secret.go", Reason: "resolves outside the workspace through a symlinked directory"}},
		{"text absent", types.EditSite{Path: "a.go", Anchor: types.EditAnchorText, Old: "z"}, types.EditRefusal{Path: "a.go", Reason: "old does not occur in the file"}},
		{"text ambiguous", types.EditSite{Path: "a.go", Anchor: types.EditAnchorText, Old: "x"}, types.EditRefusal{Path: "a.go", Reason: "old occurs 2 times; widen it to one occurrence, or set all"}},
		{"lines past the end", types.EditSite{Path: "a.go", Anchor: types.EditAnchorLines, Lines: []int{3, 3}}, types.EditRefusal{Path: "a.go", Reason: "lines [3, 3] is past the end of the file, which has 2 lines"}},
		{"lines old differs", types.EditSite{Path: "a.go", Anchor: types.EditAnchorLines, Lines: []int{2, 2}, Old: "y"}, types.EditRefusal{Path: "a.go", Reason: "old does not match the bytes at lines [2, 2]"}},
		{"digest differs", types.EditSite{Path: "a.go", Anchor: types.EditAnchorText, Old: "y", Digest: "sha256:00"}, types.EditRefusal{Path: "a.go", Reason: "the file changed since it was read: digest " + Digest([]byte("x x\ny\n")) + ", the site expects sha256:00"}},
		{"malformed", types.EditSite{Path: "a.go", Anchor: types.EditAnchorText}, types.EditRefusal{Path: "a.go", Reason: "the text anchor needs old, the bytes it replaces"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, []types.EditRefusal{tc.want}, Resolve(root, set(tc.site), declared).Refused())
		})
	}

	t.Run("overlap", func(t *testing.T) {
		t.Parallel()
		got := Resolve(root, set(
			types.EditSite{Path: "a.go", Anchor: types.EditAnchorLines, Lines: []int{1, 2}},
			types.EditSite{Path: "a.go", Anchor: types.EditAnchorText, Old: "y"},
		)).Refused()
		assert.Equal(t, []types.EditRefusal{{Edit: 1, Path: "a.go", Reason: "overlaps edit 0 at 2:1"}}, got)
	})

	t.Run("every refusal is listed", func(t *testing.T) {
		t.Parallel()
		got := Resolve(root, set(
			types.EditSite{Path: "a.go", Anchor: types.EditAnchorText, Old: "z"},
			types.EditSite{Path: "nope.go", Anchor: types.EditAnchorText, Old: "x"},
		)).Refused()
		assert.Len(t, got, 2)
	})
}

func TestEditDecodeSet(t *testing.T) {
	t.Parallel()

	got, err := DecodeSet(strings.NewReader(`{"schema_version":1,"edits":[{"path":"a.go","anchor":"lines","lines":[1,2],"new":"x\n"}]}`))
	require.NoError(t, err)
	assert.Equal(t, set(types.EditSite{Path: "a.go", Anchor: types.EditAnchorLines, Lines: []int{1, 2}, New: "x\n"}), got)

	for in, want := range map[string]string{
		``:                                  "edit: the set is empty",
		`{"edits":[]}`:                      "edit: the set carries no schema_version; this magus reads 1 through 1",
		`{"schema_version":9,"future":1}`:   "edit: the set is schema_version 9 and this magus reads 1 through 1; update magus",
		`{"schema_version":1,"edits":[]}`:   "edit: the set has no edits",
		`{"schema_version":1,"edits":[{}]}`: "edit 0: no path",
		`{"schema_version":1,"edits":[{"path":"a","anchor":"text","old":"x","new":"y","extra":1}]}`: "edit: the input is not a version 1 set",
	} {
		_, err := DecodeSet(strings.NewReader(in))
		assert.ErrorContains(t, err, want, in)
	}
}

func TestEditSchema(t *testing.T) {
	t.Parallel()

	body, err := Schema()
	require.NoError(t, err)
	var schema struct {
		Title      string   `json:"title"`
		Required   []string `json:"required"`
		Properties struct {
			Edits struct {
				Items struct {
					Required   []string `json:"required"`
					Properties struct {
						Anchor struct {
							Enum []string `json:"enum"`
						} `json:"anchor"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"edits"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &schema))
	assert.Equal(t, "magus edit set", schema.Title)
	assert.ElementsMatch(t, []string{"schema_version", "edits"}, schema.Required)
	assert.ElementsMatch(t, []string{"path", "anchor", "new"}, schema.Properties.Edits.Items.Required)
	assert.Equal(t, []string{"lines", "text"}, schema.Properties.Edits.Items.Properties.Anchor.Enum)
}
