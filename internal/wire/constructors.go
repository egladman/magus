package wire

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

// WithTarget attaches a behavioural policy to the named target.
func WithTarget(name string, opts ...TargetOption) ProjectOption {
	return func(p *types.Project) error {
		if p.TargetPolicies == nil {
			p.TargetPolicies = make(map[string]types.TargetPolicy)
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

// CheckClean enables the check-clean-after policy.
func CheckClean() TargetOption {
	return func(p *types.TargetPolicy) { p.CheckClean = true }
}

// TrackFlake returns a TargetOption that enables flake detection and auto-retry.
func TrackFlake() TargetOption {
	return func(p *types.TargetPolicy) { p.TrackFlake = true }
}

// NoCache returns a TargetOption that opts the target out of the cache, so magus
// always runs it and never replays or snapshots it.
func NoCache() TargetOption {
	return func(p *types.TargetPolicy) { p.NoCache = true }
}

// Isolated returns a TargetOption that serializes the target against the whole
// RunAll batch: nothing else runs concurrently while an isolated target runs.
func Isolated() TargetOption {
	return func(p *types.TargetPolicy) { p.Isolated = true }
}

// WithDryRun prints what would run without invoking any handler.
func WithDryRun() RunOption { return func(o *Run) { o.DryRun = true } }

// WithWrite enables mutating mode for format/generate; sugar for the built-in rw charm.
func WithWrite() RunOption { return WithCharms(types.CharmReadWrite) }

// WithCharms appends execution charms to the run; duplicates are harmless.
func WithCharms(charms ...string) RunOption {
	return func(o *Run) { o.Charms = append(o.Charms, charms...) }
}

// WithBaseRef overrides MAGUS_VCS_BASE_REF for RunAffected invocations.
func WithBaseRef(ref string) RunOption { return func(o *Run) { o.BaseRef = ref } }

// WithSpellFilter restricts Run to projects that have the named spell registered.
func WithSpellFilter(name string) RunOption { return func(o *Run) { o.Spell = name } }

// WithNoFlakeRetry disables the flake auto-retry logic.
func WithNoFlakeRetry() RunOption { return func(o *Run) { o.NoFlakeRetry = true } }
