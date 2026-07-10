package handler

import (
	"encoding/base64"

	"google.golang.org/protobuf/proto"

	statusv1 "github.com/egladman/magus/proto/gen/go/magus/status/v1"
	"github.com/egladman/magus/types"
)

// statusReportToProto maps the LIVE portion of the domain status report (types.StatusReport)
// onto the magus.status.v1 wire message, deriving the at-a-glance Health from the
// pool's presence and error state. Static config (telemetry/cache/build) is
// intentionally not on this dashboard contract - it is `magus status`/config.
func statusReportToProto(r types.StatusReport, magusVersion string) *statusv1.Status {
	s := &statusv1.Status{
		Health:       deriveHealth(r),
		MagusVersion: magusVersion,
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
			}
			s.Pool.Cache = agg
		}
	}
	return s
}

// EncodeStatusEvent marshals a status snapshot to base64(protobuf) for a StreamStatus
// SSE `data:` line - the live-dashboard delivery. The JS client base64-decodes then
// Status.fromBinary.
func EncodeStatusEvent(r types.StatusReport, magusVersion string) (string, error) {
	raw, err := proto.Marshal(statusReportToProto(r, magusVersion))
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
		InUse:         int32(p.InUse),
		Waiting:       int32(p.Waiting),
		Affected:      p.Affected,
	}
	for _, c := range p.Calls {
		out.Calls = append(out.Calls, &statusv1.Call{
			Args: c.Args, Workspace: c.Workspace, StartTime: tsFromTime(c.StartedAt), SubOp: c.SubOp,
			Invocation: c.Inv,
		})
	}
	for _, w := range p.Workspaces {
		ws := &statusv1.Workspace{
			Root: w.Root, LoadTime: tsFromTime(w.LoadedAt), LastAccessTime: tsFromTime(w.LastAccess),
		}
		if w.CacheHit != 0 || w.CacheMiss != 0 || w.CacheError != 0 || w.CacheBytes != 0 {
			ws.Cache = &statusv1.Cache{
				Hits: int64(w.CacheHit), Misses: int64(w.CacheMiss),
				Errors: int64(w.CacheError), SizeBytes: w.CacheBytes,
			}
		}
		out.Workspaces = append(out.Workspaces, ws)
	}
	return out
}
