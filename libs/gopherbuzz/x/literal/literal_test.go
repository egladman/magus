package literal

import (
	"context"
	"strings"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/token"
	"github.com/stretchr/testify/suite"
)

const goText = "x := `a` + fmt.Sprintf(\"%d\\n\", []int{1}[0]) // {} {{x}} \\d{3} ${n}\r\nend"

type rawSuite struct {
	suite.Suite
	scanner *token.Scanner
}

func TestRawSuite(t *testing.T) { suite.Run(t, new(rawSuite)) }

func (s *rawSuite) SetupTest() {
	sc, err := token.NewScanner(Raw)
	s.Require().NoError(err)
	s.scanner = sc
}

func (s *rawSuite) session(opts ...buzz.Option) *buzz.Session {
	sess := buzz.NewSession(context.Background(), append([]buzz.Option{buzz.WithEmbedded()}, opts...)...)
	s.T().Cleanup(func() { _ = sess.Close() })
	return sess
}

func (s *rawSuite) eval(literalSrc string) string {
	sess := s.session(buzz.WithScanner(s.scanner))
	s.Require().NoError(sess.Exec(context.Background(), "var v = "+literalSrc+";"))
	return sess.GetGlobal("v").AsString()
}

func (s *rawSuite) TestGoTextComesThroughByteExact() {
	s.Equal(goText, s.eval("r#\""+goText+"\"#"))
}

func (s *rawSuite) TestEachHazardOnItsOwn() {
	for _, text := range []string{"`", "{}", "{{x}}", `\d`, "${n}", `\n`, `\`,"a\n\tb\r\nc"} {
		s.Equal(text, s.eval(`r"`+text+`"`), text)
	}
}

func (s *rawSuite) TestEmptyString() {
	s.Equal("", s.eval(`r""`))
	s.Equal("", s.eval(`r#""#`))
}

func (s *rawSuite) TestLongerOpenerHoldsQuotesAndShorterRuns() {
	s.Equal(`say "hi" and "# done`, s.eval(`r##"say "hi" and "# done"##`))
	s.Equal(`a"b`, s.eval(`r#"a"b"#`))
}

func (s *rawSuite) TestIndentationIsKept() {
	s.Equal("\n    x\n  ", s.eval("r\"\n    x\n  \""))
}

func (s *rawSuite) TestFullWidthOpener() {
	hashes := strings.Repeat("#", 255)
	s.Equal(`a"#b`, s.eval("r"+hashes+`"a"#b"`+hashes))
}

func (s *rawSuite) TestIdentifiersStartingWithRAreUntouched() {
	sess := s.session(buzz.WithScanner(s.scanner))
	s.Require().NoError(sess.Exec(context.Background(), `
final rows = 1;
final r = 2;
final r2 = 3;
final sum = rows + r + r2;
`))
	s.EqualValues(6, sess.GetGlobal("sum").AsInt())
}

func (s *rawSuite) TestAnRThatOpensNothingDeclines() {
	for _, src := range []string{"r", "rows", "r#foo", "r#", "r##x", "r "} {
		_, n, err := Raw.Scan(src)
		s.Require().NoError(err, src)
		s.Zero(n, src)
	}
}

func (s *rawSuite) TestUnterminatedReportsItsPosition() {
	sess := s.session(buzz.WithScanner(s.scanner))
	err := sess.Exec(context.Background(), "var a = 1;\nvar v = r#\"abc\"")
	s.EqualError(err, "buzz: unterminated raw string at line 2:9")
}

func (s *rawSuite) TestTooManyHashesHasItsOwnMessage() {
	sess := s.session(buzz.WithScanner(s.scanner))
	hashes := strings.Repeat("#", 256)
	err := sess.Exec(context.Background(), "var v = r"+hashes+`"x"`+hashes+";")
	s.EqualError(err, "buzz: raw string opened with 256 #, the limit is 255 at line 1:9")
}

func (s *rawSuite) TestCheckerTypesItAsAString() {
	s.Require().NoError(s.session(buzz.WithScanner(s.scanner)).Exec(context.Background(), `final a: str = r"x";`))
	err := s.session(buzz.WithScanner(s.scanner)).Exec(context.Background(), `final a: int = r"x";`)
	s.Require().Error(err)
	s.Contains(err.Error(), "str")
}

func (s *rawSuite) TestPlainSessionStillRefusesIt() {
	err := s.session().Exec(context.Background(), `var v = r"x";`)
	s.ErrorContains(err, "expected ';' after statement, got string literal")
}
