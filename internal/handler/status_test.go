package handler

import (
	"encoding/base64"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	statusv1 "github.com/egladman/magus/proto/gen/go/magus/status/v1"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStatusProtoMapsPool maps a running-pool status report onto the wire message:
// health, slots, and the in-flight calls a dashboard shows.
func TestStatusProtoMapsPool(t *testing.T) {
	started := time.UnixMilli(1700)
	r := types.StatusReport{
		Pool: &types.StatusOutput{
			ParentPID: 42, Mode: "daemon", Capacity: 8, InUse: 3, Waiting: 1,
			Calls: []types.StatusCall{{Args: []string{"run", "build", "api"}, Workspace: "/ws", StartedAt: started, SubOp: "go-build"}},
		},
	}
	s := statusReportToProto(r, "v1.2.3")

	assert.Equal(t, statusv1.Health_HEALTH_HEALTHY, s.GetHealth())
	assert.Equal(t, "v1.2.3", s.GetMagusVersion())

	p := s.GetPool()
	require.NotNil(t, p)
	assert.Equal(t, int32(8), p.GetCapacity())
	assert.Equal(t, int32(3), p.GetInUse())
	assert.Equal(t, int32(1), p.GetWaiting())
	require.Len(t, p.GetCalls(), 1)
	assert.Equal(t, []string{"run", "build", "api"}, p.GetCalls()[0].GetArgs())
	assert.Equal(t, "go-build", p.GetCalls()[0].GetSubOp())
	assert.Equal(t, int64(1700), p.GetCalls()[0].GetStartTime().AsTime().UnixMilli())
}

// TestStatusProtoHealth derives DOWN when no pool is present and DEGRADED on a pool error.
func TestStatusProtoHealth(t *testing.T) {
	assert.Equal(t, statusv1.Health_HEALTH_DOWN, statusReportToProto(types.StatusReport{}, "v1").GetHealth())
	assert.Equal(t, statusv1.Health_HEALTH_DEGRADED,
		statusReportToProto(types.StatusReport{Pool: &types.StatusOutput{}, PoolError: "boom"}, "v1").GetHealth())
}

// TestEncodeStatusEventRoundTrip confirms a status snapshot decodes back: base64 -> proto.
func TestEncodeStatusEventRoundTrip(t *testing.T) {
	ev, err := EncodeStatusEvent(types.StatusReport{Pool: &types.StatusOutput{Capacity: 4}}, "v1")
	require.NoError(t, err)
	raw, err := base64.StdEncoding.DecodeString(ev)
	require.NoError(t, err)
	var got statusv1.Status
	require.NoError(t, proto.Unmarshal(raw, &got))
	assert.Equal(t, int32(4), got.GetPool().GetCapacity())
	assert.Equal(t, statusv1.Health_HEALTH_HEALTHY, got.GetHealth())
}
