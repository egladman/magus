package langservice

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type membersSuite struct {
	suite.Suite
	svc *Service
}

func TestMembersSuite(t *testing.T) { suite.Run(t, new(membersSuite)) }

func (s *membersSuite) SetupTest() { s.svc = New(fixture()) }

func (s *membersSuite) member(module, name string) Member {
	m, ok := s.svc.member(module, name)
	s.Require().True(ok, "%s\\%s", module, name)
	return m
}

func (s *membersSuite) TestOnlyExportedFunctionsAndValues() {
	members, err := s.svc.Members("fs")
	s.Require().NoError(err)
	names := make([]string, len(members))
	for i, m := range members {
		names[i] = m.Name
	}
	s.Equal([]string{"glob", "writeFile", "stat", "join", "sep"}, names)

	lib, err := s.svc.Members("lib")
	s.Require().NoError(err)
	s.Equal([]Member{{Name: "twice", Signature: "twice(n: int) > int", Doc: "Twice doubles n.", Kind: MemberFunction}}, lib)
}

func (s *membersSuite) TestTripleSlashDocKeepsBlankLines() {
	s.Equal(Member{
		Name:      "glob",
		Signature: "glob(pattern: str) > [str] !> any",
		Doc:       "Glob returns the paths matching pattern.\n\nOrder is lexical.",
		Kind:      MemberFunction,
	}, s.member("fs", "glob"))
}

func (s *membersSuite) TestDoubleSlashDoc() {
	s.Equal("Write content to path.", s.member("fs", "writeFile").Doc)
}

func (s *membersSuite) TestUndocumentedMemberAfterADocumentedOne() {
	s.Empty(s.member("fs", "stat").Doc)
}

func (s *membersSuite) TestMultiLineHeaderCollapses() {
	s.Equal("stat( path: str, ) > FileInfo !> any", s.member("fs", "stat").Signature)
}

func (s *membersSuite) TestDefaultHoldingASemicolon() {
	s.Equal(`join(parts: [str], sep: str = ";") > str`, s.member("fs", "join").Signature)
}

func (s *membersSuite) TestValue() {
	s.Equal(Member{Name: "sep", Signature: "sep: str", Doc: "The path separator.", Kind: MemberValue}, s.member("fs", "sep"))
}

func (s *membersSuite) TestFileHeaderAboveAGapIsNoDoc() {
	members, err := parseMembers("// Header.\n\nexport extern fun f() > void;\n")
	s.Require().NoError(err)
	s.Require().Len(members, 1)
	s.Empty(members[0].Doc)
}

func (s *membersSuite) TestDocFirstLineCarriesAVariadicSignature() {
	s.Equal("fmt\\sprintf(format, args...) -> string\nFormat args.", s.member("fmt", "sprintf").Doc)
}

func (s *membersSuite) TestNamespaceStatementIsNoMember() {
	members, err := s.svc.Members("magus/vcs")
	s.Require().NoError(err)
	s.Equal([]Member{{Name: "commit", Signature: "commit(message: str) > str !> any", Doc: "Commit records the staged changes.", Kind: MemberFunction}}, members)
}
