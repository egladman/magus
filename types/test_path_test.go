package types

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type TestPathSuite struct {
	suite.Suite
}

func TestTestPathSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(TestPathSuite))
}

func (s *TestPathSuite) TestGoTests() {
	s.True(IsTestPath("pkg/x_test.go"))
	s.True(IsTestPath("pkg/x_test.go:12"), "a symbol source")
}

func (s *TestPathSuite) TestTypeScriptTests() {
	s.True(IsTestPath("web/a.test.ts"))
	s.True(IsTestPath("web/a.spec.tsx"))
}

func (s *TestPathSuite) TestPythonTests() {
	s.True(IsTestPath("py/test_thing.py"))
	s.True(IsTestPath("py/thing_test.py"))
}

func (s *TestPathSuite) TestBuzzTests() {
	s.True(IsTestPath("spells/golang/spell_test.buzz"))
}

func (s *TestPathSuite) TestSourcesAreNotTests() {
	for _, p := range []string{"pkg/x.go", "web/testing.ts", "py/tester.py", "contest.go", "spells/golang/spell.buzz", ""} {
		s.False(IsTestPath(p), p)
	}
}
