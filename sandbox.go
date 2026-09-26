package magus

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/observability"
	procrun "github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// MaybeLaunchSandbox returns at once unless this process was started as the launcher
// of a sandboxed child; then it confines itself and execs that child, never returning.
//
// On Linux, magus confines each process a sandboxed run starts by re-executing the
// running binary as that launcher. A program that runs magus targets with the sandbox
// on must therefore call this first in main, before any configuration, logging or
// connection is set up, or its children start it again in their place.
func MaybeLaunchSandbox() { sandbox.MaybeLaunch() }

// SandboxMode is the sandbox mode this workspace's runs are confined under.
func (m *Magus) SandboxMode() types.SandboxMode { return m.cfg.Sandbox.Mode.Resolved() }

// ApplySandbox attaches this workspace's sandbox policy to ctx, or returns ctx unchanged
// when its sandbox mode is off. Any other mode either attaches a policy or returns an
// error: it never runs unconfined. Callers outside a target run (a `magus buzz` script)
// go through here so a script and a target are confined by the same policy; Run calls it
// too, so there is one condition rather than a copy per entry point. The write grant is
// narrowed to the acting lease's row, resolved by job.ActingLease in the order the guard
// hook resolves it.
//
// Nothing here confines this process: the policy's children are confined as each
// starts (see sandbox.Command). Required mode is refused (MGS2012) here rather than at
// the first child when the kernel cannot confine them, and best-effort without the
// kernel layer says so once (MGS2005).
func (m *Magus) ApplySandbox(ctx context.Context) (context.Context, error) {
	mode := m.cfg.Sandbox.Mode
	if !mode.Enabled() {
		return ctx, nil
	}
	loc := job.Location{CacheDir: m.CacheDir(), Root: m.ws.Root}
	// Every spell the workspace loaded, so a script, a magusfile body and a nested magus
	// get every toolchain's grants; runTarget narrows a spell op's child to its project's.
	p, err := sandbox.FromConfig(m.ws.Root, loc.CacheDir, m.cfg.Sandbox, spells.Sandboxes(project.DefaultSpellRegistry().All()))
	if err != nil {
		return ctx, err
	}
	lease, from, err := job.ActingLease(loc.CacheDir, trail.LeaseFromEnv())
	if err != nil {
		return ctx, fmt.Errorf("sandbox: %w", err)
	}
	p = sandbox.NarrowToLease(ctx, p, loc, lease, from)
	confines, err := p.KernelConfines()
	if err != nil {
		return ctx, err
	}
	if !confines {
		warnKernelUnavailable(ctx)
	}
	// The binding checks sit below observability in the import graph, so the live
	// provider reaches them as a recorder on the same ctx that carries the policy.
	if prov := observability.FromContext(ctx); prov != nil {
		ctx = sandbox.WithMetrics(ctx, prov)
		recordRules(ctx, prov, p)
	}
	return sandbox.WithPolicy(ctx, p), nil
}

// warnedKernelUnavailable keeps MGS2005 to one line per process.
var warnedKernelUnavailable sync.Once

// warnKernelUnavailable logs MGS2005 for a best-effort sandbox on a host without
// landlock. A nested magus says nothing: the invocation that started it already did,
// and a line per nested run would teach a reader to skip it.
func warnKernelUnavailable(ctx context.Context) {
	if procrun.CurrentLevel() > 0 {
		return
	}
	warnedKernelUnavailable.Do(func() {
		reason := "landlock reports ABI 0"
		if _, err := sandbox.ABI(); err != nil {
			reason = err.Error()
		}
		slog.WarnContext(ctx, types.FormatDiagnostic(types.SandboxUnsupported,
			"kernel landlock unavailable; children run under magus's binding checks and env allowlist only"),
			"reason", reason)
	})
}

// recordRules reports the allow-rules p was built from under the workspace scope.
func recordRules(ctx context.Context, prov observability.Provider, p *sandbox.Policy) {
	var read, write, exec int64
	for _, r := range p.FS.Rules {
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
		EnvExact: int64(len(p.Env.Names)),
		EnvGlob:  int64(len(p.Env.Prefixes)),
		Scope:    "workspace",
	})
}
