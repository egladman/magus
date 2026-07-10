// Package workspace holds the shared building blocks for opening a workspace:
// the WorkspaceRegistry and the project, spell, and target option constructors
// that a magusfile's register(...) calls produce, plus the Load accumulator for
// Open/Inspect.
//
// It is a separate package for two reasons:
//
//   - Import cycle: package magus imports internal/interp to evaluate magusfiles,
//     and internal/interp's Buzz bindings build project options when Buzz code
//     calls magus.project(...). Those option types cannot live in package
//     magus, and not in project either (the watch-ignore constructors need
//     internal/file/watch, which already imports project).
//   - Surface: Load and WithLimiter carry internal types (*config.Config,
//     *cache.Limiter). Keeping them here lets the daemon inject a shared limiter
//     without those internals leaking onto the public magus API.
package workspace

import (
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
)

// Load is the accumulated state of an Open or Inspect call.
type Load struct {
	ConfigPath     string
	Preloaded      *config.Config
	Limiter        *cache.Limiter
	Registry       *WorkspaceRegistry
	MetricsCollect bool // build an always-on local metrics collector (daemon dashboard feed)
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

// WithMetricsCollection builds an always-on in-process metrics collector for this workspace so
// its OTel instruments record even when telemetry export is off, and the daemon can serve OTLP
// snapshots to the /dashboard. The CLI leaves it unset to keep one-shot runs a true no-op.
func WithMetricsCollection() Option {
	return func(o *Load) { o.MetricsCollect = true }
}

// ProjectOption mutates a Project at registration time; a non-nil error aborts Open.
type ProjectOption func(p *types.Project) error

// BindingOption mutates a spell Binding at registration time.
type BindingOption func(b *types.Binding) error

// TargetOption sets a per-target execution-policy field on a types.Target at
// registration time.
type TargetOption func(t *types.Target)
