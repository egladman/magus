package sandbox

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/internal/sandbox/filesystem"
)

// fakeRecorder captures the binding-layer sandbox metric calls. It satisfies
// MetricsRecorder, which the live observability.Provider also satisfies structurally.
type fakeRecorder struct {
	checks   []checkCall
	dropped  []droppedCall
	launches []string
}

type checkCall struct{ access, decision, project string }
type droppedCall struct {
	project string
	n       int64
}

func (r *fakeRecorder) RecordSandboxCheck(_ context.Context, access, decision, project string) {
	r.checks = append(r.checks, checkCall{access, decision, project})
}

func (r *fakeRecorder) RecordSandboxEnvDropped(_ context.Context, project string, n int64) {
	r.dropped = append(r.dropped, droppedCall{project, n})
}

func (r *fakeRecorder) RecordSandboxApply(_ context.Context, _ float64, outcome, scope string) {
	r.launches = append(r.launches, scope+"/"+outcome)
}

func TestRecordLaunch(t *testing.T) {
	rec := &fakeRecorder{}
	ctx := WithMetrics(context.Background(), rec)
	RecordLaunch(ctx, 0.01, "applied")
	RecordLaunch(ctx, 0, "unsupported")
	RecordLaunch(context.Background(), 0, "applied")
	assert.Equal(t, []string{"child/applied", "child/unsupported"}, rec.launches)
}

func TestChecksRecordAllowAndDeny(t *testing.T) {
	dir := filesystem.ResolveRulePath(t.TempDir())
	policy := &Policy{FS: filesystem.Ruleset{Rules: []filesystem.Rule{{Path: dir, Read: true}}}}
	rec := &fakeRecorder{}
	ctx := WithMetrics(context.Background(), rec)

	assert.NoError(t, policy.CheckRead(ctx, filepath.Join(dir, "f")))
	assert.Error(t, policy.CheckRead(ctx, "/definitely/not/allowed/f"))
	assert.Error(t, policy.CheckExec(ctx, filepath.Join(dir, "f")))

	assert.Equal(t, []checkCall{
		{access: "read", decision: "allow"},
		{access: "read", decision: "deny"},
		{access: "exec", decision: "deny"},
	}, rec.checks)
}

func TestChecksWithoutARecorderStillCheck(t *testing.T) {
	policy := &Policy{FS: filesystem.Ruleset{Rules: []filesystem.Rule{{Path: "/", Read: true}}}}
	assert.NoError(t, policy.CheckRead(context.Background(), "/etc"))
}

func TestRecordEnvDropped(t *testing.T) {
	rec := &fakeRecorder{}
	ctx := WithMetrics(context.Background(), rec)

	RecordEnvDropped(ctx, &Policy{EnvDropped: []string{"AWS_SECRET_ACCESS_KEY", "GITHUB_TOKEN"}}, "go")
	assert.Equal(t, []droppedCall{{n: 2}}, rec.dropped)

	rec.dropped = nil
	RecordEnvDropped(ctx, &Policy{}, "go")
	assert.Empty(t, rec.dropped, "nothing dropped, nothing recorded")
}

// logged runs fn with the default logger captured at info level.
func logged(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	fn()
	return buf.String()
}

// RecordEnvDropped logs MGS2003 with the command and the drop count, the shape
// docs/reference/codes/sandbox/MGS2003.md prints, and only when something was dropped
// under a sandbox that is on.
func TestRecordEnvDroppedLogsMGS2003(t *testing.T) {
	got := logged(t, func() {
		RecordEnvDropped(context.Background(), &Policy{EnvDropped: []string{"A_KEY", "B_TOKEN", "C_TOKEN"}}, "go")
	})
	assert.Contains(t, got, "MGS2003")
	assert.Contains(t, got, "cmd=go")
	assert.Contains(t, got, "stripped_count=3")

	assert.Empty(t, logged(t, func() { RecordEnvDropped(context.Background(), nil, "go") }), "sandbox off")
	assert.Empty(t, logged(t, func() { RecordEnvDropped(context.Background(), &Policy{}, "go") }), "nothing dropped")
}
