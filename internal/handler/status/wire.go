// Package status maps the live status report onto the magus.status.v1alpha1 wire message and
// base64-encodes it for the dashboard's SSE stream.
package status

import (
	"encoding/base64"
	"errors"
	"slices"

	"google.golang.org/protobuf/proto"

	"github.com/egladman/magus/internal/rpcerr"
	statusv1 "github.com/egladman/magus/proto/gen/go/magus/status/v1alpha1"
	"github.com/egladman/magus/types"
)

// statusSnapshotToProto maps the LIVE portion of the domain status snapshot (types.StatusSnapshot)
// onto the magus.status.v1alpha1 wire message, deriving the at-a-glance Health from the
// pool's presence and error state. Static config (telemetry/cache/build) is
// intentionally not on this dashboard contract: it is `magus status`/config.
//
// The message is always whole but for the error details err names, which a caller logs
// and still sends: a dashboard short one error detail beats no dashboard.
func statusSnapshotToProto(r types.StatusSnapshot, build types.BuildInfo) (*statusv1.Status, error) {
	var dropped error
	s := &statusv1.Status{
		Health: deriveHealth(r),
		Build: &statusv1.BuildInfo{
			Version:     build.Version,
			Commit:      build.Commit,
			Date:        build.Date,
			Fingerprint: build.Fingerprint(),
		},
	}
	if r.Pool != nil {
		s.Pool, dropped = poolToProto(r.Pool)
		// Pool-wide cache activity is the sum of the warm workspaces' counters, with the
		// configured cap from the static report: the headline hit/miss tiles plus the
		// client-side trend.
		if len(r.Pool.Workspaces) > 0 || r.Cache.SizeMB > 0 {
			agg := &statusv1.Cache{SizeCapMb: int32(r.Cache.SizeMB)}
			for _, w := range r.Pool.Workspaces {
				agg.Hits += int64(w.CacheHit)
				agg.Misses += int64(w.CacheMiss)
				agg.Errors += int64(w.CacheError)
				agg.SizeBytes += w.CacheBytes
				agg.SavedMs += w.CacheSavedMs
			}
			s.Pool.Cache = agg
		}
	}
	for _, run := range r.Runs {
		s.Runs = append(s.Runs, runToProto(run))
	}
	s.BrokerPolicy = string(r.BrokerPolicy)
	if r.Broker != nil {
		s.Broker = brokerToProto(r.Broker)
		for _, svc := range r.Broker.Services {
			s.Services = append(s.Services, serviceToProto(svc))
		}
	}
	if r.Server != nil {
		s.Server = serverToProto(r.Server)
	}
	for _, l := range r.Locks {
		s.Locks = append(s.Locks, lockToProto(l))
	}
	return s, dropped
}

// brokerToProto maps the broker's report onto the wire. Its services ride Status.services,
// where the dashboard already reads them.
func brokerToProto(b *types.StatusBroker) *statusv1.Broker {
	c := b.Capacity
	out := &statusv1.Broker{
		Pid:        int32(b.PID),
		Version:    b.Version,
		Protocol:   int32(b.Protocol),
		Socket:     b.Socket,
		Executable: b.Executable,
		StartTime:  tsFromTime(b.StartTime),
		Capacity: &statusv1.Capacity{
			BudgetMb:    int32(c.BudgetMB),
			HeldMb:      int32(c.HeldMB),
			BudgetSlots: int32(c.BudgetSlots),
			HeldSlots:   int32(c.HeldSlots),
		},
		IdleExitSeconds: int32(b.IdleExitSeconds),
		Draining:        b.Draining,
	}
	for _, h := range c.Holders {
		out.Capacity.Holders = append(out.Capacity.Holders, &statusv1.Claim{
			Project:   h.Project,
			Target:    h.Target,
			Pid:       int32(h.PID),
			MemoryMb:  int32(h.MemoryMB),
			Slots:     int32(h.Slots),
			Dir:       h.Dir,
			Command:   h.Command,
			StartTime: tsFromTime(h.Since),
		})
	}
	return out
}

func serverToProto(s *types.StatusServer) *statusv1.Server {
	out := &statusv1.Server{
		Pid:        int32(s.PID),
		Version:    s.Version,
		Socket:     s.Socket,
		Executable: s.Executable,
		StartTime:  tsFromTime(s.StartTime),
		Watch:      s.Watch,
	}
	for _, l := range s.Listeners {
		out.Listeners = append(out.Listeners, &statusv1.Listener{Kind: string(l.Kind), Address: l.Address})
	}
	return out
}

// lockToProto maps one held workspace lock onto the wire message. It deliberately
// does not influence deriveHealth above: a held lock is what a working run looks
// like, and reporting it as unhealthy would make a busy peer look like an outage.
func lockToProto(l types.StatusLock) *statusv1.Lock {
	return &statusv1.Lock{
		Project: l.Project,
		Pid:     int32(l.PID),
		Command: l.Command,
		Dir:     l.Dir,
		// The pair is what a renderer needs: the age alone says nothing without the
		// threshold the server judges it by. Dropping the threshold left every console
		// row comparing against zero, so no held lock ever read as possibly abandoned.
		AcquireTime:       tsFromTime(l.AcquireTime),
		StaleAfterSeconds: int32(l.StaleAfterSeconds),
	}
}

// serviceToProto maps one hosted shared service onto the wire message.
func serviceToProto(s types.StatusService) *statusv1.Service {
	return &statusv1.Service{
		Id:         s.ID,
		Label:      s.Label,
		Command:    s.Command,
		Ports:      s.Ports,
		State:      string(s.State),
		Dependents: int32(s.Dependents),
		StartTime:  tsFromTime(s.StartedAt),
	}
}

// runToProto maps one live run and its per-target execution state onto the wire message.
func runToProto(r types.StatusRun) *statusv1.Run {
	out := &statusv1.Run{
		Inv:       r.Inv,
		Trigger:   r.Trigger,
		StartTime: tsFromTime(r.StartedAt),
	}
	for _, t := range r.Targets {
		out.Targets = append(out.Targets, &statusv1.TargetRun{
			Project:    t.Project,
			Target:     t.Target,
			State:      targetStateToProto(t.State),
			StartTime:  tsFromTime(t.StartedAt),
			EndTime:    tsFromTime(t.EndedAt),
			OutputRef:  t.OutputRef,
			DurationMs: t.DurationMs,
		})
	}
	return out
}

func targetStateToProto(s types.TargetRunState) statusv1.TargetRun_State {
	switch s {
	case types.TargetRunQueued:
		return statusv1.TargetRun_STATE_QUEUED
	case types.TargetRunRunning:
		return statusv1.TargetRun_STATE_RUNNING
	case types.TargetRunPassed:
		return statusv1.TargetRun_STATE_PASSED
	case types.TargetRunFailed:
		return statusv1.TargetRun_STATE_FAILED
	case types.TargetRunCached:
		return statusv1.TargetRun_STATE_CACHED
	default:
		return statusv1.TargetRun_STATE_UNSPECIFIED
	}
}

// EncodeStatusEvent marshals a status message to base64(protobuf) for a StreamStatus
// SSE `data:` line, the live-dashboard delivery. The JS client base64-decodes then
// Status.fromBinary.
func EncodeStatusEvent(s *statusv1.Status) (string, error) {
	raw, err := proto.Marshal(s)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// deriveHealth agrees with the readiness probe's workspaces component: a pool where every
// workspace that tried to load failed is down, not degraded, since nothing it holds serves.
func deriveHealth(r types.StatusSnapshot) statusv1.Health {
	if r.Pool == nil {
		return statusv1.Health_HEALTH_DOWN
	}
	failed := slices.ContainsFunc(r.Pool.Workspaces, func(w types.StatusWorkspace) bool { return w.State == types.WorkspaceFailed })
	loaded := slices.ContainsFunc(r.Pool.Workspaces, types.StatusWorkspace.Loaded)
	switch {
	case failed && !loaded:
		return statusv1.Health_HEALTH_DOWN
	case r.PoolError != "", failed:
		return statusv1.Health_HEALTH_DEGRADED
	default:
		return statusv1.Health_HEALTH_HEALTHY
	}
}

// workspaceStateToProto converts s to the wire enum. compat: see types.StatusWorkspace.Loaded
// for why "" maps to ACTIVE; any OTHER value this build does not recognize maps to
// STATE_UNSPECIFIED instead, never silently reading as active.
func workspaceStateToProto(s types.WorkspaceState) statusv1.Workspace_State {
	switch s {
	case "":
		return statusv1.Workspace_STATE_ACTIVE
	case types.WorkspaceLoading:
		return statusv1.Workspace_STATE_LOADING
	case types.WorkspaceActive:
		return statusv1.Workspace_STATE_ACTIVE
	case types.WorkspaceFailed:
		return statusv1.Workspace_STATE_FAILED
	default:
		return statusv1.Workspace_STATE_UNSPECIFIED
	}
}

func poolToProto(p *types.StatusOutput) (*statusv1.Pool, error) {
	var dropped []error
	out := &statusv1.Pool{
		ParentPid:    int32(p.ParentPID),
		OwnerVersion: p.Version,
		Capacity:     int32(p.Capacity),
		Running:      int32(p.Running),
		Queued:       int32(p.Queued),
		Affected:     p.Affected,
	}
	for _, c := range p.RunningTargets {
		out.RunningTargets = append(out.RunningTargets, &statusv1.RunningTarget{
			Args: c.Args, Workspace: c.Workspace, StartTime: tsFromTime(c.StartedAt), Step: c.Step,
			Invocation: c.Inv,
		})
	}
	for _, w := range p.Workspaces {
		ws := &statusv1.Workspace{
			Root: w.Root, LoadTime: tsFromTime(w.LoadedAt), LastAccessTime: tsFromTime(w.LastAccess),
			SecretProvider: w.SecretProvider,
			State:          workspaceStateToProto(w.State),
		}
		if w.State == types.WorkspaceFailed {
			var err error
			ws.Error, err = rpcerr.WorkspaceFailed(w.Root, w.Error).Status()
			dropped = append(dropped, err)
		}
		if w.CacheHit != 0 || w.CacheMiss != 0 || w.CacheError != 0 || w.CacheBytes != 0 {
			ws.Cache = &statusv1.Cache{
				Hits: int64(w.CacheHit), Misses: int64(w.CacheMiss),
				Errors: int64(w.CacheError), SizeBytes: w.CacheBytes,
				SavedMs: w.CacheSavedMs,
			}
		}
		out.Workspaces = append(out.Workspaces, ws)
	}
	return out, errors.Join(dropped...)
}
