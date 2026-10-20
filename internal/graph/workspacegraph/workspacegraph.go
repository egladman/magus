package workspacegraph

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
)

// Builders are the knowledge-graph builders over a workspace the root magus package
// evaluates. They live in the root package, which holds the evaluation they read, and
// take a config.Config, which no caller outside this module can construct; the root
// package installs them here, in its init, so the CLI can call them without the public
// API naming an internal type. Each field is documented on the function of the same name
// below.
type Builders struct {
	BuildGlobal          func(ctx context.Context, ws types.WorkspaceRepository, cfg config.Config, refresh bool, log *slog.Logger) (*knowledge.Graph, error)
	Build                func(ctx context.Context, ws types.Inspector, root string, cfg config.Config, refresh bool, log *slog.Logger) (*knowledge.Graph, error)
	MergeSymbols         func(ctx context.Context, ws types.Inspector, root string, cfg config.Config, g *knowledge.Graph, log *slog.Logger) error
	MergeSymbolsForRef   func(ctx context.Context, ws types.Inspector, root string, cfg config.Config, g *knowledge.Graph, ref string, log *slog.Logger) error
	Query                func(ctx context.Context, ws types.Inspector, root string, cfg config.Config, refresh bool, input string, budget int, log *slog.Logger) (types.KnowledgeQueryOutput, *knowledge.Graph, error)
	SymbolGaps           func(ctx context.Context, ws types.Inspector, root string, cfg config.Config, log *slog.Logger) ([]types.KnowledgeSymbolGap, bool)
	SymbolIndexTimes     func(ctx context.Context, ws types.Inspector, root string, cfg config.Config) (map[string]time.Time, bool)
	SymbolOccurrences    func(ctx context.Context, ws types.Inspector, root string, cfg config.Config, log *slog.Logger, key string) (SymbolOccurrenceRead, bool)
	RecordedHumanOnly    func(ctx context.Context, ws types.Inspector, cfg config.Config) (types.HumanOnlyIndex, bool)
	RecordedStaleIndexes func(ctx context.Context, ws types.Inspector, cfg config.Config) ([]types.KnowledgeStaleIndex, bool)
	UsePublishedShards   func(ws types.Inspector, r knowledge.RemoteShards)
	ReaderContext        func(ctx context.Context, ws types.Inspector, root string) ([]string, error)
}

var builders *Builders

// Register installs the root magus package's builders. It is called once, from that
// package's init; a second call panics, since two sets would make every function here
// depend on import order.
func Register(b Builders) {
	if builders != nil {
		panic("workspacegraph: Register called twice")
	}
	builders = &b
}

// linked returns the registered builders, and panics when no root magus package is
// linked, which no binary that evaluates a workspace can be.
func linked() *Builders {
	if builders == nil {
		panic("workspacegraph: no root magus package linked to build a knowledge graph")
	}
	return builders
}

// BuildGlobal unions the current workspace with each registered one
// (cfg.Knowledge.Workspaces), namespacing node IDs by workspace so repos can't collide. A
// workspace that fails to open is skipped with a warning, not fatal: the query degrades to
// what it can reach.
func BuildGlobal(ctx context.Context, ws types.WorkspaceRepository, cfg config.Config, refresh bool, log *slog.Logger) (*knowledge.Graph, error) {
	return linked().BuildGlobal(ctx, ws, cfg, refresh, log)
}

// Build returns the default knowledge graph of the workspace ws evaluates at root, from the
// store when its inputs are unchanged and rebuilding only the shard classes whose stamps
// moved; refresh rebuilds every class. It leaves the symbol classes to MergeSymbols.
func Build(ctx context.Context, ws types.Inspector, root string, cfg config.Config, refresh bool, log *slog.Logger) (*knowledge.Graph, error) {
	return linked().Build(ctx, ws, root, cfg, refresh, log)
}

// MergeSymbols merges every declared symbol index of the workspace into g.
func MergeSymbols(ctx context.Context, ws types.Inspector, root string, cfg config.Config, g *knowledge.Graph, log *slog.Logger) error {
	return linked().MergeSymbols(ctx, ws, root, cfg, g, log)
}

// MergeSymbolsForRef merges into g only the symbol shards that mention ref.
func MergeSymbolsForRef(ctx context.Context, ws types.Inspector, root string, cfg config.Config, g *knowledge.Graph, ref string, log *slog.Logger) error {
	return linked().MergeSymbolsForRef(ctx, ws, root, cfg, g, ref, log)
}

// Query answers input over the workspace's graph within budget, and returns the graph it
// answered from.
func Query(ctx context.Context, ws types.Inspector, root string, cfg config.Config, refresh bool, input string, budget int, log *slog.Logger) (types.KnowledgeQueryOutput, *knowledge.Graph, error) {
	return linked().Query(ctx, ws, root, cfg, refresh, input, budget, log)
}

// SymbolGaps reports the projects whose declared symbol index the workspace could not
// read, and false when the probe could not run.
func SymbolGaps(ctx context.Context, ws types.Inspector, root string, cfg config.Config, log *slog.Logger) ([]types.KnowledgeSymbolGap, bool) {
	return linked().SymbolGaps(ctx, ws, root, cfg, log)
}

// SymbolIndexTimes reports, by project path, when each project's declared symbol indexes
// were written (the oldest of several), and false when the declarations cannot be
// resolved.
func SymbolIndexTimes(ctx context.Context, ws types.Inspector, root string, cfg config.Config) (map[string]time.Time, bool) {
	return linked().SymbolIndexTimes(ctx, ws, root, cfg)
}

// SymbolOccurrences returns every verified source range where the symbol keyed by key
// appears, read from the same declared indexes the graph is built from, and false when
// the probe could not run.
func SymbolOccurrences(ctx context.Context, ws types.Inspector, root string, cfg config.Config, log *slog.Logger, key string) (SymbolOccurrenceRead, bool) {
	return linked().SymbolOccurrences(ctx, ws, root, cfg, log, key)
}

// RecordedHumanOnly answers, for a lazy workspace that has not been opened, what it
// declares human_only, from what the knowledge store recorded at its last evaluated sync.
// false means the caller must open the workspace to learn it, which it also means for a
// ws that is no lazy workspace. It never opens ws and writes nothing.
func RecordedHumanOnly(ctx context.Context, ws types.Inspector, cfg config.Config) (types.HumanOnlyIndex, bool) {
	return linked().RecordedHumanOnly(ctx, ws, cfg)
}

// RecordedStaleIndexes answers, for a lazy workspace that has not been opened, which
// declared symbol indexes the last evaluation judged stale, from the verdicts the store
// recorded. false means the caller must open the workspace and judge afresh.
func RecordedStaleIndexes(ctx context.Context, ws types.Inspector, cfg config.Config) ([]types.KnowledgeStaleIndex, bool) {
	return linked().RecordedStaleIndexes(ctx, ws, cfg)
}

// UsePublishedShards installs r as a read-only shard source on ws, consulted whenever the
// knowledge store reaches for a shard it does not hold. A ws the root package did not open
// is left alone: it has no store for r to back.
func UsePublishedShards(ws types.Inspector, r knowledge.RemoteShards) {
	linked().UsePublishedShards(ws, r)
}

// ReaderContext returns the workspace paths a reader at root is working on, sorted: the
// files the working tree changed against the VCS base and, when the process acts for a
// live lease, that job's write paths, a glob contributing its directory. Free-text
// retrieval seeds its graph list with them. An error means the VCS or the job store could
// not be asked, or that no root magus package is linked to ask them, as in a test of a
// host module over a fake workspace; a caller ranks without the context and says why.
func ReaderContext(ctx context.Context, ws types.Inspector, root string) ([]string, error) {
	if builders == nil {
		return nil, errors.New("workspacegraph: no root magus package linked to read the reader context")
	}
	return builders.ReaderContext(ctx, ws, root)
}

// SymbolOccurrenceRead is what SymbolOccurrences could read: the verified sites, the
// spellings they were checked against, and every declared index that exists but yielded
// nothing usable.
//
// Unreadable is the field that keeps the result honest. The occurrence list claims to be
// complete, so an index magus could not decode has to travel WITH the sites rather than be
// dropped on the way: a caller folds it into the coverage gaps, which turns the verdict
// from "searched everywhere" into "unknown, not absent" and names the project to rebuild.
// The sites that WERE read are still returned: a partial answer plus an accurate account
// of what is missing beats discarding both.
type SymbolOccurrenceRead struct {
	Files []types.SymbolOccurrenceFile
	// Names are the spellings an occurrence may hold; Names[0] is the identifier a rename
	// targets. See symbols.ParseOccurrences.
	Names      []string
	Unreadable []types.KnowledgeSymbolGap
}
