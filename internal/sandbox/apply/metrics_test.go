package apply

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/internal/sandbox/env"
	"github.com/egladman/magus/internal/sandbox/filesystem"
)

// applyRecorder captures the apply-family metric calls. It embeds observability.Provider
// (left nil) to satisfy the full interface; only the two methods RecordApply exercises are
// overridden, so no other method is ever called.
type applyRecorder struct {
	observability.Provider
	applies []applyCall
	rules   []rulesCall
}

type applyCall struct {
	secs           float64
	outcome, scope string
}

type rulesCall struct {
	read, write, exec, envExact, envGlob int64
	scope                                string
}

func (r *applyRecorder) RecordSandboxApply(_ context.Context, secs float64, outcome, scope string) {
	r.applies = append(r.applies, applyCall{secs, outcome, scope})
}

func (r *applyRecorder) RecordSandboxRules(_ context.Context, sr observability.SandboxRules) {
	r.rules = append(r.rules, rulesCall{sr.Read, sr.Write, sr.Exec, sr.EnvExact, sr.EnvGlob, sr.Scope})
}

func TestRecordApplyDurationAndRules(t *testing.T) {
	rec := &applyRecorder{}
	ctx := observability.WithProvider(context.Background(), rec)

	policy := &sandbox.Policy{
		FS: filesystem.Ruleset{Rules: []filesystem.Rule{
			{Read: true, Write: true, Exec: true}, // read+write+exec
			{Read: true, Exec: true},              // read+exec
			{Read: true},                          // read only
		}},
		Env: env.Allowlist{Allow: []string{"HOME", "PATH"}, Globs: []string{"LC_*"}},
	}

	RecordApply(ctx, 0.25, "applied", "workspace", policy)

	if len(rec.applies) != 1 || rec.applies[0] != (applyCall{0.25, "applied", "workspace"}) {
		t.Fatalf("apply calls = %+v, want one {0.25 applied workspace}", rec.applies)
	}
	wantRules := rulesCall{read: 3, write: 1, exec: 2, envExact: 2, envGlob: 1, scope: "workspace"}
	if len(rec.rules) != 1 || rec.rules[0] != wantRules {
		t.Fatalf("rules calls = %+v, want one %+v", rec.rules, wantRules)
	}
}

func TestRecordApplyMismatchSkipsRules(t *testing.T) {
	rec := &applyRecorder{}
	ctx := observability.WithProvider(context.Background(), rec)

	// A mismatch installs no ruleset, so the caller passes a nil policy: outcome only.
	RecordApply(ctx, 0, "mismatch", "workspace", nil)

	if len(rec.applies) != 1 || rec.applies[0].outcome != "mismatch" {
		t.Fatalf("apply calls = %+v, want one mismatch", rec.applies)
	}
	if len(rec.rules) != 0 {
		t.Errorf("expected no rules recorded for a mismatch, got %+v", rec.rules)
	}
}

func TestRecordApplyNoProviderIsNoop(t *testing.T) {
	// No provider on ctx: must not panic.
	RecordApply(context.Background(), 1, "applied", "workspace", &sandbox.Policy{})
}
