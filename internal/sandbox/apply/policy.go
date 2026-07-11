// Package apply builds per-workspace sandbox policies from config and owns the process-wide
// landlock application state. It lives here (not in sandbox or config) to break the import cycle.
package apply

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/internal/sandbox/env"
	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/egladman/magus/types"
)

// applyOnce gates landlock_restrict_self; landlock is permanent so it must run at most once per process.
var applyOnce sync.Once

// applyErr holds the outcome of the one-shot Apply call (success, ErrUnsupported, or kernel error).
var applyErr error

// warnedUnsupported gates the MGS2005 warning to at most one log line per process.
var warnedUnsupported sync.Once

// policyFingerprint is the fingerprint of the applied landlock policy.
// Subsequent Apply calls with a different fingerprint are rejected (MGS2010) because the ruleset is immutable.
var policyFingerprint string

// appliedExternally is set when the daemon has already applied the union ruleset via MarkAppliedExternally.
// In this mode per-workspace Apply calls are attach-only (no syscall, no fingerprint check).
var appliedExternally bool

// MarkAppliedExternally records that the daemon has already applied the union landlock ruleset.
// Subsequent per-workspace Apply calls become attach-only; the MGS2010 fingerprint check is skipped.
func MarkAppliedExternally(fp string) {
	applyOnce.Do(func() {})
	policyFingerprint = fp
	appliedExternally = true
}

// FromConfig assembles a sandbox Policy for root using the sandbox fields of cfg.
func FromConfig(root string, cfg config.Config) *sandbox.Policy {
	userExtras := make([]filesystem.Rule, 0, len(cfg.Sandbox.Allow))
	for _, pp := range cfg.Sandbox.Allow {
		read := true
		write := pp.Mode == "rw"
		rule, err := filesystem.ExpandUserRule(pp.Path, read, write)
		if err != nil {
			slog.Warn(types.FormatDiagnostic(types.AllowlistUnresolved,
				"sandbox.allow entry failed to resolve; skipped"),
				"path", pp.Path, "err", err)
			continue
		}
		userExtras = append(userExtras, rule)
	}
	var exact, globs []string
	for _, name := range cfg.Sandbox.Env.Passthrough {
		if strings.Contains(name, "*") {
			if bad := env.ValidateGlobs([]string{name}); bad != "" {
				slog.Warn(types.FormatDiagnostic(types.AllowlistUnresolved,
					"sandbox.env.passthrough pattern must end in '*'; ignoring"),
					"pattern", name)
				continue
			}
			globs = append(globs, name)
		} else {
			exact = append(exact, name)
		}
	}
	return sandbox.BuildPolicy(root, userExtras, nil, exact, globs)
}

// Apply applies the kernel-level landlock sandbox (once per process) and attaches policy to ctx.
// ErrUnsupported logs MGS2005 and falls through to interpreter-level enforcement.
// A fingerprint mismatch rejects the run with MGS2010 (landlock is immutable once set).
func Apply(ctx context.Context, policy *sandbox.Policy, root string) (context.Context, error) {
	// Stamp the live provider as the binding-layer sandbox metrics recorder so the
	// fs/archive/crypto/exec checks (which run below observability in the import graph
	// and cannot reach it directly) can report allow/deny decisions and dropped env
	// counts down the same ctx chain that carries the Policy.
	if prov := observability.FromContext(ctx); prov != nil {
		ctx = sandbox.WithMetrics(ctx, prov)
	}

	if appliedExternally { // daemon applied union policy; attach-only
		return sandbox.WithPolicy(ctx, policy), nil
	}

	fp := policy.Fingerprint()

	applyOnce.Do(func() {
		policyFingerprint = fp
		start := time.Now()
		applyErr = sandbox.Apply(policy)
		secs := time.Since(start).Seconds()
		switch {
		case applyErr == nil:
			RecordApply(ctx, secs, "applied", "workspace", policy)
		case errors.Is(applyErr, sandbox.ErrUnsupported):
			warnedUnsupported.Do(func() {
				slog.Warn(types.FormatDiagnostic(types.SandboxUnsupported,
					"kernel landlock unavailable; sandbox running with interpreter-level checks only"),
					"reason", applyErr.Error())
			})
			// ErrUnsupported is the documented fallback path; not fatal. Binding-level
			// checks still enforce the same rules, so record them under "unsupported".
			applyErr = nil
			RecordApply(ctx, secs, "unsupported", "workspace", policy)
		}
		// A hard kernel error falls through unrecorded; the run aborts below.
	})
	if applyErr != nil {
		// Fail closed: ruleset was partially built but restrict_self was never called.
		return ctx, fmt.Errorf("sandbox: kernel sandbox failed: %w", applyErr)
	}

	if fp != policyFingerprint { // mismatch: kernel-level and binding-level policies would disagree
		RecordApply(ctx, 0, "mismatch", "workspace", nil) // no ruleset installed; count the outcome, not rules
		return ctx, fmt.Errorf("%w: sandbox policy for workspace %q differs from the policy already applied to this daemon process (fingerprint %s vs %s); restart the daemon to pick up new sandbox configuration",
			types.DiagnosticErrorf(types.SandboxPolicyMismatch, "sandbox policy mismatch"),
			root, fp, policyFingerprint)
	}

	return sandbox.WithPolicy(ctx, policy), nil
}

// RecordApply reports one sandbox-apply attempt to the observability provider on ctx
// (a no-op when none is stamped). secs is the apply wall-clock duration; outcome is
// applied|unsupported|mismatch; scope is workspace|union. When policy is non-nil its
// filesystem and env rule counts are also recorded under the same scope; pass nil (a
// mismatch installs no ruleset) to record only the outcome.
func RecordApply(ctx context.Context, secs float64, outcome, scope string, policy *sandbox.Policy) {
	prov := observability.FromContext(ctx)
	if prov == nil {
		return
	}
	prov.RecordSandboxApply(ctx, secs, outcome, scope)
	if policy == nil {
		return
	}
	var read, write, exec int64
	for _, r := range policy.FS.Rules {
		if r.Read {
			read++
		}
		if r.Write {
			write++
		}
		if r.Exec {
			exec++
		}
	}
	prov.RecordSandboxRules(ctx, observability.SandboxRules{
		Read:     read,
		Write:    write,
		Exec:     exec,
		EnvExact: int64(len(policy.Env.Allow)),
		EnvGlob:  int64(len(policy.Env.Globs)),
		Scope:    scope,
	})
}
