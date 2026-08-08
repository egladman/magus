package workspace

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	semver "github.com/Masterminds/semver/v3"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/file/watch"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// WithDependsOn adds upstream project paths as dependencies. Paths may be repo-relative or dot-relative to the project.
//
// This is the one caller that wants ResolveDependsOn.s two-mode reading, because a human
// hand-writes these entries and both spellings are a deliberate affordance. Paths that
// come from an `import "project/<path>"` are always dot-relative and use
// file.ResolveImport instead; do not collapse the two.
func WithDependsOn(paths ...string) ProjectOption {
	return func(p *types.Project) error {
		resolved := make([]string, 0, len(paths))
		for _, raw := range paths {
			r, err := file.ResolveDependsOn(raw, p.Path)
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

// WithSources declares additional file globs (project-relative) that feed this
// project's cache key and affected-set membership, alongside whatever its
// resolved spells already contribute via their own Sources(). Use this when a
// project's real inputs reach beyond what its spells claim - e.g. non-code
// assets, sibling proto schemas, or docs a generator target reads.
func WithSources(paths ...string) ProjectOption {
	return func(p *types.Project) error {
		p.Sources = append(p.Sources, paths...)
		return nil
	}
}

// WithName sets the project's declared display name, overriding the
// path-derived default. See types.Project.Name for why the root needs it.
func WithName(name string) ProjectOption {
	return func(p *types.Project) error { p.Name = name; return nil }
}

// WithExclusive marks a project as must-not-run-alongside-peers in a RunAll batch.
func WithExclusive() ProjectOption {
	return func(p *types.Project) error { p.Exclusive = true; return nil }
}

// WithNoLanguage records why a project binds no toolchain spell deliberately.
func WithNoLanguage(reason string) ProjectOption {
	return func(p *types.Project) error { p.NoLanguage = reason; return nil }
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

// WithToolBounds sets the project's per-binary version windows, rejecting a bound that
// is not a version.
//
// Rejecting at load is what keeps VersionBounds.Check's unknown verdict a backstop
// rather than the normal path: a typo here would otherwise widen the window to
// everything and report nothing, which is the opposite of what declaring one is for.
func WithToolBounds(bounds map[string]spells.VersionBounds) ProjectOption {
	return func(p *types.Project) error {
		for _, bin := range slices.Sorted(maps.Keys(bounds)) {
			b := bounds[bin]
			for _, f := range []struct{ field, value string }{{"min", b.Min}, {"below", b.Below}} {
				if f.value == "" {
					continue
				}
				if _, err := semver.NewVersion(f.value); err != nil {
					return fmt.Errorf("magus: project %q: tools[%q].%s %q is not a valid version",
						p.Path, bin, f.field, f.value)
				}
			}
		}
		p.ToolBounds = bounds
		return nil
	}
}

// WithTarget attaches a behavioural policy to the named target. name is
// normalized (see types.DefaultTargetNameNormalizer) so a policy declared
// under any spelling matches the target under any other.
func WithTarget(name string, opts ...TargetOption) ProjectOption {
	name = types.Normalize(name)
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
		// Internal plumbing never claims the primary slot; see types.Project.AttachSpell.
		// This is the path a magusfile's explicit `"spells": [magusfile, go, ...]` list
		// takes, and magusfile is conventionally written first, so it won here too.
		if p.Spell == "" && !l.Internal() {
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
	return types.IgnorePattern{Type: types.PatternGlob, Pattern: pattern}
}

// IgnoreRegex constructs a Go-regexp ignore pattern.
func IgnoreRegex(pattern string) types.IgnorePattern {
	return types.IgnorePattern{Type: types.PatternRegex, Pattern: pattern}
}

// IgnoreLiteral constructs a literal ignore pattern.
func IgnoreLiteral(pattern string) types.IgnorePattern {
	return types.IgnorePattern{Type: types.PatternLiteral, Pattern: pattern}
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
// always runs it and never replays or snapshots it. reason states why REPLAYING the
// target would be wrong (a fresh signature, a screen capture, a go.mod mutation); it
// is recorded rather than merely documented so a reader can tell a real opt-out from
// a workaround, and so `--no-cache`, which only distrusts the cache for one run, is
// not reached for by mistake.
func SkipCache(reason string) TargetOption {
	return func(t *types.Target) { t.SkipCache = true; t.SkipCacheReason = reason }
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

// MemoryMB returns a TargetOption setting the target's memory budget in
// megabytes. See types.Target.MemoryMB.
func MemoryMB(n int) TargetOption {
	return func(t *types.Target) { t.MemoryMB = n }
}

// IncludeOS overrides whether the host OS keys this target's cache entry.
func IncludeOS(v bool) TargetOption {
	return func(t *types.Target) { t.IncludeOS = &v }
}

// IncludeArch overrides whether the host architecture keys this target's entry.
func IncludeArch(v bool) TargetOption {
	return func(t *types.Target) { t.IncludeArch = &v }
}
