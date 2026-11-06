package main

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/marker"
)

type DevShimsSuite struct{ suite.Suite }

func TestDevShimsSuite(t *testing.T) { suite.Run(t, new(DevShimsSuite)) }

// The fixtures build the marker from its parts, so this file never holds one
// the release would refuse.
func devShim(prefix string) string {
	return prefix + marker.Compat + marker.NextRelease + "): the old reader"
}

func mapFile(lines ...string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(strings.Join(lines, "\n"))}
}

func (s *DevShimsSuite) TestRefusedWhileMarkersRemain() {
	fsys := fstest.MapFS{
		"a.go":       mapFile("package a", "", devShim("// "), "func F() {}"),
		"hack/b.sh":  mapFile("#!/bin/sh", devShim("# ")),
		"clean.go":   mapFile("package clean"),
		"binary.bin": mapFile("\x00", devShim("// ")),
	}
	err := checkDevShims(fsys, []string{"a.go", "hack/b.sh", "clean.go", "binary.bin", "deleted.go"})
	s.Require().Error(err)
	s.Contains(err.Error(), "2 development-only shim(s) remain")
	s.Contains(err.Error(), "a.go:3")
	s.Contains(err.Error(), "hack/b.sh:2")
	s.Contains(err.Error(), "delete each shim")
	s.NotContains(err.Error(), "binary.bin")
}

func (s *DevShimsSuite) TestAllowedWithNone() {
	fsys := fstest.MapFS{"a.go": mapFile("package a")}
	s.NoError(checkDevShims(fsys, []string{"a.go"}))
}

func (s *DevShimsSuite) TestOtherConditionsAreIgnored() {
	fsys := fstest.MapFS{"a.go": mapFile(
		"package a",
		"// "+marker.Compat+"no store holds v1 rows): the old reader",
		`const doc = "`+marker.Compat+marker.NextRelease+`)"`,
	)}
	s.NoError(checkDevShims(fsys, []string{"a.go"}))
}
