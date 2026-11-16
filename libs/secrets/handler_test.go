package secrets

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type HandlerSuite struct {
	suite.Suite
	ctx  context.Context //nolint:containedctx // the suite's per-test context
	sink bytes.Buffer
}

func TestHandler(t *testing.T) { suite.Run(t, new(HandlerSuite)) }

func (s *HandlerSuite) SetupTest() {
	s.sink.Reset()
	r := NewResolver()
	s.ctx = ContextWithResolver(s.T().Context(), r)
}

// readEnv sets an environment variable to tok and reads it through the suite's resolver,
// so tok is registered for redaction.
func (s *HandlerSuite) readEnv(tok string) {
	s.T().Helper()
	s.T().Setenv("SECRETS_TEST_LOG_TOKEN", tok)
	_, err := ResolverFromContext(s.ctx).Read(s.ctx, "SECRETS_TEST_LOG_TOKEN")
	s.Require().NoError(err)
}

func (s *HandlerSuite) jsonLogger() *slog.Logger {
	return slog.New(NewRedactingHandler(slog.NewJSONHandler(&s.sink, &slog.HandlerOptions{Level: slog.LevelDebug})))
}

// Redaction that lived in one handler's print funnel would let a JSON handler print an
// argv attr untouched, and argv arrives as a []string, not a string.
func (s *HandlerSuite) TestCoversTheMessageAndAStringSliceAttr() {
	s.readEnv("ghp_never_json_me")
	s.jsonLogger().DebugContext(s.ctx, "run.exec tok=ghp_never_json_me",
		"cmd", "sh", "args", []string{"-p", "ghp_never_json_me"}, "dir", ".")

	s.NotContains(s.sink.String(), "ghp_never_json_me", "neither message nor attrs may carry it")
	// The SHAPE matters too: argv must stay a JSON array for anything parsing the stream.
	s.Contains(s.sink.String(), `"args":["-p","`+mask+`"]`)
}

// fmt renders a []byte as decimal bytes while encoding/json base64s it, so a redactor
// comparing against the fmt rendering would see no match and ship a recoverable token.
func (s *HandlerSuite) TestCoversGroupErrorAndByteAttrs() {
	const tok = "ghp_never_nested_me"
	s.readEnv(tok)
	s.jsonLogger().DebugContext(s.ctx, "wrapped",
		slog.Group("cmd", slog.String("argv", "-p "+tok)),
		slog.Any("err", errors.New("login failed: "+tok)),
		slog.Any("raw", []byte(tok)))

	s.NotContains(s.sink.String(), tok)
	s.NotContains(s.sink.String(), base64.StdEncoding.EncodeToString([]byte(tok)))
	s.Contains(s.sink.String(), mask)
}

func (s *HandlerSuite) TestIsAPassthroughWithoutAResolver() {
	h := NewRedactingHandler(slog.NewTextHandler(&s.sink, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.New(h).DebugContext(context.Background(), "plain", "cmd", "go build")
	s.Contains(s.sink.String(), "go build")
	s.NotContains(s.sink.String(), mask)
}

// WithAttrs and WithGroup must return a wrapper, not the bare inner handler: a logger that
// calls either would otherwise escape redaction for every later record.
func (s *HandlerSuite) TestWithAttrsStaysWrapped() {
	s.readEnv("group-token-value")
	base := NewRedactingHandler(slog.NewTextHandler(&s.sink, &slog.HandlerOptions{Level: slog.LevelDebug}))

	withAttrs := base.WithAttrs([]slog.Attr{slog.String("preset", "group-token-value")})
	s.Require().IsType(redactingHandler{}, withAttrs)
	slog.New(withAttrs).DebugContext(s.ctx, "m")
	s.NotContains(s.sink.String(), "group-token-value")
}

func (s *HandlerSuite) TestWithGroupStaysWrapped() {
	s.readEnv("group-token-value")
	base := NewRedactingHandler(slog.NewTextHandler(&s.sink, &slog.HandlerOptions{Level: slog.LevelDebug}))

	grouped := base.WithGroup("g")
	s.Require().IsType(redactingHandler{}, grouped)
	slog.New(grouped).DebugContext(s.ctx, "m", "tok", "group-token-value")
	s.NotContains(s.sink.String(), "group-token-value")
	s.True(base.Enabled(s.ctx, slog.LevelDebug))
	s.Equal(base, base.WithGroup(""), "an empty group returns the receiver, as slog.Handler requires")
}

// hidingCreds conceals its credential in String() but still marshals the field: the shape
// that defeats an fmt-only comparison, and the reason redactValue checks JSON too.
type hidingCreds struct{ Token string }

func (hidingCreds) String() string { return "creds{redacted}" }

const carrierToken = "ghp_every_carrier_token"

// logCarrier logs val as a KindAny attr and asserts neither the token nor its base64 form
// survives, while something shows the value was masked.
func (s *HandlerSuite) logCarrier(val any) {
	s.T().Helper()
	s.readEnv(carrierToken)
	s.jsonLogger().DebugContext(s.ctx, "carrier", "v", val)

	out := s.sink.String()
	s.NotContains(out, carrierToken)
	s.NotContains(out, base64.StdEncoding.EncodeToString([]byte(carrierToken)))
	// A []byte attr is masked before encoding/json sees it, so the wire carries base64 of
	// the mask rather than its characters.
	maskB64 := base64.StdEncoding.EncodeToString([]byte(mask))
	s.True(strings.Contains(out, mask) || strings.Contains(out, maskB64), "nothing shows the value was masked: %s", out)
}

func (s *HandlerSuite) TestCoversAPlainStruct() {
	s.logCarrier(struct{ Token string }{Token: carrierToken})
}

func (s *HandlerSuite) TestCoversAStructWhoseStringHidesTheField() {
	s.logCarrier(hidingCreds{Token: carrierToken})
}

func (s *HandlerSuite) TestCoversRawBytes() {
	s.logCarrier([]byte(carrierToken))
}

func (s *HandlerSuite) TestCoversAStringSlice() {
	s.logCarrier([]string{"-p", carrierToken})
}

func (s *HandlerSuite) TestCoversANestedMap() {
	s.logCarrier(map[string]any{"auth": map[string]string{"token": carrierToken}})
}
