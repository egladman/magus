package magus

import (
	"context"
	"fmt"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// Option configures Open or Inspect.
type Option = workspace.Option

// WithConfigFile causes the constructor to load magus.yaml from path instead of <root>/magus.yaml.
func WithConfigFile(path string) Option {
	return func(o *workspace.Load) { o.ConfigPath = path }
}

// WithLoadedConfig injects an already-parsed configuration, bypassing the
// default magus.yaml discovery. Env-var and flag overrides should be applied
// before calling this.
func WithLoadedConfig(cfg config.Config) Option {
	return workspace.WithLoadedConfig(cfg)
}

// WithMetricsCollection builds an always-on in-process metrics collector for this workspace
// (OTel instruments record even with telemetry export off), so the daemon can serve OTLP
// snapshots to the /dashboard via [Magus.MetricsSnapshot]. The CLI leaves it off.
func WithMetricsCollection() Option {
	return workspace.WithMetricsCollection()
}

// WithProvider injects an already-constructed observability provider so several Magus
// instances (a daemon's bridge Magus plus each per-workspace registry Magus) share ONE set
// of OTel instruments and one metrics collector. The provider is owned by the daemon
// process, not any single workspace, so workspace eviction never discards accumulated
// metrics. It supersedes [WithMetricsCollection]: Open adopts the injected provider instead
// of constructing its own.
func WithProvider(p observability.Provider) Option {
	return workspace.WithProvider(p)
}

// WorkspaceRegistry holds project-option overrides and target policies for a single Open.
type WorkspaceRegistry = workspace.WorkspaceRegistry

// NewWorkspaceRegistry returns an empty WorkspaceRegistry.
func NewWorkspaceRegistry() *WorkspaceRegistry { return workspace.NewWorkspaceRegistry() }

// WithWorkspaceRegistry injects a pre-built WorkspaceRegistry, replacing the default one.
func WithWorkspaceRegistry(reg *WorkspaceRegistry) Option {
	return func(o *workspace.Load) { o.Registry = reg }
}

// WithWorkspaceRegistryContext installs reg in ctx so interpreters can retrieve it.
func WithWorkspaceRegistryContext(ctx context.Context, reg *WorkspaceRegistry) context.Context {
	return workspace.ContextWithRegistry(ctx, reg)
}

// WorkspaceRegistryFromContext returns the WorkspaceRegistry from ctx, or nil.
func WorkspaceRegistryFromContext(ctx context.Context) *WorkspaceRegistry {
	return workspace.WorkspaceRegistryFromContext(ctx)
}

func installWorkspaceRegistry(ctx context.Context, reg *WorkspaceRegistry) context.Context {
	return workspace.ContextWithRegistry(ctx, reg)
}

// ProjectOption mutates a Project at registration time. A non-nil error aborts Open.
type ProjectOption = workspace.ProjectOption

// BindingOption mutates a spell Binding at registration time.
type BindingOption = workspace.BindingOption

// TargetOption sets a per-target execution-policy field at registration time.
type TargetOption = workspace.TargetOption

// WithDependsOn adds upstream project paths as dependencies (repo-relative or project-relative).
func WithDependsOn(paths ...string) ProjectOption { return workspace.WithDependsOn(paths...) }

// WithOutputs declares the project-relative file globs this project produces.
func WithOutputs(paths ...string) ProjectOption { return workspace.WithOutputs(paths...) }

// WithExclusive marks a project as must-not-run-alongside-peers (also serializes multi-spell fan-out).
func WithExclusive() ProjectOption { return workspace.WithExclusive() }

// WithWatchIgnore appends patterns to the project's watch ignore list; malformed patterns error at Open.
func WithWatchIgnore(patterns ...types.IgnorePattern) ProjectOption {
	return workspace.WithWatchIgnore(patterns...)
}

// IgnoreGlob constructs a doublestar-glob ignore pattern.
func IgnoreGlob(pattern string) types.IgnorePattern { return workspace.IgnoreGlob(pattern) }

// IgnoreRegex constructs a Go-regexp ignore pattern.
func IgnoreRegex(pattern string) types.IgnorePattern { return workspace.IgnoreRegex(pattern) }

// IgnoreLiteral constructs a literal ignore pattern matching any path segment at any depth.
func IgnoreLiteral(pattern string) types.IgnorePattern { return workspace.IgnoreLiteral(pattern) }

// FailOnDrift enables the drift gate: fail if the working tree is dirty after the target.
func FailOnDrift() TargetOption { return workspace.FailOnDrift() }

// Exclusive runs the target alone — no other target runs concurrently while it does.
func Exclusive() TargetOption { return workspace.Exclusive() }

// RetryOnFlake enables flake detection and auto-retry for this target.
func RetryOnFlake() TargetOption { return workspace.RetryOnFlake() }

// WithTarget attaches a behavioural policy to the named target; multiple calls are merged.
func WithTarget(name string, opts ...TargetOption) ProjectOption {
	return workspace.WithTarget(name, opts...)
}

// WithSpell registers a built-in spell by name; multiple calls fan out in parallel (sequential with WithExclusive).
func WithSpell(name string, opts ...BindingOption) ProjectOption {
	return func(p *types.Project) error {
		if name == "" {
			return fmt.Errorf("magus: WithSpell on %q: %w", p.Path, types.ErrSpellNameRequired)
		}
		l, ok := project.DefaultSpellRegistry().Lookup(name)
		if !ok {
			return fmt.Errorf("magus: WithSpell(%q) on %q: %w", name, p.Path, types.ErrSpellNotRegistered)
		}
		return bindSpell(p, l, name, opts...)
	}
}

func bindSpell(p *types.Project, spell *types.Spell, name string, opts ...BindingOption) error {
	b := &types.Binding{Name: name}
	for _, opt := range opts {
		if err := opt(b); err != nil {
			return err
		}
	}
	if p.Spell == "" {
		p.Spell = name
	}
	p.Spells = append(p.Spells, name)
	p.Bindings = append(p.Bindings, b)
	p.Sources = append(p.Sources, spell.Sources()...)
	p.Outputs = append(p.Outputs, spell.Outputs()...)
	return nil
}

// WithClaim extends the spell's declared claims with additional globs.
func WithClaim(globs ...string) BindingOption {
	return func(b *types.Binding) error {
		b.AddedClaims = append(b.AddedClaims, globs...)
		return nil
	}
}

// WithoutClaim removes globs from a spell's effective claims.
func WithoutClaim(globs ...string) BindingOption {
	return func(b *types.Binding) error {
		b.RemovedClaims = append(b.RemovedClaims, globs...)
		return nil
	}
}

// WithClaimWeight sets the binding's claim weight; higher weight wins on overlap, ties go last-wins.
func WithClaimWeight(weight int) BindingOption {
	return func(b *types.Binding) error {
		b.ClaimWeight = weight
		return nil
	}
}
