package bindings

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/workspace"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/libs/testkit/register"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loadModelTiers runs src as the root magusfile of a workspace that has wired harnesses,
// and returns the registry with the session already CLOSED: a fork asks long after the
// load that declared the tiers.
func loadModelTiers(t *testing.T, src string, harnesses ...string) (*workspace.WorkspaceRegistry, *agent.ModelTierState, error) {
	t.Helper()
	reg := workspace.NewWorkspaceRegistry()
	t.Cleanup(func() { _ = reg.Close() })
	for _, h := range harnesses {
		reg.AddHarness(h)
	}
	tiers := &agent.ModelTierState{}
	ctx := agent.WithModelTierState(workspace.ContextWithRegistry(t.Context(), reg), tiers)
	return reg, tiers, execModelTiers(t, ctx, src)
}

func requireTiers(t *testing.T, tiers *agent.ModelTierState) *agent.ModelTierSet {
	t.Helper()
	set, err := tiers.Tiers()
	require.NoError(t, err)
	return set
}

func execModelTiers(t *testing.T, ctx context.Context, src string) error {
	t.Helper()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	require.NoError(t, registerAllBuzz(ctx, sess, map[string]vm.Callable{}, map[string]vm.Value{}, true))
	err := sess.Exec(ctx, src)
	_ = sess.Close()
	return err
}

const levelsDecl = `
import "magus";

enum Level { low, high }

fun model(level: Level) > str {
    return match (level) { .low -> "small", .high -> "big" };
}

fun bounds(level: Level) > magus\ModelTierBounds {
    return match (level) {
        .low -> magus\ModelTierBounds{ maxLive = 3, maxContext = 5000 },
        .high -> magus\ModelTierBounds{ maxLive = 1 },
    };
}

fun skills(level: Level) > magus\SkillVariant {
    return match (level) {
        .low -> magus\SkillVariant.full,
        .high -> magus\SkillVariant.short,
    };
}
`

func TestModelTiersDeclareTheWorkspacesOwnEnum(t *testing.T) {
	_, tiers, err := loadModelTiers(t, levelsDecl+`
magus\modelTiers(Level, default: Level.high,
    hosts: {"claude-code": model},
    bounds: bounds, skills: skills);
`, "claude-code")
	require.NoError(t, err)

	set := requireTiers(t, tiers)
	require.NotNil(t, set)
	var names []string
	for _, tier := range set.All() {
		names = append(names, tier.Name())
	}
	assert.Equal(t, []string{"low", "high"}, names, "declaration order is rank")
	assert.Equal(t, "high", set.Default().Name())

	low, err := set.Parse("low")
	require.NoError(t, err)
	high, err := set.Parse("high")
	require.NoError(t, err)
	assert.Equal(t, agent.ModelTierBounds{MaxLive: 3, MaxContext: 5000}, set.Bounds(low))
	assert.Equal(t, agent.ModelTierBounds{MaxLive: 1}, set.Bounds(high), "a field the function leaves out is no cap")
	assert.Equal(t, agent.SkillVariantFull, set.Skills(low))
	assert.Equal(t, agent.SkillVariantShort, set.Skills(high))

	alias, err := set.Alias("claude-code", high)
	require.NoError(t, err)
	assert.Equal(t, "big", alias)
	alias, err = set.Alias("claude-code", low)
	require.NoError(t, err)
	assert.Equal(t, "small", alias)
}

func TestModelTiersHostsAreKeyedByWhatProviderReturns(t *testing.T) {
	reg, tiers, err := loadModelTiers(t, levelsDecl+`
magus\harness.provider({"name": "cursor"});
final cc = magus\harness.provider({"name": "claude-code"});
magus\modelTiers(Level, default: Level.low,
    hosts: {cc: model}, bounds: bounds, skills: skills);
`)
	require.NoError(t, err, "a provider call that discards its key still wires the harness")
	assert.Equal(t, []string{"cursor", "claude-code"}, reg.Harnesses())

	set := requireTiers(t, tiers)
	alias, err := set.Alias("claude-code", set.Default())
	require.NoError(t, err)
	assert.Equal(t, "small", alias)
	_, err = set.Alias("cursor", set.Default())
	require.ErrorContains(t, err, "hosts are claude-code", "the discarded key maps nothing")
}

func TestModelTiersWithoutAHostsMapMapNoHarness(t *testing.T) {
	_, tiers, err := loadModelTiers(t, levelsDecl+`
magus\modelTiers(Level, default: Level.low, bounds: bounds, skills: skills);
`)
	require.NoError(t, err)
	set := requireTiers(t, tiers)
	_, err = set.Alias("claude-code", set.Default())
	require.ErrorContains(t, err, "maps no model for harness claude-code")
}

func TestModelTiersDefaultToTheShippedDeclaration(t *testing.T) {
	_, tiers, err := loadModelTiers(t, `import "magus";`)
	require.NoError(t, err)

	set := requireTiers(t, tiers)
	require.NotNil(t, set, "a workspace that declares nothing gets magus/modeltiers")
	var names []string
	for _, tier := range set.All() {
		names = append(names, tier.Name())
	}
	assert.Equal(t, []string{"routine", "bounded", "design", "frontier"}, names)
	assert.Equal(t, "bounded", set.Default().Name())
	routine, err := set.Parse("routine")
	require.NoError(t, err)
	assert.Equal(t, agent.ModelTierBounds{MaxContext: 100000}, set.Bounds(routine))
	bounded, err := set.Parse("bounded")
	require.NoError(t, err)
	assert.Zero(t, set.Bounds(bounded).MaxLive, "the default caps no tier's live jobs")
	assert.Equal(t, agent.SkillVariantFull, set.Skills(routine), "routine workers load the full skill bodies")
	assert.Same(t, set, requireTiers(t, tiers), "the default is built once")
}

func TestModelTiersNullTurnsThemOff(t *testing.T) {
	_, tiers, err := loadModelTiers(t, `
import "magus";
magus\modelTiers(null);
`)
	require.NoError(t, err)
	assert.Nil(t, requireTiers(t, tiers), "off is not the shipped default")
}

func TestModelTiersNullTakesNothingElse(t *testing.T) {
	_, _, err := loadModelTiers(t, levelsDecl+`
magus\modelTiers(null, default: Level.low);
`)
	require.ErrorContains(t, err, string(types.ModelTierDeclarationUnusable))
	assert.ErrorContains(t, err, "takes no other argument")
}

func TestModelTiersMisdeclarationIsCoded(t *testing.T) {
	cases := []struct {
		name, call, want string
		wired            []string
	}{
		{"tiers are not an enum", `magus\modelTiers("low", default: Level.low, bounds: bounds, skills: skills);`,
			"tiers must be an enum", nil},
		{"default is another enum's case", `
enum Other { low }
magus\modelTiers(Level, default: Other.low, bounds: bounds, skills: skills);`,
			"default must be a case of Level", nil},
		{"no default", `magus\modelTiers(Level, bounds: bounds, skills: skills);`,
			"default must be a case of Level", nil},
		{"bounds is not a function", `magus\modelTiers(Level, default: Level.low, bounds: 3, skills: skills);`,
			"bounds must be a function", nil},
		{"no skills", `magus\modelTiers(Level, default: Level.low, bounds: bounds);`,
			"skills must be a function", nil},
		{"a host that is not wired", `magus\modelTiers(Level, default: Level.low,
    hosts: {"codex": model}, bounds: bounds, skills: skills);`,
			"has not wired", []string{"claude-code"}},
		{"a host keyed by something other than an id", `magus\modelTiers(Level, default: Level.low,
    hosts: {3: model}, bounds: bounds, skills: skills);`,
			"must be the harness id", []string{"claude-code"}},
		{"two tiers apart with one alias", `
enum Spread { low, mid, high }
fun spreadModel(s: Spread) > str { return match (s) { .low -> "one", .mid -> "two", .high -> "one" }; }
fun spreadBounds(s: Spread) > magus\ModelTierBounds { return magus\ModelTierBounds{}; }
fun spreadSkills(s: Spread) > magus\SkillVariant { return magus\SkillVariant.short; }
magus\modelTiers(Spread, default: Spread.low,
    hosts: {"claude-code": spreadModel}, bounds: spreadBounds, skills: spreadSkills);`,
			"only adjacent tiers may share an id", []string{"claude-code"}},
		{"an empty alias", `
fun none(level: Level) > str { return ""; }
magus\modelTiers(Level, default: Level.low,
    hosts: {"claude-code": none}, bounds: bounds, skills: skills);`,
			"empty alias", []string{"claude-code"}},
		{"a negative bound", `
fun negative(level: Level) > magus\ModelTierBounds { return magus\ModelTierBounds{ maxLive = -1 }; }
magus\modelTiers(Level, default: Level.low, bounds: negative, skills: skills);`,
			"negative", nil},
		{"a mapping that raises", `
fun broken(level: Level) > magus\SkillVariant !> str { throw "no skills"; }
magus\modelTiers(Level, default: Level.low, bounds: bounds, skills: broken);`,
			"skills raised for Level.low", nil},
		{"declared twice", `
magus\modelTiers(Level, default: Level.low, bounds: bounds, skills: skills);
magus\modelTiers(Level, default: Level.high, bounds: bounds, skills: skills);`,
			"already declared", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := loadModelTiers(t, levelsDecl+tc.call, tc.wired...)
			require.Error(t, err)
			assert.ErrorContains(t, err, string(types.ModelTierDeclarationUnusable))
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func TestModelTiersNeedTheRootMagusfile(t *testing.T) {
	call := `
import "magus";
magus\modelTiers(null);
`
	t.Run("from a project's magusfile", func(t *testing.T) {
		reg := workspace.NewWorkspaceRegistry()
		ctx := interp.WithProjectPath(workspace.ContextWithRegistry(t.Context(), reg), "libs/foo")
		err := execModelTiers(t, ctx, call)
		require.ErrorContains(t, err, string(types.ModelTierDeclarationUnusable))
		assert.ErrorContains(t, err, "root magusfile")
	})
	t.Run("from the root project", func(t *testing.T) {
		reg := workspace.NewWorkspaceRegistry()
		ctx := interp.WithProjectPath(workspace.ContextWithRegistry(t.Context(), reg), ".")
		require.NoError(t, execModelTiers(t, ctx, call))
	})
	t.Run("with no workspace to declare into", func(t *testing.T) {
		err := execModelTiers(t, t.Context(), call)
		require.ErrorContains(t, err, string(types.MagusfileOnlyMember))
	})
}

const matrixDecl = levelsDecl + `
object Choice { tier: Level, skills: magus\SkillVariant, reason: str }

magus\modelTiers(Level, default: Level.low,
    hosts: {"claude-code": model}, bounds: bounds, skills: skills);
`

func TestModelTierMatrixChoosesAfterTheLoadCloses(t *testing.T) {
	reg, _, err := loadModelTiers(t, matrixDecl+`
magus\modelTier(fun (req: ModelTierRequest) > Choice? {
    if (req.id == "default-me") { return null; }
    return Choice{ tier = Level.high, skills = magus\SkillVariant.full, reason = "{req.id}: {req.kind.value}" };
});
`, "claude-code")
	require.NoError(t, err)
	matrix := reg.ModelTierMatrix()
	require.NotNil(t, matrix, "the root magusfile's registration reaches the registry")

	got, err := matrix(t.Context(), types.ModelTierRequest{ID: "wide", Kind: types.JobKindCheck})
	require.NoError(t, err)
	assert.Equal(t, &agent.JobModelTier{
		Name: "high", Rank: 1, Skills: agent.SkillVariantFull,
		Source: agent.ModelTierSourceMatrix, Reason: "wide: check", Site: "magusfile",
	}, got)

	got, err = matrix(t.Context(), types.ModelTierRequest{ID: "default-me"})
	require.NoError(t, err)
	assert.Nil(t, got, "null leaves the declared default to decide")
}

func TestModelTierMatrixAnswersAreChecked(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"no reason", `return Choice{ tier = Level.low, skills = magus\SkillVariant.short, reason = "  " };`, "no reason"},
		{"another enum's case", `return .{ tier = Other.a, skills = magus\SkillVariant.short, reason = "x" };`, "tier"},
		{"not an object", `return "high";`, "not null or an object"},
		{"raises", `throw "boom";`, "the matrix raised"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg, _, err := loadModelTiers(t, fmt.Sprintf(`%s
enum Other { a }
magus\modelTier(fun (req: ModelTierRequest) > any !> any {
    %s
});
`, matrixDecl, tc.body), "claude-code")
			require.NoError(t, err)
			_, err = reg.ModelTierMatrix()(t.Context(), types.ModelTierRequest{})
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func levelSet(t *testing.T) *agent.ModelTierSet {
	t.Helper()
	set, err := agent.NewModelTierSet(agent.ModelTierDeclaration{
		Enum: "Level", Site: "magusfile", Cases: []string{"low", "high"},
		Bounds: make([]agent.ModelTierBounds, 2),
		Skills: []agent.SkillVariant{agent.SkillVariantFull, agent.SkillVariantShort},
	})
	require.NoError(t, err)
	return set
}

func userMatrixModule(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "matrix.buzz")
	require.NoError(t, os.WriteFile(path, []byte(fmt.Sprintf(`import "magus";
enum Level { low, middle, high }
export fun choose(req: ModelTierRequest) > any {
%s
}
`, body)), 0o644))
	return path
}

func TestUserModelTierMatrix(t *testing.T) {
	set := levelSet(t)
	tiers := func(context.Context) *agent.ModelTierSet { return set }

	ask := func(t *testing.T, path string, tiers func(context.Context) *agent.ModelTierSet) (*agent.JobModelTier, error) {
		t.Helper()
		return userModelTierMatrix(path, tiers)(t.Context(), types.ModelTierRequest{ID: "j"})
	}

	t.Run("answers carry the user source", func(t *testing.T) {
		path := userMatrixModule(t, `return .{ tier = Level.high, skills = magus\SkillVariant.short, reason = "mine" };`)
		got, err := ask(t, path, tiers)
		require.NoError(t, err)
		assert.Equal(t, &agent.JobModelTier{
			Name: "high", Rank: 1, Skills: agent.SkillVariantShort,
			Source: agent.ModelTierSourceUserMatrix, Reason: "mine", Site: path,
		}, got)
	})
	t.Run("null declines", func(t *testing.T) {
		got, err := ask(t, userMatrixModule(t, `return null;`), tiers)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
	t.Run("a tier the workspace lacks fails", func(t *testing.T) {
		path := userMatrixModule(t, `return .{ tier = Level.middle, skills = magus\SkillVariant.short, reason = "x" };`)
		_, err := ask(t, path, tiers)
		require.ErrorContains(t, err, "middle")
	})
	t.Run("tiers off fails", func(t *testing.T) {
		_, err := ask(t, userMatrixModule(t, `return null;`), func(context.Context) *agent.ModelTierSet { return nil })
		require.ErrorContains(t, err, "turned them off")
	})
	t.Run("a module that does not load fails", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "broken.buzz")
		require.NoError(t, os.WriteFile(path, []byte("this is not buzz"), 0o644))
		_, err := ask(t, path, tiers)
		require.ErrorContains(t, err, "does not load")
	})
	t.Run("a relative path fails", func(t *testing.T) {
		_, err := ask(t, "matrix.buzz", tiers)
		require.ErrorContains(t, err, "absolute path")
	})
}

func TestModelTierMatrixCannotTouchTheJobStore(t *testing.T) {
	reg, _, err := loadModelTiers(t, matrixDecl+`
magus\modelTier(fun (req: ModelTierRequest) > any !> any {
    magus\workspace().putJob("smuggled", opts: {"agent": {"instructions": "from a matrix", "worker": {"type": "resumed"}}});
    return null;
});
`, "claude-code")
	require.NoError(t, err)
	_, err = reg.ModelTierMatrix()(t.Context(), types.ModelTierRequest{ID: "x"})
	require.ErrorContains(t, err, "read-only")
}

func TestModelTierMatrixWithTiersOffFails(t *testing.T) {
	reg, _, err := loadModelTiers(t, `
import "magus";
magus\modelTiers(null);
magus\modelTier(fun (req: ModelTierRequest) > any { return null; });
`)
	require.NoError(t, err)
	_, err = reg.ModelTierMatrix()(t.Context(), types.ModelTierRequest{})
	require.ErrorContains(t, err, "turned them off")
}

func TestModelTierMatrixMisdeclarationIsCoded(t *testing.T) {
	fn := vm.DirectValue("matrix", func(context.Context, []vm.Value) (vm.Value, error) { return vm.Null, nil })
	register := func(ctx context.Context) vm.Value {
		jobMap := vm.NewMap()
		registerModelTierMatrix(workspace.ContextWithRegistry(ctx, workspace.NewWorkspaceRegistry()), nil, nil, jobMap)
		return requireDirect(t, jobMap, "modelTier")
	}

	t.Run("not a function", func(t *testing.T) {
		err := callVoidDirect(t, register(t.Context()), vm.StringValue("matrix"))
		require.ErrorContains(t, err, string(types.ModelTierDeclarationUnusable))
		assert.ErrorContains(t, err, "expected one function")
	})
	t.Run("outside the root magusfile", func(t *testing.T) {
		err := callVoidDirect(t, register(interp.WithProjectPath(t.Context(), "libs/foo")), fn)
		require.ErrorContains(t, err, string(types.ModelTierDeclarationUnusable))
		assert.ErrorContains(t, err, "root magusfile")
	})
}

// A workspace that declares no tiers gets the shipped ones mapped to each wired harness's
// own model rows, by the exact ids they name, so a spawn's model resolves to a tier.
func TestModelTiersDefaultMapsTheHarnessRows(t *testing.T) {
	rows := func(tiers ...string) spells.Option {
		list := make([]string, len(tiers))
		for i, tier := range tiers {
			list[i] = fmt.Sprintf("ModelRow{id = %q, tier = %q}", "m-"+tier, tier)
		}
		return spells.WithFuncSource(fmt.Sprintf("import \"magus/spell\";\nexport fun mgs_listModels() > [ModelRow] { return [%s]; }\n", strings.Join(list, ", ")))
	}
	full := register.Spell(t, "tiered-test", rows("routine", "bounded", "design", "frontier"))
	_, tiers, err := loadModelTiers(t, `import "magus";`, full)
	require.NoError(t, err)
	set := requireTiers(t, tiers)
	require.NotNil(t, set)
	routine, err := set.Parse("routine")
	require.NoError(t, err)
	alias, err := set.Alias(full, routine)
	require.NoError(t, err)
	assert.Equal(t, "m-routine", alias)
	resolved, ok := set.Resolve(full, "m-design")
	require.True(t, ok)
	assert.Equal(t, "design", resolved.Name())

	partial := register.Spell(t, "half-tiered-test", rows("routine"))
	_, err = defaultModelTierSet(t.Context(), []string{partial})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `add tier = "bounded"`)
}

// A harness spell whose rows still name the three old tiers maps the first
// three of the four, and names the one it lacks.
func TestModelTiersDefaultReadsOldTierNamesOnHarnessRows(t *testing.T) {
	old := register.Spell(t, "old-tiered-test", spells.WithFuncSource(`import "magus/spell";
export fun mgs_listModels() > [ModelRow] {
    return [
        ModelRow{id = "m-low", tier = "economy"}, ModelRow{id = "m-mid", tier = "standard"},
        ModelRow{id = "m-high", tier = "principal"}, ModelRow{id = "m-top", tier = "frontier"},
    ];
}
`))
	set, err := defaultModelTierSet(t.Context(), []string{old})
	require.NoError(t, err)
	var aliases []string
	for _, tier := range set.All() {
		alias, err := set.Alias(old, tier)
		require.NoError(t, err)
		aliases = append(aliases, alias)
	}
	assert.Equal(t, []string{"m-low", "m-mid", "m-high", "m-top"}, aliases)
}

func TestModelTiersLetAdjacentTiersShareAnAlias(t *testing.T) {
	_, tiers, err := loadModelTiers(t, levelsDecl+`
fun same(level: Level) > str { return "one"; }
magus\modelTiers(Level, default: Level.low,
    hosts: {"claude-code": same}, bounds: bounds, skills: skills);`, "claude-code")
	require.NoError(t, err)
	set := requireTiers(t, tiers)
	require.NotNil(t, set)
	high, err := set.Parse("high")
	require.NoError(t, err)
	assert.Equal(t, []string{"claude-code runs low and high on one id, one"}, set.SharedAliases(high))
}
