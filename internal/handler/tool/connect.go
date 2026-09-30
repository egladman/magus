// Package tool serves the toolchain view: every binary a workspace's spells drive, the
// version each one reported, the window it is held to, and where its release cycle stands.
//
// Read-only. The probe is a cache-key input, the spell window is `supported` on
// spells.Tool, the workspace window is a project's `tools` key. End-of-life data comes from
// the workspace's lifecycle provider, the one field that reaches the network; nothing here
// selects a version or fails a build.
package tool

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	toolv1 "github.com/egladman/magus/proto/gen/go/magus/tool/v1alpha1"
	"github.com/egladman/magus/proto/gen/go/magus/tool/v1alpha1/toolv1alpha1connect"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// probeTTL bounds how stale a cached probe may be before the next request re-runs it.
//
// A probe forks a process. At op dispatch that cost is already paid because the version
// keys the cache, but a console page has no build to piggyback on, so rendering this view
// would otherwise fork once per declared tool per page load, and a dashboard that
// refreshes is a fork loop. A tool's version changes when someone installs one, which is
// rare on the timescale of a page, so a minute of staleness buys the whole cost back.
//
// The response carries probed_at rather than hiding the age: a reader can see the reading
// is a minute old instead of assuming it is live.
const probeTTL = time.Minute

// probeTimeout bounds ONE probe. A `--version` that reads stdin or reaches the network
// would otherwise hang the request for as long as the client is willing to wait.
const probeTimeout = 10 * time.Second

// lifecycleTTL bounds how long a live lifecycle answer is reused. The provider fetches
// over the network and end-of-life dates move on the scale of months, so a dashboard
// that refreshes asks at most once an hour. An answer that did not arrive (offline,
// unreached) is kept only for probeTTL, so a host that comes back is noticed.
const lifecycleTTL = time.Hour

// workspace is what this handler reads: one method, because that is all it calls. The
// narrow view the other read services take, and what lets the tests run without a tree.
type workspace interface {
	All() []*types.Project
}

// lifecycleAsker is the workspace that can ask its lifecycle provider (a *magus.Magus
// can). A workspace that cannot reads as one with no provider wired.
type lifecycleAsker interface {
	Lifecycles(ctx context.Context, installed ...types.ToolRow) (types.LifecycleStatus, []spells.Lifecycle, error)
}

// Service is the ToolService Connect handler.
type Service struct {
	ws workspace

	mu     sync.Mutex
	probes map[string]probe // key: spell \x00 bin \x00 dir
	// A field rather than the const so a test can shorten it. Nothing else sets it.
	ttl time.Duration

	lifecycleMu sync.Mutex
	lifecycle   *lifecycleAnswer
}

// lifecycleAnswer is one memoized provider answer.
type lifecycleAnswer struct {
	status     types.LifecycleStatus
	lifecycles []spells.Lifecycle
	at         time.Time
}

// probe is one cached version reading. A zero at means no probe ran; an empty version
// with a non-zero at means one ran and read nothing usable. Both are VERDICT_UNKNOWN.
type probe struct {
	version string
	at      time.Time
}

// NewService builds the handler over ws.
func NewService(ws workspace) *Service {
	return &Service{ws: ws, probes: map[string]probe{}, ttl: probeTTL}
}

var _ toolv1alpha1connect.ToolServiceHandler = (*Service)(nil)

// ListTools reports every project's tools, or one project's when the request names it.
func (s *Service) ListTools(ctx context.Context, req *connect.Request[toolv1.ListToolsRequest]) (*connect.Response[toolv1.ListToolsResponse], error) {
	want := req.Msg.GetParent()
	out := &toolv1.ListToolsResponse{}
	matched := false
	var installed []types.ToolRow
	for _, p := range s.ws.All() {
		if want != "" && p.Path != want {
			continue
		}
		matched = true
		// Probing forks. An abandoned request must stop, not walk the rest of the
		// workspace failing instantly and return a blank report as success.
		if err := ctx.Err(); err != nil {
			return nil, connect.NewError(connect.CodeCanceled, err)
		}
		tools := s.projectTools(ctx, p)
		if len(tools) == 0 {
			continue // a project declaring no probeable tool is noise in this view
		}
		for _, t := range tools {
			installed = append(installed, types.ToolRow{Project: p.Path, Bin: t.Bin, Lifecycle: t.Lifecycle, InstalledVersion: t.InstalledVersion})
		}
		out.Projects = append(out.Projects, &toolv1.Project{Path: p.Path, Name: p.Name, Tools: tools})
	}
	// An unknown project is a client error: as an empty list it is indistinguishable from
	// a project that declares no tool.
	if want != "" && !matched {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no project %q in this workspace", want))
	}
	answer, err := s.lifecycles(ctx, installed)
	if err != nil {
		// A malformed provider answer is a bug in the provider spell, and the console
		// surfaces it rather than rendering every row unknown.
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out.Lifecycle = lifecycleToProto(answer.status)
	now := time.Now()
	for _, p := range out.Projects {
		for _, t := range p.Tools {
			t.Cycle, t.Eol, t.Support = placeCycle(t.Lifecycle, t.InstalledVersion, answer, now)
		}
	}
	return connect.NewResponse(out), nil
}

// lifecycles returns the provider's answer, memoized: lifecycleTTL for one that arrived,
// probeTTL for one that did not.
func (s *Service) lifecycles(ctx context.Context, installed []types.ToolRow) (lifecycleAnswer, error) {
	asker, ok := s.ws.(lifecycleAsker)
	if !ok {
		return lifecycleAnswer{status: types.LifecycleStatus{State: types.LifecycleUnwired}}, nil
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if a := s.lifecycle; a != nil {
		ttl := s.ttl
		if a.status.State == types.LifecycleLive || a.status.State == types.LifecycleUnwired {
			ttl = lifecycleTTL
		}
		if time.Since(a.at) < ttl {
			return *a, nil
		}
	}
	status, got, err := asker.Lifecycles(ctx, installed...)
	if err != nil {
		return lifecycleAnswer{}, err
	}
	a := lifecycleAnswer{status: status, lifecycles: got, at: time.Now()}
	s.lifecycle = &a
	return a, nil
}

// placeCycle is the lifecycle half of a row, the same join `magus describe tools` makes:
// empty when the spell names no product or no provider is wired, UNKNOWN for anything the
// answer cannot place.
func placeCycle(key, version string, a lifecycleAnswer, now time.Time) (cycle, eol string, support toolv1.Support) {
	if key == "" || a.status.State == types.LifecycleUnwired {
		return "", "", toolv1.Support_SUPPORT_UNSPECIFIED
	}
	i := slices.IndexFunc(a.lifecycles, func(l spells.Lifecycle) bool { return l.Key == key })
	if i < 0 || version == "" {
		return "", "", toolv1.Support_SUPPORT_UNKNOWN
	}
	c, s := a.lifecycles[i].SupportOf(version, now)
	switch s {
	case spells.SupportSupported:
		return c.Cycle, c.EOL, toolv1.Support_SUPPORT_SUPPORTED
	case spells.SupportEOL:
		return c.Cycle, c.EOL, toolv1.Support_SUPPORT_EOL
	case spells.SupportUnannounced:
		return c.Cycle, c.EOL, toolv1.Support_SUPPORT_UNANNOUNCED
	default:
		return c.Cycle, c.EOL, toolv1.Support_SUPPORT_UNKNOWN
	}
}

func lifecycleToProto(s types.LifecycleStatus) *toolv1.Lifecycle {
	out := &toolv1.Lifecycle{Provider: s.Provider, Sources: s.Sources, Detail: s.Detail}
	switch s.State {
	case types.LifecycleLive:
		out.State = toolv1.LifecycleState_LIFECYCLE_STATE_LIVE
	case types.LifecycleCached:
		out.State = toolv1.LifecycleState_LIFECYCLE_STATE_CACHED
	case types.LifecycleOffline:
		out.State = toolv1.LifecycleState_LIFECYCLE_STATE_OFFLINE
	case types.LifecycleUnreached:
		out.State = toolv1.LifecycleState_LIFECYCLE_STATE_UNREACHED
	case types.LifecycleUnwired:
		out.State = toolv1.LifecycleState_LIFECYCLE_STATE_UNWIRED
	}
	if at, err := time.Parse(time.RFC3339, s.AsOf); err == nil {
		out.AsOf = timestamppb.New(at)
	}
	if at, err := time.Parse(time.RFC3339, s.FetchedAt); err == nil {
		out.FetchedAt = timestamppb.New(at)
	}
	return out
}

// projectTools collects one project's tools, intersecting each spell's declared window
// with the project's own and resolving the verdict against the probed version.
func (s *Service) projectTools(ctx context.Context, p *types.Project) []*toolv1.Tool {
	var out []*toolv1.Tool
	for _, sp := range p.ResolvedSpells {
		for _, bin := range sp.ToolNames() {
			t, _ := sp.Tool(bin) // ToolNames ranges the same map Tool reads
			if t.Probe.Bin == "" {
				// Nothing to ask, including a tool keyed by a declared constant: that
				// token was typed by an author, not read off anything installed.
				// `magus describe tools` skips it too, so the surfaces agree.
				continue
			}
			projBounds := p.ToolBounds[bin]
			effective := t.Supported.Intersect(projBounds)
			pr := s.probeVersion(ctx, sp, bin, p.Dir)

			row := &toolv1.Tool{
				Bin:              bin,
				Spell:            sp.Name(),
				Lifecycle:        t.Lifecycle,
				InstalledVersion: pr.version,
				SpellBounds:      boundsToProto(t.Supported),
				WorkspaceBounds:  boundsToProto(projBounds),
				Effective:        boundsToProto(effective),
			}
			// Only for a reading that happened: a probe that never ran must not carry a
			// timestamp saying it did.
			if !pr.at.IsZero() {
				row.ProbeTime = timestamppb.New(pr.at)
			}
			row.Verdict = verdict(effective, pr.version)
			row.DiagnosticCode = diagnosticCode(row.Verdict)
			out = append(out, row)
		}
	}
	return out
}

// probeVersion returns the tool's reading, cache first. A failed probe yields an empty
// version, not an error: an absent tool is not a violation.
//
// Failures are cached too. Absent tools are the population this view exists to show, so
// caching only successes left exactly them re-forking every request, the fork loop
// probeTTL exists to prevent.
func (s *Service) probeVersion(ctx context.Context, sp *spells.Spell, bin, dir string) probe {
	// Keyed by spell as well as (bin, dir), because the argv comes from the spell: two
	// spells declaring the same bin ask different questions and must not share an answer.
	key := sp.Name() + "\x00" + bin + "\x00" + dir
	s.mu.Lock()
	if c, ok := s.probes[key]; ok && time.Since(c.at) < s.ttl {
		s.mu.Unlock()
		return c
	}
	s.mu.Unlock()

	// A deadline of its own, because probeTTL bounds how OFTEN a probe runs and not how
	// long one takes. Probes here are serial, so one hung binary would otherwise hold the
	// whole request for as long as the client waits.
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	raw, err := sp.ProbeVersion(pctx, bin, dir)
	// A cancelled request is not a reading. Caching it would pin an empty version for a
	// minute on the strength of the client having gone away.
	if ctx.Err() != nil {
		return probe{}
	}
	got := probe{at: time.Now()}
	if err == nil {
		got.version, _ = spells.ExtractVersion(raw)
	}
	s.mu.Lock()
	s.probes[key] = got
	s.mu.Unlock()
	return got
}

// verdict maps a window plus a probed version onto the wire enum, so the console and a
// terminal never disagree about the same pair.
func verdict(b spells.VersionBounds, version string) toolv1.Verdict {
	if version == "" {
		return toolv1.Verdict_VERDICT_UNKNOWN
	}
	switch b.Check(version) {
	case spells.VerdictTooOld:
		return toolv1.Verdict_VERDICT_TOO_OLD
	case spells.VerdictTooNew:
		return toolv1.Verdict_VERDICT_TOO_NEW
	case spells.VerdictInside:
		return toolv1.Verdict_VERDICT_INSIDE
	default:
		// Inside is explicit above so this arm can be UNKNOWN: a verdict added upstream
		// must read as "could not check", never "checked, fine".
		return toolv1.Verdict_VERDICT_UNKNOWN
	}
}

// diagnosticCode is the code the CLI raises for a verdict. Derived, not returned beside
// it, so a new verdict cannot be wired with its code left blank.
func diagnosticCode(v toolv1.Verdict) string {
	switch v {
	case toolv1.Verdict_VERDICT_TOO_OLD:
		return string(types.ToolTooOld)
	case toolv1.Verdict_VERDICT_TOO_NEW:
		return string(types.ToolTooNew)
	default:
		return ""
	}
}

// boundsToProto converts a window to the wire type, returning nil for an unconstrained one
// so a client can distinguish "no window" from "a window with empty ends".
func boundsToProto(b spells.VersionBounds) *toolv1.VersionBounds {
	if b.IsZero() {
		return nil
	}
	return &toolv1.VersionBounds{Min: b.Min, Below: b.Below}
}
