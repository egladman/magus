// Package console is the pure application logic behind the browser Graph Explorer,
// dashboard, and log viewer. It computes DOMAIN values (a status report, a knowledge
// graph, a target graph, display journals, viewer links) and knows nothing about HTTP:
// the route handlers in internal/handler/{status,graph} wrap these methods and own all
// wire encoding, CORS, and streaming. Keeping this package free of any HTTP dependency
// makes the read-only bridge testable as plain function calls.
package console

import (
	"context"
	"github.com/egladman/magus/types/daemon"
	"github.com/egladman/magus/types/diff"
	"path/filepath"
	"sync"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/review"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
)

// Service assembles the read-only workspace views the console serves. It holds the
// opened workspace plus the static status base, and exposes exported methods that return
// domain types. Construct it with NewService; the optional test seams (With* options)
// stand in for the workspace and server socket so handlers can be exercised without a
// real environment.
type Service struct {
	magus        *magus.Magus
	config       config.Config
	statusBase   daemon.Base
	version      string
	serverSocket string
	// startedAt marks when this service (and thus the server it fronts) began observing, stamped
	// onto every status report as ObservingSince so the dashboard can say the counters are cumulative
	// from here, not all-time. Captured at construction; the server holds one Service for its lifetime.
	startedAt time.Time

	// runsFn returns the server's live runs, folded onto the status report. Nil when
	// this service is not backed by a server run registry (a plain CLI status query), leaving
	// StatusSnapshot.Runs empty.
	runsFn func() []daemon.Run

	// brokerFn returns the broker's status, folded onto the report as its broker section.
	// Nil when this service fronts no server, leaving StatusSnapshot.Broker nil.
	brokerFn func() *daemon.Broker

	// History-metrics cache: git-backed Board fields reused for historyMetricsTTL so
	// repeated dashboard polls collapse onto one git-log scan. The mutex serializes cold assembly.
	historyMetricsMu    sync.Mutex
	historyMetricsCache *historyMetricsEntry
	historyMetricsTTL   time.Duration

	// Test seams. Production leaves these nil; the real Magus / server socket is used.
	statusSnapshotFn func(ctx context.Context) daemon.Snapshot
	knowledgeGraphFn func(ctx context.Context, withSymbols bool) (*knowledge.Graph, error)
	describeGraphFn  func() types.TargetGraphOutput
	historyMetricsFn func(ctx context.Context) (Board, error)
	workingDiffFn    func(ctx context.Context, paths []string) (string, error)
	diffFn           func(ctx context.Context, paths []string, opts diff.Options) (diff.Diff, error)
}

// Option customizes a Service. The With* options inject test seams and the explicit
// server socket; production callers pass none.
type Option func(*Service)

// WithServerSocket sets an explicit server socket address for the status report,
// bypassing proc.DiscoverSocket. Empty means auto-discover at request time.
func WithServerSocket(addr string) Option {
	return func(s *Service) { s.serverSocket = addr }
}

// WithStatusSnapshotFunction replaces the server query used to assemble StatusSnapshot. Tests
// pass this to drive status paths without a running server.
func WithStatusSnapshotFunction(fn func(ctx context.Context) daemon.Snapshot) Option {
	return func(s *Service) { s.statusSnapshotFn = fn }
}

// WithKnowledgeGraphFunction replaces the workspace knowledge graph. Tests pass an in-memory graph.
func WithKnowledgeGraphFunction(fn func(ctx context.Context, withSymbols bool) (*knowledge.Graph, error)) Option {
	return func(s *Service) { s.knowledgeGraphFn = fn }
}

// WithDescribeGraphFunction replaces Magus.TargetGraph. Tests pass a canned target graph.
func WithDescribeGraphFunction(fn func() types.TargetGraphOutput) Option {
	return func(s *Service) { s.describeGraphFn = fn }
}

// WithHistoryMetricsFunction replaces the git-backed Board computation. Tests drive the
// cache without a real workspace or git history.
func WithHistoryMetricsFunction(fn func(ctx context.Context) (Board, error)) Option {
	return func(s *Service) { s.historyMetricsFn = fn }
}

// WithHistoryMetricsTTL overrides how long git-backed Board fields are reused. Zero
// disables caching; negative is treated as zero. Production leaves defaultHistoryMetricsTTL.
func WithHistoryMetricsTTL(d time.Duration) Option {
	return func(s *Service) {
		if d < 0 {
			d = 0
		}
		s.historyMetricsTTL = d
	}
}

// WithRuns supplies the server's live-run source (RunRegistry.Snapshot). The status
// report then carries the per-target execution state of every adopted run, on both the GET
// and the SSE frame. Only the server sets this; a plain CLI status query omits it.
func WithRuns(fn func() []daemon.Run) Option {
	return func(s *Service) { s.runsFn = fn }
}

// WithBrokerStatus supplies the broker's status: capacity, the claims holding it and the
// shared services it keeps warm. The status report then carries them, on both the GET and
// the SSE frame. fn returns nil when no broker answers.
func WithBrokerStatus(fn func() *daemon.Broker) Option {
	return func(s *Service) { s.brokerFn = fn }
}

// NewService builds a Service from the opened workspace (m), its resolved config, the
// static status base (telemetry/cache/build fields the bridge cannot compute itself),
// and the running magus version. m may be nil only when every graph/status path is
// overridden by a With* seam (tests).
func NewService(m *magus.Magus, cfg config.Config, base daemon.Base, version string, opts ...Option) *Service {
	s := &Service{magus: m, config: cfg, statusBase: base, version: version, historyMetricsTTL: defaultHistoryMetricsTTL, startedAt: time.Now().UTC()}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Version is the running magus version stamped onto the status wire message by the
// events handler.
func (s *Service) Version() string { return s.version }

// StatusSnapshot assembles the full status report: the static telemetry/cache/build fields
// from the status base merged with the live pool state. The pool comes from the injected
// StatusSnapshotFn when set (tests), otherwise queried from the server socket.
// As in `magus server status`, a query failure is a PoolError, not an error.
func (s *Service) StatusSnapshot(ctx context.Context) daemon.Snapshot {
	out := s.statusSnapshot(ctx)
	// Live runs come from this server's in-process run registry (not the pool query), so
	// they ride the same report whether the pool is assembled from a seam or a socket query.
	if s.runsFn != nil {
		out.Runs = s.runsFn()
	}
	// The broker is its own process, asked separately from the pool, and folded on the
	// same way live runs are.
	if s.brokerFn != nil {
		out.Broker = s.brokerFn()
	}
	// Per-project SCIP index freshness, computed from this server's opened workspace so
	// the dashboard shows the same "up to date / out of date" the CLI status does.
	if s.magus != nil {
		out.SymbolIndexes = s.magus.SymbolIndexStatus(ctx)
		// Held locks come from the workspace cache rather than the pool query, because a
		// lock is taken by whichever process mutates a project, usually a plain
		// `magus run` the server never sees.
		out.Locks = s.magus.HeldLocks()
	}
	return out
}

// statusSnapshot assembles the base report (telemetry/cache/build plus live pool), before the
// server's live runs are folded on by StatusSnapshot.
func (s *Service) statusSnapshot(ctx context.Context) daemon.Snapshot {
	if s.statusSnapshotFn != nil {
		return s.statusSnapshotFn(ctx)
	}
	out := daemon.Snapshot{
		Telemetry:      s.statusBase.Telemetry,
		Cache:          s.statusBase.Cache,
		Build:          s.statusBase.Build,
		ObservingSince: s.startedAt,
		BrokerPolicy:   s.config.Broker.Resolved(),
		Config: daemon.Config{
			DefaultCharms: s.config.DefaultCharms,
			Concurrency: daemon.Concurrency{
				Configured: s.config.Concurrency,
				Profile:    s.config.ConcurrencyProfile,
				Effective:  cache.ResolveConcurrency(s.config.Concurrency, s.config.ConcurrencyProfile),
			},
			Sandbox: s.config.Sandbox.Mode.Enabled(),
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
	// Affected stays unset; the Graph Explorer's live "affected" view is kept disabled
	// client-side for that reason (see proc.StatusReply.StatusOutput).
	out.Pool = reply.StatusOutput()
	out.Server = reply.Server
	return out
}

func (s *Service) resolveStatusAddr(ctx context.Context) (string, error) {
	if v := s.config.Server.Address; v != "" {
		return v, nil
	}
	if s.serverSocket != "" {
		return s.serverSocket, nil
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
		g, err := s.knowledgeGraph(ctx, knowledge.SeedsLazyLayer(sel))
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

// WorkingDiff returns the working tree's uncommitted changes as one unified patch, scoped to
// paths when non-empty. It backs the console's review app.
//
// Deliberately NOT cached, unlike HistoryMetrics beside it. The history metrics fold a
// bounded git-log scan that costs the same answer for minutes at a time; a working diff is
// the thing the reader is editing RIGHT NOW, and a review app showing a diff from thirty
// seconds ago is worse than one that takes another few milliseconds. The backend's own diff
// is fast because it reads the index, not history.
func (s *Service) WorkingDiff(ctx context.Context, paths []string) (string, error) {
	if s.workingDiffFn != nil {
		return s.workingDiffFn(ctx, paths)
	}
	if s.magus == nil {
		return "", ErrNoWorkspace
	}
	return s.magus.WorkingDiff(ctx, paths)
}

// ReviewOrigin reports the branch and remote a provider needs to find the review open for this
// tree. Empty on a workspace-less service, which is the same answer a tree with no remote
// gives and needs no separate branch in any caller.
func (s *Service) ReviewOrigin(ctx context.Context) diff.ReviewOrigin {
	if s.magus == nil {
		return diff.ReviewOrigin{}
	}
	return s.magus.ReviewOrigin(ctx)
}

// BranchChanges reports what other remote-tracking branches are changing, so a reader can be
// told a file in front of them is being edited elsewhere too. None on a workspace-less service,
// which reads the same as a backend that cannot answer: nothing is claimed either way.
func (s *Service) BranchChanges(ctx context.Context, limit int) ([]types.BranchChange, error) {
	if s.magus == nil {
		return nil, nil
	}
	return s.magus.BranchChanges(ctx, limit)
}

// ProjectTargets names the targets the workspace declares for a project: the allowlist a run
// asked for from outside a terminal is checked against. None on a workspace-less service, which
// refuses every such run rather than admitting one it cannot check.
func (s *Service) ProjectTargets(ctx context.Context, project string) []string {
	if s.magus == nil {
		return nil
	}
	return s.magus.ProjectTargets(ctx, project)
}

// Diff annotates a changed-path set: role, owning project, changed-symbol reach, coverage,
// and the blast radius, ordered by what magus recommends reading first.
//
// It is a SECOND round trip on purpose, and the split is the performance design. WorkingDiff
// above is a index read and returns in milliseconds; this one loads the knowledge graph with
// its symbol shards and computes a reverse closure. Folding them into one route would hold
// the whole diff behind the slowest overlay, so the console paints the patch from the first
// call and decorates it when this one lands. A reader is scrolling code either way.
func (s *Service) Diff(ctx context.Context, paths []string) (diff.Diff, error) {
	return s.DiffWith(ctx, paths, diff.Options{})
}

// DiffWith is [Service.Diff] with the options [diff.Options] describes. A caller that reads
// no ranking of the changeset, such as a thread brief, sets SkipOrder so the order is never
// computed.
func (s *Service) DiffWith(ctx context.Context, paths []string, opts diff.Options) (diff.Diff, error) {
	if s.diffFn != nil {
		return s.diffFn(ctx, paths, opts)
	}
	if s.magus == nil {
		return diff.Diff{}, ErrNoWorkspace
	}
	rev, err := s.magus.DiffWith(ctx, paths, opts)
	if err != nil {
		return diff.Diff{}, err
	}
	// Fold on the churn lenses from the CACHED insight scan rather than a fresh one. This is
	// the reason AttachChurn takes its data as an argument: the server already keeps a bounded
	// git-log scan warm for the dashboard, so a review costs nothing extra to answer "is this
	// file being rewritten over and over", which is the question a diff cannot answer and the
	// one worth asking while somebody is still looking at the file.
	//
	// Best-effort, like every other overlay: a workspace with no history yields no churn, and
	// the files stay nil rather than reporting a confident zero.
	if board, herr := s.HistoryMetrics(ctx); herr == nil {
		rev.AttachChurn(board.Hotspots.Files, board.Trend.Projects)
	}
	// The agent record: which sessions wrote each file and what they had read first, from the
	// guard hook's trail and from loaded transcripts.
	trail.AttachTouches(&rev, s.magus.Root(), s.magus.CacheDir())
	// Which of these files somebody has recorded reading, from the same store `magus diff
	// --ack` writes. The console gets it because "how much of this has anyone read" is a
	// question a review app should answer without the reader dropping to a terminal.
	// The console reads the server's working tree and nothing else, so it names that source
	// explicitly rather than inheriting a default.
	root := s.magus.Root()
	digest := func(path string) string { return review.DigestFile(filepath.Join(root, filepath.FromSlash(path))) }
	if states, serr := review.ReadStates(s.magus.CacheDir(), paths, digest); serr == nil {
		rev.AttachReadState(states)
	}
	// Where the reader left off, from the same receipts. It ships to the console for the reason
	// the read states do: a client that can show what moved since the last pass and one that
	// cannot are not two clients of one review, and the terminal already had it.
	if store, serr := review.Load(s.magus.CacheDir()); serr == nil {
		rev.AttachReviewed(store.ReviewedAt(paths))
	}
	return rev, nil
}

// knowledgeGraph resolves the workspace graph, honoring the test seam. withSymbols loads
// the @symbols shards; the production path requests the @symbols scope only when
// they are actually needed so the default export stays lazy.
func (s *Service) knowledgeGraph(ctx context.Context, withSymbols bool) (*knowledge.Graph, error) {
	if s.knowledgeGraphFn != nil {
		return s.knowledgeGraphFn(ctx, withSymbols)
	}
	// The graph is one of the methods workspace.ViewAs adds to an opened *magus.Magus.
	g := workspace.ViewAs[interface {
		KnowledgeGraph(ctx context.Context, req types.GraphRequest) (*knowledge.Graph, error)
	}](s.magus)
	if withSymbols {
		return g.KnowledgeGraph(ctx, types.GraphRequest{Symbols: types.AllSymbols})
	}
	return g.KnowledgeGraph(ctx, types.GraphRequest{})
}

// TargetGraph returns the describe-graph view (targets flavor). The error return
// reports a cancelled ctx cutting the underlying Magus.TargetGraph walk short,
// keeping the seam uniform with Graph.
func (s *Service) TargetGraph(ctx context.Context) (types.TargetGraphOutput, error) {
	if s.describeGraphFn != nil {
		return s.describeGraphFn(), nil
	}
	if s.magus == nil {
		return types.TargetGraphOutput{}, ErrNoWorkspace
	}
	return s.magus.TargetGraph(ctx)
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
			Kind:  types.KindProject,
			Label: p.Path,
		})
		for _, dep := range p.DependsOn {
			links = append(links, types.KnowledgeEdge{
				Source:   p.Path,
				Target:   dep,
				Relation: types.RelationDependsOn,
			})
		}
	}

	return types.KnowledgeGraphOutput{
		Definition:    tg.Definition,
		APIVersion: types.KnowledgeAPIVersion,
		Directed:      true,
		NodeCount:     len(nodes),
		EdgeCount:     len(links),
		Nodes:         nodes,
		Links:         links,
		// The vocabulary travels with every flavor, because the schema version is what a
		// consumer reads to decide whether it can resolve a predicate without a matching
		// binary. Stamping the version while leaving these null tells it the vocabulary is
		// there and then hands it nothing, worse than an older version, which at least
		// says to fall back.
		Relations:           types.KnowledgeRelationDefinitions(),
		RelationFingerprint: types.KnowledgeRelationFingerprint(),
	}
}
