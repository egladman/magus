// Package console is the pure application logic behind the browser Graph Explorer,
// dashboard, and log viewer. It computes DOMAIN values (a status report, a knowledge
// graph, a target graph, display journals, viewer links) and knows nothing about HTTP:
// the route handlers in internal/handler/{status,graph} wrap these methods and own all
// wire encoding, CORS, and streaming. Keeping this package free of any HTTP dependency
// makes the read-only bridge surface testable as plain function calls.
package console

import (
	"context"
	"sync"
	"time"

	magus "github.com/egladman/magus"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/types"
)

// Service assembles the read-only workspace views the console serves. It holds the
// opened workspace plus the static status base, and exposes exported methods that return
// domain types. Construct it with NewService; the optional test seams (With* options)
// stand in for the workspace and daemon socket so handlers can be exercised without a
// real environment.
type Service struct {
	magus        *magus.Magus
	config       config.Config
	statusBase   types.StatusBase
	version      string
	daemonSocket string
	// startedAt marks when this service (and thus the daemon it fronts) began observing, stamped
	// onto every status report as ObservingSince so the dashboard can say the counters are cumulative
	// from here, not all-time. Captured at construction; the daemon holds one Service for its lifetime.
	startedAt time.Time

	// runsFn returns the daemon's live runs, folded onto the status report. Nil when
	// this service is not backed by a daemon run registry (a plain CLI status query), leaving
	// StatusReport.Runs empty.
	runsFn func() []types.StatusRun

	// servicesFn returns the daemon's hosted shared services, folded onto the status
	// report. Nil when this service is not backed by a daemon service registry, leaving
	// StatusReport.Services empty.
	servicesFn func() []types.StatusService

	// Insight cache: an assembled InsightView reused for insightTTL so repeated dashboard
	// polls collapse onto one git-log scan. The mutex also serializes cold-cache assembly.
	insightMu    sync.Mutex
	insightCache *insightEntry
	insightTTL   time.Duration

	// Test seams. Production leaves these nil; the real Magus / daemon socket is used.
	statusReportFn   func(ctx context.Context) types.StatusReport
	knowledgeGraphFn func(ctx context.Context, withSymbols bool) (*knowledge.Graph, error)
	describeGraphFn  func() types.TargetGraphOutput
	insightFn        func(ctx context.Context) (types.InsightView, error)
}

// Option customizes a Service. The With* options inject test seams and the explicit
// daemon socket; production callers pass none.
type Option func(*Service)

// WithDaemonSocket sets an explicit daemon socket address for the status report,
// bypassing proc.DiscoverSocket. Empty means auto-discover at request time.
func WithDaemonSocket(addr string) Option {
	return func(s *Service) { s.daemonSocket = addr }
}

// WithStatusReportFn replaces the daemon query used to assemble StatusReport. Tests
// pass this to drive status paths without a running daemon.
func WithStatusReportFn(fn func(ctx context.Context) types.StatusReport) Option {
	return func(s *Service) { s.statusReportFn = fn }
}

// WithKnowledgeGraphFn replaces Magus.KnowledgeGraph. Tests pass an in-memory graph.
func WithKnowledgeGraphFn(fn func(ctx context.Context, withSymbols bool) (*knowledge.Graph, error)) Option {
	return func(s *Service) { s.knowledgeGraphFn = fn }
}

// WithDescribeGraphFn replaces Magus.DescribeGraph. Tests pass a canned target graph.
func WithDescribeGraphFn(fn func() types.TargetGraphOutput) Option {
	return func(s *Service) { s.describeGraphFn = fn }
}

// WithInsightFn replaces the workspace lens computation behind Insight. Tests pass this to
// drive the cache without a real workspace or git history.
func WithInsightFn(fn func(ctx context.Context) (types.InsightView, error)) Option {
	return func(s *Service) { s.insightFn = fn }
}

// WithInsightTTL overrides how long an assembled InsightView is reused before the git-log
// scan runs again. Zero disables caching (every call recomputes); negative is treated as
// zero. Production leaves it at defaultInsightTTL.
func WithInsightTTL(d time.Duration) Option {
	return func(s *Service) {
		if d < 0 {
			d = 0
		}
		s.insightTTL = d
	}
}

// WithRuns supplies the daemon's live-run source (RunRegistry.Snapshot). The status
// report then carries the per-target execution state of every adopted run, on both the GET
// and the SSE frame. Only the daemon sets this; a plain CLI status query omits it.
func WithRuns(fn func() []types.StatusRun) Option {
	return func(s *Service) { s.runsFn = fn }
}

// WithServices supplies the daemon's hosted-services source (service.Registry.Snapshot).
// The status report then carries the long-running shared services the daemon is keeping
// warm, on both the GET and the SSE frame. Only the daemon sets this; a plain CLI status
// query omits it.
func WithServices(fn func() []types.StatusService) Option {
	return func(s *Service) { s.servicesFn = fn }
}

// NewService builds a Service from the opened workspace (m), its resolved config, the
// static status base (telemetry/cache/build fields the bridge cannot compute itself),
// and the running magus version. m may be nil only when every graph/status path is
// overridden by a With* seam (tests).
func NewService(m *magus.Magus, cfg config.Config, base types.StatusBase, version string, opts ...Option) *Service {
	s := &Service{magus: m, config: cfg, statusBase: base, version: version, insightTTL: defaultInsightTTL, startedAt: time.Now().UTC()}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Version is the running magus version stamped onto the status wire message by the
// events handler.
func (s *Service) Version() string { return s.version }

// StatusReport assembles the full status report: the static telemetry/cache/build fields
// from the status base merged with the live pool state. The pool comes from the injected
// StatusReportFn when set (tests), otherwise queried from the daemon socket; a query
// failure is reported as PoolError rather than an error return, matching `magus status`.
func (s *Service) StatusReport(ctx context.Context) types.StatusReport {
	out := s.statusReport(ctx)
	// Live runs come from this daemon's in-process run registry (not the pool query), so
	// they ride the same report whether the pool is assembled from a seam or a socket query.
	if s.runsFn != nil {
		out.Runs = s.runsFn()
	}
	// Hosted shared services come from this daemon's in-process service registry (not the
	// pool query), folded on the same way live runs are.
	if s.servicesFn != nil {
		out.Services = s.servicesFn()
	}
	// Per-project SCIP index freshness, computed from this daemon's opened workspace so
	// the dashboard shows the same "up to date / out of date" the CLI status does.
	if s.magus != nil {
		out.SymbolIndexes = s.magus.SymbolIndexStatus(ctx)
	}
	return out
}

// statusReport assembles the base report (telemetry/cache/build plus live pool), before the
// daemon's live runs are folded on by StatusReport.
func (s *Service) statusReport(ctx context.Context) types.StatusReport {
	if s.statusReportFn != nil {
		return s.statusReportFn(ctx)
	}
	out := types.StatusReport{
		Telemetry:      s.statusBase.Telemetry,
		Cache:          s.statusBase.Cache,
		Build:          s.statusBase.Build,
		ObservingSince: s.startedAt,
		Config: types.StatusConfig{
			DefaultCharms: s.config.DefaultCharms,
			Concurrency:   s.config.Concurrency,
			Sandbox:       s.config.Sandbox.Enabled,
		},
	}
	addr, err := s.resolveStatusAddr(ctx)
	if err != nil {
		out.PoolError = err.Error()
		return out
	}
	reply, qerr := proc.QueryStatus(ctx, addr)
	if qerr != nil {
		out.PoolError = qerr.Error()
		return out
	}
	out.Pool = statusOutputFromReply(reply)
	return out
}

// statusOutputFromReply converts a proc.StatusReply into a types.StatusOutput, mirroring
// the conversion in cmd/magus/status.go so both consumers produce identical shapes.
//
// It deliberately leaves StatusOutput.Affected unset (deferred, not an oversight):
// computing it needs a workspace-scoped VCS diff (magus.Magus.Affected), a meaningfully
// heavier per-request operation than the rest of this path. The Graph Explorer's live
// "affected" view is correspondingly kept disabled client-side.
func statusOutputFromReply(r *proc.StatusReply) *types.StatusOutput {
	if r == nil {
		return nil
	}
	out := &types.StatusOutput{
		ParentPID:     r.ParentPID,
		DaemonVersion: r.DaemonVersion,
		Mode:          r.Mode,
		Capacity:      r.Capacity,
		Running:       r.Running,
		Queued:        r.Queued,
	}
	for _, c := range r.Calls {
		out.RunningTargets = append(out.RunningTargets, types.StatusRunningTarget{
			Args:      c.Args,
			Workspace: c.Workspace,
			StartedAt: c.StartedAt,
			Step:      c.SubOp,
			Inv:       c.Inv,
		})
	}
	for _, ws := range r.Workspaces {
		out.Workspaces = append(out.Workspaces, types.StatusWorkspace{
			Root:       ws.Root,
			LoadedAt:   ws.LoadedAt,
			LastAccess: ws.LastAccess,
			CacheHit:   ws.CacheHit,
			CacheMiss:  ws.CacheMiss,
			CacheError: ws.CacheError,
			CacheBytes: ws.CacheBytes,
		})
	}
	return out
}

func (s *Service) resolveStatusAddr(ctx context.Context) (string, error) {
	if v := s.config.Daemon.Address; v != "" {
		return v, nil
	}
	if s.daemonSocket != "" {
		return s.daemonSocket, nil
	}
	return proc.DiscoverSocket(ctx)
}

// Graph returns a knowledge-graph flavor as a domain value:
//   - "skeleton": project nodes + project->project depends_on edges only (KBs at any scale)
//   - "select":   the scoped neighborhood induced by sel (graph export --select semantics)
//   - "full" (default): the whole graph
//
// Symbol shards are loaded only when the select terms seed symbols, preserving the store's
// lazy-load contract (@symbols excluded from the default export).
func (s *Service) Graph(ctx context.Context, flavor, sel string) (types.KnowledgeGraphOutput, error) {
	switch flavor {
	case "skeleton":
		tg, err := s.TargetGraph(ctx)
		if err != nil {
			return types.KnowledgeGraphOutput{}, err
		}
		return projectSkeleton(tg), nil
	case "select":
		g, err := s.knowledgeGraph(ctx, knowledge.SeedsSymbols(sel))
		if err != nil {
			return types.KnowledgeGraphOutput{}, err
		}
		return g.Select(sel, knowledge.DefaultBudget), nil
	default: // "full"
		g, err := s.knowledgeGraph(ctx, false)
		if err != nil {
			return types.KnowledgeGraphOutput{}, err
		}
		return g.Output(), nil
	}
}

// knowledgeGraph resolves the workspace graph, honoring the test seam. withSymbols loads
// the @symbols shards; the production path routes to KnowledgeGraphWithSymbols only when
// they are actually needed so the default export stays lazy.
func (s *Service) knowledgeGraph(ctx context.Context, withSymbols bool) (*knowledge.Graph, error) {
	if s.knowledgeGraphFn != nil {
		return s.knowledgeGraphFn(ctx, withSymbols)
	}
	if withSymbols {
		return s.magus.KnowledgeGraphWithSymbols(ctx)
	}
	return s.magus.KnowledgeGraph(ctx, false)
}

// TargetGraph returns the describe-graph view (targets flavor). It never errors today
// (DescribeGraph is in-memory); the error return keeps the seam uniform with Graph and
// leaves room for a future workspace-backed implementation.
func (s *Service) TargetGraph(ctx context.Context) (types.TargetGraphOutput, error) {
	if s.describeGraphFn != nil {
		return s.describeGraphFn(), nil
	}
	return s.magus.DescribeGraph(ctx), nil
}

// projectSkeleton reduces a TargetGraphOutput to only project nodes and project->project
// depends_on edges, producing a KnowledgeGraphOutput the explorer renders as the collapsed
// default view.
func projectSkeleton(tg types.TargetGraphOutput) types.KnowledgeGraphOutput {
	nodes := make([]types.KnowledgeNode, 0, len(tg.Projects))
	var links []types.KnowledgeEdge

	for _, p := range tg.Projects {
		nodes = append(nodes, types.KnowledgeNode{
			ID:    p.Path,
			Kind:  "project",
			Label: p.Path,
		})
		for _, dep := range p.DependsOn {
			links = append(links, types.KnowledgeEdge{
				Source:   p.Path,
				Target:   dep,
				Relation: "depends_on",
			})
		}
	}

	return types.KnowledgeGraphOutput{
		Definition:    tg.Definition,
		SchemaVersion: types.KnowledgeSchemaVersion,
		Directed:      true,
		NodeCount:     len(nodes),
		EdgeCount:     len(links),
		Nodes:         nodes,
		Links:         links,
	}
}
