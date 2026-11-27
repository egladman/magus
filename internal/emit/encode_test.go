package emit

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/spells"
)

type EncodeSuite struct{ suite.Suite }

func TestEncode(t *testing.T) { suite.Run(t, new(EncodeSuite)) }

func (s *EncodeSuite) encode(enc spells.Encoding, v string) (string, string) {
	out, _, msg := newEncoder().encode(enc, v)
	return out, msg
}

func (s *EncodeSuite) TestTheFirstMatchingEscapeWinsAtEachPosition() {
	enc := spells.Encoding{Name: "q", Open: "'", Close: "'", Escapes: []spells.Escape{
		{Text: "''", To: "!!"}, {Text: "'", To: `'\''`},
	}}
	out, msg := s.encode(enc, "a'b''c")
	s.Empty(msg)
	s.Equal(`'a'\''b!!c'`, out)
}

func (s *EncodeSuite) TestAnIdentityEscapePassesAControlThrough() {
	enc := spells.Encoding{Name: "q", Escapes: []spells.Escape{{Text: "\n", To: "\n"}}}
	out, msg := s.encode(enc, "a\nb")
	s.Empty(msg)
	s.Equal("a\nb", out)
}

func (s *EncodeSuite) TestAControlWithNoEscapeUsesTheControlForm() {
	for control, want := range map[string]string{
		`\x{hex2}`:   `\x01`,
		`\u{{hex2}}`: `\u{01}`,
		`\u{hex4}`:   `\u0001`,
		`\{oct3}`:    `\001`,
	} {
		out, msg := s.encode(spells.Encoding{Name: "q", Control: control}, "\x01")
		s.Empty(msg)
		s.Equal(want, out, control)
	}
}

func (s *EncodeSuite) TestAControlIsRefusedWhenTheEncodingHasNoForm() {
	for _, v := range []string{"\x01", "\x7f", "\t"} {
		_, msg := s.encode(spells.Encoding{Name: "q"}, v)
		s.Contains(msg, "control character")
	}
}

func (s *EncodeSuite) TestThePatternMustMatchTheWholeValue() {
	enc := spells.Encoding{Name: "ident", Pattern: `[a-z]+`}
	_, msg := s.encode(enc, "abc")
	s.Empty(msg)
	_, msg = s.encode(enc, "abc1")
	s.Contains(msg, "does not match")
	_, msg = s.encode(enc, "")
	s.Contains(msg, "does not match")
}

func (s *EncodeSuite) TestAPatternWithItsOwnAnchorsStillMatches() {
	_, msg := s.encode(spells.Encoding{Name: "ident", Pattern: `^[a-z]+$`}, "abc")
	s.Empty(msg)
}

func (s *EncodeSuite) TestAReservedValueIsRefused() {
	_, msg := s.encode(spells.Encoding{Name: "ident", Reserved: []string{"type"}}, "type")
	s.Contains(msg, "reserved")
}

func (s *EncodeSuite) TestInvalidUTF8IsRefused() {
	_, msg := s.encode(spells.Encoding{Name: "q"}, "a\xffb")
	s.Contains(msg, "UTF-8")
}

func (s *EncodeSuite) TestAPatternThatDoesNotCompileIsReported() {
	_, code, msg := newEncoder().encode(spells.Encoding{Name: "q", Pattern: "("}, "a")
	s.Equal(emitUnknownSpec, code)
	s.Contains(msg, "does not compile")
}

func (s *EncodeSuite) TestMultiByteTextIsCopied() {
	out, msg := s.encode(spells.Encoding{Name: "q", Open: `"`, Close: `"`}, "héllo")
	s.Empty(msg)
	s.Equal(`"héllo"`, out)
}

func (s *EncodeSuite) TestKindNamesAndStringForms() {
	s.Equal("an int", kindName(3))
	s.Equal("a double", kindName(1.5))
	s.Equal("a bool", kindName(true))
	s.Equal("a list", kindName([]any{}))
	s.Equal("a map", kindName(map[string]any{}))

	for v, want := range map[any]string{"x": "x", 7: "7", int64(-2): "-2", true: "true", 2.5: "2.5"} {
		got, ok := stringForm(v)
		s.True(ok)
		s.Equal(want, got)
	}
	_, ok := stringForm([]any{})
	s.False(ok)
}
