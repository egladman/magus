package interactive

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/log/attr"
)

type HintSuite struct {
	suite.Suite
	buf  *bytes.Buffer
	prev *slog.Logger
}

func TestHintSuite(t *testing.T) { suite.Run(t, new(HintSuite)) }

func (s *HintSuite) SetupTest() {
	s.prev = slog.Default()
	s.buf = s.capture(slog.LevelInfo)
}

func (s *HintSuite) TearDownTest() {
	slog.SetDefault(s.prev)
	SetHintsEnabled(true)
}

// capture logs the default slog handler at level to a fresh buffer.
func (s *HintSuite) capture(level slog.Level) *bytes.Buffer {
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})))
	return &buf
}

func (s *HintSuite) TestHintLogsAHintRecordOncePerInvocation() {
	ctx := WithHints(s.T().Context())
	Hint(ctx, "pass --force to replace it", attr.Component("spell"))
	Hint(ctx, "pass --force to replace it")
	s.Equal("level=INFO msg=\"pass --force to replace it\" component=spell notice=hint\n", s.buf.String())
}

// A server serves many invocations, and each one is taught again.
func (s *HintSuite) TestEachInvocationShowsTheHintAgain() {
	Hint(WithHints(s.T().Context()), "pass --force to replace it")
	Hint(WithHints(s.T().Context()), "pass --force to replace it")
	s.Equal(2, bytes.Count(s.buf.Bytes(), []byte("notice=hint")))
}

func (s *HintSuite) TestAContextWithoutASetShowsEveryHint() {
	Hint(context.Background(), "pass --force to replace it")
	Hint(context.Background(), "pass --force to replace it")
	s.Equal(2, bytes.Count(s.buf.Bytes(), []byte("notice=hint")))
}

func (s *HintSuite) TestHintLogsNothingWithHintsDisabled() {
	ctx := WithHints(s.T().Context())
	SetHintsEnabled(false)
	Hint(ctx, "try `magus run` instead")
	s.Empty(s.buf.String())

	SetHintsEnabled(true)
	Hint(ctx, "try `magus run` instead")
	s.Contains(s.buf.String(), "notice=hint", "a hint withheld while disabled is still shown once enabled")
}

// A handler that refuses info leaves the showing unspent for a later display.
func (s *HintSuite) TestHintKeepsItsShowingWhenTheHandlerRefusesIt() {
	ctx := WithHints(s.T().Context())
	s.capture(slog.LevelError)
	Hint(ctx, "read that failure with magus query output")

	buf := s.capture(slog.LevelInfo)
	Hint(ctx, "read that failure with magus query output")
	s.Contains(buf.String(), "notice=hint")
}
