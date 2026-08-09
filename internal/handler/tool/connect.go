// Package tool serves the toolchain view: every binary a workspace's spells drive, the
// version each one reported, and the window it is held to.
//
// Read-only, and a READ in the stricter sense docs/scope.md uses: every field comes from
// state magus already builds to run a correct build. The probe is a cache-key input, the
// spell window is `supported` on spells.Tool, the workspace window is a project's `tools`
// key. Nothing here learns which versions exist upstream, selects one, or carries
// end-of-life data.
package tool

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	toolv1 "github.com/egladman/magus/proto/gen/go/magus/tool/v1"
	"github.com/egladman/magus/proto/gen/go/magus/tool/v1/toolv1connect"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// probeTTL bounds how stale a cached probe may be before the next request re-runs it.
//
// A probe forks a process. At op dispatch that cost is already paid because the version
// keys the cache, but a console page has no build to piggyback on, so rendering this view
// would otherwise fork once per declared tool per page load - and a dashboard that
// refreshes is a fork loop. A tool's version changes when someone installs one, which is
// rare on the timescale of a page, so a minute of staleness buys the whole cost back.
//
// The response carries probed_at rather than hiding the age: a reader can see the reading
// is a minute old instead of assuming it is live.
const probeTTL = time.Minute

// workspace is the slice of the workspace this handler reads. An interface rather than
// *magus.Magus so the handler is testable without a real tree, matching how the other
// read services take a narrow source.
type workspace interface {
	All() []*types.Project
	Root() string
}

// Service is the ToolService Connect handler.
type Service struct {
	ws  workspace
	log *slog.Logger

	mu     sync.Mutex
	probes map[string]probe // key: bin \x00 dir
}

// probe is one cached version reading.
type probe struct {
	version string
	at      time.Time
}

// NewService builds the handler over ws.
func NewService(ws workspace, log *slog.Logger) *Service {
	return &Service{ws: ws, log: log, probes: map[string]probe{}}
}

var _ toolv1connect.ToolServiceHandler = (*Service)(nil)

// ListTools reports every project's tools, or one project's when the request names it.
func (s *Service) ListTools(ctx context.Context, req *connect.Request[toolv1.ListToolsRequest]) (*connect.Response[toolv1.ListToolsResponse], error) {
	want := req.Msg.GetProject()
	out := &toolv1.ListToolsResponse{}
	for _, p := range s.ws.All() {
		if want != "" && p.Path != want {
			continue
		}
		proj := &toolv1.Project{Path: p.Path, Name: p.Name, Tools: s.toolsOf(ctx, p)}
		if len(proj.Tools) == 0 {
			continue // a project driving no probed tool is noise in this view
		}
		out.Projects = append(out.Projects, proj)
	}
	return connect.NewResponse(out), nil
}

// toolsOf collects one project's tools, intersecting each spell's declared window with the
// project's own and resolving the verdict against the probed version.
func (s *Service) toolsOf(ctx context.Context, p *types.Project) []*toolv1.Tool {
	var out []*toolv1.Tool
	for _, sp := range p.ResolvedSpells {
		for _, bin := range sp.ToolNames() {
			t, ok := sp.Tool(bin)
			if !ok || t.Probe.Bin == "" {
				continue // nothing to report for a tool magus cannot ask
			}
			ws := p.ToolBounds[bin]
			effective := t.Supported.Intersect(ws)
			version, at := s.probeVersion(ctx, sp, bin, p.Dir)

			row := &toolv1.Tool{
				Bin:              bin,
				Spell:            sp.Name(),
				InstalledVersion: version,
				SpellBounds:      bounds(t.Supported),
				WorkspaceBounds:  bounds(ws),
				Effective:        bounds(effective),
			}
			if !at.IsZero() {
				row.ProbedAt = timestamppb.New(at)
			}
			row.Verdict, row.DiagnosticCode = verdict(effective, version)
			out = append(out, row)
		}
	}
	return out
}

// probeVersion returns the tool's extracted version and when it was read, going to the
// cache first. A probe that fails yields "" rather than an error: an absent tool is not a
// version violation, the same call the CLI's bounds check makes.
func (s *Service) probeVersion(ctx context.Context, sp *spells.Spell, bin, dir string) (string, time.Time) {
	key := bin + "\x00" + dir
	s.mu.Lock()
	if c, ok := s.probes[key]; ok && time.Since(c.at) < probeTTL {
		s.mu.Unlock()
		return c.version, c.at
	}
	s.mu.Unlock()

	raw, err := sp.ProbeVersion(ctx, bin, dir)
	if err != nil {
		return "", time.Time{}
	}
	v, ok := spells.ExtractVersion(raw)
	if !ok {
		v = ""
	}
	now := time.Now()
	s.mu.Lock()
	s.probes[key] = probe{version: v, at: now}
	s.mu.Unlock()
	return v, now
}

// verdict maps a window plus a probed version onto the wire enum and the diagnostic the
// CLI would raise for the same pair, so the console and a terminal never disagree.
func verdict(b spells.VersionBounds, version string) (toolv1.Verdict, string) {
	if version == "" {
		return toolv1.Verdict_VERDICT_UNKNOWN, ""
	}
	switch b.Check(version) {
	case spells.VerdictTooOld:
		return toolv1.Verdict_VERDICT_TOO_OLD, string(types.ToolTooOld)
	case spells.VerdictTooNew:
		return toolv1.Verdict_VERDICT_TOO_NEW, string(types.ToolTooNew)
	case spells.VerdictUnknown:
		return toolv1.Verdict_VERDICT_UNKNOWN, ""
	default:
		return toolv1.Verdict_VERDICT_INSIDE, ""
	}
}

// bounds converts a window to the wire type, returning nil for an unconstrained one so a
// client can distinguish "no window" from "a window with empty ends".
func bounds(b spells.VersionBounds) *toolv1.VersionBounds {
	if b.IsZero() {
		return nil
	}
	return &toolv1.VersionBounds{Min: b.Min, Below: b.Below}
}
