package types

import (
	"testing"

	json "github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testHost = HarnessKey{ID: "test-host"}

func threeTiers() ModelTierDeclaration {
	return ModelTierDeclaration{
		Enum:    "ModelTier",
		Site:    "magusfile.buzz:3",
		Cases:   []string{"economy", "standard", "principal"},
		Default: 1,
		Bounds:  []ModelTierBounds{{MaxLive: 4, MaxContext: 100000}, {MaxLive: 2}, {MaxLive: 1}},
		Skills:  []SkillVariant{SkillVariantFull, SkillVariantShort, SkillVariantShort},
		Hosts:   map[HarnessKey][]string{testHost: {"small", "medium", "large"}},
	}
}

func mustTiers(t *testing.T, decl ModelTierDeclaration) *ModelTierSet {
	t.Helper()
	set, err := NewModelTierSet(decl)
	require.NoError(t, err)
	return set
}

func TestModelTierSetReadsTheDeclaration(t *testing.T) {
	t.Parallel()
	set := mustTiers(t, threeTiers())

	names := []string{}
	for _, tier := range set.All() {
		names = append(names, tier.Name())
	}
	assert.Equal(t, []string{"economy", "standard", "principal"}, names)

	def := set.Default()
	assert.Equal(t, "standard", def.Name())
	assert.Equal(t, 1, def.Rank())

	economy, ok := set.At(0)
	require.True(t, ok)
	assert.Equal(t, ModelTierBounds{MaxLive: 4, MaxContext: 100000}, set.Bounds(economy))
	assert.Equal(t, SkillVariantFull, set.Skills(economy))
	assert.Equal(t, SkillVariantShort, set.Skills(def))

	_, ok = set.At(3)
	assert.False(t, ok)
	_, ok = set.At(-1)
	assert.False(t, ok)
}

func TestModelTierSetKeepsNoAliasOfItsDeclaration(t *testing.T) {
	t.Parallel()
	decl := threeTiers()
	set := mustTiers(t, decl)
	decl.Cases[0] = "changed"
	decl.Hosts[testHost][0] = "changed"

	economy, err := set.Parse("economy")
	require.NoError(t, err)
	alias, err := set.Alias(testHost, economy)
	require.NoError(t, err)
	assert.Equal(t, "small", alias)
}

func TestModelTierParseOfAnUnknownNameNamesTheCases(t *testing.T) {
	t.Parallel()
	set := mustTiers(t, threeTiers())

	_, err := set.Parse("standrd")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"standrd"`)
	assert.Contains(t, err.Error(), "economy, standard, principal")
	assert.Contains(t, err.Error(), "magusfile.buzz:3")
}

func TestModelTierAboveComparesOnlyWithinOneSet(t *testing.T) {
	t.Parallel()
	set := mustTiers(t, threeTiers())
	other := mustTiers(t, threeTiers())
	economy, _ := set.At(0)
	principal, _ := set.At(2)

	above, err := principal.Above(economy)
	require.NoError(t, err)
	assert.True(t, above)
	above, err = economy.Above(principal)
	require.NoError(t, err)
	assert.False(t, above)
	above, err = economy.Above(economy)
	require.NoError(t, err)
	assert.False(t, above)

	foreign, _ := other.At(0)
	_, err = principal.Above(foreign)
	require.Error(t, err, "an equal declaration is still a different set")
	assert.Contains(t, err.Error(), "different declarations")

	_, err = principal.Above(ModelTier{})
	require.Error(t, err)
}

func TestModelTierZeroValueIsUnassigned(t *testing.T) {
	t.Parallel()
	var zero ModelTier
	assert.True(t, zero.IsZero())
	assert.Equal(t, "", zero.Name())
	assert.Equal(t, -1, zero.Rank())

	set := mustTiers(t, threeTiers())
	assert.False(t, set.Default().IsZero())
	assert.Equal(t, ModelTierBounds{}, set.Bounds(zero))
	assert.Equal(t, SkillVariant(""), set.Skills(zero))
}

func TestModelTierMarshalsItsName(t *testing.T) {
	t.Parallel()
	set := mustTiers(t, threeTiers())
	out, err := json.Marshal(map[string]ModelTier{"tier": set.Default()})
	require.NoError(t, err)
	assert.JSONEq(t, `{"tier":"standard"}`, string(out))
}

func TestModelTierAliasAndResolveRoundTrip(t *testing.T) {
	t.Parallel()
	set := mustTiers(t, threeTiers())

	for _, tier := range set.All() {
		alias, err := set.Alias(testHost, tier)
		require.NoError(t, err)
		got, ok := set.Resolve(testHost, alias)
		require.Truef(t, ok, "alias %q resolves", alias)
		assert.Equal(t, tier, got)
	}

	_, ok := set.Resolve(testHost, "unmapped-model")
	assert.False(t, ok)
	_, ok = set.Resolve(HarnessKey{ID: "other"}, "small")
	assert.False(t, ok)

	_, err := set.Alias(HarnessKey{ID: "other"}, set.Default())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "test-host", "the error names the hosts the declaration maps")

	foreign := mustTiers(t, threeTiers()).Default()
	_, err = set.Alias(testHost, foreign)
	require.Error(t, err)
}

func TestNewModelTierSetRefusesMisconfiguration(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		edit func(*ModelTierDeclaration)
		want string
	}{
		{"no cases", func(d *ModelTierDeclaration) {
			d.Cases, d.Bounds, d.Skills, d.Hosts = nil, nil, nil, nil
		}, "declares no cases"},
		{"no enum name", func(d *ModelTierDeclaration) { d.Enum = "" }, "has no name"},
		{"duplicate case", func(d *ModelTierDeclaration) { d.Cases[2] = "economy" }, `"economy" is declared twice`},
		{"default past the cases", func(d *ModelTierDeclaration) { d.Default = 3 }, "default rank 3 is outside 0..2"},
		{"negative default", func(d *ModelTierDeclaration) { d.Default = -1 }, "default rank -1"},
		{"bounds short", func(d *ModelTierDeclaration) { d.Bounds = d.Bounds[:2] }, "bounds covers 2 cases"},
		{"negative bound", func(d *ModelTierDeclaration) { d.Bounds[1].MaxLive = -1 }, "bounds for rank 1 are negative"},
		{"skills short", func(d *ModelTierDeclaration) { d.Skills = d.Skills[:1] }, "skills covers 1 cases"},
		{"skill unset", func(d *ModelTierDeclaration) { d.Skills[0] = "" }, "skills for rank 0 is unset"},
		{"skill unknown", func(d *ModelTierDeclaration) { d.Skills[0] = "medium" }, "the variants are short, full"},
		{"host list short", func(d *ModelTierDeclaration) {
			d.Hosts[testHost] = []string{"small", "large"}
		}, "host test-host maps 2 cases and the enum declares 3"},
		{"host list long", func(d *ModelTierDeclaration) {
			d.Hosts[testHost] = []string{"a", "b", "c", "d"}
		}, "host test-host maps 4 cases"},
		{"empty alias", func(d *ModelTierDeclaration) { d.Hosts[testHost][1] = "" }, "maps rank 1 to an empty alias"},
		{"shared alias", func(d *ModelTierDeclaration) { d.Hosts[testHost][2] = "small" }, `maps two cases to "small"`},
		{"empty host id", func(d *ModelTierDeclaration) { d.Hosts[HarnessKey{}] = []string{"a", "b", "c"} }, "has no harness id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			decl := threeTiers()
			tc.edit(&decl)
			set, err := NewModelTierSet(decl)
			require.Error(t, err)
			assert.Nil(t, set)
			assert.Contains(t, err.Error(), tc.want)
			assert.ErrorIs(t, err, errModelTierDeclaration)
		})
	}
}

func TestNewModelTierSetReportsEveryProblem(t *testing.T) {
	t.Parallel()
	decl := threeTiers()
	decl.Default = 9
	decl.Skills = nil
	_, err := NewModelTierSet(decl)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "default rank 9")
	assert.Contains(t, err.Error(), "skills covers 0 cases")
}

func TestEnumModelTierVocabulariesCloseTheirSets(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"short", "full"}, SkillVariant("").Values())
	assert.Equal(t, []string{"check", "symbol", "paths", "script", "prose"}, JobKind("").Values())
	assert.Equal(t, []string{"row", "matrix", "default"}, ModelTierSource("").Values())
	assert.False(t, SkillVariant("medium").Valid())
	assert.True(t, SkillVariantFull.Full())
	assert.False(t, SkillVariantFull.Short())
	assert.True(t, SkillVariantShort.Short())
	assert.Equal(t, "short", SkillVariantShort.String(), "the skill-variant stamp prints this")
}

func TestModelTierSetNamesItsSiteAndHosts(t *testing.T) {
	t.Parallel()

	decl := threeTiers()
	decl.Hosts = map[HarnessKey][]string{
		{ID: "zed"}:     {"a", "b", "c"},
		{ID: "claude"}:  {"d", "e", "f"},
		{ID: "bravado"}: {"g", "h", "i"},
	}
	set := mustTiers(t, decl)

	assert.Equal(t, "magusfile.buzz:3", set.Site())
	assert.Equal(t, []HarnessKey{{ID: "bravado"}, {ID: "claude"}, {ID: "zed"}}, set.Hosts())
	assert.Empty(t, mustTiers(t, func() ModelTierDeclaration { d := threeTiers(); d.Hosts = nil; return d }()).Hosts())
}

// An outcome recorded before attempts were numbered reads as a first attempt, so the history
// it sits in needs no rewrite, and a retryable ending writes no retry member.
func TestJobOutcomeOmitsAnUnnumberedAttemptAndARetryableEnding(t *testing.T) {
	t.Parallel()

	members := func(o JobOutcome) map[string]any {
		raw, err := json.Marshal(o)
		require.NoError(t, err)
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		return m
	}
	first := members(JobOutcome{Job: "a", Kind: JobKindCheck, State: StatePass})
	assert.NotContains(t, first, "attempt")
	assert.NotContains(t, first, "retry")

	again := members(JobOutcome{Job: "a", Kind: JobKindCheck, State: StateFail, Attempt: 2, Retry: RetryAfterWidening})
	assert.Equal(t, float64(2), again["attempt"])
	assert.Equal(t, "widening", again["retry"])
}

func TestJobModelTierStringIsTheLineForkPrints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		tier JobModelTier
		want string
	}{
		{"reason and site", JobModelTier{Name: "economy", Source: ModelTierSourceMatrix, Reason: "small", Site: "magusfile.buzz:20"}, `economy (rank 0), matrix: "small" at magusfile.buzz:20`},
		{"reason alone", JobModelTier{Name: "standard", Rank: 1, Source: ModelTierSourceDefault, Reason: "the declared default"}, `standard (rank 1), default: "the declared default"`},
		{"neither", JobModelTier{Name: "principal", Rank: 2, Source: ModelTierSourceRow}, "principal (rank 2), row"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.tier.String())
		})
	}
}
