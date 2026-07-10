package handler

import (
	"encoding/base64"

	"google.golang.org/protobuf/proto"

	statusv1 "github.com/egladman/magus/proto/gen/go/magus/status/v1"
	"github.com/egladman/magus/types"
)

// StatusProto maps the LIVE portion of the domain status report (types.StatusReport)
// onto the magus.status.v1 wire message, deriving the at-a-glance Health from the
// pool's presence and error state. Static config (telemetry/cache/build) is
// intentionally not on this dashboard contract - it is `magus status`/config.
func StatusProto(r types.StatusReport, magusVersion string) *statusv1.Status {
	s := &statusv1.Status{
		Health:       healthFor(r),
		MagusVersion: magusVersion,
	}
	if r.Pool != nil {
		s.Pool = poolToProto(r.Pool)
	}
	return s
}

// EncodeStatusEvent marshals a status snapshot to base64(protobuf) for a StreamStatus
// SSE `data:` line - the live-dashboard delivery. The JS client base64-decodes then
// Status.fromBinary.
func EncodeStatusEvent(r types.StatusReport, magusVersion string) (string, error) {
	raw, err := proto.Marshal(StatusProto(r, magusVersion))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

func healthFor(r types.StatusReport) statusv1.Health {
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
		})
	}
	for _, w := range p.Workspaces {
		out.Workspaces = append(out.Workspaces, &statusv1.Workspace{
			Root: w.Root, LoadTime: tsFromTime(w.LoadedAt), LastAccessTime: tsFromTime(w.LastAccess),
		})
	}
	return out
}
