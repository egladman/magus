package magus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/egladman/magus/broker"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/service"
	"github.com/egladman/magus/internal/service/identity"
	"github.com/egladman/magus/internal/serviceaudit"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// wireBroker resolves the broker policy and returns the cache options that route
// admission through the broker. Under off it wires nothing and leaves m.broker nil, so
// services run in-process too. It never dials: the client connects on the first claim,
// so a command that runs no step never touches the broker.
//
// A client the caller passed is used or refused, never dropped: a nil one, or one beside
// a resolved policy of off, is an error naming where the off came from.
func (m *Magus) wireBroker() ([]cache.Option, error) {
	policy, source := m.brokerPolicy, "WithBrokerPolicy"
	if policy == "" {
		policy, source = m.cfg.Broker, "the workspace's broker setting"
	}
	if !policy.Valid() {
		return nil, fmt.Errorf("magus: unknown broker policy %q (want one of %v)", policy, policy.Values())
	}
	if m.brokerGiven && m.broker == nil {
		return nil, errors.New("magus: WithBroker was given a nil client")
	}
	if policy.Resolved() == types.BrokerOff {
		if m.brokerGiven {
			return nil, fmt.Errorf("magus: WithBroker passed a client, but %s is off, so it would never be used; drop one or the other", source)
		}
		return nil, nil
	}
	if m.broker == nil {
		m.broker = broker.NewClient(broker.DefaultAddr(), broker.WithIdentity(os.Args, m.version))
		m.ownsBroker = true
	}
	opts := []cache.Option{cache.WithMachineAdmission(m.broker), cache.WithMachineWait(m.cfg.CapacityWait)}
	if policy.Resolved() == types.BrokerRequired {
		opts = append(opts, cache.WithMachineAdmissionRequired())
	}
	return opts, nil
}

// closeBroker hangs up a broker client Open made, releasing whatever it still holds.
func (m *Magus) closeBroker() error {
	if !m.ownsBroker || m.broker == nil {
		return nil
	}
	return m.broker.Close()
}

// newServiceSession builds the run's service [service.Session]. With a broker it routes
// shared services there, so they stay warm across separate `magus run` invocations;
// under `broker: off`, or when the broker cannot host one, it hosts them in-process for
// this run only. This is the one place the run wires the broker's service calls to the
// service supervisor, kept out of the hot executeStages path.
func (m *Magus) newServiceSession(_ context.Context) *service.Session {
	reg := service.New(service.ExecRunner{}, 0)
	b := m.broker
	if b == nil {
		return service.NewSession(reg, nil, nil)
	}
	acquire := func(ctx context.Context, key string, svc spells.Service) error {
		return b.AcquireService(ctx, key, broker.NewServiceSpec(svc))
	}
	release := func(relCtx context.Context, key string) {
		// relCtx is ReleaseAll's teardown ctx, detached from the run's and bounded, so a
		// wedged broker cannot hang exit. The reference also rides the connection, so
		// the broker drops it when this process exits even if this release is lost.
		if err := b.ReleaseService(relCtx, key); err != nil {
			slog.DebugContext(relCtx, "magus: releasing a broker-hosted service failed; the broker drops it when this process exits",
				slog.String("key", key), slog.String("err", err.Error()))
		}
	}
	return service.NewSession(reg, acquire, release)
}

// warnNearDuplicateServices emits MGS5001 when a run brings up services that look
// like near-duplicate copies of one shared service. It is scoped to the run's
// reachable projects (the seed projects plus their cross-project dependency
// closure) so it reflects what will actually run rather than the whole workspace;
// that repo-wide view is the `magus doctor` audit. A run with fewer than two
// near-duplicates emits nothing, so the warning stays a real signal.
func (m *Magus) warnNearDuplicateServices(seeds []*types.Project, charms []string) {
	clusters := serviceaudit.NearDuplicates(m.reachableProjects(seeds), charms)
	msg := identity.FormatWarning(clusters)
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
