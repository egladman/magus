// Package wire holds option-carrier types that the public magus package re-exports.
// Constructors that take internal types (config.Config, *cache.Limiter, etc.) live here
// so in-tree callers can reach them without exposing internals on the public magus surface.
package wire

import (
	"io"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/types"
)

// Load is the accumulated state of an Open or Inspect call.
type Load struct {
	ConfigPath string
	Preloaded  *config.Config
	Limiter    *cache.Limiter
	Registry   *WorkspaceRegistry
}

// Option configures Open or Inspect.
type Option func(*Load)

// WithLoadedConfig injects an already-parsed config instead of reading magus.yaml.
func WithLoadedConfig(cfg config.Config) Option {
	return func(o *Load) { o.Preloaded = &cfg }
}

// WithLimiter injects a pre-built concurrency limiter (e.g. shared across daemon workspaces).
func WithLimiter(lim *cache.Limiter) Option {
	return func(o *Load) { o.Limiter = lim }
}

// Run is the accumulated state of a Run/CI/RunAffected call.
type Run struct {
	DryRun       bool
	Charms       []string       // execution charms propagated via context; "rw" enables mutating targets
	Report       *report.Writer // caller-owned; caller closes; mutually exclusive with ReportWriter
	ReportWriter io.Writer      // run engine wraps this in its own Writer (public seam; no internal import needed)
	NoFlakeRetry bool
	BaseRef      string
	Race         bool     // MGS4001/4002/4004 race diagnostics; near-zero overhead
	RaceReplay   bool     // MGS4003 determinism replay; ~2× wall-clock; orthogonal to Race
	Spell        string   // when set, restricts execution to this spell; unmatched projects are skipped
	Step         bool     // forces Concurrency=1; StepGate comes from ctx
	ExtraArgs    []string // forwarded to spells via project.WithExtraArgs
	Normalizer   types.TargetNameNormalizer
}

// RunOption configures a Run, CI, or RunAffected invocation.
type RunOption func(*Run)

// WithReport wires w to receive one JSONL event per target.
func WithReport(w *report.Writer) RunOption {
	return func(o *Run) { o.Report = w }
}

// WithReportWriter wires a plain io.Writer to receive JSONL run events.
// The run engine constructs and closes the report.Writer around w.
func WithReportWriter(w io.Writer) RunOption {
	return func(o *Run) { o.ReportWriter = w }
}

// Compose is the accumulated state of a ComposeGraph call.
type Compose struct {
	Graph       *types.Graph
	History     *forecast.History
	Target      string
	Upstream    bool
	SpellFilter string
	RootFilter  []string
}

// ComposeOption configures a ComposeGraph call.
type ComposeOption func(*Compose)

// WithGraphHistory enables per-node DurationMs prediction using forecast history for the given target.
func WithGraphHistory(h *forecast.History, target string) ComposeOption {
	return func(c *Compose) { c.History = h; c.Target = target }
}

// ProjectOption mutates a Project at registration time; non-nil error aborts Open.
type ProjectOption func(p *types.Project) error

// BindingOption mutates a spell Binding at registration time.
type BindingOption func(b *types.Binding) error

// TargetOption mutates a types.TargetPolicy at registration time.
type TargetOption func(p *types.TargetPolicy)
