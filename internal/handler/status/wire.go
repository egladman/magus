// Package status maps the live status report onto the magus.status.v1alpha1 wire message and
// base64-encodes it for the dashboard's SSE stream.
package status

import (
	"encoding/base64"

	"google.golang.org/protobuf/proto"

	statusv1 "github.com/egladman/magus/proto/gen/go/magus/status/v1alpha1"
	"github.com/egladman/magus/types"
)

// statusReportToProto maps the LIVE portion of the domain status report (types.StatusReport)
// onto the magus.status.v1alpha1 wire message, deriving the at-a-glance Health from the
// pool's presence and error state. Static config (telemetry/cache/build) is
// intentionally not on this dashboard contract - it is `magus status`/config.
func statusReportToProto(r types.StatusReport, build types.BuildInfo) *statusv1.Status {
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
		s.Pool = poolToProto(r.Pool)
		// Pool-wide cache activity is the sum of the warm workspaces' counters, with the
		// configured cap from the static report - the headline hit/miss tiles plus the
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
	for _, svc := range r.Services {
		s.Services = append(s.Services, serviceToProto(svc))
	}
	for _, l := range r.Locks {
		s.Locks = append(s.Locks, lockToProto(l))
	}
	return s
}

// lockToProto maps one held workspace lock onto the wire message. It deliberately
// does not influence deriveHealth above: a held lock is what a working run looks
// like, and reporting it as unhealthy would make a queued peer look like an outage.
func lockToProto(l types.StatusLock) *statusv1.Lock {
	waiters := make([]*statusv1.LockWaiter, 0, len(l.Waiters))
	for _, w := range l.Waiters {
		waiters = append(waiters, &statusv1.LockWaiter{
			Pid: int32(w.PID), Command: w.Command, Dir: w.Dir, WaitTime: tsFromTime(w.WaitTime),
		})
	}
	return &statusv1.Lock{
		Waiters: waiters,
		Project: l.Project,
		Pid:     int32(l.PID),
		Command: l.Command,
		Dir:     l.Dir,
		// The pair is what a renderer needs: the age alone says nothing without the
		// threshold the daemon judges it by. Dropping the threshold left every console
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

// EncodeStatusEvent marshals a status snapshot to base64(protobuf) for a StreamStatus
// SSE `data:` line - the live-dashboard delivery. The JS client base64-decodes then
// Status.fromBinary.
func EncodeStatusEvent(r types.StatusReport, build types.BuildInfo) (string, error) {
	raw, err := proto.Marshal(statusReportToProto(r, build))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

func deriveHealth(r types.StatusReport) statusv1.Health {
	switch {
	case r.Pool == nil:
		return statusv1.Health_HEALTH_DOWN
	case r.PoolError != "":
		return statusv1.Health_HEALTH_DEGRADED
	default:
		return statusv1.Health_HEALTH_HEALTHY
	}
}

func poolToProto(p *types.StatusOutput) *statusv1.Pool {
	out := &statusv1.Pool{
		ParentPid:     int32(p.ParentPID),
		DaemonVersion: p.DaemonVersion,
		Mode:          p.Mode,
		Capacity:      int32(p.Capacity),
		Running:       int32(p.Running),
		Queued:        int32(p.Queued),
		Affected:      p.Affected,
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
	return out
}
