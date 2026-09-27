package edit

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// site replaces old at line:col, a range on one line.
func site(path string, line, col int, old, repl string) Site {
	return Site{
		Path:  path,
		Start: types.EditPosition{Line: line, Column: col},
		End:   types.EditPosition{Line: line, Column: col + len(old)},
		Old:   old,
		New:   repl,
	}
}

func TestEditRefusedPlanWritesNothing(t *testing.T) {
	t.Parallel()

	files := map[string]string{"a.go": "package a\n\nvar x = 1\n", "b/b.go": "package b\n\nvar y = 2\n"}
	root := writeFiles(t, files)

	p := Resolve(root, []Site{site("a.go", 3, 5, "x", "z"), site("b/b.go", 3, 5, "w", "z")})

	require.ErrorIs(t, p.Apply(), ErrRefused)
	assert.Equal(t, []types.EditRefusal{
		{Path: "b/b.go", Line: 3, Column: 5, Reason: `holds "y", not "w": the file changed since it was read`},
	}, p.Refused())
	assert.Nil(t, p.Files())
	assert.Nil(t, p.Spans())
	for rel, body := range files {
		assert.Equal(t, body, readFile(t, root, rel), rel)
	}
	assert.ElementsMatch(t, []string{"a.go", "b/b.go"}, dirEntries(t, root))
}

func TestEditApplyWritesEveryFile(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"a.go":   "package a\n\nfunc f() { g(); g() }\n",
		"b/b.go": "package b\n\nvar _ = a.g\n",
	}
	root := writeFiles(t, files)

	p := Resolve(root, []Site{
		site("a.go", 3, 17, "g", "longer"),
		site("./b/b.go", 3, 11, "g", "longer"),
		site("a.go", 3, 12, "g", "longer"),
	})
	require.Empty(t, p.Refused())
	require.NoError(t, p.Apply())

	assert.Equal(t, "package a\n\nfunc f() { longer(); longer() }\n", readFile(t, root, "a.go"))
	assert.Equal(t, "package b\n\nvar _ = a.longer\n", readFile(t, root, "b/b.go"))
	assert.Equal(t, []types.EditedFile{
		{Path: "a.go", DigestBefore: digest([]byte(files["a.go"])), DigestAfter: digest([]byte(readFile(t, root, "a.go")))},
		{Path: "b/b.go", DigestBefore: digest([]byte(files["b/b.go"])), DigestAfter: digest([]byte(readFile(t, root, "b/b.go")))},
	}, p.Files())
	assert.Equal(t, []types.EditSpan{
		{Path: "a.go", Start: types.EditPosition{Line: 3, Column: 12}, End: types.EditPosition{Line: 3, Column: 13}},
		{Path: "a.go", Start: types.EditPosition{Line: 3, Column: 17}, End: types.EditPosition{Line: 3, Column: 18}},
		{Path: "b/b.go", Start: types.EditPosition{Line: 3, Column: 11}, End: types.EditPosition{Line: 3, Column: 12}},
	}, p.Spans())
	assert.ElementsMatch(t, []string{"a.go", "b/b.go"}, dirEntries(t, root))
}

func TestEditGradeRefusesTheFile(t *testing.T) {
	t.Parallel()

	files := map[string]string{"a.go": "x\n", "b.go": "x\n"}
	root := writeFiles(t, files)
	var graded []string

	p := Resolve(root, []Site{site("a.go", 1, 1, "x", "y"), site("b.go", 1, 1, "x", "y")},
		WithGrade(func(path string, before, after []byte) (string, string) {
			graded = append(graded, path+": "+string(before)+" -> "+string(after))
			if path == "b.go" {
				return "claimed-declaration", "lease other claims it"
			}
			return "", ""
		}))

	require.ErrorIs(t, p.Apply(), ErrRefused)
	assert.Equal(t, []string{"a.go: x\n -> y\n", "b.go: x\n -> y\n"}, graded)
	assert.Equal(t, []types.EditRefusal{{Path: "b.go", Rule: "claimed-declaration", Reason: "lease other claims it"}}, p.Refused())
	for rel, body := range files {
		assert.Equal(t, body, readFile(t, root, rel), rel)
	}
}

func TestEditRenameFailureRestoresEarlierFiles(t *testing.T) {
	t.Parallel()

	files := map[string]string{"a.go": "one\n", "b.go": "two\n"}
	root := writeFiles(t, files)
	p := Resolve(root, []Site{site("a.go", 1, 1, "one", "ONE"), site("b.go", 1, 1, "two", "TWO")})
	require.Empty(t, p.Refused())
	var landed []string
	p.opts.rename = func(oldpath, newpath string) error {
		if filepath.Base(newpath) == "b.go" {
			return errors.New("injected")
		}
		body, err := os.ReadFile(oldpath)
		require.NoError(t, err)
		landed = append(landed, filepath.Base(newpath)+"="+string(body))
		return os.Rename(oldpath, newpath)
	}

	err := p.Apply()

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
	p := Resolve(root, []Site{site("a.go", 1, 1, "one", "ONE"), site("b.go", 1, 1, "two", "TWO")})
	calls := 0
	p.opts.rename = func(oldpath, newpath string) error {
		if calls++; calls == 1 {
			return os.Rename(oldpath, newpath)
		}
		return errors.New("injected")
	}

	err := p.Apply()

	require.ErrorIs(t, err, ErrRestoreFailed)
	assert.Equal(t, "edit: write b.go: injected\nedit: restore failed: a.go: injected", err.Error())
	assert.Equal(t, "ONE\n", readFile(t, root, "a.go"))
	assert.ElementsMatch(t, []string{"a.go", "b.go"}, dirEntries(t, root))
}

func TestEditFileChangedAfterResolveIsNotOverwritten(t *testing.T) {
	t.Parallel()

	root := writeFiles(t, map[string]string{"a.go": "one\n", "b.go": "two\n"})
	p := Resolve(root, []Site{site("a.go", 1, 1, "one", "ONE"), site("b.go", 1, 1, "two", "TWO")})
	require.NoError(t, os.WriteFile(filepath.Join(root, "b.go"), []byte("two, and someone else's line\n"), 0o644))

	err := p.Apply()

	require.ErrorContains(t, err, "edit: write b.go: changed on disk since it was read")
	assert.Equal(t, "one\n", readFile(t, root, "a.go"))
	assert.Equal(t, "two, and someone else's line\n", readFile(t, root, "b.go"))
}

func TestEditPreservesMode(t *testing.T) {
	t.Parallel()

	root := writeFiles(t, map[string]string{"run.sh": "echo one\n"})
	require.NoError(t, os.Chmod(filepath.Join(root, "run.sh"), 0o755))

	require.NoError(t, Resolve(root, []Site{site("run.sh", 1, 6, "one", "two")}).Apply())

	info, err := os.Stat(filepath.Join(root, "run.sh"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
	assert.Equal(t, "echo two\n", readFile(t, root, "run.sh"))
}

func TestEditResolveRefusals(t *testing.T) {
	t.Parallel()

	root := writeFiles(t, map[string]string{"a.go": "ab\ncd", "over.go": "abc\n", "dir/keep": ""})
	require.NoError(t, os.Symlink("a.go", filepath.Join(root, "link.go")))

	for _, tc := range []struct {
		name  string
		sites []Site
		want  []types.EditRefusal
	}{
		{"nothing", nil, []types.EditRefusal{{Reason: "nothing to edit"}}},
		{"outside", []Site{site("../x.go", 1, 1, "a", "b")}, []types.EditRefusal{{Path: "../x.go", Reason: "not a path inside the workspace"}}},
		{"absolute", []Site{site("/etc/hosts", 1, 1, "a", "b")}, []types.EditRefusal{{Path: "/etc/hosts", Reason: "not a path inside the workspace"}}},
		{"missing", []Site{site("nope.go", 1, 1, "a", "b")}, []types.EditRefusal{{Path: "nope.go", Reason: "no such file"}}},
		{"directory", []Site{site("dir", 1, 1, "a", "b")}, []types.EditRefusal{{Path: "dir", Reason: "not a regular file (d---------)"}}},
		{"symlink", []Site{site("link.go", 1, 1, "a", "b")}, []types.EditRefusal{{Path: "link.go", Reason: "not a regular file (L---------)"}}},
		{"line past the end", []Site{site("a.go", 3, 1, "", "b")}, []types.EditRefusal{{Path: "a.go", Line: 3, Column: 1, Reason: "3:1 is not a position in the file"}}},
		{"column past the terminator", []Site{site("a.go", 1, 3, "\n", "")}, []types.EditRefusal{{Path: "a.go", Line: 1, Column: 3, Reason: "1:4 is not a position in the file after the start"}}},
		{"old differs", []Site{site("a.go", 2, 1, "cx", "b")}, []types.EditRefusal{{Path: "a.go", Line: 2, Column: 1, Reason: `holds "cd", not "cx": the file changed since it was read`}}},
		{"overlap", []Site{site("over.go", 1, 1, "ab", "x"), site("over.go", 1, 2, "bc", "y")}, []types.EditRefusal{{Path: "over.go", Line: 1, Column: 2, Reason: "overlaps the site at 1:1"}}},
		{"same start", []Site{site("over.go", 1, 1, "a", "x"), site("over.go", 1, 1, "ab", "y")}, []types.EditRefusal{{Path: "over.go", Line: 1, Column: 1, Reason: "overlaps the site at 1:1"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, Resolve(root, tc.sites).Refused())
		})
	}
}

func TestEditUnterminatedLastLine(t *testing.T) {
	t.Parallel()

	root := writeFiles(t, map[string]string{"a.go": "ab\ncd"})

	require.NoError(t, Resolve(root, []Site{site("a.go", 2, 2, "d", "D")}).Apply())

	assert.Equal(t, "ab\ncD", readFile(t, root, "a.go"))
}

func TestEditFormatRefusal(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "a.go:3:5: gone [claimed-declaration]", FormatRefusal(types.EditRefusal{Path: "a.go", Line: 3, Column: 5, Rule: "claimed-declaration", Reason: "gone"}))
	assert.Equal(t, "a.go: no such file", FormatRefusal(types.EditRefusal{Path: "a.go", Reason: "no such file"}))
	assert.Equal(t, "nothing to edit", FormatRefusal(types.EditRefusal{Reason: "nothing to edit"}))
}
