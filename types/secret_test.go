package types

import (
	"bytes"
	"encoding"
	"encoding/gob"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/suite"
	"gopkg.in/yaml.v3"

	"github.com/egladman/magus/internal/json"
)

const probePlaintext = "ghp_probe_token_value"

// SecretSuite renders one Secret every way it can leave the process. fmt routes %v %+v %s
// %q %x %X through String and %#v through GoString; every other verb skips both and
// prints the struct, whose only field is a pointer, so an address.
type SecretSuite struct {
	suite.Suite
	secret Secret
}

func (s *SecretSuite) SetupTest() {
	s.secret = NewSecret(probePlaintext)
}

func TestSecretSuite(t *testing.T) {
	suite.Run(t, new(SecretSuite))
}

func (s *SecretSuite) noPlaintext(got string) {
	s.T().Helper()
	plainHex := hex.EncodeToString([]byte(probePlaintext))
	s.NotContains(got, probePlaintext)
	s.NotContains(got, plainHex)
	s.NotContains(got, strings.ToUpper(plainHex))
}

// redacts asserts verb renders a Secret and a *Secret exactly as it renders the mask.
func (s *SecretSuite) redacts(verb string) {
	s.T().Helper()
	want := fmt.Sprintf(verb, secretMask)
	s.Equal(want, fmt.Sprintf(verb, s.secret), "Secret under %s", verb)
	s.Equal(want, fmt.Sprintf(verb, &s.secret), "*Secret under %s", verb)
}

func (s *SecretSuite) TestPercentVRedacts() {
	s.redacts("%v")
	s.redacts("%-10v")
}

func (s *SecretSuite) TestPercentPlusVRedacts() {
	s.redacts("%+v")
}

func (s *SecretSuite) TestPercentSRedacts() {
	s.redacts("%s")
	s.redacts("%10s")
	s.redacts("%.2s")
}

func (s *SecretSuite) TestPercentQRedacts() {
	s.redacts("%q")
	s.redacts("%#q")
}

func (s *SecretSuite) TestPercentXRedactsAsHexOfTheMask() {
	s.redacts("%x")
	s.redacts("%X")
	s.redacts("%#x")
	s.redacts("% x")
}

func (s *SecretSuite) TestGoSyntaxRedacts() {
	s.Equal(secretMask, fmt.Sprintf("%#v", s.secret))
	s.Equal(secretMask, fmt.Sprintf("%#v", &s.secret))
}

func (s *SecretSuite) TestTypeVerbNamesTheType() {
	s.Equal("types.Secret", fmt.Sprintf("%T", s.secret))
	s.Equal("*types.Secret", fmt.Sprintf("%T", &s.secret))
}

// render takes the verb as a variable: vet rejects a constant format whose verb does not
// fit the argument, and these tests exercise exactly that.
func render(verb string, arg any) string { return fmt.Sprintf(verb, arg) }

func (s *SecretSuite) TestIntegerVerbsPrintNoPlaintext() {
	s.noPlaintext(fmt.Sprintf("%d", s.secret))
	s.noPlaintext(fmt.Sprintf("%08d", s.secret))
	s.noPlaintext(fmt.Sprintf("%b", s.secret))
	s.noPlaintext(fmt.Sprintf("%o", s.secret))
	s.noPlaintext(render("%U", s.secret))
	s.noPlaintext(render("%c", s.secret))
	s.noPlaintext(fmt.Sprintf("%d", &s.secret))
}

func (s *SecretSuite) TestBadVerbsPrintNoPlaintext() {
	s.noPlaintext(render("%e", s.secret))
	s.noPlaintext(render("%t", s.secret))
	s.noPlaintext(fmt.Sprintf("%p", s.secret))
	s.noPlaintext(fmt.Sprintf("%p", &s.secret))
}

func (s *SecretSuite) TestExportedFieldRedacts() {
	v := struct{ Token Secret }{s.secret}
	s.Equal("{***}", fmt.Sprintf("%v", v))
	s.Equal("{Token:***}", fmt.Sprintf("%+v", v))
	s.Equal("struct { Token types.Secret }{Token:***}", fmt.Sprintf("%#v", v))
	s.Equal(`{"***"}`, fmt.Sprintf("%q", v))
	s.noPlaintext(fmt.Sprintf("%d", v))
}

func (s *SecretSuite) TestExportedPointerFieldRedacts() {
	v := struct{ Token *Secret }{&s.secret}
	s.Equal("{***}", fmt.Sprintf("%v", v))
	s.Equal("struct { Token *types.Secret }{Token:***}", fmt.Sprintf("%#v", v))
}

func (s *SecretSuite) TestSliceRedacts() {
	s.Equal("[***]", fmt.Sprintf("%v", []Secret{s.secret}))
	s.Equal("[]types.Secret{***}", fmt.Sprintf("%#v", []Secret{s.secret}))
	s.Equal("[***]", fmt.Sprintf("%v", []*Secret{&s.secret}))
}

func (s *SecretSuite) TestMapRedacts() {
	m := map[string]Secret{"token": s.secret}
	s.Equal("map[token:***]", fmt.Sprintf("%v", m))
	s.Equal(`map[string]types.Secret{"token":***}`, fmt.Sprintf("%#v", m))
}

// TestUnexportedFieldPrintsNoPlaintext pins why Secret holds a pointer: fmt calls no
// method through an unexported field, so it prints the field's pointer as an address.
func (s *SecretSuite) TestUnexportedFieldPrintsNoPlaintext() {
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

func (leakySecret) String() string   { return secretMask }
func (leakySecret) GoString() string { return secretMask }

func (s *SecretSuite) TestValueBackedSecretLeaksThroughUnexportedField() {
	v := struct{ token leakySecret }{leakySecret{probePlaintext}}
	s.Contains(fmt.Sprintf("%v", v), probePlaintext)
	s.Contains(fmt.Sprintf("%#v", v), probePlaintext)
}

func (s *SecretSuite) TestErrorsRedact() {
	s.Equal("login with *** failed", fmt.Errorf("login with %v failed", s.secret).Error())
	s.Equal("push: token *** rejected", fmt.Errorf("push: %w", fmt.Errorf("token %s rejected", s.secret)).Error())
	s.Equal("first\ntoken ***", errors.Join(errors.New("first"), fmt.Errorf("token %v", s.secret)).Error())
}

func (s *SecretSuite) TestRecoveredPanicRedacts() {
	got := func() (got string) {
		defer func() { got = fmt.Sprint(recover()) }()
		panic(s.secret)
	}()
	s.Equal(secretMask, got)
}

type secretDoc struct {
	Token Secret `json:"token" yaml:"token" toml:"token"`
}

func (s *SecretSuite) TestJSONRedacts() {
	b, err := json.Marshal(secretDoc{Token: s.secret})
	s.Require().NoError(err)
	s.JSONEq(`{"token":"***"}`, string(b))

	b, err = json.Marshal(struct{ Token *Secret }{&s.secret})
	s.Require().NoError(err)
	s.JSONEq(`{"Token":"***"}`, string(b))
}

func (s *SecretSuite) TestYAMLRedacts() {
	b, err := yaml.Marshal(secretDoc{Token: s.secret})
	s.Require().NoError(err)
	s.Equal("token: '***'\n", string(b))
}

func (s *SecretSuite) TestTOMLRedacts() {
	b, err := toml.Marshal(secretDoc{Token: s.secret})
	s.Require().NoError(err)
	s.Equal("token = '***'\n", string(b))
}

func (s *SecretSuite) logged(handler func(*bytes.Buffer) slog.Handler) string {
	var buf bytes.Buffer
	slog.New(handler(&buf)).InfoContext(s.T().Context(), "using credential",
		slog.Any("token", s.secret), slog.Any("pointer", &s.secret), "bare", s.secret,
		slog.Any("holder", struct{ Token Secret }{s.secret}))
	return buf.String()
}

func (s *SecretSuite) TestSlogJSONRedacts() {
	got := s.logged(func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) })
	s.noPlaintext(got)
	s.Contains(got, `"token":"***","pointer":"***","bare":"***","holder":{"Token":"***"}`)
}

func (s *SecretSuite) TestSlogTextRedacts() {
	got := s.logged(func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) })
	s.noPlaintext(got)
	s.Contains(got, `token=*** pointer=*** bare=*** holder={Token:***}`)
}

// TestDecodingIsRefused pins the absent decoders: plaintext in a config file or a response
// body must not become a Secret by accident.
func (s *SecretSuite) TestDecodingIsRefused() {
	ptr := reflect.PointerTo(reflect.TypeFor[Secret]())
	s.False(ptr.Implements(reflect.TypeFor[encoding.TextUnmarshaler]()), "Secret must not implement UnmarshalText")
	s.False(ptr.Implements(reflect.TypeFor[interface{ UnmarshalJSON([]byte) error }]()), "Secret must not implement UnmarshalJSON")

	var d secretDoc
	s.Error(json.Unmarshal([]byte(`{"token":"`+probePlaintext+`"}`), &d))
	s.Error(yaml.Unmarshal([]byte("token: "+probePlaintext+"\n"), &d))
	s.Error(toml.Unmarshal([]byte(`token = "`+probePlaintext+`"`), &d))
	s.True(d.Token.IsZero())
}

func (s *SecretSuite) TestGobIsRefused() {
	s.Error(gob.NewEncoder(&bytes.Buffer{}).Encode(secretDoc{Token: s.secret}), "gob must refuse a type with no exported field")
}

func (s *SecretSuite) TestRevealReturnsThePlaintext() {
	s.Equal(probePlaintext, s.secret.Reveal())
	s.Empty(Secret{}.Reveal())
}

func (s *SecretSuite) TestZeroAndLenRevealNothing() {
	s.True(Secret{}.IsZero())
	s.False(NewSecret("x").IsZero())
	s.Equal(5, NewSecret("abcde").Len())
	s.Equal(NewSecret("a-very-long-credential").String(), NewSecret("short").String())
}
