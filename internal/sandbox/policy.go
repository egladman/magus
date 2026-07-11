// Package sandbox confines spell code to a workspace-bounded filesystem and environment.
// Kernel-level (landlock, Linux 5.13+) and interpreter-level (fs/sh/env bindings) enforcement.
// A nil [Policy] means sandbox is off; all checks pass through.
package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/egladman/magus/internal/sandbox/env"
	"github.com/egladman/magus/internal/sandbox/filesystem"
)

// ErrUnsupported is returned by Apply when landlock is unavailable (non-Linux, kernel <5.13, LSM disabled).
var ErrUnsupported = errors.New("sandbox: kernel sandbox unsupported on this host")

// Policy is the runtime sandbox policy attached to a context and consulted by spell bindings.
// Immutable after construction; safe for concurrent reads. A nil Policy disables all checks.
type Policy struct {
	Workspace  string             // absolute symlink-resolved workspace root
	FS         filesystem.Ruleset // allowlist for CheckRead/CheckWrite/CheckExec
	Env        env.Allowlist      // allowlist of inheritable env-var names
	BaseEnv    []string           // frozen pre-scrubbed env snapshot (prevents cross-run mutation)
	EnvDropped []string           // names withheld from BaseEnv by the allowlist, recorded for the env-dropped metric
}

// CheckRead reports whether the policy permits a read of path; nil Policy permits everything.
func (p *Policy) CheckRead(path string) error {
	if p == nil {
		return nil
	}
	return p.FS.CheckRead(path)
}

// CheckWrite reports whether the policy permits a write to path. A nil Policy
// permits everything.
func (p *Policy) CheckWrite(path string) error {
	if p == nil {
		return nil
	}
	return p.FS.CheckWrite(path)
}

// CheckExec reports whether the policy permits execution of the binary at
// path. A nil Policy permits everything.
func (p *Policy) CheckExec(path string) error {
	if p == nil {
		return nil
	}
	return p.FS.CheckExec(path)
}

// CheckReadCtx is CheckRead plus a binding-layer metric recording the allow/deny
// decision to the MetricsRecorder on ctx. Use it at the ctx-carrying fs/archive/crypto
// binding sites so magus's own read checks are counted (see RecordCheck).
func (p *Policy) CheckReadCtx(ctx context.Context, path string) error {
	err := p.CheckRead(path)
	RecordCheck(ctx, "read", err)
	return err
}

// CheckWriteCtx is CheckWrite plus the binding-layer write-decision metric.
func (p *Policy) CheckWriteCtx(ctx context.Context, path string) error {
	err := p.CheckWrite(path)
	RecordCheck(ctx, "write", err)
	return err
}

// CheckExecCtx is CheckExec plus the binding-layer exec-decision metric.
func (p *Policy) CheckExecCtx(ctx context.Context, path string) error {
	err := p.CheckExec(path)
	RecordCheck(ctx, "exec", err)
	return err
}

// ScrubEnv returns environ filtered to the allowlist, plus dropped names. A nil Policy is a no-op.
func (p *Policy) ScrubEnv(environ []string) (kept, dropped []string) {
	if p == nil {
		return environ, nil
	}
	return p.Env.Scrub(environ)
}

// AllowEnv reports whether name is allowed by the policy. A nil Policy permits
// everything.
func (p *Policy) AllowEnv(name string) bool {
	if p == nil {
		return true
	}
	return p.Env.Allows(name)
}

// Fingerprint returns a stable hash of the policy's FS rules and env config.
// Policies with equal fingerprints can share a landlock ruleset.
func (p *Policy) Fingerprint() string {
	if p == nil {
		return ""
	}
	rules := make([]string, len(p.FS.Rules))
	for i, r := range p.FS.Rules {
		rules[i] = fmt.Sprintf("%s:r=%v:w=%v:x=%v", r.Path, r.Read, r.Write, r.Exec)
	}
	slices.Sort(rules)
	envAllow := make([]string, len(p.Env.Allow))
	copy(envAllow, p.Env.Allow)
	slices.Sort(envAllow)
	globs := make([]string, len(p.Env.Globs))
	copy(globs, p.Env.Globs)
	slices.Sort(globs)
	h := sha256.New()
	fmt.Fprintf(h, "rules=%v;envAllow=%v;globs=%v", rules, envAllow, globs)
	return hex.EncodeToString(h.Sum(nil)[:8]) // first 8 bytes → 16 hex chars
}

// RecordConnect logs an outbound network connection attempt for audit
// purposes. It does not block. A nil policy is a no-op.
func (p *Policy) RecordConnect(ctx context.Context, method, rawURL string) {
	if p == nil {
		return
	}
	slog.InfoContext(
		ctx, "[MGS2009] sandbox: outbound network request",
		"method", method,
		"url", rawURL,
		"see", "https://github.com/egladman/magus/blob/main/docs/codes/sandbox/MGS2009.md",
	)
}

// UnionPolicies returns the set-union of all input policies (for multi-workspace daemons).
// FS rules with the same path are merged by OR-ing Read/Write/Exec. nil inputs are ignored.
// Per-workspace binding-layer checks remain strict; only the kernel landlock layer sees the union.
func UnionPolicies(ps ...*Policy) *Policy {
	out := &Policy{}
	seenRule := make(map[string]int) // path -> index into out.FS.Rules
	seenEnv := make(map[string]struct{})
	seenGlob := make(map[string]struct{})
	seenBase := make(map[string]struct{})
	for _, p := range ps {
		if p == nil {
			continue
		}
		for _, r := range p.FS.Rules {
			if idx, ok := seenRule[r.Path]; ok {
				out.FS.Rules[idx].Read = out.FS.Rules[idx].Read || r.Read
				out.FS.Rules[idx].Write = out.FS.Rules[idx].Write || r.Write
				out.FS.Rules[idx].Exec = out.FS.Rules[idx].Exec || r.Exec
				continue
			}
			seenRule[r.Path] = len(out.FS.Rules)
			out.FS.Rules = append(out.FS.Rules, r)
		}
		for _, n := range p.Env.Allow {
			if _, ok := seenEnv[n]; ok {
				continue
			}
			seenEnv[n] = struct{}{}
			out.Env.Allow = append(out.Env.Allow, n)
		}
		for _, g := range p.Env.Globs {
			if _, ok := seenGlob[g]; ok {
				continue
			}
			seenGlob[g] = struct{}{}
			out.Env.Globs = append(out.Env.Globs, g)
		}
		for _, kv := range p.BaseEnv {
			if _, ok := seenBase[kv]; ok {
				continue
			}
			seenBase[kv] = struct{}{}
			out.BaseEnv = append(out.BaseEnv, kv)
		}
	}
	return out
}

type policyKey struct{}

// WithPolicy attaches p to ctx; pass nil to clear. Read with FromContext.
func WithPolicy(ctx context.Context, p *Policy) context.Context {
	return context.WithValue(ctx, policyKey{}, p)
}

// FromContext returns the Policy attached by WithPolicy, or nil when absent (sandbox off).
func FromContext(ctx context.Context) *Policy {
	if ctx == nil {
		return nil
	}
	p, _ := ctx.Value(policyKey{}).(*Policy)
	return p
}
