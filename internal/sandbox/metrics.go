package sandbox

import (
	"context"
	"log/slog"
	"sync"

	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/egladman/magus/types"
)

// MetricsRecorder is the narrow slice of the observability provider that the
// binding-layer sandbox checks report into. It lives here (not as a direct
// observability import) because internal/sandbox sits below internal/observability
// in the import graph (observability -> cache -> proc/run -> sandbox), so sandbox
// cannot import observability without a cycle. The full observability.Provider
// satisfies this interface structurally; the root package, which can see both, stamps
// the live provider onto ctx via WithMetrics.
type MetricsRecorder interface {
	RecordSandboxCheck(ctx context.Context, access, decision, project string)
	RecordSandboxEnvDropped(ctx context.Context, project string, n int64)
	RecordSandboxApply(ctx context.Context, secs float64, outcome, scope string)
}

// RecordLaunch reports how one child of a policy started: "applied" when the launcher
// confined it, secs being the time to compile its ruleset, or "unsupported" when a
// best-effort policy ran it without the kernel layer. A no-op without a recorder.
func RecordLaunch(ctx context.Context, secs float64, outcome string) {
	if rec := metricsFromContext(ctx); rec != nil {
		rec.RecordSandboxApply(ctx, secs, outcome, "child")
	}
}

type metricsKey struct{}

// WithMetrics attaches rec to ctx so the binding-layer checks (fs/archive/crypto/exec)
// can report allow/deny decisions and dropped env counts. Pass nil to clear.
func WithMetrics(ctx context.Context, rec MetricsRecorder) context.Context {
	return context.WithValue(ctx, metricsKey{}, rec)
}

func metricsFromContext(ctx context.Context) MetricsRecorder {
	if ctx == nil {
		return nil
	}
	rec, _ := ctx.Value(metricsKey{}).(MetricsRecorder)
	return rec
}

// recordCheck reports one binding-layer allow/deny decision to the recorder on ctx.
// These count magus's OWN checks before it touches the filesystem, not subprocess
// syscalls: the kernel landlock layer governs those and reports nothing here.
func recordCheck(ctx context.Context, access filesystem.Access, err error) {
	rec := metricsFromContext(ctx)
	if rec == nil {
		return
	}
	decision := "allow"
	if err != nil {
		decision = "deny"
	}
	project, _, _ := journal.StepFromContext(ctx)
	rec.RecordSandboxCheck(ctx, access.String(), decision, project)
}

// RecordEnvDropped reports the number of environment variables p withheld from its
// frozen BaseEnv, attributed to the current step's project (a no-op when no recorder is
// on ctx or nothing was dropped), and logs MGS2003 once for this exec of cmd so a
// behavior change from a missing variable is traceable
// (docs/reference/codes/sandbox/MGS2003.md).
func RecordEnvDropped(ctx context.Context, p *Policy, cmd string) {
	if p == nil || len(p.EnvDropped) == 0 {
		return
	}
	slog.InfoContext(ctx, types.FormatDiagnostic(types.EnvStripped,
		"env vars stripped from child process by sandbox"),
		"cmd", cmd, "stripped_count", len(p.EnvDropped))
	rec := metricsFromContext(ctx)
	if rec == nil {
		return
	}
	project, _, _ := journal.StepFromContext(ctx)
	rec.RecordSandboxEnvDropped(ctx, project, int64(len(p.EnvDropped)))
}

// withheldReads is the set of (project, target, variable) reads RecordEnvWithheld has
// already reported. A nil set reports every read: a Policy assembled by hand, as a test
// does, has none.
type withheldReads struct {
	mu       sync.Mutex
	reported map[string]struct{}
}

// firstReport reports whether key has not been reported yet, and marks it reported.
func (w *withheldReads) firstReport(key string) bool {
	if w == nil {
		return true
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.reported[key]; ok {
		return false
	}
	if w.reported == nil {
		w.reported = map[string]struct{}{}
	}
	w.reported[key] = struct{}{}
	return true
}

// RecordEnvWithheld reports MGS2013: code under p read name, which is set, and p
// withholds it, so the read answered unset. The read keeps answering unset; this is how
// whoever reads the run learns why, with the variable named and its value never. Warn
// rather than MGS2003's info: a read that came back empty is a decision the code went on
// to make, where a stripped child environment is a fact about every child. Once per
// target and variable.
func RecordEnvWithheld(ctx context.Context, p *Policy, name string) {
	project, target, _ := journal.StepFromContext(ctx)
	if !p.withheld.firstReport(project + "\x00" + target + "\x00" + name) {
		return
	}
	slog.WarnContext(ctx, types.FormatDiagnostic(types.EnvReadWithheld,
		"a sandboxed read of a set variable answered unset"),
		"name", name, "project", project, "target", target)
}
