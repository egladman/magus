package magus

import (
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/wire"
)

// Option configures Open or Inspect.
type Option = wire.Option

// WithConfigFile causes the constructor to load magus.yaml from path instead of <root>/magus.yaml.
func WithConfigFile(path string) Option {
	return func(o *wire.Load) { o.ConfigPath = path }
}

// WithWorkspaceRegistry injects a pre-built WorkspaceRegistry, replacing the default one.
func WithWorkspaceRegistry(reg *WorkspaceRegistry) Option {
	return func(o *wire.Load) { o.Registry = reg }
}

// WithLoadedConfig injects an already-parsed configuration, bypassing the
// default magus.yaml discovery. Env-var and flag overrides should be applied
// before calling this.
func WithLoadedConfig(cfg config.Config) Option {
	return wire.WithLoadedConfig(cfg)
}
