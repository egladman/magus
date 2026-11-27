package parsecache_test

import (
	"context"
	"strings"
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

// padded lifts src past the cache's minimum cached length so the cache is exercised.
func padded(src string) string {
	return src + "\n// " + strings.Repeat("x", 300) + "\n"
}

const rawSrc = `final x = r"a\b";`

func (s *DoorSuite) TestParseCarriesTheScanner() {
	_, err := parsecache.Parse(rawSrc)
	s.NoError(err)
	_, err = buzz.Parse(rawSrc)
	s.Error(err)
}

func (s *DoorSuite) TestTokenizeAndParserShareOneScanner() {
	p, err := parsecache.Parser()
	s.Require().NoError(err)
	toks, err := p.Scanner.Tokenize(`r"a"`)
	s.Require().NoError(err)
	s.Equal(token.String, toks[0].Kind)
}

func (s *DoorSuite) TestSessionExecsRawString() {
	opts, err := parsecache.Session()
	s.Require().NoError(err)
	sess := buzz.NewSession(context.Background(), append([]buzz.Option{buzz.WithEmbedded()}, opts...)...)
	s.T().Cleanup(func() { _ = sess.Close() })
	s.Require().NoError(sess.Exec(context.Background(), `var v = r"a\b";`))
	s.Equal(`a\b`, sess.GetGlobal("v").AsString())
}

func (s *DoorSuite) TestCacheNeverCrossesScanners() {
	plain := buzz.Parser{Cache: parsecache.Shared()}
	for _, src := range []string{padded(rawSrc), padded(`final y = r"c\d";`)} {
		_, err := parsecache.Parse(src)
		s.NoError(err)
		_, err = plain.Parse(src)
		s.Error(err, "a cached extended lex is never served to a plain parse")
	}
	plainFirst := padded(`final z = r"e\f";`)
	_, err := plain.Parse(plainFirst)
	s.Error(err)
	_, err = parsecache.Parse(plainFirst)
	s.NoError(err)
}
