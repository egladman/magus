package status

import (
	"context"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	statusv1 "github.com/egladman/magus/proto/gen/go/magus/status/v1"
	"github.com/egladman/magus/proto/gen/go/magus/status/v1/statusv1connect"
	"github.com/egladman/magus/types"
)

// defaultStreamInterval is how often StreamStatus re-samples the live report to decide whether
// to push. It mirrors the SSE status cadence (EventsHandler's 2s poll) so the typed Connect
// stream and the base64-SSE stream reflect pool changes at the same granularity.
const defaultStreamInterval = 2 * time.Second

// ConnectService is the typed Connect surface over the SAME live status report the JSON
// /api/v1/status route and the base64-SSE status frame already serve. It converges the
// one-shot status onto the wire contract (magus.status.v1.Status) so the dashboard reads a
// single typed message instead of hand-shaped JSON. Read-only: it only reports, never mutates.
type ConnectService struct {
	src      statusSource
	build    types.BuildInfo
	log      *slog.Logger
	interval time.Duration
}

// NewConnectService builds the StatusService Connect handler over src (the live report source,
// satisfied by *console.Service). build stamps the reporting binary's identity onto every
// snapshot, exactly as the JSON and SSE surfaces do.
func NewConnectService(src statusSource, build types.BuildInfo, log *slog.Logger) *ConnectService {
	return &ConnectService{src: src, build: build, log: log, interval: defaultStreamInterval}
}

var _ statusv1connect.StatusServiceHandler = (*ConnectService)(nil)

// GetStatus returns the current live snapshot as a typed magus.status.v1.Status - the unary
// equivalent of a single GET /api/v1/status, but on the wire contract.
func (s *ConnectService) GetStatus(ctx context.Context, _ *connect.Request[statusv1.GetStatusRequest]) (*connect.Response[statusv1.GetStatusResponse], error) {
	return connect.NewResponse(&statusv1.GetStatusResponse{
		Status: statusReportToProto(s.src.StatusReport(ctx), s.build),
	}), nil
}

// StreamStatus pushes a snapshot on connect, then re-samples on a ticker and pushes again only
// when the encoded snapshot changes - the typed twin of the base64-SSE status frame. It returns
// when the client disconnects (ctx cancelled). A send error means the stream is gone; return it
// so Connect tears the RPC down.
func (s *ConnectService) StreamStatus(ctx context.Context, _ *connect.Request[statusv1.StreamStatusRequest], stream *connect.ServerStream[statusv1.StreamStatusResponse]) error {
	// Push the initial snapshot immediately so a subscriber renders without waiting a full tick.
	last := statusReportToProto(s.src.StatusReport(ctx), s.build)
	if err := stream.Send(&statusv1.StreamStatusResponse{Status: last}); err != nil {
		return err
	}

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			next := statusReportToProto(s.src.StatusReport(ctx), s.build)
			// Skip unchanged snapshots so a quiescent pool does not spam the stream; proto
			// identity here is structural, so an equal message means nothing a client cares
			// about moved.
			if proto.Equal(last, next) {
				continue
			}
			last = next
			if err := stream.Send(&statusv1.StreamStatusResponse{Status: next}); err != nil {
				return err
			}
		}
	}
}
