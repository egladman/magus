// Package sandbox confines spell code and the processes magus starts to a
// workspace-bounded filesystem and a scrubbed environment.
//
// Two layers enforce one Policy. Magus's own checks run at the fs, archive, crypto,
// http and exec bindings on every platform, and see only what goes through a
// binding. The kernel layer confines each child magus starts: Command re-executes
// magus as a launcher that applies a landlock ruleset to itself and then execs the
// child, so the child and everything it starts are held to the policy. Magus itself
// is confined only by `magus buzz --read-only` (see ApplyReadOnly), so in-process Buzz
// otherwise rests on the binding checks alone. A nil Policy means the sandbox is off
// and every check passes.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/egladman/magus/internal/sandbox/env"
	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// ErrUnsupported is returned by ABI and Command when landlock is unavailable
// (non-Linux, kernel <5.13, LSM disabled).
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
	// Mode decides a child the kernel cannot confine: best-effort runs it under the
	// binding checks and the env allowlist, required refuses it (MGS2012).
	Mode types.SandboxMode
	// Workspace and GitDirs locate the control files CheckWrite refuses (see
	// controlFile). Resolved like rule paths; empty locates none.
	Workspace string
	GitDirs   []string
	// Lease is the job lease whose declared boundary narrowed FS, empty on a policy
	// derived from config alone. Read only to name the boundary on a denial.
	Lease string
	// LeaseFrom is which source answered Lease, recorded beside it on a denial.
	LeaseFrom types.LeaseSource
	// ReadOnly refuses every write and every exec whatever FS grants. Set only by
	// [ReadOnly], the policy `magus buzz --read-only` runs under.
	ReadOnly bool
	// unconfined marks a read-only policy derived from no policy at all: reads and the
	// environment pass as they would with the sandbox off.
	unconfined bool
}

// CheckRead reports whether the policy permits a read of path, recording the decision
// to the metrics recorder on ctx and a denial to the run's trail. A nil Policy permits
// everything and records nothing.
func (p *Policy) CheckRead(ctx context.Context, path string) error {
	return p.check(ctx, filesystem.Read, path)
}

// CheckWrite is CheckRead for a write to path. A write to a control file (see
// controlFile) is refused even inside a write grant, and a ReadOnly policy refuses
// every write.
func (p *Policy) CheckWrite(ctx context.Context, path string) error {
	return p.check(ctx, filesystem.Write, path)
}

// CheckExec is CheckRead for executing the binary at path. It does not search $PATH;
// resolve the name with exec.LookPath first. A ReadOnly policy refuses every exec.
func (p *Policy) CheckExec(ctx context.Context, path string) error {
	return p.check(ctx, filesystem.Exec, path)
}

func (p *Policy) check(ctx context.Context, access filesystem.Access, path string) error {
	if p == nil || (p.unconfined && access == filesystem.Read) {
		return nil
	}
	var err error
	if p.ReadOnly && access != filesystem.Read {
		err = readOnlyDenied(path)
	} else {
		err = p.FS.Check(path, access)
	}
	if err == nil && access == filesystem.Write {
		if abs := filesystem.ResolveRulePath(path); p.controlFile(abs) {
			err = fmt.Errorf("%w: write of %s: another tool runs code from it later, outside the sandbox", filesystem.ErrDenied, abs)
		}
	}
	recordCheck(ctx, access, err)
	recordDenial(ctx, p, access, path, err)
	return err
}

// controlNames are the files and directories, at any depth of the workspace, that
// another tool reads and runs code from after the run ends: version-manager pins and
// hooks, direnv, git hook managers, and agent and editor settings. A write to one is
// a way to run code outside the sandbox later, so the binding checks refuse it under
// every mode but off. The kernel layer cannot: landlock has no deny rule inside a
// grant, so a child can still write them.
//
// ./magus is deliberately absent: go-build writes it, and a person runs it next.
var controlNames = map[string]bool{
	"magus.yaml":              true,
	"mise.toml":               true,
	".mise.toml":              true,
	"mise.local.toml":         true,
	".mise.local.toml":        true,
	".mise":                   true,
	".tool-versions":          true,
	".envrc":                  true,
	".claude":                 true,
	".cursor":                 true,
	".mcp.json":               true,
	".pre-commit-config.yaml": true,
	".husky":                  true,
	"lefthook.yml":            true,
	"lefthook.yaml":           true,
	".lefthook.yml":           true,
	".lefthook.yaml":          true,
}

// gitControlNames are the entries of a git directory git runs or obeys: hooks, config
// (core.hooksPath, core.fsmonitor, aliases) and info (attributes, exclude).
var gitControlNames = map[string]bool{"hooks": true, "config": true, "config.worktree": true, "info": true}

// controlFile reports whether abs, a resolved path, is or lies inside a control file
// of the workspace or of one of its git directories. A linked worktree's .git is a
// file naming its git directory, so it is a control file too.
func (p *Policy) controlFile(abs string) bool {
	for _, dir := range p.GitDirs {
		if rel, ok := relUnder(abs, dir); ok {
			first, _, _ := strings.Cut(rel, string(filepath.Separator))
			if rel == "" || gitControlNames[first] {
				return true
			}
		}
	}
	rel, ok := relUnder(abs, p.Workspace)
	if !ok || rel == "" {
		return false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	for i, c := range parts {
		next := ""
		if i+1 < len(parts) {
			next = parts[i+1]
		}
		switch {
		case controlNames[c]:
			return true
		case c == ".vscode" && next == "tasks.json":
			return true
		case c == ".git" && (next == "" || gitControlNames[next]):
			return true
		}
	}
	return false
}

// relUnder is abs relative to root when abs is at or under it; root "" holds nothing.
func relUnder(abs, root string) (string, bool) {
	if !filesystem.Under(abs, root) {
		return "", false
	}
	return strings.TrimPrefix(abs[len(root):], string(filepath.Separator)), true
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

// TempBase is the directory a run under p makes its temp files in: p's private temp
// dir, or "" (os.TempDir) for a nil p. The shared temp dir is granted to nothing under
// a policy, so a file made there is one the run's own children cannot touch.
func (p *Policy) TempBase() string {
	if p == nil {
		return ""
	}
	return p.TempDir
}

// AllowsEnv reports whether the policy lets a child inherit the variable name. A nil
// Policy permits everything.
func (p *Policy) AllowsEnv(name string) bool {
	if p == nil || p.unconfined {
		return true
	}
	return p.Env.Allows(name)
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
