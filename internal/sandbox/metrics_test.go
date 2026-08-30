package sandbox

import (
	"bytes"
	"context"
	"log/slog"
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
	RecordEnvDropped(ctx, "go", policy)

	if len(rec.dropped) != 1 {
		t.Fatalf("recorded %d env-dropped calls, want 1: %+v", len(rec.dropped), rec.dropped)
	}
	if got := rec.dropped[0]; got.n != 2 || got.project != "" {
		t.Errorf("env-dropped = %+v, want {project:\"\" n:2}", got)
	}

	// Nothing dropped: no call.
	rec.dropped = nil
	RecordEnvDropped(ctx, "go", &Policy{})
	if len(rec.dropped) != 0 {
		t.Errorf("expected no env-dropped call for an empty policy, got %+v", rec.dropped)
	}
}

// TestRecordEnvDropped_LogsMGS2003Notice pins the raise site: sandbox enabled (a
// non-nil policy) plus at least one dropped var must log MGS2003 with the command
// and the drop count, matching docs/reference/codes/sandbox/MGS2003.md's shape.
func TestRecordEnvDropped_LogsMGS2003Notice(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	policy := &Policy{EnvDropped: []string{"AWS_SECRET_ACCESS_KEY", "GITHUB_TOKEN", "VAULT_TOKEN"}}
	RecordEnvDropped(context.Background(), "go", policy)

	got := buf.String()
	if !bytes.Contains(buf.Bytes(), []byte("MGS2003")) {
		t.Fatalf("expected MGS2003 in log output, got: %s", got)
	}
	if !bytes.Contains(buf.Bytes(), []byte("cmd=go")) {
		t.Errorf("expected cmd=go in log output, got: %s", got)
	}
	if !bytes.Contains(buf.Bytes(), []byte("stripped_count=3")) {
		t.Errorf("expected stripped_count=3 in log output, got: %s", got)
	}
}

// TestRecordEnvDropped_SilentWhenSandboxOff is the negative control: a nil policy
// (sandbox disabled) must log nothing, matching MGS2003's "sandbox must be
// enabled" gate.
func TestRecordEnvDropped_SilentWhenSandboxOff(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	RecordEnvDropped(context.Background(), "go", nil)

	if buf.Len() != 0 {
		t.Errorf("expected no log output with sandbox off, got: %s", buf.String())
	}
}

// TestRecordEnvDropped_SilentWhenNothingDropped covers a policy that is present
// (sandbox on) but stripped nothing - the count-must-be-positive half of the gate.
func TestRecordEnvDropped_SilentWhenNothingDropped(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	RecordEnvDropped(context.Background(), "go", &Policy{})

	if buf.Len() != 0 {
		t.Errorf("expected no log output when nothing was dropped, got: %s", buf.String())
	}
}
