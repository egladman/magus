package edit

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

func TestMain(m *testing.M) { testkit.Main(m) }

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
		if err != nil || p == root {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			rel += "/"
		}
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

func sites(s ...Site) Request { return Request{Sites: s} }

type ResolveSuite struct{ suite.Suite }

func TestEditResolve(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(ResolveSuite))
}

func (s *ResolveSuite) tree(files map[string]string) string { return writeFiles(s.T(), files) }

func (s *ResolveSuite) refusals(root string, req Request) []types.EditRefusal {
	return Resolve(root, req).Refused()
}

func (s *ResolveSuite) TestRefusedPlanWritesNothing() {
	files := map[string]string{"a.go": "package a\n\nvar x = 1\n", "b/b.go": "package b\n\nvar y = 2\n"}
	root := s.tree(files)

	p := Resolve(root, sites(site("a.go", 3, 5, "x", "z"), site("b/b.go", 3, 5, "w", "z")))

	s.Require().ErrorIs(p.Apply(), ErrRefused)
	s.Equal([]types.EditRefusal{
		{Path: "b/b.go", Line: 3, Column: 5, Reason: `holds "y", not "w": the file changed since it was read`},
	}, p.Refused())
	s.Nil(p.Files())
	s.Nil(p.Spans())
	for rel, body := range files {
		s.Equal(body, readFile(s.T(), root, rel), rel)
	}
	s.ElementsMatch([]string{"a.go", "b/", "b/b.go"}, dirEntries(s.T(), root))
}

func (s *ResolveSuite) TestApplyWritesEveryFile() {
	files := map[string]string{
		"a.go":   "package a\n\nfunc f() { g(); g() }\n",
		"b/b.go": "package b\n\nvar _ = a.g\n",
	}
	root := s.tree(files)

	p := Resolve(root, sites(
		site("a.go", 3, 17, "g", "longer"),
		site("./b/b.go", 3, 11, "g", "longer"),
		site("a.go", 3, 12, "g", "longer"),
	))
	s.Require().Empty(p.Refused())
	s.Require().NoError(p.Apply())

	s.Equal("package a\n\nfunc f() { longer(); longer() }\n", readFile(s.T(), root, "a.go"))
	s.Equal("package b\n\nvar _ = a.longer\n", readFile(s.T(), root, "b/b.go"))
	s.Equal([]types.EditedFile{
		{Path: "a.go", DigestBefore: digest([]byte(files["a.go"])), DigestAfter: digest([]byte(readFile(s.T(), root, "a.go")))},
		{Path: "b/b.go", DigestBefore: digest([]byte(files["b/b.go"])), DigestAfter: digest([]byte(readFile(s.T(), root, "b/b.go")))},
	}, p.Files())
	s.Equal([]types.EditSpan{
		{Path: "a.go", Start: types.EditPosition{Line: 3, Column: 12}, End: types.EditPosition{Line: 3, Column: 13}},
		{Path: "a.go", Start: types.EditPosition{Line: 3, Column: 17}, End: types.EditPosition{Line: 3, Column: 18}},
		{Path: "b/b.go", Start: types.EditPosition{Line: 3, Column: 11}, End: types.EditPosition{Line: 3, Column: 12}},
	}, p.Spans())
	s.Empty(p.MakesDirs())
	s.ElementsMatch([]string{"a.go", "b/", "b/b.go"}, dirEntries(s.T(), root))
}

func (s *ResolveSuite) TestGradeRefusesTheFile() {
	files := map[string]string{"a.go": "x\n", "b.go": "x\n"}
	root := s.tree(files)
	var graded []string

	p := Resolve(root, sites(site("a.go", 1, 1, "x", "y"), site("b.go", 1, 1, "x", "y")),
		WithGrade(func(path string, before, after []byte) (string, string) {
			graded = append(graded, fmt.Sprintf("%s: %s -> %s", path, string(before), string(after)))
			if path == "b.go" {
				return "claimed-declaration", "lease other claims it"
			}
			return "", ""
		}))

	s.Require().ErrorIs(p.Apply(), ErrRefused)
	s.Equal([]string{"a.go: x\n -> y\n", "b.go: x\n -> y\n"}, graded)
	s.Equal([]types.EditRefusal{{Path: "b.go", Rule: "claimed-declaration", Reason: "lease other claims it"}}, p.Refused())
	for rel, body := range files {
		s.Equal(body, readFile(s.T(), root, rel), rel)
	}
}

func (s *ResolveSuite) TestRenameFailureRestoresEarlierFiles() {
	files := map[string]string{"a.go": "one\n", "b.go": "two\n"}
	root := s.tree(files)
	p := Resolve(root, sites(site("a.go", 1, 1, "one", "ONE"), site("b.go", 1, 1, "two", "TWO")))
	s.Require().Empty(p.Refused())
	var landed []string
	p.opts.rename = func(oldpath, newpath string) error {
		if filepath.Base(newpath) == "b.go" {
			return errors.New("injected")
		}
		body, err := os.ReadFile(oldpath)
		s.Require().NoError(err)
		landed = append(landed, fmt.Sprintf("%s=%s", filepath.Base(newpath), string(body)))
		return os.Rename(oldpath, newpath)
	}

	err := p.Apply()

	s.Require().EqualError(err, "every file written before it was restored: edit: write b.go: injected")
	s.Equal([]string{"a.go=ONE\n", "a.go=one\n"}, landed, "a.go lands, then its original lands back")
	for rel, body := range files {
		s.Equal(body, readFile(s.T(), root, rel), rel)
	}
	s.ElementsMatch([]string{"a.go", "b.go"}, dirEntries(s.T(), root))
}

func (s *ResolveSuite) TestRestoreFailureIsNamed() {
	root := s.tree(map[string]string{"a.go": "one\n", "b.go": "two\n"})
	p := Resolve(root, sites(site("a.go", 1, 1, "one", "ONE"), site("b.go", 1, 1, "two", "TWO")))
	calls := 0
	p.opts.rename = func(oldpath, newpath string) error {
		if calls++; calls == 1 {
			return os.Rename(oldpath, newpath)
		}
		return errors.New("injected")
	}

	err := p.Apply()

	s.Require().ErrorIs(err, ErrRestoreFailed)
	s.Equal("edit: write b.go: injected\nedit: restore failed: a.go: injected", err.Error())
	s.Equal("ONE\n", readFile(s.T(), root, "a.go"))
	s.ElementsMatch([]string{"a.go", "b.go"}, dirEntries(s.T(), root))
}

func (s *ResolveSuite) TestFileChangedAfterResolveIsNotOverwritten() {
	root := s.tree(map[string]string{"a.go": "one\n", "b.go": "two\n"})
	p := Resolve(root, sites(site("a.go", 1, 1, "one", "ONE"), site("b.go", 1, 1, "two", "TWO")))
	s.Require().NoError(os.WriteFile(filepath.Join(root, "b.go"), []byte("two, and someone else's line\n"), 0o644))

	err := p.Apply()

	s.Require().ErrorContains(err, "edit: write b.go: changed on disk since it was read")
	s.Equal("one\n", readFile(s.T(), root, "a.go"))
	s.Equal("two, and someone else's line\n", readFile(s.T(), root, "b.go"))
}

func (s *ResolveSuite) TestPreservesMode() {
	root := s.tree(map[string]string{"run.sh": "echo one\n"})
	s.Require().NoError(os.Chmod(filepath.Join(root, "run.sh"), 0o755))

	s.Require().NoError(Resolve(root, sites(site("run.sh", 1, 6, "one", "two"))).Apply())

	info, err := os.Stat(filepath.Join(root, "run.sh"))
	s.Require().NoError(err)
	s.Equal(os.FileMode(0o755), info.Mode().Perm())
	s.Equal("echo two\n", readFile(s.T(), root, "run.sh"))
}

func (s *ResolveSuite) TestEmptyRequestWritesNothing() {
	root := s.tree(map[string]string{"a.go": "x\n"})

	p := Resolve(root, Request{})

	s.Empty(p.Refused())
	s.Empty(p.Files())
	s.Require().NoError(p.Apply())
	s.Equal([]string{"a.go"}, dirEntries(s.T(), root))
}

func (s *ResolveSuite) TestOutsidePathRefused() {
	s.Equal([]types.EditRefusal{{Path: "../x.go", Reason: "not a path inside the workspace"}},
		s.refusals(s.tree(nil), sites(site("../x.go", 1, 1, "a", "b"))))
}

func (s *ResolveSuite) TestAbsolutePathRefused() {
	s.Equal([]types.EditRefusal{{Path: "/etc/hosts", Reason: "not a path inside the workspace"}},
		s.refusals(s.tree(nil), sites(site("/etc/hosts", 1, 1, "a", "b"))))
}

func (s *ResolveSuite) TestMissingFileRefused() {
	s.Equal([]types.EditRefusal{{Path: "nope.go", Reason: "no such file"}},
		s.refusals(s.tree(nil), sites(site("nope.go", 1, 1, "a", "b"))))
}

func (s *ResolveSuite) TestDirectoryRefused() {
	root := s.tree(map[string]string{"dir/keep": ""})
	s.Equal([]types.EditRefusal{{Path: "dir", Reason: "not a regular file (d---------)"}},
		s.refusals(root, sites(site("dir", 1, 1, "a", "b"))))
}

func (s *ResolveSuite) TestSymlinkRefused() {
	root := s.tree(map[string]string{"a.go": "ab\n"})
	s.Require().NoError(os.Symlink("a.go", filepath.Join(root, "link.go")))
	s.Equal([]types.EditRefusal{{Path: "link.go", Reason: "not a regular file (L---------)"}},
		s.refusals(root, sites(site("link.go", 1, 1, "a", "b"))))
}

func (s *ResolveSuite) TestLinePastTheEndRefused() {
	root := s.tree(map[string]string{"a.go": "ab\ncd"})
	s.Equal([]types.EditRefusal{{Path: "a.go", Line: 3, Column: 1, Reason: "3:1 is not a position in the file"}},
		s.refusals(root, sites(site("a.go", 3, 1, "", "b"))))
}

func (s *ResolveSuite) TestColumnPastTheTerminatorRefused() {
	root := s.tree(map[string]string{"a.go": "ab\ncd"})
	s.Equal([]types.EditRefusal{{Path: "a.go", Line: 1, Column: 3, Reason: "1:4 is not a position in the file after the start"}},
		s.refusals(root, sites(site("a.go", 1, 3, "\n", ""))))
}

func (s *ResolveSuite) TestOldDiffersRefused() {
	root := s.tree(map[string]string{"a.go": "ab\ncd"})
	s.Equal([]types.EditRefusal{{Path: "a.go", Line: 2, Column: 1, Reason: `holds "cd", not "cx": the file changed since it was read`}},
		s.refusals(root, sites(site("a.go", 2, 1, "cx", "b"))))
}

func (s *ResolveSuite) TestOverlapRefused() {
	root := s.tree(map[string]string{"over.go": "abc\n"})
	s.Equal([]types.EditRefusal{{Path: "over.go", Line: 1, Column: 2, Reason: "overlaps the site at 1:1"}},
		s.refusals(root, sites(site("over.go", 1, 1, "ab", "x"), site("over.go", 1, 2, "bc", "y"))))
}

func (s *ResolveSuite) TestSameStartRefused() {
	root := s.tree(map[string]string{"over.go": "abc\n"})
	s.Equal([]types.EditRefusal{{Path: "over.go", Line: 1, Column: 1, Reason: "overlaps the site at 1:1"}},
		s.refusals(root, sites(site("over.go", 1, 1, "a", "x"), site("over.go", 1, 1, "ab", "y"))))
}

func (s *ResolveSuite) TestUnterminatedLastLine() {
	root := s.tree(map[string]string{"a.go": "ab\ncd"})

	s.Require().NoError(Resolve(root, sites(site("a.go", 2, 2, "d", "D"))).Apply())

	s.Equal("ab\ncD", readFile(s.T(), root, "a.go"))
}

func (s *ResolveSuite) TestFormatRefusal() {
	s.Equal("a.go:3:5: gone [claimed-declaration]", FormatRefusal(types.EditRefusal{Path: "a.go", Line: 3, Column: 5, Rule: "claimed-declaration", Reason: "gone"}))
	s.Equal("a.go: no such file", FormatRefusal(types.EditRefusal{Path: "a.go", Reason: "no such file"}))
	s.Equal("nothing to edit", FormatRefusal(types.EditRefusal{Reason: "nothing to edit"}))
}

func (s *ResolveSuite) TestCreatesFileAndParents() {
	root := s.tree(map[string]string{"a/keep": ""})

	p := Resolve(root, Request{Creates: []Creation{{Path: "a/b/c/new.go", Content: "package c\n"}}})

	s.Require().Empty(p.Refused())
	s.Equal([]string{"a/b", "a/b/c"}, p.MakesDirs())
	s.Equal([]types.EditedFile{{Path: "a/b/c/new.go", DigestAfter: digest([]byte("package c\n"))}}, p.Files())
	s.Empty(dirEntriesUnder(s.T(), root, "a/b"), "Resolve writes nothing")
	s.Require().NoError(p.Apply())
	s.Equal("package c\n", readFile(s.T(), root, "a/b/c/new.go"))
	info, err := os.Stat(filepath.Join(root, "a/b/c/new.go"))
	s.Require().NoError(err)
	s.Equal(os.FileMode(0o644), info.Mode().Perm())
	s.ElementsMatch([]string{"a/", "a/keep", "a/b/", "a/b/c/", "a/b/c/new.go"}, dirEntries(s.T(), root))
}

func (s *ResolveSuite) TestCreationBesideARewrite() {
	root := s.tree(map[string]string{"a.go": "one\n"})

	p := Resolve(root, Request{Sites: []Site{site("a.go", 1, 1, "one", "two")}, Creates: []Creation{{Path: "b.go", Content: "new\n"}}})

	s.Require().NoError(p.Apply())
	s.Equal("two\n", readFile(s.T(), root, "a.go"))
	s.Equal("new\n", readFile(s.T(), root, "b.go"))
	s.Empty(p.MakesDirs())
}

func (s *ResolveSuite) TestCreationOverAnExistingFileRefused() {
	root := s.tree(map[string]string{"a.go": "one\n"})
	s.Equal([]types.EditRefusal{{Path: "a.go", Reason: "already exists; change it with old and new"}},
		s.refusals(root, Request{Creates: []Creation{{Path: "a.go", Content: "x"}}}))
}

func (s *ResolveSuite) TestCreationOutsideRefused() {
	s.Equal([]types.EditRefusal{{Path: "../new.go", Reason: "not a path inside the workspace"}},
		s.refusals(s.tree(nil), Request{Creates: []Creation{{Path: "../new.go", Content: "x"}}}))
}

func (s *ResolveSuite) TestCreationUnderAFileRefused() {
	root := s.tree(map[string]string{"a.go": "one\n"})
	s.Equal([]types.EditRefusal{{Path: "a.go/b/new.go", Reason: "a.go is not a directory"}},
		s.refusals(root, Request{Creates: []Creation{{Path: "a.go/b/new.go", Content: "x"}}}))
}

func (s *ResolveSuite) TestCreationThroughASymlinkedDirectoryRefused() {
	root := s.tree(nil)
	s.Require().NoError(os.Symlink(s.T().TempDir(), filepath.Join(root, "out")))
	s.Equal([]types.EditRefusal{{Path: "out/new.go", Reason: "resolves outside the workspace through a symlinked directory"}},
		s.refusals(root, Request{Creates: []Creation{{Path: "out/new.go", Content: "x"}}}))
}

func (s *ResolveSuite) TestCreatedTwiceRefused() {
	s.Equal([]types.EditRefusal{{Path: "new.go", Reason: "created twice; keep one creation"}},
		s.refusals(s.tree(nil), Request{Creates: []Creation{{Path: "new.go", Content: "x"}, {Path: "./new.go", Content: "y"}}}))
}

func (s *ResolveSuite) TestCreatedAndEditedRefused() {
	root := s.tree(map[string]string{"a.go": "one\n"})
	s.Contains(s.refusals(root, Request{Sites: []Site{site("a.go", 1, 1, "one", "two")}, Creates: []Creation{{Path: "a.go", Content: "x"}}}),
		types.EditRefusal{Path: "a.go", Reason: "both created and edited; create it with its final content"})
}

func (s *ResolveSuite) TestCreationAsAnotherCreationsParentRefused() {
	s.Equal([]types.EditRefusal{{Path: "a", Reason: "created as a file and as the parent of another creation"}},
		s.refusals(s.tree(nil), Request{Creates: []Creation{{Path: "a", Content: "x"}, {Path: "a/b.go", Content: "y"}}}))
}

func (s *ResolveSuite) TestCreationIsGradedWithNilBefore() {
	root := s.tree(nil)
	var before []byte
	graded := false

	p := Resolve(root, Request{Creates: []Creation{{Path: "new.go", Content: "x\n"}}},
		WithGrade(func(_ string, b, _ []byte) (string, string) {
			before, graded = b, true
			return "path-write-denied", "outside the lease's write paths"
		}))

	s.True(graded)
	s.Nil(before)
	s.Equal([]types.EditRefusal{{Path: "new.go", Rule: "path-write-denied", Reason: "outside the lease's write paths"}}, p.Refused())
	s.Nil(p.MakesDirs())
}

func (s *ResolveSuite) TestFailedApplyRemovesCreationsAndTheirDirectories() {
	root := s.tree(map[string]string{"z.go": "one\n"})
	p := Resolve(root, Request{
		Sites:   []Site{site("z.go", 1, 1, "one", "two")},
		Creates: []Creation{{Path: "a/b/new.go", Content: "x\n"}},
	})
	s.Require().Empty(p.Refused())
	p.opts.rename = func(oldpath, newpath string) error {
		if filepath.Base(newpath) == "z.go" {
			return errors.New("injected")
		}
		return os.Rename(oldpath, newpath)
	}

	err := p.Apply()

	s.Require().EqualError(err, "every file written before it was restored: edit: write z.go: injected")
	s.Equal([]string{"z.go"}, dirEntries(s.T(), root))
	s.Equal("one\n", readFile(s.T(), root, "z.go"))
}

func (s *ResolveSuite) TestCreationRacedOnDiskIsNotOverwritten() {
	root := s.tree(nil)
	p := Resolve(root, Request{Creates: []Creation{{Path: "new.go", Content: "mine\n"}}})
	s.Require().NoError(os.WriteFile(filepath.Join(root, "new.go"), []byte("theirs\n"), 0o644))

	err := p.Apply()

	s.Require().ErrorContains(err, "edit: write new.go: created on disk since it was planned")
	s.Equal("theirs\n", readFile(s.T(), root, "new.go"))
	s.Equal([]string{"new.go"}, dirEntries(s.T(), root))
}

func (s *ResolveSuite) TestDigestIsStableAcrossSiteOrder() {
	root := s.tree(map[string]string{"a.go": "one two\n", "b.go": "three\n"})

	first := Resolve(root, sites(site("a.go", 1, 1, "one", "1"), site("a.go", 1, 5, "two", "2"), site("b.go", 1, 1, "three", "3")))
	second := Resolve(root, sites(site("b.go", 1, 1, "three", "3"), site("a.go", 1, 5, "two", "2"), site("a.go", 1, 1, "one", "1")))

	s.NotEmpty(first.Digest())
	s.Equal(first.Digest(), second.Digest())
}

func (s *ResolveSuite) TestDigestNamesEachFileBeforeAndAfter() {
	root := s.tree(map[string]string{"a.go": "one\n"})

	p := Resolve(root, Request{Sites: []Site{site("a.go", 1, 1, "one", "two")}, Creates: []Creation{{Path: "b.go", Content: "new\n"}}})

	h := sha256.New()
	fmt.Fprintf(h, "a.go\x00%s\x00%s\n", digest([]byte("one\n")), digest([]byte("two\n")))
	fmt.Fprintf(h, "b.go\x00\x00%s\n", digest([]byte("new\n")))
	s.Equal("sha256:"+hex.EncodeToString(h.Sum(nil)), p.Digest())
}

func (s *ResolveSuite) TestDigestChangesWithTheAfterBytes() {
	root := s.tree(map[string]string{"a.go": "one\n"})

	s.NotEqual(Resolve(root, sites(site("a.go", 1, 1, "one", "two"))).Digest(),
		Resolve(root, sites(site("a.go", 1, 1, "one", "three"))).Digest())
}

func (s *ResolveSuite) TestDigestIsEmptyForARefusedPlan() {
	s.Empty(Resolve(s.tree(nil), sites(site("nope.go", 1, 1, "a", "b"))).Digest())
}

// dirEntriesUnder lists dir's tree under root, or nothing when dir
// does not exist.
func dirEntriesUnder(t *testing.T, root, dir string) []string {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, dir)); err != nil {
		return nil
	}
	return dirEntries(t, filepath.Join(root, dir))
}
