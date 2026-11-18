package recorder

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/egladman/magus/libs/secrets"
	"github.com/egladman/magus/types"
)

// Sink receives every record an invocation emits. Emit calls it on the
// emitting goroutine, from one goroutine per project, so it is safe for
// concurrent use and does not block. One goroutine's records arrive in order;
// across goroutines Record.Time orders them. It never calls Emit under its
// ctx, does not keep ctx, and says once when it stops recording.
type Sink func(ctx context.Context, r journal.Record)

type (
	sinksKey struct{}
	invKey   struct{}
	stepKey  struct{}
	stepInfo struct{ project, target string }
)

// WithSinks returns ctx with sinks receiving what is emitted under it, in the
// order given. A nil Sink is skipped.
func WithSinks(ctx context.Context, sinks ...Sink) context.Context {
	kept := make([]Sink, 0, len(sinks))
	for _, s := range sinks {
		if s != nil {
			kept = append(kept, s)
		}
	}
	return context.WithValue(ctx, sinksKey{}, kept)
}

// WithInvocationID threads the invocation id stamped onto events emitted under ctx.
func WithInvocationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, invKey{}, id)
}

// InvocationIDFromContext returns the invocation id attached with [WithInvocationID], or "".
func InvocationIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(invKey{}).(string); ok {
		return id
	}
	return ""
}

// WithStep tags ctx with the project/target whose subprocesses are about to run, so the exec
// primitive ([internal/proc/spawn]) can label the exec events it emits without magus threading
// project/target through every layer by hand. Set once per target step, around the op body.
func WithStep(ctx context.Context, project, target string) context.Context {
	return context.WithValue(ctx, stepKey{}, stepInfo{project: project, target: target})
}

// StepFromContext returns the project/target set by [WithStep] and whether one was set. A
// false ok means we are not inside a captured target step (e.g. an internal probe), so no
// exec event should be emitted.
func StepFromContext(ctx context.Context) (project, target string, ok bool) {
	if s, has := ctx.Value(stepKey{}).(stepInfo); has {
		return s.project, s.target, true
	}
	return "", "", false
}

// Emit records body under ctx: it stamps the time and the invocation id from
// ctx, redacts every free-text field through the run's secret resolver, and
// calls each sink. With no sink on ctx it does nothing.
func Emit[B journal.Body](ctx context.Context, body B) {
	sinks, _ := ctx.Value(sinksKey{}).([]Sink)
	if len(sinks) == 0 {
		return
	}
	r := journal.NewRecord(time.Now(), InvocationIDFromContext(ctx), redact(ctx, body))
	for _, s := range sinks {
		s(ctx, r)
	}
}

// redact masks the free-text fields of body here rather than at each emit
// site, a rule someone eventually forgets. TestEveryFreeTextFieldIsRedacted
// holds every string field to this switch or to its enumerations.
func redact[B journal.Body](ctx context.Context, body B) B {
	switch b := any(&body).(type) {
	case *journal.Started:
		b.Command.Arguments = redactAll(ctx, b.Command.Arguments)
	case *journal.Exec:
		b.Command = secrets.RedactString(ctx, b.Command)
	case *journal.Output:
		b.Text = secrets.RedactString(ctx, b.Text)
	case *journal.Run:
		b.Error = secrets.RedactString(ctx, b.Error)
	case *journal.Secret:
		b.Text = secrets.RedactString(ctx, b.Text)
	}
	return body
}

func redactAll(ctx context.Context, in []string) []string {
	if len(in) == 0 {
		return in
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = secrets.RedactString(ctx, s)
	}
	return out
}

// FileSink appends each record as one line to a writer. It flushes every kind
// but output. The first write that fails is logged once through slog, and
// nothing is written after it.
type FileSink struct {
	mu     sync.Mutex
	w      *bufio.Writer
	failed bool
}

// NewFileSink returns a FileSink over w (buffered).
func NewFileSink(w io.Writer) *FileSink {
	return &FileSink{w: bufio.NewWriter(w)}
}

// Handle writes r; it is a [Sink].
func (s *FileSink) Handle(ctx context.Context, r journal.Record) {
	line, err := r.MarshalJSON()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed {
		return
	}
	if err != nil {
		s.fail(ctx, err)
		return
	}
	if _, err := s.w.Write(line); err != nil {
		s.fail(ctx, err)
		return
	}
	if err := s.w.WriteByte('\n'); err != nil {
		s.fail(ctx, err)
		return
	}
	// Flush everything except output lines. The run log is not only a durable record;
	// it is what a live follower reads (`magus events --follow` tails <cacheDir>/runs/),
	// and a purely buffered handler makes that stream lag by up to a bufio page, so a
	// short run delivers nothing until it ends.
	//
	// The flush costs a write(2), about 1.2us; BenchmarkEmitResult (io.Discard) against
	// BenchmarkEmitResultFile (a real file) prices it. Flushed kinds are bounded by
	// SUBPROCESS count, so a large run pays single-digit milliseconds. Output scales
	// with build size instead, so it stays buffered and a follower gets chunks.
	if r.Kind() != journal.KindOutput {
		if err := s.w.Flush(); err != nil {
			s.fail(ctx, err)
		}
	}
}

// fail says once that the journal stopped recording. Callers hold mu.
func (s *FileSink) fail(ctx context.Context, err error) {
	s.failed = true
	slog.WarnContext(ctx, "journal.write", attr.Error(err),
		slog.String("msg", "the journal stopped recording; later records are not on disk"))
}

// Flush writes any buffered records to the underlying writer.
func (s *FileSink) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed {
		return
	}
	if err := s.w.Flush(); err != nil {
		s.fail(context.Background(), err)
	}
}

// NewSecretResolver builds a secrets.Resolver the way every magus scope needs one: a value
// too short to mask is reported as MGS2011, and the endpoint forwarder logs through
// slog's default logger, which magus wraps in [secrets.NewRedactingHandler]. opts apply
// after those, so a caller's own options win.
//
// It lives here, the lowest package every caller imports, so no caller builds a bare
// resolver that would keep the MGS2011 notice to itself.
func NewSecretResolver(opts ...secrets.Option) *secrets.Resolver {
	base := make([]secrets.Option, 0, 2+len(opts))
	base = append(base, secrets.WithShortSecret(warnUnmasked), secrets.WithLogger(slog.New(defaultHandler{})))
	return secrets.NewResolver(append(base, opts...)...)
}

// warnUnmasked says out loud that a resolved value will not be masked: the caller has no
// other way to learn that the credential it was handed is unprotected in magus's output.
func warnUnmasked(ctx context.Context, ref string) {
	slog.WarnContext(ctx, "secret.short",
		slog.String("code", string(types.SecretTooShortToMask)),
		slog.String("ref", ref),
		slog.Int("min_length", secrets.MinMaskLen),
		slog.String("msg", fmt.Sprintf(
			"secret %q is shorter than %d characters, so its value is NOT redacted from magus output; see %s",
			ref, secrets.MinMaskLen, types.CodeURL(types.SecretTooShortToMask))))
}

// defaultHandler sends each record to whatever slog.Default is when the record is
// logged, not when the resolver was built: a server builds a resolver per workspace
// before and after the CLI installs its handler.
type defaultHandler struct{}

func (defaultHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return slog.Default().Handler().Enabled(ctx, l)
}

func (defaultHandler) Handle(ctx context.Context, r slog.Record) error {
	return slog.Default().Handler().Handle(ctx, r)
}

func (defaultHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return slog.Default().Handler().WithAttrs(as)
}

func (defaultHandler) WithGroup(name string) slog.Handler {
	return slog.Default().Handler().WithGroup(name)
}
