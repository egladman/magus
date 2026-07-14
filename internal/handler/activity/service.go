// Package activity is the console-facing ActivityService handler: it lists recent activity
// events (newest first, filtered) and serves a payload blob by ref for the /dashboard and log
// viewer. It is READ-only and maps the on-disk activity.Event (internal/activity) to the
// magus.activity.v1 wire type at the boundary - the store owns the format, this owns the wire.
// Mounted on the console's human-facing API surface by the daemon, never under /mcp.
package activity

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	activitystore "github.com/egladman/magus/internal/activity"
	activityv1 "github.com/egladman/magus/proto/gen/go/magus/activity/v1"
	"github.com/egladman/magus/proto/gen/go/magus/activity/v1/activityv1connect"
)

const (
	defaultPageSize = 200
	maxPageSize     = 1000
)

// Service implements activityv1connect.ActivityServiceHandler over a workspace's activity
// trail at cacheDir. Read-only: producers (the MCP handler, and later jobs/config/token) write
// the trail; this reads it.
type Service struct {
	cacheDir string
}

// NewService builds a Service reading the trail under cacheDir.
func NewService(cacheDir string) *Service { return &Service{cacheDir: cacheDir} }

var _ activityv1connect.ActivityServiceHandler = (*Service)(nil)

// ListActivity returns recent events, newest first, narrowed by the request filter. Paging is
// a simple recent-window today (page_size, capped); page_token is unused, so next_page_token
// is always empty - enough for the dashboard's "recent activity" view.
func (s *Service) ListActivity(_ context.Context, req *connect.Request[activityv1.ListActivityRequest]) (*connect.Response[activityv1.ListActivityResponse], error) {
	limit := int(req.Msg.GetPageSize())
	if limit <= 0 {
		limit = defaultPageSize
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}
	events, err := activitystore.ReadRecent(s.cacheDir, limit)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	filter := req.Msg.GetFilter()
	out := make([]*activityv1.ActivityEvent, 0, len(events))
	for _, e := range events {
		if matchFilter(e, filter) {
			out = append(out, toProto(e))
		}
	}
	return connect.NewResponse(&activityv1.ListActivityResponse{Events: out}), nil
}

// GetPayload serves a stored request or response body by its ref.
func (s *Service) GetPayload(_ context.Context, req *connect.Request[activityv1.GetPayloadRequest]) (*connect.Response[activityv1.GetPayloadResponse], error) {
	body, err := activitystore.Blob(s.cacheDir, req.Msg.GetRef())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(&activityv1.GetPayloadResponse{Body: body, Bytes: int64(len(body))}), nil
}

// matchFilter applies the ActivityQuery's set filters (kinds/actors/actions), ANDed; an empty
// or absent field does not constrain. The time window is not yet applied - ReadRecent already
// bounds the result to the recent tail.
func matchFilter(e activitystore.Event, q *activityv1.ActivityQuery) bool {
	if q == nil {
		return true
	}
	if kinds := q.GetKinds(); len(kinds) > 0 && !containsKind(kinds, kindToProto(e.Kind)) {
		return false
	}
	if actors := q.GetActors(); len(actors) > 0 && !containsString(actors, e.Actor) {
		return false
	}
	if actions := q.GetActions(); len(actions) > 0 && !containsString(actions, e.Action) {
		return false
	}
	return true
}

func toProto(e activitystore.Event) *activityv1.ActivityEvent {
	pe := &activityv1.ActivityEvent{
		Time:          timestamppb.New(time.UnixMilli(e.TimeMs)),
		Kind:          kindToProto(e.Kind),
		Actor:         e.Actor,
		Action:        e.Action,
		Outcome:       outcomeToProto(e.Outcome),
		Error:         e.Error,
		RequestRef:    e.RequestRef,
		ResponseRef:   e.ResponseRef,
		Preview:       e.Preview,
		RequestBytes:  e.RequestBytes,
		ResponseBytes: e.ResponseBytes,
	}
	if e.DurationMs > 0 {
		pe.Duration = durationpb.New(time.Duration(e.DurationMs) * time.Millisecond)
	}
	return pe
}

func kindToProto(k string) activityv1.Kind {
	switch k {
	case activitystore.KindMCPToolCall:
		return activityv1.Kind_KIND_MCP_TOOL_CALL
	case activitystore.KindJob:
		return activityv1.Kind_KIND_JOB
	case activitystore.KindConfigChange:
		return activityv1.Kind_KIND_CONFIG_CHANGE
	case activitystore.KindTokenLifecycle:
		return activityv1.Kind_KIND_TOKEN_LIFECYCLE
	case activitystore.KindSandboxDenial:
		return activityv1.Kind_KIND_SANDBOX_DENIAL
	default:
		return activityv1.Kind_KIND_UNSPECIFIED
	}
}

func outcomeToProto(o string) activityv1.Outcome {
	switch o {
	case activitystore.OutcomeOK:
		return activityv1.Outcome_OUTCOME_OK
	case activitystore.OutcomeError:
		return activityv1.Outcome_OUTCOME_ERROR
	default:
		return activityv1.Outcome_OUTCOME_UNSPECIFIED
	}
}

func containsKind(haystack []activityv1.Kind, needle activityv1.Kind) bool {
	for _, k := range haystack {
		if k == needle {
			return true
		}
	}
	return false
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
