package magus

import (
	"fmt"

	"github.com/egladman/magus/internal/wire"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// ProjectOption mutates a Project at registration time. A non-nil error aborts Open.
type ProjectOption = wire.ProjectOption

// BindingOption mutates a spell Binding at registration time.
type BindingOption = wire.BindingOption

// TargetOption mutates a types.TargetPolicy at registration time.
type TargetOption = wire.TargetOption

// WithDependsOn adds upstream project paths as dependencies (repo-relative or project-relative).
func WithDependsOn(paths ...string) ProjectOption { return wire.WithDependsOn(paths...) }

// WithOutputs declares the project-relative file globs this project produces.
func WithOutputs(paths ...string) ProjectOption { return wire.WithOutputs(paths...) }

// WithExclusive marks a project as must-not-run-alongside-peers (also serializes multi-spell fan-out).
func WithExclusive() ProjectOption { return wire.WithExclusive() }

// WithWatchIgnore appends patterns to the project's watch ignore list; malformed patterns error at Open.
func WithWatchIgnore(patterns ...types.IgnorePattern) ProjectOption {
	return wire.WithWatchIgnore(patterns...)
}

// IgnoreGlob constructs a doublestar-glob ignore pattern.
func IgnoreGlob(pattern string) types.IgnorePattern { return wire.IgnoreGlob(pattern) }

// IgnoreRegex constructs a Go-regexp ignore pattern.
func IgnoreRegex(pattern string) types.IgnorePattern { return wire.IgnoreRegex(pattern) }

// IgnoreLiteral constructs a literal ignore pattern matching any path segment at any depth.
func IgnoreLiteral(pattern string) types.IgnorePattern { return wire.IgnoreLiteral(pattern) }

// CheckClean enables the check-clean-after policy: fail if the working tree is dirty after the target.
func CheckClean() TargetOption { return wire.CheckClean() }

// Isolated serializes the target against the whole batch: nothing else runs concurrently while it runs.
func Isolated() TargetOption { return wire.Isolated() }

// TrackFlake enables flake detection and auto-retry for this target.
func TrackFlake() TargetOption { return wire.TrackFlake() }

// WithTarget attaches a behavioural policy to the named target; multiple calls are merged.
func WithTarget(name string, opts ...TargetOption) ProjectOption {
	return wire.WithTarget(name, opts...)
}

// WithSpell registers a built-in spell by name; multiple calls fan out in parallel (sequential with WithExclusive).
func WithSpell(name string, opts ...BindingOption) ProjectOption {
	return func(p *types.Project) error {
		if name == "" {
			return fmt.Errorf("magus: WithSpell on %q: %w", p.Path, ErrSpellNameRequired)
		}
		l, ok := project.DefaultSpellRegistry().Lookup(name)
		if !ok {
			return fmt.Errorf("magus: WithSpell(%q) on %q: %w", name, p.Path, ErrSpellNotRegistered)
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
