package bindings

import (
	"context"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// applyOpts runs the parsed project options against a fresh root project so tests can
// assert the resulting policy fields.
func applyOpts(t *testing.T, opts vm.Value) *types.Project {
	t.Helper()
	return applyOptsAt(t, ".", opts)
}

// applyOptsAt is applyOpts against a project at projectPath. Sources are the one option
// whose stored value depends on where the project sits, because a glob may reach out of
// it; every other case reads the same from the root.
func applyOptsAt(t *testing.T, projectPath string, opts vm.Value) *types.Project {
	t.Helper()
	got, err := parseBuzzProjectOpts(context.Background(), opts)
	require.NoError(t, err)
	p := &types.Project{Path: projectPath}
	for _, o := range got {
		require.NoError(t, o(p))
	}
	return p
}

// targetsOpts builds a `{"targets": {name: policy}}` options map.
func targetsOpts(name string, policy vm.Value) vm.Value {
	targets := vm.NewMap()
	targets.MapSet(name, policy)
	opts := vm.NewMap()
	opts.MapSet("targets", targets)
	return opts
}

func TestParseBuzzProjectOpts_TargetSlots(t *testing.T) {
	pol := vm.NewMap()
	pol.MapSet("slots", vm.IntValue(4))
	p := applyOpts(t, targetsOpts("lint", pol))
	assert.Equal(t, 4, p.TargetPolicies["lint"].Slots)
}

func TestParseBuzzProjectOpts_TargetSlotsNonPositiveErrors(t *testing.T) {
	pol := vm.NewMap()
	pol.MapSet("slots", vm.IntValue(0))
	_, err := parseBuzzProjectOpts(context.Background(), targetsOpts("lint", pol))
	assert.ErrorContains(t, err, `targets["lint"].slots must be >= 1`)
}

// A non-int slots value must be a load error, not reinterpreted: AsInt reads a
// float's raw bits as an int, which would otherwise flow a garbage value into
// the policy.
func TestParseBuzzProjectOpts_TargetSlotsNonIntErrors(t *testing.T) {
	pol := vm.NewMap()
	pol.MapSet("slots", vm.FloatValue(2.5))
	_, err := parseBuzzProjectOpts(context.Background(), targetsOpts("lint", pol))
	assert.ErrorContains(t, err, `targets["lint"].slots must be a whole number`)
}

func TestParseBuzzProjectOpts_TargetTimeout(t *testing.T) {
	pol := vm.NewMap()
	pol.MapSet("timeout", vm.StrValue("15m"))
	p := applyOpts(t, targetsOpts("security", pol))
	assert.Equal(t, "15m", p.TargetPolicies["security"].Timeout)
	assert.Equal(t, 15*time.Minute, p.TargetPolicies["security"].TimeoutDuration())
}

// An absent timeout leaves the target unbounded, which is the behavior every target
// had before the key existed. Pinned rather than assumed: the whole design rests on
// declaring nothing changing nothing.
func TestParseBuzzProjectOpts_TargetTimeoutAbsentIsUnbounded(t *testing.T) {
	pol := vm.NewMap()
	pol.MapSet("slots", vm.IntValue(2))
	p := applyOpts(t, targetsOpts("security", pol))
	assert.Empty(t, p.TargetPolicies["security"].Timeout)
	assert.Zero(t, p.TargetPolicies["security"].TimeoutDuration())
}

func TestParseBuzzProjectOpts_TargetTimeoutMalformedErrors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value vm.Value
		want  string
	}{
		{"not a duration", vm.StrValue("15 minutes"), `targets["security"].timeout: "15 minutes" is not a duration`},
		{"zero", vm.StrValue("0s"), `targets["security"].timeout: "0s" is not positive`},
		{"negative", vm.StrValue("-1m"), `targets["security"].timeout: "-1m" is not positive`},
		{"an integer of unstated unit", vm.IntValue(900), `targets["security"].timeout must be a duration string`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pol := vm.NewMap()
			pol.MapSet("timeout", tc.value)
			_, err := parseBuzzProjectOpts(context.Background(), targetsOpts("security", pol))
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

// TestParseBuzzProjectOpts_Sources pins the CLEANED stored form and, with it, the truth
// the cleaning buys: a magusfile may reach into a sibling tree, and the reach resolves
// against the declaring project, so "../proto/**/*.proto" from docs/ declares
// "proto/**/*.proto" to the cache key and to affected attribution alike. Storing the
// spelling raw roots it to "docs/../proto/**/*.proto", which matches no path any walk
// produces, so the declaration reads as supported and keys nothing.
func TestParseBuzzProjectOpts_Sources(t *testing.T) {
	opts := vm.NewMap()
	opts.MapSet("sources", vm.ListValue([]vm.Value{vm.StrValue("./guides/**"), vm.StrValue("../proto/**/*.proto")}))
	p := applyOptsAt(t, "docs", opts)
	assert.Equal(t, []string{"guides/**", "../proto/**/*.proto"}, p.Sources)
	assert.Equal(t, []string{"docs/guides/**", "proto/**/*.proto"}, p.DeclaredGlobs(),
		"both globs root at the workspace; only the reaching one moves")
}

// TestParseBuzzProjectOpts_SourcesRejectsAWorkspaceEscape pins the one reach that is a
// load error rather than a declaration. Nothing outside the workspace root is ever
// walked, so the glob could only be stored and ignored - the silent shape this whole
// affordance was fixed to stop producing.
func TestParseBuzzProjectOpts_SourcesRejectsAWorkspaceEscape(t *testing.T) {
	opts := vm.NewMap()
	opts.MapSet("sources", vm.ListValue([]vm.Value{vm.StrValue("../../elsewhere/**")}))
	got, err := parseBuzzProjectOpts(context.Background(), opts)
	require.NoError(t, err, "parsing builds the option; the project it anchors against decides")
	require.Len(t, got, 1)

	err = got[0](&types.Project{Path: "docs"})
	assert.ErrorContains(t, err, `source glob "../../elsewhere/**" escapes the workspace root`)
}

// no_language exempts a project from doctor's language-coverage check, so it has to cost a
// sentence. A bool would let the exemption in anonymously, which is the failure mode: the
// next reader finds a silenced check instead of a reason.
func TestParseBuzzProjectOpts_NoLanguage(t *testing.T) {
	t.Run("a reason is recorded", func(t *testing.T) {
		opts := vm.NewMap()
		opts.MapSet("no_language", vm.StrValue("polyglot harness; no single pack describes it"))
		p := applyOpts(t, opts)
		assert.Equal(t, "polyglot harness; no single pack describes it", p.NoLanguage)
	})

	for _, tc := range []struct {
		name string
		val  vm.Value
	}{
		{"a bare true is not a reason", vm.BoolValue(true)},
		{"empty is not a reason", vm.StrValue("")},
		{"whitespace is not a reason", vm.StrValue("   ")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := vm.NewMap()
			opts.MapSet("no_language", tc.val)
			_, err := parseBuzzProjectOpts(context.Background(), opts)
			assert.ErrorContains(t, err, `"no_language" needs a reason string`)
		})
	}
}

// skip_cache claims that REPLAYING a target would be wrong, which is a different and much
// stronger statement than --no-cache's "not this run". A bare true could not tell the two
// apart, and six uses in this repo turned out to be workarounds for a snapshot error the
// engine no longer raises, so the reason is what keeps the opt-out honest.
func TestParseBuzzProjectOpts_SkipCacheReason(t *testing.T) {
	t.Run("a reason is recorded", func(t *testing.T) {
		pol := vm.NewMap()
		pol.MapSet("skip_cache", vm.StrValue("signs a fresh artifact per invocation"))
		p := applyOpts(t, targetsOpts("release-sign", pol))
		got := p.TargetPolicies["release-sign"]
		assert.True(t, got.SkipCache)
		assert.Equal(t, "signs a fresh artifact per invocation", got.SkipCacheReason)
	})

	for _, tc := range []struct {
		name string
		val  vm.Value
	}{
		{"a bare true is not a reason", vm.BoolValue(true)},
		{"empty is not a reason", vm.StrValue("")},
		{"whitespace is not a reason", vm.StrValue("  ")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pol := vm.NewMap()
			pol.MapSet("skip_cache", tc.val)
			_, err := parseBuzzProjectOpts(context.Background(), targetsOpts("lint", pol))
			assert.ErrorContains(t, err, "needs a reason string")
			assert.ErrorContains(t, err, "--no-cache", "the error must point at the weaker control")
		})
	}
}

// retry_on_volatile asks magus to rerun a red target until it goes green, so it sets the
// same bar skip_cache does one step further out: the claim is that this target fails
// without the code being wrong. A bare true cannot tell a suite somebody measured from a
// bug they stopped chasing, and both read green in CI.
func TestParseBuzzProjectOpts_RetryOnVolatileReason(t *testing.T) {
	t.Run("a reason is recorded", func(t *testing.T) {
		pol := vm.NewMap()
		pol.MapSet("retry_on_volatile", vm.StrValue("talks to a shared broker that drops a connection under load"))
		p := applyOpts(t, targetsOpts("integration", pol))
		got := p.TargetPolicies["integration"]
		assert.True(t, got.RetryOnVolatile)
		assert.Equal(t, "talks to a shared broker that drops a connection under load", got.RetryOnVolatileReason)
	})

	t.Run("an undeclared target is not retryable", func(t *testing.T) {
		pol := vm.NewMap()
		pol.MapSet("slots", vm.IntValue(4))
		p := applyOpts(t, targetsOpts("lint", pol))
		assert.False(t, p.TargetPolicies["lint"].RetryOnVolatile)
	})

	for _, tc := range []struct {
		name string
		val  vm.Value
	}{
		{"a bare true is not a reason", vm.BoolValue(true)},
		{"empty is not a reason", vm.StrValue("")},
		{"whitespace is not a reason", vm.StrValue("  ")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pol := vm.NewMap()
			pol.MapSet("retry_on_volatile", tc.val)
			_, err := parseBuzzProjectOpts(context.Background(), targetsOpts("test", pol))
			assert.ErrorContains(t, err, "needs a reason string")
			assert.ErrorContains(t, err, "leave the policy off",
				"the error must say what to do when a failure means the code IS wrong")
		})
	}
}

func TestParseBuzzProjectOpts_UnknownTopLevelKeyErrors(t *testing.T) {
	opts := vm.NewMap()
	opts.MapSet("depend_on", vm.ListValue([]vm.Value{vm.StrValue("api")}))
	_, err := parseBuzzProjectOpts(context.Background(), opts)
	assert.ErrorContains(t, err, `unknown option "depend_on"`)
	assert.ErrorContains(t, err, `did you mean "depends_on"`)
}

// The old camelCase spelling (pre-A6) is now itself an unknown-key error, with
// a did-you-mean pointing at the canonical snake_case name.
func TestParseBuzzProjectOpts_UnknownTargetPolicyKeyErrors(t *testing.T) {
	pol := vm.NewMap()
	pol.MapSet("skipCache", vm.BoolValue(true))
	_, err := parseBuzzProjectOpts(context.Background(), targetsOpts("lint", pol))
	assert.ErrorContains(t, err, `unknown option "skipCache"`)
	assert.ErrorContains(t, err, `did you mean "skip_cache"`)
}

// includePolicy builds `{"cache": {"include": {axis: {"enabled": on}}}}`.
func includePolicy(axis string, on bool) vm.Value {
	flag := vm.NewMap()
	flag.MapSet("enabled", vm.BoolValue(on))
	include := vm.NewMap()
	include.MapSet(axis, flag)
	cache := vm.NewMap()
	cache.MapSet("include", include)
	pol := vm.NewMap()
	pol.MapSet("cache", cache)
	return pol
}

// The nested form mirrors magus.yaml's cache.include.*.enabled, so one decision reads
// the same way wherever it is written.
func TestTargetCacheIncludeOverride(t *testing.T) {
	p := applyOpts(t, targetsOpts("image", includePolicy("arch", false)))
	pol := p.TargetPolicies["image"]

	require.NotNil(t, pol.IncludeArch, "arch override not decoded")
	assert.False(t, *pol.IncludeArch)
	assert.Nil(t, pol.IncludeOS, "an unmentioned axis must inherit, not default")
}

// A misspelled nesting level would leave the target inheriting the workspace answer,
// which looks identical to a cache that works - so it is a load error.
func TestTargetCacheIncludeRejectsWrongShape(t *testing.T) {
	bad := vm.NewMap()
	cache := vm.NewMap()
	cache.MapSet("includes", vm.NewMap()) // plural typo
	bad.MapSet("cache", cache)
	_, err := parseBuzzProjectOpts(context.Background(), targetsOpts("image", bad))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "include")

	noEnabled := vm.NewMap()
	c2 := vm.NewMap()
	inc := vm.NewMap()
	inc.MapSet("os", vm.NewMap()) // present but empty
	c2.MapSet("include", inc)
	noEnabled.MapSet("cache", c2)
	_, err = parseBuzzProjectOpts(context.Background(), targetsOpts("image", noEnabled))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "enabled")
}

// toolsOpts builds a `{"tools": {bin: {min, below}}}` options map.
func toolsOpts(bin string, kv map[string]string) vm.Value {
	bounds := vm.NewMap()
	for k, v := range kv {
		bounds.MapSet(k, vm.StrValue(v))
	}
	tools := vm.NewMap()
	tools.MapSet(bin, bounds)
	opts := vm.NewMap()
	opts.MapSet("tools", tools)
	return opts
}

func TestParseBuzzProjectOpts_Tools(t *testing.T) {
	t.Run("both bounds decode", func(t *testing.T) {
		p := applyOpts(t, toolsOpts("node", map[string]string{"min": "22", "below": "25"}))
		assert.Equal(t, spells.VersionBounds{Min: "22", Below: "25"}, p.ToolBounds["node"])
	})

	t.Run("a ceiling alone is enough", func(t *testing.T) {
		p := applyOpts(t, toolsOpts("node", map[string]string{"below": "25"}))
		assert.Equal(t, spells.VersionBounds{Below: "25"}, p.ToolBounds["node"])
	})

	// A bound that is not a version must fail at LOAD. Letting it through would make
	// VersionBounds.Check return unknown, which never fails a build - so a typo would
	// silently widen the window to everything, the exact opposite of declaring one.
	t.Run("a non-version bound is a load error", func(t *testing.T) {
		got, err := parseBuzzProjectOpts(context.Background(), toolsOpts("node", map[string]string{"min": "latest"}))
		require.NoError(t, err, "decode succeeds; the option itself rejects")
		p := &types.Project{Path: "."}
		var applyErr error
		for _, o := range got {
			if e := o(p); e != nil {
				applyErr = e
			}
		}
		assert.ErrorContains(t, applyErr, `tools["node"].min "latest" is not a valid version`)
	})

	// An unknown key inside a tool entry is a load error naming the two valid ones, and
	// deliberately NOT the "your magus may predate this" hint: min/below is a closed
	// vocabulary that does not grow, so a stray key there is always a typo. ("minimum"
	// is too far from "min" for the suggester's distance threshold, which is fine - the
	// known-options list is two items long and the fix is visible in the message.)
	t.Run("an unknown bound key is a plain load error, with no upgrade hint", func(t *testing.T) {
		_, err := parseBuzzProjectOpts(context.Background(), toolsOpts("node", map[string]string{"minimum": "22"}))
		assert.ErrorContains(t, err, `tools["node"]: unknown option "minimum"`)
		assert.ErrorContains(t, err, "known options: below, min")
		assert.NotContains(t, err.Error(), "magus self update",
			"a closed sub-map must not suggest upgrading; the key cannot be from the future")
	})

	// An empty entry contributes nothing, so a project that writes one is not treated as
	// having declared a window (which would make the run-start check compare nothing).
	t.Run("an empty entry is dropped", func(t *testing.T) {
		p := applyOpts(t, toolsOpts("node", map[string]string{}))
		assert.Empty(t, p.ToolBounds)
	})

	// A non-string min/below (e.g. an unquoted number: `tools = {go = {min = 22}}`)
	// must be a load error naming the offending field, not a crash: AsString() on a
	// non-string Value is an unchecked cast in the nanbox/unsafe representations.
	t.Run("a non-string min is a load error, not a crash", func(t *testing.T) {
		bounds := vm.NewMap()
		bounds.MapSet("min", vm.IntValue(22))
		tools := vm.NewMap()
		tools.MapSet("go", bounds)
		opts := vm.NewMap()
		opts.MapSet("tools", tools)
		_, err := parseBuzzProjectOpts(context.Background(), opts)
		assert.ErrorContains(t, err, `tools["go"].min must be a string version`)
	})

	t.Run("a non-string below is a load error, not a crash", func(t *testing.T) {
		bounds := vm.NewMap()
		bounds.MapSet("below", vm.IntValue(25))
		tools := vm.NewMap()
		tools.MapSet("go", bounds)
		opts := vm.NewMap()
		opts.MapSet("tools", tools)
		_, err := parseBuzzProjectOpts(context.Background(), opts)
		assert.ErrorContains(t, err, `tools["go"].below must be a string version`)
	})
}

// review_required names where an unread change actually costs something, so `magus diff
// --impact` can single those paths out and stay quiet everywhere else.
//
// This pins the PARSER, which is the half that had no test: the matcher and the report both
// had coverage while nothing asserted that declaring the key in a magusfile reaches
// Project.ReviewRequired at all. A persona run declared it and reported no effect, and
// though that run turned out to be against a tree without the feature, the gap it pointed
// at was real.
func TestParseBuzzProjectOpts_ReviewRequired(t *testing.T) {
	t.Run("globs are recorded in order", func(t *testing.T) {
		opts := vm.NewMap()
		opts.MapSet("review_required", vm.ListValue([]vm.Value{
			vm.StrValue("internal/secret/**"), vm.StrValue("internal/cache/key.go"),
		}))

		p := applyOpts(t, opts)
		assert.Equal(t, []string{"internal/secret/**", "internal/cache/key.go"}, p.ReviewRequired)
	})

	t.Run("undeclared leaves it empty", func(t *testing.T) {
		assert.Empty(t, applyOpts(t, vm.NewMap()).ReviewRequired)
	})

	// An empty list is a declaration that says nothing, and accepting it would leave the
	// author believing they had marked paths that magus will never single out.
	for _, tc := range []struct {
		name string
		vals []string
	}{
		{"an empty list names nothing", nil},
		{"a list of blanks names nothing", []string{"", "   "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items := make([]vm.Value, 0, len(tc.vals))
			for _, v := range tc.vals {
				items = append(items, vm.StrValue(v))
			}
			opts := vm.NewMap()
			opts.MapSet("review_required", vm.ListValue(items))

			_, err := parseBuzzProjectOpts(context.Background(), opts)
			assert.ErrorContains(t, err, `"review_required" needs at least one glob`)
		})
	}
}

// gate_low_risk feeds the ci-gate redundancy check's prose class. Unlike
// review_required an EMPTY list is a legal declaration - it is the spelling
// that turns the prose class off - so this pins that [] parses and sets the
// declared flag, while a non-list or a blank entry stays a load error.
func TestParseBuzzProjectOpts_GateLowRisk(t *testing.T) {
	t.Run("globs are recorded and the declaration is marked", func(t *testing.T) {
		opts := vm.NewMap()
		opts.MapSet("gate_low_risk", vm.ListValue([]vm.Value{
			vm.StrValue("**/*.md"), vm.StrValue("notes/**"),
		}))

		p := applyOpts(t, opts)
		assert.Equal(t, []string{"**/*.md", "notes/**"}, p.GateLowRisk)
		assert.True(t, p.GateLowRiskDeclared)
	})

	t.Run("an empty list declares the prose class off", func(t *testing.T) {
		opts := vm.NewMap()
		opts.MapSet("gate_low_risk", vm.ListValue(nil))

		p := applyOpts(t, opts)
		assert.Empty(t, p.GateLowRisk)
		assert.True(t, p.GateLowRiskDeclared)
	})

	t.Run("undeclared leaves the built-in defaults in force", func(t *testing.T) {
		p := applyOpts(t, vm.NewMap())
		assert.Empty(t, p.GateLowRisk)
		assert.False(t, p.GateLowRiskDeclared)
	})

	t.Run("a non-list is a load error", func(t *testing.T) {
		opts := vm.NewMap()
		opts.MapSet("gate_low_risk", vm.StrValue("**/*.md"))
		_, err := parseBuzzProjectOpts(context.Background(), opts)
		assert.ErrorContains(t, err, `"gate_low_risk" takes a list of globs`)
	})

	t.Run("a blank entry is a load error", func(t *testing.T) {
		opts := vm.NewMap()
		opts.MapSet("gate_low_risk", vm.ListValue([]vm.Value{vm.StrValue("  ")}))
		_, err := parseBuzzProjectOpts(context.Background(), opts)
		assert.ErrorContains(t, err, `"gate_low_risk" entries must be non-empty glob strings`)
	})
}
