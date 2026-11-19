package bindings

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/libs/secrets"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

const testSecret = "hunter2-token"

// testContext is a context carrying a resolver: capturing a command's output
// refuses to run without one, so a test that captures needs this.
func testContext() context.Context {
	return testkit.WithEnvResolver(context.Background())
}

type emitMagusLogSuite struct {
	suite.Suite
	process  bytes.Buffer
	previous *slog.Logger
	resolver *secrets.Resolver
}

func TestEmitMagusLog(t *testing.T) { suite.Run(t, new(emitMagusLogSuite)) }

func (s *emitMagusLogSuite) SetupTest() {
	s.process.Reset()
	s.previous = slog.Default()
	// The display's text handler, redacting as cmd/magus installs it.
	slog.SetDefault(slog.New(secrets.NewRedactingHandler(slog.NewTextHandler(&s.process, &slog.HandlerOptions{
		Level: slog.LevelInfo,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	}))))
	s.resolver = testkit.EnvResolver(s.T())
	s.Require().NoError(s.resolver.Register(secrets.New(testSecret)))
}

func (s *emitMagusLogSuite) TearDownTest() { slog.SetDefault(s.previous) }

// inTarget runs body as a passing target step of a cache whose log.format is
// format, with output collapsed as on a default run. It returns the step's
// captured output and the step's context, which outlives the step.
func (s *emitMagusLogSuite) inTarget(format string, body func(context.Context)) (string, context.Context) {
	root := s.T().TempDir()
	c, err := cache.Open(s.T().Context(), filepath.Join(s.T().TempDir(), ".magus"),
		cache.WithLog(format, slog.LevelInfo), cache.WithLogger(slog.New(slog.DiscardHandler)),
		cache.WithCollapse(true))
	s.Require().NoError(err)
	ctx := secrets.WithResolver(s.T().Context(), s.resolver)
	var logPath string
	var stepCtx context.Context
	_, err = c.Run(ctx, cache.Step{ProjectPath: "p", Target: "t", WorkspaceRoot: root, NoCache: true},
		func(ctx context.Context) error {
			logPath, stepCtx = types.CaptureLog(ctx), ctx
			body(ctx)
			return nil
		})
	s.Require().NoError(err)
	s.Require().NotEmpty(logPath)
	data, err := os.ReadFile(logPath)
	s.Require().NoError(err)
	return string(data), stepCtx
}

func (s *emitMagusLogSuite) TestALogInATargetIsCapturedInTheCachesLogFormat() {
	captured, _ := s.inTarget("json", func(ctx context.Context) {
		emitMagusLog(ctx, slog.LevelWarn, "gone", map[string]string{"b": "2", "a": "1"})
	})

	s.JSONEq(`{"level":"WARN","msg":"gone","a":"1","b":"2"}`, captured)
}

func (s *emitMagusLogSuite) TestALogInATargetIsCapturedAsTextUnderTheTextFormat() {
	captured, _ := s.inTarget("text", func(ctx context.Context) {
		emitMagusLog(ctx, slog.LevelError, "gone", map[string]string{"b": "2", "a": "1"})
	})

	s.Equal("level=ERROR msg=gone a=1 b=2\n", captured)
}

func (s *emitMagusLogSuite) TestADebugLogInATargetIsCapturedButNotShown() {
	captured, _ := s.inTarget("text", func(ctx context.Context) {
		emitMagusLog(ctx, slog.LevelDebug, "quiet", nil)
	})

	s.Equal("level=DEBUG msg=quiet\n", captured)
	s.Empty(s.process.String())
}

func (s *emitMagusLogSuite) TestAWarningInAPassingTargetReachesTheConsole() {
	captured, _ := s.inTarget("text", func(ctx context.Context) {
		emitMagusLog(ctx, slog.LevelWarn, "loud", map[string]string{"k": "v"})
	})

	s.Equal("level=WARN msg=loud k=v\n", s.process.String())
	s.Equal("level=WARN msg=loud k=v\n", captured)
}

func (s *emitMagusLogSuite) TestASecretInAFieldKeyIsRedactedOnTheConsoleAndInTheCapture() {
	captured, _ := s.inTarget("json", func(ctx context.Context) {
		emitMagusLog(ctx, slog.LevelWarn, "leak", map[string]string{testSecret: "v"})
	})

	s.Equal("level=WARN msg=leak ***=v\n", s.process.String())
	s.JSONEq(`{"level":"WARN","msg":"leak","***":"v"}`, captured)
}

func (s *emitMagusLogSuite) TestASecretInAFieldKeyIsRedactedOutsideATarget() {
	ctx := secrets.WithResolver(s.T().Context(), s.resolver)

	emitMagusLog(ctx, slog.LevelWarn, "leak", map[string]string{testSecret: "v"})

	s.Equal("level=WARN msg=leak ***=v\n", s.process.String())
}

func (s *emitMagusLogSuite) TestALogAfterItsStepEndedSaysSoOnTheConsole() {
	captured, stepCtx := s.inTarget("text", func(context.Context) {})

	emitMagusLog(stepCtx, slog.LevelWarn, "late", nil)

	s.Empty(captured)
	s.Equal(`level=WARN msg=late capture="the target's step has ended, so its captured output is closed"`+"\n",
		s.process.String())
}

func (s *emitMagusLogSuite) TestALogOutsideATargetGoesToTheProcessLoggerOnly() {
	emitMagusLog(testContext(), slog.LevelWarn, "loud", map[string]string{"k": "v"})

	s.Equal("level=WARN msg=loud k=v\n", s.process.String())
}
