package workspace

import (
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

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
//
// A glob may REACH OUT of the project ("../proto/**" from docs/); that is what makes
// this the option for a sibling schema. types.RootGlob resolves the reach when the
// glob is rooted at the workspace, so the cache key and affected attribution both see
// "proto/**".
//
// Storing the path.Clean'd spelling is what keeps one glob one string. Every check
// downstream compares declarations by string equality - Project.DeclaredGlobs dedups
// that way, MGS1005 asks whether a per-target glob is already project-wide the same
// way - and "./docs/**" and "docs/**" are one declaration, not two.
//
// A glob reaching PAST the workspace root is rejected here, where it is written, rather
// than stored and ignored. The source walk starts at the workspace root and yields
// workspace-relative paths, so a glob outside it can never match a file, never move a
// cache key, and never mark this project affected - accepting one would record a
// declaration magus has no way to honor.
func WithSources(paths ...string) ProjectOption {
	return func(p *types.Project) error {
		cleaned := make([]string, 0, len(paths))
		for _, raw := range paths {
			glob := path.Clean(raw)
			rooted := types.RootGlob(p.Path, glob)
			if rooted == ".." || strings.HasPrefix(rooted, "../") {
				return fmt.Errorf("magus: project %q: source glob %q escapes the workspace root (it resolves to %q); "+
					"a path outside the workspace can never key a cache entry", p.Path, raw, rooted)
			}
			cleaned = append(cleaned, glob)
		}
		p.Sources = append(p.Sources, cleaned...)
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

// WithReviewRequired declares the globs where an unread change is worth reporting.
//
// A glob escaping the workspace root is REFUSED, the same way WithSources refuses one, and
// for a sharper reason: a source glob that escapes merely fails to key a cache entry, while
// this one silently matches nothing and the project goes on believing it has marked its
// riskiest paths. A feature whose whole job is to say "read this one" must not fail quiet.
func WithReviewRequired(globs ...string) ProjectOption {
	return func(p *types.Project) error {
		cleaned := make([]string, 0, len(globs))
		for _, raw := range globs {
			glob := path.Clean(raw)
			rooted := types.RootGlob(p.Path, glob)
			if rooted == ".." || strings.HasPrefix(rooted, "../") {
				return fmt.Errorf("magus: project %q: review_required glob %q escapes the workspace root "+
					"(it resolves to %q); it would match nothing and silently mark no paths at all", p.Path, raw, rooted)
			}
			cleaned = append(cleaned, glob)
		}
		p.ReviewRequired = append(p.ReviewRequired, cleaned...)
		return nil
	}
}

// WithGateLowRisk declares the globs the ci-gate redundancy check classifies as
// prose. Zero globs is a legal, meaningful declaration - it turns the prose
// class off - so the DECLARED flag is set here, not inferred from length.
// Escaping globs are refused for WithReviewRequired's reason: one would
// silently classify nothing while the author believes it does.
func WithGateLowRisk(globs ...string) ProjectOption {
	return func(p *types.Project) error {
		cleaned := make([]string, 0, len(globs))
		for _, raw := range globs {
			glob := path.Clean(raw)
			rooted := types.RootGlob(p.Path, glob)
			if rooted == ".." || strings.HasPrefix(rooted, "../") {
				return fmt.Errorf("magus: project %q: gate_low_risk glob %q escapes the workspace root "+
					"(it resolves to %q); it would classify nothing at all", p.Path, raw, rooted)
			}
			cleaned = append(cleaned, glob)
		}
		p.GateLowRisk = append(p.GateLowRisk, cleaned...)
		p.GateLowRiskDeclared = true
		return nil
	}
}

// WithGateInheritOff declares that this workspace's CI plan never inherits a
// green run's verdict (magus.project's "gate_inherit": false). Off is the only
// declaration that exists: on is the default, and a switch that restates it
// would invite per-project toggling of a decision that is workspace-wide.
func WithGateInheritOff() ProjectOption {
	return func(p *types.Project) error {
		p.GateInheritOff = true
		return nil
	}
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

// WithTarget attaches a behavioral policy to the named target. name is
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

// Drift sets what happens when this target's declared outputs move under a read-only run.
// The zero policy already gates a target that declares outputs, so this is for stating that
// out loud, downgrading to a warning, or switching it off with a reason.
func Drift(policy types.DriftPolicy, reason string) TargetOption {
	return func(t *types.Target) {
		t.Drift = policy
		t.DriftReason = reason
	}
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

// Timeout returns a TargetOption setting the target's wall-clock ceiling, as a Go
// duration string. See types.Target.Timeout; validate with types.ParseTimeout before
// calling, since this stores the spelling verbatim.
func Timeout(d string) TargetOption {
	return func(t *types.Target) { t.Timeout = d }
}

// IncludeOS overrides whether the host OS keys this target's cache entry.
func IncludeOS(v bool) TargetOption {
	return func(t *types.Target) { t.IncludeOS = &v }
}

// IncludeArch overrides whether the host architecture keys this target's entry.
func IncludeArch(v bool) TargetOption {
	return func(t *types.Target) { t.IncludeArch = &v }
}
