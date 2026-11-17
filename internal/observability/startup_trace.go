package observability

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
)

// StartupTracer records named phase durations; all methods are no-ops when disabled.
// Enable via -vvv, MAGUS_LOG_LEVEL=trace, or log.level: trace in magus.yaml.
type StartupTracer struct {
	enabled bool
	mu      sync.Mutex
	phases  []tracePhase
	start   time.Time
	w       io.Writer
	profile *buzz.Profile
}

type tracePhase struct {
	name string
	d    time.Duration
}

// NewStartupTracer returns a tracer that records when enabled and does nothing otherwise.
func NewStartupTracer(enabled bool) *StartupTracer {
	t := &StartupTracer{w: os.Stderr}
	if enabled {
		t.Enable()
		t.profile = buzz.NewProfile()
	}
	return t
}

// Enable turns a disabled tracer on from now: the retrofit for a trace level only the
// config file set, whose pre-config phases are missed. It starts no Buzz profile, and is a
// no-op on an enabled tracer.
func (t *StartupTracer) Enable() {
	if t.enabled {
		return
	}
	t.enabled = true
	t.start = time.Now()
}

// Enabled reports whether the tracer records.
func (t *StartupTracer) Enabled() bool { return t.enabled }

// Profile is the Buzz compile profile the trace reports beside its phases, nil unless the
// tracer was created enabled.
func (t *StartupTracer) Profile() *buzz.Profile { return t.profile }

// SetWriter sends the report to w instead of stderr.
func (t *StartupTracer) SetWriter(w io.Writer) { t.w = w }

// Phase times a named phase; use `defer t.Phase("name")()` to bracket a block.
func (t *StartupTracer) Phase(name string) func() {
	if !t.enabled {
		return func() {}
	}
	begin := time.Now()
	return func() {
		d := time.Since(begin)
		t.mu.Lock()
		t.phases = append(t.phases, tracePhase{name, d})
		t.mu.Unlock()
	}
}

// Done prints the recorded phases, the total and the Buzz profile.
func (t *StartupTracer) Done() {
	if !t.enabled {
		return
	}
	total := time.Since(t.start)
	t.mu.Lock()
	phases := t.phases
	t.mu.Unlock()

	fmt.Fprintln(t.w, "magus startup trace:")
	for _, p := range phases {
		fmt.Fprintf(t.w, "  %-35s %v\n", p.name, p.d.Round(time.Microsecond))
	}
	fmt.Fprintf(t.w, "  %-35s %v\n", "total", total.Round(time.Microsecond))
	if t.profile != nil {
		if rep := t.profile.Report(); rep != "" {
			fmt.Fprint(t.w, rep)
		}
	}
}

type traceCtxKey struct{}

// WithTrace carries t on ctx for TraceFromContext.
func WithTrace(ctx context.Context, t *StartupTracer) context.Context {
	return context.WithValue(ctx, traceCtxKey{}, t)
}

// TraceFromContext is the tracer WithTrace put on ctx, or a disabled one when none is.
func TraceFromContext(ctx context.Context) *StartupTracer {
	if t, ok := ctx.Value(traceCtxKey{}).(*StartupTracer); ok {
		return t
	}
	return NewStartupTracer(false)
}
