package bindings

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/proc/spawn"
)

type emitMagusLogSuite struct {
	suite.Suite
	process  bytes.Buffer
	stdout   bytes.Buffer
	stderr   bytes.Buffer
	previous *slog.Logger
}

func TestEmitMagusLog(t *testing.T) { suite.Run(t, new(emitMagusLogSuite)) }

func (s *emitMagusLogSuite) SetupTest() {
	s.process.Reset()
	s.stdout.Reset()
	s.stderr.Reset()
	s.previous = slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&s.process, &slog.HandlerOptions{Level: slog.LevelInfo})))
}

func (s *emitMagusLogSuite) TearDownTest() { slog.SetDefault(s.previous) }

func (s *emitMagusLogSuite) captured() context.Context {
	return spawn.WithOutputWriters(context.Background(), &s.stdout, &s.stderr)
}

func (s *emitMagusLogSuite) TestALogInATargetLandsInItsCapturedStderr() {
	emitMagusLog(s.captured(), slog.LevelError, "figure \"x\": gone", map[string]string{"b": "2", "a": "1"})

	s.Equal("level=ERROR msg=\"figure \\\"x\\\": gone\" a=1 b=2\n", s.stderr.String())
	s.Empty(s.stdout.String())
	s.Empty(s.process.String())
}

func (s *emitMagusLogSuite) TestALogBelowTheProcessLevelIsDroppedInATarget() {
	emitMagusLog(s.captured(), slog.LevelDebug, "quiet", nil)

	s.Empty(s.stderr.String())
	s.Empty(s.process.String())
}

func (s *emitMagusLogSuite) TestALogOutsideATargetGoesToTheProcessLogger() {
	emitMagusLog(context.Background(), slog.LevelWarn, "loud", map[string]string{"k": "v"})

	s.Contains(s.process.String(), "level=WARN msg=loud k=v")
	s.Empty(s.stderr.String())
}
