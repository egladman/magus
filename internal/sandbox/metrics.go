package sandbox

import (
	"context"

	"github.com/egladman/magus/internal/journal"
)

// MetricsRecorder is the narrow slice of the observability provider that the
// binding-layer sandbox checks report into. It lives here (not as a direct
// observability import) because internal/sandbox sits below internal/observability
// in the import graph (observability -> cache -> proc/run -> sandbox), so sandbox
// cannot import observability without a cycle. The full observability.Provider
// satisfies this interface structurally; a higher package that can see both
// (internal/sandbox/apply) stamps the live provider onto ctx via WithMetrics.
type MetricsRecorder interface {
	RecordSandboxCheck(ctx context.Context, access, decision, project string)
	RecordSandboxEnvDropped(ctx context.Context, project string, n int64)
}

type metricsKey struct{}

// WithMetrics attaches rec to ctx so the binding-layer checks (fs/archive/crypto/exec)
// can report allow/deny decisions and dropped env counts. Pass nil to clear.
func WithMetrics(ctx context.Context, rec MetricsRecorder) context.Context {
	return context.WithValue(ctx, metricsKey{}, rec)
}

// MetricsFromContext returns the MetricsRecorder attached by WithMetrics, or nil.
func MetricsFromContext(ctx context.Context) MetricsRecorder {
	if ctx == nil {
		return nil
	}
	rec, _ := ctx.Value(metricsKey{}).(MetricsRecorder)
	return rec
}

// RecordCheck reports one binding-layer allow/deny decision to the MetricsRecorder on
// ctx (a no-op when none is stamped). err nil is an allow, non-nil a deny.
//
// These count magus's OWN filesystem-binding access checks - the fs, archive, crypto,
// and exec bindings consulting the resolved Policy before touching the filesystem. They
// are NOT subprocess syscalls: the kernel landlock layer governs those and does not
// report them here.
func RecordCheck(ctx context.Context, access string, err error) {
	rec := MetricsFromContext(ctx)
	if rec == nil {
		return
	}
	decision := "allow"
	if err != nil {
		decision = "deny"
	}
	project, _, _ := journal.StepFromContext(ctx)
	rec.RecordSandboxCheck(ctx, access, decision, project)
}

// RecordEnvDropped reports the number of environment variables policy withheld from its
// frozen BaseEnv, attributed to the current step's project (a no-op when no recorder is
// on ctx or nothing was dropped). This counts variables magus itself dropped from
// subprocess environments per the env allowlist, not anything the kernel enforces.
func RecordEnvDropped(ctx context.Context, policy *Policy) {
	if policy == nil || len(policy.EnvDropped) == 0 {
		return
	}
	rec := MetricsFromContext(ctx)
	if rec == nil {
		return
	}
	project, _, _ := journal.StepFromContext(ctx)
	rec.RecordSandboxEnvDropped(ctx, project, int64(len(policy.EnvDropped)))
}
