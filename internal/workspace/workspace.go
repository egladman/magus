// Package workspace holds the shared building blocks for opening a workspace:
// the WorkspaceRegistry and the project, spell, and target option constructors
// that a magusfile's register(...) calls produce, plus the Load accumulator for
// Open/Inspect.
//
// It is a separate package for two reasons:
//
//   - Import cycle: package magus imports internal/interp to evaluate magusfiles,
//     and internal/interp's Buzz bindings build project options when Buzz code
//     calls magus.project.register(...). Those option types cannot live in package
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

// ProjectOption mutates a Project at registration time; a non-nil error aborts Open.
type ProjectOption func(p *types.Project) error

// BindingOption mutates a spell Binding at registration time.
type BindingOption func(b *types.Binding) error

// TargetOption mutates a types.TargetPolicy at registration time.
type TargetOption func(p *types.TargetPolicy)
