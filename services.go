package magus

import (
	"context"
	"log/slog"
	"os"

	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/service"
	"github.com/egladman/magus/internal/serviceaudit"
	"github.com/egladman/magus/internal/serviceident"
	"github.com/egladman/magus/types"
)

// newServiceSession builds the run's service [service.Session]. When a stable daemon
// is reachable it routes shared services there so they stay warm across separate
// `magus run` invocations; otherwise it hosts them in-process for this run only. This
// is the one place the run wires proc's service RPC to the service supervisor, kept
// out of the hot executeStages path.
func (m *Magus) newServiceSession(ctx context.Context) *service.Session {
	reg := service.New(service.ExecRunner{}, 0)
	addr, ok := proc.LookupStableSocket(ctx)
	if !ok {
		return service.NewSession(reg, nil, nil) // no daemon: in-process only
	}
	acquire := func(ctx context.Context, key string, svc types.Service) error {
		return proc.AcquireService(ctx, addr, key, svc)
	}
	release := func(key string) {
		// context.Background: release must run even after the run's ctx is cancelled
		// (Ctrl-C), or the daemon's ref-count would leak and the service never reap.
		if err := proc.ReleaseService(context.Background(), addr, key); err != nil {
			slog.Warn("magus: releasing daemon-hosted service failed; it will idle-reap on the daemon",
				slog.String("key", key), slog.String("err", err.Error()))
		}
	}
	return service.NewSession(reg, acquire, release)
}

// warnNearDuplicateServices emits MGS5001 when a run brings up services that look
// like near-duplicate copies of one shared service. It is scoped to the run's
// reachable projects (the seed projects plus their cross-project dependency
// closure) so it reflects what will actually run rather than the whole workspace -
// that repo-wide view is the `magus doctor` audit. A run with fewer than two
// near-duplicates emits nothing, so the warning stays a real signal.
func (m *Magus) warnNearDuplicateServices(seeds []*types.Project, charms []string) {
	clusters := serviceaudit.NearDuplicates(m.reachableProjects(seeds), charms)
	msg := serviceident.FormatWarning(clusters)
	if msg == "" {
		return
	}
	interactive.Emit(os.Stderr, types.DiagnosticErrorf(types.NearDuplicateServices, "%s", msg).Error())
}

// reachableProjects returns seeds plus every project reachable from them through
// cross-project dependency edges (DependsOn), deduplicated. A shared service
// commonly lives in a dependency project pulled in via magus.needs rather than in
// a directly-requested project, so scoping to seeds alone would miss it.
func (m *Magus) reachableProjects(seeds []*types.Project) []*types.Project {
	seen := make(map[string]struct{}, len(seeds))
	var out []*types.Project
	var walk func(p *types.Project)
	walk = func(p *types.Project) {
		if p == nil {
			return
		}
		if _, ok := seen[p.Path]; ok {
			return
		}
		seen[p.Path] = struct{}{}
		out = append(out, p)
		for _, dep := range p.DependsOn {
			walk(m.ws.Get(dep))
		}
	}
	for _, p := range seeds {
		walk(p)
	}
	return out
}
