package std

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"
)

type HTMLSuite struct{ suite.Suite }

func TestHTMLSuite(t *testing.T) { suite.Run(t, new(HTMLSuite)) }

func (s *HTMLSuite) escape(in string) string {
	got, err := HTMLEscape(context.Background(), in)
	s.Require().NoError(err)
	return got
}

func (s *HTMLSuite) TestEscapesAllFiveCharacters() {
	s.Equal("a &amp; b &lt; c &gt; d &#34; e &#39; f", s.escape(`a & b < c > d " e ' f`))
}

func (s *HTMLSuite) TestLeavesSafeTextAlone() {
	s.Equal("plain text", s.escape("plain text"))
}

func (s *HTMLSuite) TestEscapesEveryOccurrence() {
	s.Equal("&amp;a&amp;b&amp;", s.escape("&a&b&"))
}

func (s *HTMLSuite) TestDoesNotRescanIntroducedEntities() {
	s.Equal("&lt;&amp;&gt;", s.escape("<&>"))
}

func (s *HTMLSuite) TestModuleIsRegisteredAsPortable() {
	m, ok := Get("html")
	s.Require().True(ok)
	s.True(m.WASM)
}
