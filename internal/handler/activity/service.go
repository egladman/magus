// Package activity is the console-facing ActivityService handler: it lists recent activity
// events (newest first, filtered) and serves a payload blob by ref for the /dashboard and log
// viewer. The view is DAEMON-WIDE - it merges the trail of every workspace the daemon has
// loaded, because the panel exists to answer "what is touching this daemon" and a per-workspace
// view would silently under-report every other workspace. It is READ-only and maps the on-disk
// trail.Event (internal/trail) to the magus.activity.v1 wire type at the boundary - the store
// owns the format, this owns the wire.
// Mounted on the console's human-facing API surface by the daemon, never under /mcp.
package activity

import (
	"cmp"
	"context"
	"errors"
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

// Workspace names one workspace whose trail the view merges: its root (the identity a reader
// groups rows by) and the cache dir holding the trail. The daemon serves many workspaces, and a
// producer outside the daemon process - the agent hook, which runs as a short-lived client and
// resolves the LOCAL cache dir - writes to its own workspace's trail, so the daemon-wide view is
// the union of them.
type Workspace struct {
	Root     string
	CacheDir string
}

// Service implements activityv1connect.ActivityServiceHandler over the activity trails of every
// workspace the daemon has loaded. Read-only: producers (the MCP handler, agent hooks, and later
// jobs/config/token) write the trails; this reads them.
type Service struct {
	// workspaces is a FUNCTION, not a captured slice, because the daemon's workspace set is
	// live: an adopted run loads a workspace and the idle janitor evicts one while the daemon
	// runs, so a slice taken at mount time would go stale within one session.
	workspaces func() []Workspace
}

// NewService builds a Service merging the trails of the workspaces the source reports at call
// time. A nil source (or one that reports none) yields an empty view rather than an error.
func NewService(workspaces func() []Workspace) *Service { return &Service{workspaces: workspaces} }

// loaded returns the workspaces to read this call, skipping any without a cache dir to read.
func (s *Service) loaded() []Workspace {
	if s.workspaces == nil {
		return nil
	}
	out := make([]Workspace, 0, 4)
	for _, w := range s.workspaces() {
		if w.CacheDir != "" {
			out = append(out, w)
		}
	}
	return out
}

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
	events := readMerged(s.loaded(), limit)
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
			Host:          encodeHost(e),
			Session:       e.Session,
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

// readMerged returns the limit most recent events across every workspace's trail, newest first.
//
// The cap applies to the MERGED set, not per trail: reading limit from each and concatenating
// would neither be the limit most recent daemon-wide nor in time order. Each trail is read for
// up to limit events (any one of them could supply the whole page), then the union is sorted and
// truncated. A trail that cannot be read - a pruned workspace, a permissions error, a workspace
// that has recorded nothing yet - is SKIPPED: one unreadable workspace must not blank the panel
// for the rest.
func readMerged(workspaces []Workspace, limit int) []trail.Event {
	merged := make([]trail.Event, 0, limit)
	for _, w := range workspaces {
		events, err := trail.ReadRecent(w.CacheDir, limit)
		if err != nil {
			continue
		}
		// Events are merged AS RECORDED. Event.Workspace means "the root this action pertained
		// to", and it is deliberately empty for a daemon-wide action - trail.go says so, and an
		// MCP call genuinely is not bound to one workspace.
		//
		// An earlier revision filled those blanks from the trail's owning root, on the theory that
		// a merged view should have no unattributable rows. That was wrong twice over: it made the
		// field mean "the workspace whose trail recorded this" for some events and "the workspace
		// this pertained to" for others, so nothing downstream could rely on either reading; and it
		// made a daemon-wide MCP call claim to belong to whichever workspace's trail happened to
		// catch it, which is simply false. Having to flip an existing assert.Empty assertion to
		// land it was the signal that a real invariant was being overwritten.
		//
		// No consumer needs the substitution: the agent-activity tile groups by host and session,
		// and a reader who wants "which trail" can already tell from the events that DO carry a
		// workspace. An empty value stays empty and keeps meaning what it always meant.
		merged = append(merged, events...)
	}
	// Stable so events sharing a millisecond keep their per-trail order rather than shuffling
	// between calls.
	slices.SortStableFunc(merged, func(a, b trail.Event) int { return cmp.Compare(b.Ts, a.Ts) })
	if len(merged) > limit {
		merged = merged[:limit]
	}
	return merged
}

// GetPayload serves a stored request or response body by its ref. Refs are content-addressed, so
// the same ref names the same bytes in whichever workspace's blob store holds it; the first hit
// across the loaded workspaces answers, and only an unresolvable ref is NotFound.
func (s *Service) GetPayload(_ context.Context, req *connect.Request[activityv1.GetPayloadRequest]) (*connect.Response[activityv1.GetPayloadResponse], error) {
	ref := req.Msg.GetRef()
	var err error
	for _, w := range s.loaded() {
		var body []byte
		if body, err = trail.ReadBlob(w.CacheDir, ref); err == nil {
			return connect.NewResponse(&activityv1.GetPayloadResponse{Body: body, Bytes: int64(len(body))}), nil
		}
	}
	if err == nil {
		err = errors.New("trail: no workspace holds this payload")
	}
	return nil, connect.NewError(connect.CodeNotFound, err)
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

// encodeHost answers "which agent host is behind this event" for both producers that can answer
// it. A hook records the host its wrapper passed in; an MCP call has no such wrapper, but its
// HTTP User-Agent names the same thing, so it fills the field here. The mapping lives at the
// wire boundary rather than in the store because the stored event should keep saying exactly what
// its producer observed - a User-Agent is not a host name, it is being READ as one.
func encodeHost(e trail.Event) string {
	if e.Host != "" {
		return e.Host
	}
	if e.Kind == trail.KindMCPToolCall {
		return e.UserAgent
	}
	return ""
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
	case trail.KindAgentCommand:
		return activityv1.Kind_KIND_AGENT_COMMAND
	case trail.KindMemory:
		return activityv1.Kind_KIND_MEMORY
	case trail.KindCredentialGrant:
		return activityv1.Kind_KIND_CREDENTIAL_GRANT
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
