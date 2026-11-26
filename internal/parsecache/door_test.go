package parsecache_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/parsecache"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/token"
)

type DoorSuite struct{ suite.Suite }

func TestDoorSuite(t *testing.T) { suite.Run(t, new(DoorSuite)) }

func (s *DoorSuite) TestTokenizeReadsRawString() {
	toks, err := parsecache.Tokenize(`r#"a "quoted" b"#`)
	s.Require().NoError(err)
	s.Require().NotEmpty(toks)
	s.Equal(token.String, toks[0].Kind)
	s.Equal(`a "quoted" b`, toks[0].Val)
}

func (s *DoorSuite) TestParserParsesRawString() {
	p, err := parsecache.Parser()
	s.Require().NoError(err)
	_, err = p.ParseEmbedded(`final x = r"a\b";`)
	s.NoError(err)
	_, err = buzz.ParseEmbedded(`final x = r"a\b";`)
	s.Error(err, "upstream scanning must not accept the raw form")
}

func (s *DoorSuite) TestParserSharesTheProcessCache() {
	a, err := parsecache.Parser()
	s.Require().NoError(err)
	b, err := parsecache.Parser()
	s.Require().NoError(err)
	s.Same(a.Scanner, b.Scanner)
	s.Same(a.Cache, b.Cache)
	s.Same(parsecache.Shared(), a.Cache)
}

func (s *DoorSuite) TestSessionCarriesBothOptions() {
	opts, err := parsecache.Session()
	s.Require().NoError(err)
	s.Len(opts, 2)
}
