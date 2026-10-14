package bindings

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/workspace"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loadModelTiers runs src as the root magusfile of a workspace that has wired harnesses,
// and returns the registry with the session already CLOSED: a fork asks long after the
// load that declared the tiers.
func loadModelTiers(t *testing.T, src string, harnesses ...string) (*workspace.WorkspaceRegistry, error) {
	t.Helper()
	reg := workspace.NewWorkspaceRegistry()
	t.Cleanup(func() { _ = reg.Close() })
	for _, h := range harnesses {
		reg.AddHarness(h)
	}
	return reg, execModelTiers(t, workspace.ContextWithRegistry(t.Context(), reg), src)
}

func execModelTiers(t *testing.T, ctx context.Context, src string) error {
	t.Helper()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	registerAllBuzz(ctx, sess, map[string]vm.Callable{}, map[string]vm.Value{}, true)
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
	reg, err := loadModelTiers(t, levelsDecl+`
magus\job.modelTiers(Level, default: Level.high,
    hosts: {magus\HarnessKey{ id = "claude-code" }: model},
    bounds: bounds, skills: skills);
`, "claude-code")
	require.NoError(t, err)

	set := reg.ModelTiers()
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
	assert.Equal(t, types.ModelTierBounds{MaxLive: 3, MaxContext: 5000}, set.Bounds(low))
	assert.Equal(t, types.ModelTierBounds{MaxLive: 1}, set.Bounds(high), "a field the function leaves out is no cap")
	assert.Equal(t, types.SkillVariantFull, set.Skills(low))
	assert.Equal(t, types.SkillVariantShort, set.Skills(high))

	alias, err := set.Alias(types.HarnessKey{ID: "claude-code"}, high)
	require.NoError(t, err)
	assert.Equal(t, "big", alias)
	alias, err = set.Alias(types.HarnessKey{ID: "claude-code"}, low)
	require.NoError(t, err)
	assert.Equal(t, "small", alias)
}

func TestModelTiersHostsAreKeyedByWhatProviderReturns(t *testing.T) {
	reg, err := loadModelTiers(t, levelsDecl+`
magus\harness.provider({"name": "cursor"});
final cc = magus\harness.provider({"name": "claude-code"});
magus\job.modelTiers(Level, default: Level.low,
    hosts: {cc: model}, bounds: bounds, skills: skills);
`)
	require.NoError(t, err, "a provider call that discards its key still wires the harness")
	assert.Equal(t, []string{"cursor", "claude-code"}, reg.Harnesses())

	set := reg.ModelTiers()
	alias, err := set.Alias(types.HarnessKey{ID: "claude-code"}, set.Default())
	require.NoError(t, err)
	assert.Equal(t, "small", alias)
	_, err = set.Alias(types.HarnessKey{ID: "cursor"}, set.Default())
	require.ErrorContains(t, err, "hosts are claude-code", "the discarded key maps nothing")
}

func TestModelTiersWithoutAHostsMapMapNoHarness(t *testing.T) {
	reg, err := loadModelTiers(t, levelsDecl+`
magus\job.modelTiers(Level, default: Level.low, bounds: bounds, skills: skills);
`)
	require.NoError(t, err)
	_, err = reg.ModelTiers().Alias(types.HarnessKey{ID: "claude-code"}, reg.ModelTiers().Default())
	require.ErrorContains(t, err, "maps no model for harness claude-code")
}

func TestModelTiersDefaultToTheShippedDeclaration(t *testing.T) {
	reg, err := loadModelTiers(t, `import "magus";`)
	require.NoError(t, err)

	set := reg.ModelTiers()
	require.NotNil(t, set, "a workspace that declares nothing gets magus/modeltiers")
	var names []string
	for _, tier := range set.All() {
		names = append(names, tier.Name())
	}
	assert.Equal(t, []string{"economy", "standard", "principal"}, names)
	assert.Equal(t, "standard", set.Default().Name())
	economy, err := set.Parse("economy")
	require.NoError(t, err)
	assert.Equal(t, types.ModelTierBounds{MaxLive: 4, MaxContext: 100000}, set.Bounds(economy))
	assert.Equal(t, types.SkillVariantFull, set.Skills(economy), "economy workers load the full skill bodies")
	assert.Same(t, set, reg.ModelTiers(), "the default is built once")
}

func TestModelTiersNullTurnsThemOff(t *testing.T) {
	reg, err := loadModelTiers(t, `
import "magus";
magus\job.modelTiers(null);
`)
	require.NoError(t, err)
	assert.Nil(t, reg.ModelTiers(), "off is not the shipped default")
}

func TestModelTiersNullTakesNothingElse(t *testing.T) {
	_, err := loadModelTiers(t, levelsDecl+`
magus\job.modelTiers(null, default: Level.low);
`)
	require.ErrorContains(t, err, string(types.ModelTierDeclarationUnusable))
	assert.ErrorContains(t, err, "takes no other argument")
}

func TestModelTiersMisdeclarationIsCoded(t *testing.T) {
	cases := []struct {
		name, call, want string
		wired            []string
	}{
		{"tiers are not an enum", `magus\job.modelTiers("low", default: Level.low, bounds: bounds, skills: skills);`,
			"tiers must be an enum", nil},
		{"default is another enum's case", `
enum Other { low }
magus\job.modelTiers(Level, default: Other.low, bounds: bounds, skills: skills);`,
			"default must be a case of Level", nil},
		{"no default", `magus\job.modelTiers(Level, bounds: bounds, skills: skills);`,
			"default must be a case of Level", nil},
		{"bounds is not a function", `magus\job.modelTiers(Level, default: Level.low, bounds: 3, skills: skills);`,
			"bounds must be a function", nil},
		{"no skills", `magus\job.modelTiers(Level, default: Level.low, bounds: bounds);`,
			"skills must be a function", nil},
		{"a host that is not wired", `magus\job.modelTiers(Level, default: Level.low,
    hosts: {magus\HarnessKey{ id = "codex" }: model}, bounds: bounds, skills: skills);`,
			"has not wired", []string{"claude-code"}},
		{"a host keyed by a string", `magus\job.modelTiers(Level, default: Level.low,
    hosts: {"claude-code": model}, bounds: bounds, skills: skills);`,
			"must be the magus\\HarnessKey", []string{"claude-code"}},
		{"two tiers with one alias", `
fun same(level: Level) > str { return "one"; }
magus\job.modelTiers(Level, default: Level.low,
    hosts: {magus\HarnessKey{ id = "claude-code" }: same}, bounds: bounds, skills: skills);`,
			"maps two cases", []string{"claude-code"}},
		{"an empty alias", `
fun none(level: Level) > str { return ""; }
magus\job.modelTiers(Level, default: Level.low,
    hosts: {magus\HarnessKey{ id = "claude-code" }: none}, bounds: bounds, skills: skills);`,
			"empty alias", []string{"claude-code"}},
		{"a negative bound", `
fun negative(level: Level) > magus\ModelTierBounds { return magus\ModelTierBounds{ maxLive = -1 }; }
magus\job.modelTiers(Level, default: Level.low, bounds: negative, skills: skills);`,
			"negative", nil},
		{"a mapping that raises", `
fun broken(level: Level) > magus\SkillVariant !> str { throw "no skills"; }
magus\job.modelTiers(Level, default: Level.low, bounds: bounds, skills: broken);`,
			"skills raised for Level.low", nil},
		{"declared twice", `
magus\job.modelTiers(Level, default: Level.low, bounds: bounds, skills: skills);
magus\job.modelTiers(Level, default: Level.high, bounds: bounds, skills: skills);`,
			"already declared", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadModelTiers(t, levelsDecl+tc.call, tc.wired...)
			require.Error(t, err)
			assert.ErrorContains(t, err, string(types.ModelTierDeclarationUnusable))
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func TestModelTiersNeedTheRootMagusfile(t *testing.T) {
	call := `
import "magus";
magus\job.modelTiers(null);
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

magus\job.modelTiers(Level, default: Level.low,
    hosts: {magus\HarnessKey{ id = "claude-code" }: model}, bounds: bounds, skills: skills);
`

func TestModelTierMatrixChoosesAfterTheLoadCloses(t *testing.T) {
	reg, err := loadModelTiers(t, matrixDecl+`
magus\job.modelTier(fun (req: ModelTierRequest) > Choice? {
    if (req.id == "default-me") { return null; }
    return Choice{ tier = Level.high, skills = magus\SkillVariant.full, reason = "{req.id}: {req.kind.value}" };
});
`, "claude-code")
	require.NoError(t, err)
	matrix := reg.ModelTierMatrix()
	require.NotNil(t, matrix, "the root magusfile's registration reaches the registry")

	got, err := matrix(t.Context(), types.ModelTierRequest{ID: "wide", Kind: types.JobKindCheck})
	require.NoError(t, err)
	assert.Equal(t, &types.JobModelTier{
		Name: "high", Rank: 1, Skills: types.SkillVariantFull,
		Source: types.ModelTierSourceMatrix, Reason: "wide: check", Site: "magusfile",
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
			reg, err := loadModelTiers(t, matrixDecl+`
enum Other { a }
magus\job.modelTier(fun (req: ModelTierRequest) > any !> any {
    `+tc.body+`
});
`, "claude-code")
			require.NoError(t, err)
			_, err = reg.ModelTierMatrix()(t.Context(), types.ModelTierRequest{})
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestModelTierMatrixCannotTouchTheJobStore(t *testing.T) {
	reg, err := loadModelTiers(t, matrixDecl+`
magus\job.modelTier(fun (req: ModelTierRequest) > any !> any {
    magus\job.put("smuggled", opts: {"criteria": "from a matrix"});
    return null;
});
`, "claude-code")
	require.NoError(t, err)
	_, err = reg.ModelTierMatrix()(t.Context(), types.ModelTierRequest{ID: "x"})
	require.ErrorContains(t, err, "read-only")
}

func TestModelTierMatrixWithTiersOffFails(t *testing.T) {
	reg, err := loadModelTiers(t, `
import "magus";
magus\job.modelTiers(null);
magus\job.modelTier(fun (req: ModelTierRequest) > any { return null; });
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
		err := callVoidDirect(t, register(t.Context()), vm.StrValue("matrix"))
		require.ErrorContains(t, err, string(types.ModelTierDeclarationUnusable))
		assert.ErrorContains(t, err, "expected one function")
	})
	t.Run("outside the root magusfile", func(t *testing.T) {
		err := callVoidDirect(t, register(interp.WithProjectPath(t.Context(), "libs/foo")), fn)
		require.ErrorContains(t, err, string(types.ModelTierDeclarationUnusable))
		assert.ErrorContains(t, err, "root magusfile")
	})
}
