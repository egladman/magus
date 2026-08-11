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
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/types"
)

// Load is the accumulated state of an Open or Inspect call.
type Load struct {
	ConfigPath     string
	Preloaded      *config.Config
	Limiter        *cache.Limiter
	Registry       *WorkspaceRegistry
	MetricsCollect bool // build an always-on local metrics collector (daemon dashboard feed)
	// Provider injects an already-constructed observability provider so several Magus
	// instances share one set of OTel instruments and one metrics collector. When set it
	// takes precedence over MetricsCollect (Open skips otlp.New and adopts it).
	Provider observability.Provider
	// Version is the running build's version, used only to check the workspace's
	// required_version floor. Empty disables the check, which is the same escape
	// hatch the daemon adoption gate uses (see internal/proc/identity.go): a bare
	// library caller that never set a version has no version to be too old.
	Version string
	// SkipWorkspaceProviders opens the workspace without running its wired workspace
	// providers, so it holds only the magusfile-declared projects.
	SkipWorkspaceProviders bool
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

// WithTelemetryProvider injects an already-constructed observability provider so several
// Magus instances (a daemon's bridge plus each of its per-workspace registry Magus) share
// ONE set of OTel instruments and one metrics collector. The provider is owned by the
// caller (the daemon process), not by any single workspace, so workspace eviction never
// discards accumulated metrics. It supersedes WithMetricsCollection: Open adopts the
// injected provider instead of constructing its own.
//
// Named Telemetry, not just WithProvider, because "provider" is already taken twice over
// in this package: ProviderRunner/ProviderCache/RegisterProviderRunner/
// WithoutWorkspaceProviders all mean a WORKSPACE provider (magus\workspace.provider), and
// observability.WithProvider (a different package, same short name) stores a Provider on a
// context rather than an Option. This is the one that meant something else entirely.
func WithTelemetryProvider(p observability.Provider) Option {
	return func(o *Load) { o.Provider = p }
}

// WithVersion supplies the running build's version so Open and Inspect can check
// it against the workspace's required_version floor. cmd/magus passes its
// linker-stamped version; a caller that omits it gets no floor check.
func WithVersion(v string) Option {
	return func(o *Load) { o.Version = v }
}

// WithoutWorkspaceProviders opens the workspace without running its wired workspace
// providers (magus\workspace.provider), leaving only the magusfile-declared projects.
//
// It exists for a caller inspecting a tree that is not a working checkout - `magus
// graph diff --rev` exports a bare revision to a temp dir, with no node_modules, no
// installed toolchain and no VCS metadata. A provider shells out to a foreign tool
// that needs all three, so running it there fails the open and takes the whole
// command with it. The base side of a diff is deliberately narrower rather than
// broken; a project that only a provider knows about shows up as added.
//
// Unrelated to WithTelemetryProvider above, which injects an observability provider.
func WithoutWorkspaceProviders() Option {
	return func(o *Load) { o.SkipWorkspaceProviders = true }
}

// ProjectOption mutates a Project at registration time; a non-nil error aborts Open.
type ProjectOption func(p *types.Project) error

// BindingOption mutates a spell Binding at registration time.
type BindingOption func(b *types.Binding) error

// TargetOption sets a per-target execution-policy field on a types.Target at
// registration time.
type TargetOption func(t *types.Target)
