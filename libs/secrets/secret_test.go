package secrets

import (
	"bytes"
	"encoding"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/suite"
	"gopkg.in/yaml.v3"
)

const probePlaintext = "ghp_probe_token_value"

type secretSuite struct {
	suite.Suite
	secret Secret
}

func (s *secretSuite) SetupTest() {
	s.secret = New(probePlaintext)
}

func TestSecret(t *testing.T) { suite.Run(t, new(secretSuite)) }

func (s *secretSuite) noPlaintext(got string) {
	s.T().Helper()
	plainHex := hex.EncodeToString([]byte(probePlaintext))
	s.NotContains(got, probePlaintext)
	s.NotContains(got, plainHex)
	s.NotContains(got, strings.ToUpper(plainHex))
}

// secretRedacts asserts verb renders a Secret and a *Secret exactly as it renders the mask.
// fmt routes %v %+v %s %q %x %X through String and %#v through GoString; every other verb
// skips both and prints the struct, whose only field is a pointer, so an address.
func (s *secretSuite) secretRedacts(verb string) {
	s.T().Helper()
	want := fmt.Sprintf(verb, mask)
	s.Equal(want, fmt.Sprintf(verb, s.secret), "Secret under %s", verb)
	s.Equal(want, fmt.Sprintf(verb, &s.secret), "*Secret under %s", verb)
}

func (s *secretSuite) TestPercentVRedacts() {
	s.secretRedacts("%v")
	s.secretRedacts("%-10v")
}

func (s *secretSuite) TestPercentPlusVRedacts() {
	s.secretRedacts("%+v")
}

func (s *secretSuite) TestPercentSRedacts() {
	s.secretRedacts("%s")
	s.secretRedacts("%10s")
	s.secretRedacts("%.2s")
}

func (s *secretSuite) TestPercentQRedacts() {
	s.secretRedacts("%q")
	s.secretRedacts("%#q")
}

func (s *secretSuite) TestPercentXRedactsAsHexOfTheMask() {
	s.secretRedacts("%x")
	s.secretRedacts("%X")
	s.secretRedacts("%#x")
	s.secretRedacts("% x")
}

func (s *secretSuite) TestGoSyntaxRedacts() {
	s.Equal(mask, fmt.Sprintf("%#v", s.secret))
	s.Equal(mask, fmt.Sprintf("%#v", &s.secret))
}

func (s *secretSuite) TestTypeVerbNamesTheType() {
	s.Equal("secrets.Secret", fmt.Sprintf("%T", s.secret))
	s.Equal("*secrets.Secret", fmt.Sprintf("%T", &s.secret))
}

// render takes the verb as a variable: vet rejects a constant format whose verb does not
// fit the argument, and these tests exercise exactly that.
func render(verb string, arg any) string { return fmt.Sprintf(verb, arg) }

func (s *secretSuite) TestIntegerVerbsPrintNoPlaintext() {
	s.noPlaintext(fmt.Sprintf("%d", s.secret))
	s.noPlaintext(fmt.Sprintf("%08d", s.secret))
	s.noPlaintext(fmt.Sprintf("%b", s.secret))
	s.noPlaintext(fmt.Sprintf("%o", s.secret))
	s.noPlaintext(render("%U", s.secret))
	s.noPlaintext(render("%c", s.secret))
	s.noPlaintext(fmt.Sprintf("%d", &s.secret))
}

func (s *secretSuite) TestBadVerbsPrintNoPlaintext() {
	s.noPlaintext(render("%e", s.secret))
	s.noPlaintext(render("%t", s.secret))
	s.noPlaintext(render("%p", s.secret))
	s.noPlaintext(fmt.Sprintf("%p", &s.secret))
}

func (s *secretSuite) TestExportedFieldRedacts() {
	v := struct{ Token Secret }{s.secret}
	s.Equal("{***}", fmt.Sprintf("%v", v))
	s.Equal("{Token:***}", fmt.Sprintf("%+v", v))
	s.Equal("struct { Token secrets.Secret }{Token:***}", fmt.Sprintf("%#v", v))
	s.Equal(`{"***"}`, fmt.Sprintf("%q", v))
	s.noPlaintext(fmt.Sprintf("%d", v))
}

func (s *secretSuite) TestExportedPointerFieldRedacts() {
	v := struct{ Token *Secret }{&s.secret}
	s.Equal("{***}", fmt.Sprintf("%v", v))
	s.Equal("struct { Token *secrets.Secret }{Token:***}", fmt.Sprintf("%#v", v))
}

func (s *secretSuite) TestSliceRedacts() {
	s.Equal("[***]", fmt.Sprintf("%v", []Secret{s.secret}))
	s.Equal("[]secrets.Secret{***}", fmt.Sprintf("%#v", []Secret{s.secret}))
	s.Equal("[***]", fmt.Sprintf("%v", []*Secret{&s.secret}))
}

func (s *secretSuite) TestMapRedacts() {
	m := map[string]Secret{"token": s.secret}
	s.Equal("map[token:***]", fmt.Sprintf("%v", m))
	s.Equal(`map[string]secrets.Secret{"token":***}`, fmt.Sprintf("%#v", m))
}

// TestSecretUnexportedFieldPrintsNoPlaintext pins why Secret holds a pointer: fmt calls no
// method through an unexported field, so it prints the field's pointer as an address.
func (s *secretSuite) TestUnexportedFieldPrintsNoPlaintext() {
	v := struct {
		Name  string
		token Secret
	}{"registry", s.secret}
	for _, got := range []string{fmt.Sprintf("%v", v), fmt.Sprintf("%+v", v), fmt.Sprintf("%#v", v), render("%s", v)} {
		s.noPlaintext(got)
		s.Contains(got, "registry")
		s.Contains(got, "0x")
	}
}

// leakySecret holds its plaintext by value: the shape Secret must not take.
type leakySecret struct{ s string }

func (leakySecret) String() string   { return mask }
func (leakySecret) GoString() string { return mask }

func (s *secretSuite) TestValueBackedFieldWouldLeak() {
	v := struct{ token leakySecret }{leakySecret{probePlaintext}}
	s.Contains(fmt.Sprintf("%v", v), probePlaintext)
	s.Contains(fmt.Sprintf("%#v", v), probePlaintext)
}

func (s *secretSuite) TestErrorsRedact() {
	s.Equal("login with *** failed", fmt.Errorf("login with %v failed", s.secret).Error())
	s.Equal("push: token *** rejected", fmt.Errorf("push: %w", fmt.Errorf("token %s rejected", s.secret)).Error())
	s.Equal("first\ntoken ***", errors.Join(errors.New("first"), fmt.Errorf("token %v", s.secret)).Error())
}

func (s *secretSuite) TestRecoveredPanicRedacts() {
	got := func() (got string) {
		defer func() { got = fmt.Sprint(recover()) }()
		panic(s.secret)
	}()
	s.Equal(mask, got)
}

type secretDoc struct {
	Token Secret `json:"token" yaml:"token" toml:"token"`
}

func (s *secretSuite) TestJSONRedacts() {
	b, err := json.Marshal(secretDoc{Token: s.secret})
	s.Require().NoError(err)
	s.JSONEq(`{"token":"***"}`, string(b))

	b, err = json.Marshal(struct {
		Token *Secret `json:"token"`
	}{&s.secret})
	s.Require().NoError(err)
	s.JSONEq(`{"token":"***"}`, string(b))
}

func (s *secretSuite) TestYAMLRedacts() {
	b, err := yaml.Marshal(secretDoc{Token: s.secret})
	s.Require().NoError(err)
	s.Equal("token: '***'\n", string(b))
}

func (s *secretSuite) TestTOMLRedacts() {
	b, err := toml.Marshal(secretDoc{Token: s.secret})
	s.Require().NoError(err)
	s.Equal("token = '***'\n", string(b))
}

func (s *secretSuite) secretLogged(handler func(*bytes.Buffer) slog.Handler) string {
	var buf bytes.Buffer
	slog.New(handler(&buf)).InfoContext(s.T().Context(), "using credential",
		slog.Any("token", s.secret), slog.Any("pointer", &s.secret),
		slog.Any("holder", struct{ Token Secret }{s.secret}))
	return buf.String()
}

func (s *secretSuite) TestSlogJSONRedacts() {
	got := s.secretLogged(func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) })
	s.noPlaintext(got)
	s.Contains(got, `"token":"***","pointer":"***","holder":{"Token":"***"}`)
}

func (s *secretSuite) TestSlogTextRedacts() {
	got := s.secretLogged(func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) })
	s.noPlaintext(got)
	s.Contains(got, `token=*** pointer=*** holder={Token:***}`)
}

// TestSecretDecodingIsRefused pins the absent decoders: plaintext in a config file or a
// response body must not become a Secret by accident.
func (s *secretSuite) TestDecodingIsRefused() {
	ptr := reflect.PointerTo(reflect.TypeFor[Secret]())
	s.False(ptr.Implements(reflect.TypeFor[encoding.TextUnmarshaler]()), "Secret must not implement UnmarshalText")
	s.False(ptr.Implements(reflect.TypeFor[interface{ UnmarshalJSON([]byte) error }]()), "Secret must not implement UnmarshalJSON")

	var d secretDoc
	s.Error(json.Unmarshal([]byte(`{"token":"`+probePlaintext+`"}`), &d))
	s.Error(yaml.Unmarshal([]byte("token: "+probePlaintext+"\n"), &d))
	s.Error(toml.Unmarshal([]byte(`token = "`+probePlaintext+`"`), &d))
	s.True(d.Token.IsZero())
}

func (s *secretSuite) TestGobIsRefused() {
	s.Error(gob.NewEncoder(&bytes.Buffer{}).Encode(secretDoc{Token: s.secret}), "gob must refuse a type with no exported field")
}

func (s *secretSuite) TestRevealReturnsThePlaintext() {
	s.Equal(probePlaintext, s.secret.Reveal())
	s.Empty(Secret{}.Reveal())
}

func (s *secretSuite) TestZeroAndLenRevealNothing() {
	s.True(Secret{}.IsZero())
	s.False(New("x").IsZero())
	s.Equal(5, New("abcde").Len())
	s.Equal(New("a-very-long-credential").String(), New("short").String())
}
