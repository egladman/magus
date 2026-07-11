package sandbox

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/sandbox/filesystem"
)

// fakeRecorder captures the binding-layer sandbox metric calls. It satisfies
// MetricsRecorder, which the live observability.Provider also satisfies structurally.
type fakeRecorder struct {
	checks  []checkCall
	dropped []droppedCall
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

func TestCheckCtxRecordsAllowAndDeny(t *testing.T) {
	dir := filesystem.ResolveRulePath(t.TempDir())
	policy := &Policy{FS: filesystem.Ruleset{Rules: []filesystem.Rule{
		{Path: dir, Read: true},
	}}}

	rec := &fakeRecorder{}
	ctx := WithMetrics(context.Background(), rec)

	// Allow: a path under the granted rule.
	if err := policy.CheckReadCtx(ctx, filepath.Join(dir, "f")); err != nil {
		t.Fatalf("CheckReadCtx allow: unexpected error: %v", err)
	}
	// Deny: a path outside the allowlist.
	if err := policy.CheckReadCtx(ctx, "/definitely/not/allowed/f"); err == nil {
		t.Fatal("CheckReadCtx deny: expected an error, got nil")
	}

	want := []checkCall{
		{access: "read", decision: "allow", project: ""},
		{access: "read", decision: "deny", project: ""},
	}
	if len(rec.checks) != len(want) {
		t.Fatalf("recorded %d checks, want %d: %+v", len(rec.checks), len(want), rec.checks)
	}
	for i, w := range want {
		if rec.checks[i] != w {
			t.Errorf("check[%d] = %+v, want %+v", i, rec.checks[i], w)
		}
	}
}

func TestCheckCtxNoRecorderIsNoop(t *testing.T) {
	policy := &Policy{FS: filesystem.Ruleset{Rules: []filesystem.Rule{{Path: "/", Read: true}}}}
	// No MetricsRecorder on ctx: must not panic and must return the raw check result.
	if err := policy.CheckReadCtx(context.Background(), "/etc"); err != nil {
		t.Fatalf("CheckReadCtx without recorder: unexpected error: %v", err)
	}
}

func TestRecordEnvDropped(t *testing.T) {
	rec := &fakeRecorder{}
	ctx := WithMetrics(context.Background(), rec)

	policy := &Policy{EnvDropped: []string{"AWS_SECRET_ACCESS_KEY", "GITHUB_TOKEN"}}
	RecordEnvDropped(ctx, policy)

	if len(rec.dropped) != 1 {
		t.Fatalf("recorded %d env-dropped calls, want 1: %+v", len(rec.dropped), rec.dropped)
	}
	if got := rec.dropped[0]; got.n != 2 || got.project != "" {
		t.Errorf("env-dropped = %+v, want {project:\"\" n:2}", got)
	}

	// Nothing dropped: no call.
	rec.dropped = nil
	RecordEnvDropped(ctx, &Policy{})
	if len(rec.dropped) != 0 {
		t.Errorf("expected no env-dropped call for an empty policy, got %+v", rec.dropped)
	}
}
