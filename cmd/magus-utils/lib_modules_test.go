package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

const termkitGen = "github.com/egladman/magus/std/termkit/gen"

type LibModulesSuite struct{ suite.Suite }

func TestLibModules(t *testing.T) { suite.Run(t, new(LibModulesSuite)) }

func (s *LibModulesSuite) termkit() *libModule {
	lib, err := loadLib(filepath.Join("..", "..", "libs", "termkit"), termkitGen)
	s.Require().NoError(err)
	return lib
}

// writeLib lays out a one-package library module in a temp directory.
func (s *LibModulesSuite) writeLib(src string) string {
	dir := s.T().TempDir()
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/libs/kit\n\ngo 1.26.0\n"), 0o644))
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "kit.go"), []byte(src), 0o644))
	return dir
}

func (s *LibModulesSuite) TestEveryTermkitExportHasABinding() {
	s.Empty(s.termkit().gaps())
}

func (s *LibModulesSuite) TestBuzzModulesMirrorTheGoImportPaths() {
	paths := map[string]string{}
	for _, p := range s.termkit().pkgs {
		paths[p.importPath] = p.buzzPath
	}
	s.Equal(map[string]string{
		"github.com/egladman/magus/libs/termkit":          "termkit",
		"github.com/egladman/magus/libs/termkit/layout":   "termkit/layout",
		"github.com/egladman/magus/libs/termkit/notify":   "termkit/notify",
		"github.com/egladman/magus/libs/termkit/pick":     "termkit/pick",
		"github.com/egladman/magus/libs/termkit/progress": "termkit/progress",
		"github.com/egladman/magus/libs/termkit/screen":   "termkit/screen",
	}, paths)
}

func (s *LibModulesSuite) TestAnExportWithNoBuzzFormIsAGap() {
	lib, err := loadLib(s.writeLib("package kit\n\nfunc Feed(c chan int) {}\n"), termkitGen)
	s.Require().NoError(err)
	s.Equal([]string{"example.com/libs/kit.Feed: parameter c: type chan int has no Buzz form"}, lib.gaps())
}

func (s *LibModulesSuite) TestInternalPackagesAreNotBound() {
	dir := s.writeLib("package kit\n\nfunc Ok() bool { return true }\n")
	s.Require().NoError(os.MkdirAll(filepath.Join(dir, "internal", "x"), 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "internal", "x", "x.go"), []byte("package x\n\nfunc Feed(c chan int) {}\n"), 0o644))
	lib, err := loadLib(dir, termkitGen)
	s.Require().NoError(err)
	s.Len(lib.pkgs, 1)
	s.Empty(lib.gaps())
}

func (s *LibModulesSuite) TestARecordMethodTakesTheRecordAsAMap() {
	lib, err := loadLib(s.writeLib("package kit\n\ntype Pair struct{ A, B string }\n\nfunc (p Pair) Join() string { return p.A + p.B }\n"), termkitGen)
	s.Require().NoError(err)
	src, err := lib.emitPackage(lib.pkgs[0])
	s.Require().NoError(err)
	s.Contains(string(src), `Name: "pair_join"`)
	s.Contains(string(src), `{Name: "pair", Type: std.TypeAnyMap}`)
	s.Contains(string(src), "func DecodePair(v any) (lib.Pair, error)")
}

func (s *LibModulesSuite) TestAStatefulTypeCrossesAsAHandle() {
	lib, err := loadLib(s.writeLib("package kit\n\ntype Box struct{ n int }\n\nfunc New() *Box { return &Box{} }\n\nfunc (b *Box) Add(n int) int { b.n += n; return b.n }\n"), termkitGen)
	s.Require().NoError(err)
	src, err := lib.emitPackage(lib.pkgs[0])
	s.Require().NoError(err)
	s.Contains(string(src), `var BoxHandles = convert.NewTable[*lib.Box]("Box")`)
	s.Contains(string(src), `{Name: "box", Type: std.TypeInt}`)
}

func (s *LibModulesSuite) TestSnakeSplitsAcronyms() {
	s.Equal("sgr_bold", snake("SGRBold"))
	s.Equal("utf8_locale", snake("UTF8Locale"))
	s.Equal("new_zone", snake("NewZone"))
}
