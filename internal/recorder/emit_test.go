package recorder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/egladman/magus/libs/secrets"
	"github.com/egladman/magus/types"
)

type EmitSuite struct{ suite.Suite }

func TestEmit(t *testing.T) { suite.Run(t, new(EmitSuite)) }

// collector is the whole Sink double: a closure over a slice.
type collector struct {
	mu  sync.Mutex
	got []journal.Record
}

func (c *collector) Handle(_ context.Context, r journal.Record) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got = append(c.got, r)
}

func (s *EmitSuite) TestFileSinkAppendsJSONL() {
	var buf bytes.Buffer
	fs := NewFileSink(&buf)
	ctx := WithSinks(testCtx(s.T()), fs.Handle)

	Emit(ctx, journal.Output{Stream: journal.Stdout, Project: "web", Target: "build", Text: "line one"})
	Emit(ctx, journal.Run{Status: journal.StatusPass, Ref: "outabc", Duration: 5 * time.Millisecond})
	fs.Flush()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	s.Require().Len(lines, 2)
	var first journal.Record
	s.Require().NoError(first.UnmarshalJSON([]byte(lines[0])))
	s.False(first.Time.IsZero(), "Emit stamps a time")
	out, ok := first.Output()
	s.Require().True(ok)
	s.Equal(journal.Output{Stream: journal.Stdout, Project: "web", Target: "build", Text: "line one"}, out)
}

func (s *EmitSuite) TestEmitStampsTheInvocationFromContext() {
	var c collector
	ctx := WithInvocationID(WithSinks(testCtx(s.T()), c.Handle), "inv9")
	Emit(ctx, journal.Output{Text: "hi"})
	s.Require().Len(c.got, 1)
	s.Equal("inv9", c.got[0].Invocation)
}

func (s *EmitSuite) TestEmitWithNoSinkDoesNothing() {
	s.NotPanics(func() { Emit(testCtx(s.T()), journal.Output{Text: "x"}) })
}

func (s *EmitSuite) TestWithSinksSkipsANilSinkAndKeepsOrder() {
	var order []string
	first := func(context.Context, journal.Record) { order = append(order, "first") }
	second := func(context.Context, journal.Record) { order = append(order, "second") }
	Emit(WithSinks(testCtx(s.T()), first, nil, second), journal.Finished{Status: journal.StatusPass})
	s.Equal([]string{"first", "second"}, order)
}

func (s *EmitSuite) TestNewInvocationIDIsUniqueAndValid() {
	a, b := NewInvocationID(), NewInvocationID()
	s.NotEqual(a, b)
	s.True(LooksLikeInvocationID(a))
	s.True(journal.ValidInvocationID(a))
}

// A pasted id kept out of the graph grammar reads as a missing run, not
// matches: 0. Only shapes that must never route are refused here.
func (s *EmitSuite) TestLooksLikeInvocationID() {
	for _, id := range []string{"invmsm3vcou1", "inv123", "invabc0z9"} {
		s.True(LooksLikeInvocationID(id), "%q", id)
	}
	for _, id := range []string{"inv", "INV123", "inv-123", "inv 123", "kind:spell", "out1a2b3c", ""} {
		s.False(LooksLikeInvocationID(id), "%q", id)
	}
}

func (s *EmitSuite) TestWithStepAndStepFromContext() {
	_, _, ok := StepFromContext(s.T().Context())
	s.False(ok, "a bare context is not inside a captured step")

	project, target, ok := StepFromContext(WithStep(s.T().Context(), "api", "build"))
	s.True(ok)
	s.Equal("api", project)
	s.Equal("build", target)
}

// TestFileSinkFlushesEverythingButOutput pins what makes `magus events
// --follow` fire: a follower tails this file. Output stays buffered on
// purpose, as the one high-volume kind a follower opts into.
func (s *EmitSuite) TestFileSinkFlushesEverythingButOutput() {
	var buf bytes.Buffer
	ctx := WithSinks(testCtx(s.T()), NewFileSink(&buf).Handle)

	Emit(ctx, journal.Output{Text: "buffered"})
	s.Require().Empty(buf.String(), "output must not force a flush")

	Emit(ctx, journal.Run{Project: "api", Target: "build", Status: journal.StatusPass})
	s.Contains(buf.String(), `"kind":"result"`, "a result must reach a follower before the run ends")
	s.Contains(buf.String(), "buffered", "the flush carries the buffered output with it")
}

type failingWriter struct{ writes int }

func (w *failingWriter) Write([]byte) (int, error) {
	w.writes++
	return 0, errors.New("disk full")
}

func (s *EmitSuite) TestFileSinkSaysOnceThatItStopped() {
	var logged bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	defer slog.SetDefault(prev)

	w := &failingWriter{}
	ctx := WithSinks(testCtx(s.T()), NewFileSink(w).Handle)
	Emit(ctx, journal.Finished{Status: journal.StatusPass})
	Emit(ctx, journal.Finished{Status: journal.StatusFail})
	s.Equal(1, strings.Count(logged.String(), "journal.write"), "a failed write is said once")
	s.Equal(1, w.writes, "nothing is written after the failure")
}

// TestEveryFreeTextFieldIsRedacted fails on a body's string field that Emit
// neither redacts nor lists here as an enumeration.
func (s *EmitSuite) TestEveryFreeTextFieldIsRedacted() {
	redacted := map[string]bool{
		"Started.Command.Arguments": true,
		"Exec.Command":              true,
		"Output.Text":               true,
		"Run.Error":                 true,
		"Secret.Text":               true,
	}
	enumerations := map[string]bool{
		"Started.Command.Cwd":      true,
		"Started.Command.Trigger":  true,
		"Started.Version":          true,
		"Finished.Status":          true,
		"Exec.Project":             true,
		"Exec.Target":              true,
		"Output.Project":           true,
		"Output.Target":            true,
		"Output.Stream":            true,
		"Run.Project":              true,
		"Run.Target":               true,
		"Run.Level":                true,
		"Run.Status":               true,
		"Run.Ref":                  true,
		"Run.CacheKey":             true,
		"Run.DiagnosticCode":       true,
		"Run.Sizing":               true,
		"Scope.Undeclared.Project": true,
		"Scope.Undeclared.Files":   true,
		"Scope.Undeclared.Inputs":  true,
		"Secret.Project":           true,
		"Secret.Target":            true,
	}
	bodies := []any{journal.Started{}, journal.Finished{}, journal.Exec{}, journal.Output{}, journal.Run{}, journal.Scope{}, journal.Secret{}}
	for _, body := range bodies {
		t := reflect.TypeOf(body)
		for _, path := range stringFields(t, t.Name()) {
			s.True(redacted[path] || enumerations[path], "%s is a string no one redacts: redact it in Emit or list it as an enumeration", path)
		}
	}

	const credential = "sk-live-must-not-be-persisted"
	s.T().Setenv("RECORDER_TEST_TOKEN", credential)
	res := secrets.NewResolver()
	ctx := secrets.WithResolver(s.T().Context(), res)
	_, err := res.Read(ctx, "RECORDER_TEST_TOKEN")
	s.Require().NoError(err)
	var buf bytes.Buffer
	fs := NewFileSink(&buf)
	ctx = WithSinks(ctx, fs.Handle)
	Emit(ctx, journal.Started{Command: journal.Command{Arguments: []string{"run", "--token=" + credential}}})
	Emit(ctx, journal.Exec{Command: "curl -H " + credential})
	Emit(ctx, journal.Output{Text: credential})
	Emit(ctx, journal.Run{Error: "rejected " + credential})
	Emit(ctx, journal.Secret{Text: credential})
	fs.Flush()
	s.NotContains(buf.String(), credential)
	s.Equal(5, strings.Count(buf.String(), "\n"))
}

// stringFields names every string and []string field under t, by path.
func stringFields(t reflect.Type, prefix string) []string {
	var out []string
	for i := range t.NumField() {
		f := t.Field(i)
		path := fmt.Sprintf("%s.%s", prefix, f.Name)
		ft := f.Type
		if ft.Kind() == reflect.Slice {
			ft = ft.Elem()
		}
		switch ft.Kind() {
		case reflect.String:
			out = append(out, path)
		case reflect.Struct:
			if ft != reflect.TypeFor[time.Time]() {
				out = append(out, stringFields(ft, path)...)
			}
		}
	}
	return out
}

// BenchmarkEmitOutput measures the hot path: one output record to a file
// sink, the per-subprocess-line cost.
func BenchmarkEmitOutput(b *testing.B) {
	ctx := WithInvocationID(WithSinks(bgCtx(),NewFileSink(io.Discard).Handle), "invbench")
	out := journal.Output{Stream: journal.Stdout, Project: "web", Target: "build", Text: "some subprocess output line here"}
	b.ReportAllocs()
	for b.Loop() {
		Emit(ctx, out)
	}
}

// BenchmarkEmitResult is the per-target cost, and the counterpart to
// BenchmarkEmitOutput: a result flushes so a live follower sees it, and output
// stays buffered.
func BenchmarkEmitResult(b *testing.B) {
	ctx := WithInvocationID(WithSinks(bgCtx(),NewFileSink(io.Discard).Handle), "invbench")
	run := journal.Run{Project: "web", Target: "build", Status: journal.StatusPass, Ref: "outa1b2c3d4e5f6", Duration: 1200 * time.Millisecond}
	b.ReportAllocs()
	for b.Loop() {
		Emit(ctx, run)
	}
}

// BenchmarkEmitResultFile is BenchmarkEmitResult against a REAL file rather
// than io.Discard, because a flush there is the write(2) the policy prices.
func BenchmarkEmitResultFile(b *testing.B) {
	f, err := os.CreateTemp(b.TempDir(), "run-*.jsonl")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	ctx := WithInvocationID(WithSinks(bgCtx(),NewFileSink(f).Handle), "invbench")
	run := journal.Run{Project: "web", Target: "build", Status: journal.StatusPass, Ref: "outa1b2c3d4e5f6", Duration: 1200 * time.Millisecond}
	b.ReportAllocs()
	for b.Loop() {
		Emit(ctx, run)
	}
}

// SecretResolverSuite swaps slog's default for a JSON sink, so it must not run in
// parallel with anything else that logs through the default.
type SecretResolverSuite struct {
	suite.Suite
	sink bytes.Buffer
	prev *slog.Logger
}

func TestSecretResolver(t *testing.T) { suite.Run(t, new(SecretResolverSuite)) }

func (s *SecretResolverSuite) SetupTest() {
	s.sink.Reset()
	s.prev = slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&s.sink, &slog.HandlerOptions{Level: slog.LevelDebug})))
}

func (s *SecretResolverSuite) TearDownTest() { slog.SetDefault(s.prev) }

// Declining to redact a short value is correct; declining in SILENCE would leave the
// caller treating it as protected. MGS2011 is the whole mitigation available.
func (s *SecretResolverSuite) TestAValueTooShortToMaskLogsMGS2011() {
	s.T().Setenv("MAGUS_TEST_TINY", "ab")
	_, err := NewSecretResolver().Read(s.T().Context(), "MAGUS_TEST_TINY")
	s.Require().NoError(err, "the read succeeds; only the protection is declined")

	out := s.sink.String()
	s.Contains(out, string(types.SecretTooShortToMask), "the notice names its code")
	s.Contains(out, "MAGUS_TEST_TINY", "and the reference it applies to")
	s.Contains(out, "NOT redacted", "and states plainly that the value is unprotected")
}

// A long enough value must NOT trip the notice, or it becomes noise every run.
func (s *SecretResolverSuite) TestAMaskableValueLogsNothing() {
	s.T().Setenv("MAGUS_TEST_LONG_ENOUGH", "long-enough-token")
	_, err := NewSecretResolver().Read(s.T().Context(), "MAGUS_TEST_LONG_ENOUGH")
	s.Require().NoError(err)
	s.NotContains(s.sink.String(), string(types.SecretTooShortToMask))
}

// The forwarder's records reach whatever default is installed when they are logged,
// including one installed after the resolver was built.
func (s *SecretResolverSuite) TestTheDefaultHandlerFollowsALaterDefault() {
	h := defaultHandler{}
	var later bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&later, nil)))
	s.Require().NoError(h.Handle(s.T().Context(), slog.NewRecord(time.Now(), slog.LevelWarn, "secret.endpoint.proxy", 0)))
	s.Contains(later.String(), "secret.endpoint.proxy")
	s.Empty(s.sink.String())
}
