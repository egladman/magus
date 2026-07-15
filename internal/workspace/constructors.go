package workspace

import (
	"errors"
	"fmt"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/file/watch"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// WithDependsOn adds upstream project paths as dependencies. Paths may be repo-relative or dot-relative to the project.
func WithDependsOn(paths ...string) ProjectOption {
	return func(p *types.Project) error {
		resolved := make([]string, 0, len(paths))
		for _, raw := range paths {
			r, err := file.Resolve(raw, p.Path)
			if err != nil {
				return err
			}
			resolved = append(resolved, r)
		}
		p.DependsOn = append(p.DependsOn, resolved...)
		return nil
	}
}

// WithOutputs declares the file globs this project produces (project-relative).
func WithOutputs(paths ...string) ProjectOption {
	return func(p *types.Project) error {
		p.Outputs = append(p.Outputs, paths...)
		return nil
	}
}

// WithExclusive marks a project as must-not-run-alongside-peers in a RunAll batch.
func WithExclusive() ProjectOption {
	return func(p *types.Project) error { p.Exclusive = true; return nil }
}

// WithWatchIgnore appends patterns to the project's watch ignore list.
func WithWatchIgnore(patterns ...types.IgnorePattern) ProjectOption {
	return func(p *types.Project) error {
		for _, pat := range patterns {
			if err := watch.ValidatePattern(pat); err != nil {
				return fmt.Errorf("magus: WithWatchIgnore on %q: %w", p.Path, err)
			}
		}
		p.WatchIgnores = append(p.WatchIgnores, patterns...)
		return nil
	}
}

// WithTarget attaches a behavioural policy to the named target. name is
// normalized (see types.DefaultTargetNameNormalizer) so a policy declared
// under any spelling matches the target under any other.
func WithTarget(name string, opts ...TargetOption) ProjectOption {
	name = types.DefaultTargetNameNormalizer.NormalizeTargetName(name)
	return func(p *types.Project) error {
		if p.TargetPolicies == nil {
			p.TargetPolicies = make(map[string]types.Target)
		}
		pol := p.TargetPolicies[name]
		for _, o := range opts {
			o(&pol)
		}
		p.TargetPolicies[name] = pol
		return nil
	}
}

// WithRegisteredSpell registers a built-in spell by name (wire-layer equivalent of magus.WithSpell).
func WithRegisteredSpell(name string, opts ...BindingOption) ProjectOption {
	return func(p *types.Project) error {
		if name == "" {
			return errors.New("magus: spell name required")
		}
		l, ok := project.DefaultSpellRegistry().Lookup(name)
		if !ok {
			return fmt.Errorf("magus: spell %q not registered", name)
		}
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
		p.Sources = append(p.Sources, l.Sources()...)
		p.Outputs = append(p.Outputs, l.Outputs()...)
		return nil
	}
}

// IgnoreGlob constructs a doublestar-glob ignore pattern.
func IgnoreGlob(pattern string) types.IgnorePattern {
	return types.IgnorePattern{Type: watch.PatternGlob, Pattern: pattern}
}

// IgnoreRegex constructs a Go-regexp ignore pattern.
func IgnoreRegex(pattern string) types.IgnorePattern {
	return types.IgnorePattern{Type: watch.PatternRegex, Pattern: pattern}
}

// IgnoreLiteral constructs a literal ignore pattern.
func IgnoreLiteral(pattern string) types.IgnorePattern {
	return types.IgnorePattern{Type: watch.PatternLiteral, Pattern: pattern}
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

// WithClaimWeight sets the binding's claim weight.
func WithClaimWeight(weight int) BindingOption {
	return func(b *types.Binding) error {
		b.ClaimWeight = weight
		return nil
	}
}

// FailOnDrift enables the drift gate: fail the run if the working tree is dirty
// after the target runs.
func FailOnDrift() TargetOption {
	return func(t *types.Target) { t.FailOnDrift = true }
}

// RetryOnVolatile returns a TargetOption that enables volatility detection and auto-retry.
func RetryOnVolatile() TargetOption {
	return func(t *types.Target) { t.RetryOnVolatile = true }
}

// SkipCache returns a TargetOption that opts the target out of the cache, so magus
// always runs it and never replays or snapshots it.
func SkipCache() TargetOption {
	return func(t *types.Target) { t.SkipCache = true }
}

// Exclusive returns a TargetOption that runs the target alone — no other target
// runs concurrently while it does.
func Exclusive() TargetOption {
	return func(t *types.Target) { t.Exclusive = true }
}

// Slots returns a TargetOption that makes the target hold n concurrency slots
// while it runs, throttling parallel work around a resource-heavy step. n is
// clamped to the run's total slot budget at schedule time; n >= the budget makes
// the target hold every slot, so no peer runs concurrently with it.
func Slots(n int) TargetOption {
	return func(t *types.Target) { t.Slots = n }
}
