// Package activity is the console-facing ActivityService handler: it lists recent activity
// events (newest first, filtered) and serves a payload blob by ref for the /dashboard and log
// viewer. It is READ-only and maps the on-disk trail.Event (internal/trail) to the
// magus.activity.v1 wire type at the boundary - the store owns the format, this owns the wire.
// Mounted on the console's human-facing API surface by the daemon, never under /mcp.
package activity

import (
	"context"
	"slices"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/egladman/magus/internal/trail"
	activityv1 "github.com/egladman/magus/proto/gen/go/magus/activity/v1"
	"github.com/egladman/magus/proto/gen/go/magus/activity/v1/activityv1connect"
)

const (
	defaultPageSize = 200
	maxPageSize     = 1000
)

// Service implements activityv1connect.ActivityServiceHandler over a workspace's activity trail
// at cacheDir. Read-only: producers (the MCP handler, and later jobs/config/token) write the
// trail; this reads it.
type Service struct {
	cacheDir string
}

// NewService builds a Service reading the trail under cacheDir.
func NewService(cacheDir string) *Service { return &Service{cacheDir: cacheDir} }

var _ activityv1connect.ActivityServiceHandler = (*Service)(nil)

// ListActivity returns recent events, newest first, narrowed by the request filter. Paging is a
// simple recent-window today (page_size, capped); page_token is unused, so next_page_token is
// always empty - enough for the dashboard's "recent activity" view.
func (s *Service) ListActivity(_ context.Context, req *connect.Request[activityv1.ListActivityRequest]) (*connect.Response[activityv1.ListActivityResponse], error) {
	limit := int(req.Msg.GetPageSize())
	if limit <= 0 {
		limit = defaultPageSize
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}
	events, err := trail.ReadRecent(s.cacheDir, limit)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	filter := req.Msg.GetFilter()
	out := make([]*activityv1.ActivityEvent, 0, len(events))
	for _, e := range events {
		if !matchFilter(e, filter) {
			continue
		}
		pe := &activityv1.ActivityEvent{
			Time:          timestamppb.New(time.UnixMilli(e.Ts)),
			Kind:          encodeKind(e.Kind),
			Actor:         e.Actor,
			Workspace:     e.Workspace,
			Action:        e.Action,
			Outcome:       encodeOutcome(e.Outcome),
			Error:         e.Error,
			RequestRef:    e.RequestRef,
			ResponseRef:   e.ResponseRef,
			Preview:       e.Preview,
			RequestBytes:  e.RequestBytes,
			ResponseBytes: e.ResponseBytes,
		}
		if e.DurMs > 0 {
			pe.Duration = durationpb.New(time.Duration(e.DurMs) * time.Millisecond)
		}
		out = append(out, pe)
	}
	return connect.NewResponse(&activityv1.ListActivityResponse{Events: out}), nil
}

// GetPayload serves a stored request or response body by its ref.
func (s *Service) GetPayload(_ context.Context, req *connect.Request[activityv1.GetPayloadRequest]) (*connect.Response[activityv1.GetPayloadResponse], error) {
	body, err := trail.ReadBlob(s.cacheDir, req.Msg.GetRef())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(&activityv1.GetPayloadResponse{Body: body, Bytes: int64(len(body))}), nil
}

// matchFilter applies the ActivityQuery's set filters (kinds/actors/actions) and the time
// window, all ANDed; an empty or absent field does not constrain.
func matchFilter(e trail.Event, q *activityv1.ActivityQuery) bool {
	if q == nil {
		return true
	}
	if kinds := q.GetKinds(); len(kinds) > 0 && !slices.Contains(kinds, encodeKind(e.Kind)) {
		return false
	}
	if actors := q.GetActors(); len(actors) > 0 && !slices.Contains(actors, e.Actor) {
		return false
	}
	if actions := q.GetActions(); len(actions) > 0 && !slices.Contains(actions, e.Action) {
		return false
	}
	if window := q.GetTime(); window != nil {
		if since := window.GetSince(); since != nil && e.Ts < since.AsTime().UnixMilli() {
			return false
		}
		if until := window.GetUntil(); until != nil && e.Ts > until.AsTime().UnixMilli() {
			return false
		}
	}
	return true
}

func encodeKind(k trail.Kind) activityv1.Kind {
	switch k {
	case trail.KindMCPToolCall:
		return activityv1.Kind_KIND_MCP_TOOL_CALL
	case trail.KindJob:
		return activityv1.Kind_KIND_JOB
	case trail.KindConfigChange:
		return activityv1.Kind_KIND_CONFIG_CHANGE
	case trail.KindTokenLifecycle:
		return activityv1.Kind_KIND_TOKEN_LIFECYCLE
	case trail.KindSandboxDenial:
		return activityv1.Kind_KIND_SANDBOX_DENIAL
	default:
		return activityv1.Kind_KIND_UNSPECIFIED
	}
}

func encodeOutcome(o string) activityv1.Outcome {
	switch o {
	case trail.OutcomeOK:
		return activityv1.Outcome_OUTCOME_OK
	case trail.OutcomeError:
		return activityv1.Outcome_OUTCOME_ERROR
	default:
		return activityv1.Outcome_OUTCOME_UNSPECIFIED
	}
}
