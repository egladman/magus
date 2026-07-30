package apply

import (
	"context"
	"errors"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/internal/sandbox/env"
	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFromConfigFlowsAllowIntoPolicy checks the pure config→policy assembly: an
// extra sandbox.allow entry must change the resulting policy's fingerprint (and
// thus its kernel ruleset) relative to the same workspace with no extras. This is
// platform-independent — it never touches landlock.
func TestFromConfigFlowsAllowIntoPolicy(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	extra := t.TempDir()

	base := FromConfig(root, config.Config{})
	require.NotNil(t, base)
	withAllow := FromConfig(root, config.Config{
		Sandbox: config.SandboxConfig{
			Allow: []config.SandboxAllowPath{{Path: extra, Mode: "rw"}},
		},
	})
	require.NotNil(t, withAllow)

	assert.NotEqual(t, base.Fingerprint(), withAllow.Fingerprint(),
		"a sandbox.allow entry must alter the policy fingerprint")
}

// TestFromConfigFingerprintDiffersByRoot checks two distinct workspace roots yield
// distinct fingerprints, since the workspace path is itself a rule. This underpins
// the mismatch guard exercised below.
func TestFromConfigFingerprintDiffersByRoot(t *testing.T) {
	t.Parallel()
	a := FromConfig(t.TempDir(), config.Config{})
	b := FromConfig(t.TempDir(), config.Config{})
	assert.NotEqual(t, a.Fingerprint(), b.Fingerprint(),
		"different workspace roots should produce different fingerprints")
}

// TestApplyFingerprintMismatch exercises the security-critical MGS2010 guard: once
// a policy has been applied to the process, a later Apply with a *different*
// fingerprint must be rejected (landlock is immutable once set, so a divergent
// per-workspace policy can never be honoured and must fail closed).
//
// The package's apply state (applyOnce, policyFingerprint) is process-global and
// can only be driven once per process, so the whole first-apply → mismatch →
// idempotent-reapply sequence lives in this single test.
//
// It is skipped where landlock is actually supported: the first Apply there would
// call landlock_restrict_self and permanently sandbox the test runner. On those
// hosts the mismatch path is covered by the Linux integration tests. Where
// landlock is unsupported (e.g. darwin, or a kernel <5.13), sandbox.Apply returns
// ErrUnsupported, Apply swallows it as the documented fallback, and the pure-Go
// fingerprint guard below still runs.
func TestApplyFingerprintMismatch(t *testing.T) {
	if sandbox.Supported() {
		t.Skip("landlock is supported here; the first Apply would restrict the test process. " +
			"Covered by Linux integration tests instead.")
	}

	ctx := context.Background()
	rootA := t.TempDir()
	rootB := t.TempDir()
	policyA := FromConfig(rootA, config.Config{})
	policyB := FromConfig(rootB, config.Config{})
	require.NotEqual(t, policyA.Fingerprint(), policyB.Fingerprint(),
		"test needs two policies with different fingerprints")

	// First Apply records policyA's fingerprint. ErrUnsupported is swallowed as the
	// documented fallback, so this returns no error and attaches the policy to ctx.
	ctxA, err := Apply(ctx, policyA, rootA)
	require.NoError(t, err, "first Apply should succeed (ErrUnsupported is a soft fallback)")
	assert.Same(t, policyA, sandbox.FromContext(ctxA), "policyA should be attached to the returned ctx")

	// Second Apply with a divergent fingerprint must be rejected with MGS2010 and
	// must NOT attach the new policy (the returned ctx is the unchanged input).
	gotCtx, err := Apply(ctx, policyB, rootB)
	require.Error(t, err, "a divergent policy must be rejected")
	assert.ErrorIs(t, err, types.SandboxPolicyMismatch,
		"mismatch must carry the MGS2010 diagnostic")
	assert.ErrorContains(t, err, rootB, "mismatch error should name the offending workspace")
	// context.Context is an interface, not a pointer, so compare by value: a rejected
	// Apply returns the unchanged input ctx (with no policy attached).
	assert.Equal(t, ctx, gotCtx, "a rejected Apply must return the input ctx unchanged")
	assert.Nil(t, sandbox.FromContext(gotCtx), "a rejected Apply must not attach its policy")

	// Re-applying the *same* fingerprint is idempotent: it succeeds and re-attaches.
	ctxA2, err := Apply(ctx, policyA, rootA)
	require.NoError(t, err, "re-applying the same fingerprint should succeed")
	assert.Same(t, policyA, sandbox.FromContext(ctxA2))
}

// sanity: the sentinel-based match used above behaves as errors.Is expects, so a
// regression in DiagnosticError.Is would surface here rather than as a confusing
// failure in the mismatch test.
func TestDiagnosticMismatchSentinel(t *testing.T) {
	t.Parallel()
	err := types.DiagnosticErrorf(types.SandboxPolicyMismatch, "x")
	assert.True(t, errors.Is(err, types.SandboxPolicyMismatch))
	assert.False(t, errors.Is(err, types.SandboxUnsupported))
}

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
