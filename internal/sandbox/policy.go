// Package sandbox confines spell code and the processes magus starts to a
// workspace-bounded filesystem and a scrubbed environment.
//
// Two layers enforce one Policy. Kernel landlock (Linux 5.13+) governs every file
// the process and its children touch. Magus's own checks run at the fs, archive,
// crypto, http and exec bindings on every platform, and see only what goes through
// a binding: without landlock, a subprocess is confined in its environment and its
// first exec, and nothing else. A nil Policy means the sandbox is off and every
// check passes.
package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/egladman/magus/internal/sandbox/env"
	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// ErrUnsupported is returned by Apply when landlock is unavailable (non-Linux, kernel <5.13, LSM disabled).
var ErrUnsupported = errors.New("sandbox: kernel sandbox unsupported on this host")

// Policy is the runtime sandbox policy attached to a context and consulted by spell bindings.
// Immutable after construction; safe for concurrent reads. A nil Policy disables all checks,
// so a caller whose sandbox mode is not off must attach a built one or refuse to run.
type Policy struct {
	FS  filesystem.Ruleset // allowlist for CheckRead/CheckWrite/CheckExec
	Env env.Allowlist      // allowlist of inheritable env-var names
	// BaseEnv is every child's environment: the host's, scrubbed to Env, with TMPDIR
	// pointed at TempDir. Frozen at build time so one run cannot change the next's.
	// Non-nil on a built policy; a child of a policy never inherits the host's.
	BaseEnv    []string
	EnvDropped []string // names withheld from BaseEnv by the allowlist, recorded for the env-dropped metric
	// TempDir is the private temp directory children get as TMPDIR, in place of the
	// shared one, which holds other programs' sockets.
	TempDir string
	// Lease is the job lease whose declared boundary narrowed FS, empty on a policy
	// derived from config alone. Read only to name the boundary on a denial: it is not a
	// [Policy.Fingerprint] input, because the kernel ruleset is built from FS and two
	// policies with equal rules must still share one landlock application.
	Lease string
	// LeaseFrom is which source answered Lease, recorded beside it on a denial.
	LeaseFrom types.LeaseSource
}

// CheckRead reports whether the policy permits a read of path, recording the decision
// to the metrics recorder on ctx and a denial to the run's trail. A nil Policy permits
// everything and records nothing.
func (p *Policy) CheckRead(ctx context.Context, path string) error {
	return p.check(ctx, filesystem.Read, path)
}

// CheckWrite is CheckRead for a write to path.
func (p *Policy) CheckWrite(ctx context.Context, path string) error {
	return p.check(ctx, filesystem.Write, path)
}

// CheckExec is CheckRead for executing the binary at path. It does not search $PATH;
// resolve the name with exec.LookPath first.
func (p *Policy) CheckExec(ctx context.Context, path string) error {
	return p.check(ctx, filesystem.Exec, path)
}

func (p *Policy) check(ctx context.Context, access filesystem.Access, path string) error {
	if p == nil {
		return nil
	}
	err := p.FS.Check(path, access)
	recordCheck(ctx, access, err)
	recordDenial(ctx, p, access, path, err)
	return err
}

// recordDenial records a refused access on the run's trail, the producer
// trail.KindSandboxDenial was declared for.
//
// It sits in check rather than at each deny site because this is where the decision is
// MADE: fs, archive, crypto, the http bindings and the exec pre-check all reach the
// policy through it, so one producer covers every Go-side denial and a new binding
// inherits it.
//
// It is not a syscall audit. The kernel landlock layer denies without reporting anything
// back to Go, so an event here means "magus's own check refused", which is the only
// denial anything in this process can witness.
//
// Allows are dropped. A read check fires once per glob match, and a durable append-only
// file is the wrong place for a hot loop's happy path.
func recordDenial(ctx context.Context, p *Policy, access filesystem.Access, path string, err error) {
	if err == nil {
		return
	}
	base := trail.BaseFromContext(ctx)
	if base == "" {
		return
	}
	trail.Append(ctx, base, trail.Event{
		Ts:   time.Now().UnixMilli(),
		Kind: trail.KindSandboxDenial,
		// Set only on a lease-narrowed policy, so a reader can tell a boundary a lease
		// declared for itself from the workspace default every run already has.
		Lease:     p.Lease,
		LeaseFrom: p.LeaseFrom,
		// Reads as a sentence where the console renders it: "Sandbox denied read of <path>."
		Action:  access.String() + " of " + path,
		Outcome: trail.OutcomeError,
		Error:   err.Error(),
	})
}

// AllowsEnv reports whether the policy lets a child inherit the variable name. A nil
// Policy permits everything.
func (p *Policy) AllowsEnv(name string) bool {
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
	names := slices.Sorted(slices.Values(p.Env.Names))
	prefixes := slices.Sorted(slices.Values(p.Env.Prefixes))
	h := sha256.New()
	fmt.Fprintf(h, "rules=%v;envAllow=%v;globs=%v", rules, names, prefixes)
	return hex.EncodeToString(h.Sum(nil)[:8]) // first 8 bytes → 16 hex chars
}

// UnionPolicies returns the set-union of all input policies (for multi-workspace servers).
// FS rules with the same path are merged by OR-ing Read/Write/Exec. nil inputs are ignored.
// Per-workspace binding-layer checks remain strict; only the kernel landlock layer sees the union.
func UnionPolicies(ps ...*Policy) *Policy {
	out := &Policy{}
	seenRule := make(map[string]int) // path -> index into out.FS.Rules
	seenEnv := make(map[string]struct{})
	seenPrefix := make(map[string]struct{})
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
		for _, n := range p.Env.Names {
			if _, ok := seenEnv[n]; ok {
				continue
			}
			seenEnv[n] = struct{}{}
			out.Env.Names = append(out.Env.Names, n)
		}
		for _, g := range p.Env.Prefixes {
			if _, ok := seenPrefix[g]; ok {
				continue
			}
			seenPrefix[g] = struct{}{}
			out.Env.Prefixes = append(out.Env.Prefixes, g)
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

// WithPolicy attaches p to ctx; pass nil to clear. Read with PolicyFromContext.
func WithPolicy(ctx context.Context, p *Policy) context.Context {
	return context.WithValue(ctx, policyKey{}, p)
}

// PolicyFromContext returns the Policy attached by WithPolicy, or nil when absent (sandbox off).
func PolicyFromContext(ctx context.Context) *Policy {
	if ctx == nil {
		return nil
	}
	p, _ := ctx.Value(policyKey{}).(*Policy)
	return p
}
