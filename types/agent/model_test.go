package agent

import (
	"errors"
	"testing"

	"github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/suite"
)

const testHost = "test-host"

func fourTiers() ModelTierDeclaration {
	return ModelTierDeclaration{
		Enum:    "ModelTier",
		Site:    "magusfile.buzz:3",
		Cases:   []string{"routine", "bounded", "design", "frontier"},
		Default: 1,
		Bounds:  []ModelTierBounds{{MaxLive: 4, MaxContext: 100000}, {MaxLive: 2}, {MaxLive: 1}, {MaxLive: 1}},
		Skills:  []SkillVariant{SkillVariantFull, SkillVariantShort, SkillVariantShort, SkillVariantShort},
		Hosts:   map[string][]string{testHost: {"small", "medium", "large", "largest"}},
	}
}

type modelTierSuite struct {
	suite.Suite
}

func TestModelTier(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(modelTierSuite))
}

func (s *modelTierSuite) tiers(decl ModelTierDeclaration) *ModelTierSet {
	set, err := NewModelTierSet(decl)
	s.Require().NoError(err)
	return set
}

func (s *modelTierSuite) at(set *ModelTierSet, rank int) ModelTier {
	t, ok := set.At(rank)
	s.Require().True(ok)
	return t
}

// refused is the error NewModelTierSet gives decl once edit has changed it.
func (s *modelTierSuite) refused(edit func(*ModelTierDeclaration)) error {
	decl := fourTiers()
	edit(&decl)
	set, err := NewModelTierSet(decl)
	s.Require().Error(err)
	s.Nil(set)
	s.ErrorIs(err, errModelTierDeclaration)
	return err
}

func (s *modelTierSuite) TestTheSetReadsTheDeclaration() {
	set := s.tiers(fourTiers())

	names := []string{}
	for _, tier := range set.All() {
		names = append(names, tier.Name())
	}
	s.Equal([]string{"routine", "bounded", "design", "frontier"}, names)

	def := set.Default()
	s.Equal("bounded", def.Name())
	s.Equal(1, def.Rank())

	routine := s.at(set, 0)
	s.Equal(ModelTierBounds{MaxLive: 4, MaxContext: 100000}, set.Bounds(routine))
	s.Equal(SkillVariantFull, set.Skills(routine))
	s.Equal(SkillVariantShort, set.Skills(def))

	_, ok := set.At(4)
	s.False(ok)
	_, ok = set.At(-1)
	s.False(ok)
}

func (s *modelTierSuite) TestTheSetKeepsNoAliasOfItsDeclaration() {
	decl := fourTiers()
	set := s.tiers(decl)
	decl.Cases[0] = "changed"
	decl.Hosts[testHost][0] = "changed"

	routine, err := set.Parse("routine")
	s.Require().NoError(err)
	alias, err := set.Alias(testHost, routine)
	s.Require().NoError(err)
	s.Equal("small", alias)
}

func (s *modelTierSuite) TestParseOfAnUnknownNameNamesTheCases() {
	_, err := s.tiers(fourTiers()).Parse("bounde")
	s.Require().Error(err)
	s.Contains(err.Error(), `"bounde"`)
	s.Contains(err.Error(), "routine, bounded, design, frontier")
	s.Contains(err.Error(), "magusfile.buzz:3")
}

func (s *modelTierSuite) TestParseReadsEachOldShippedNameAsItsNewOne() {
	set := s.tiers(fourTiers())
	for old, renamed := range map[string]string{"economy": "routine", "standard": "bounded", "principal": "design"} {
		t, err := set.Parse(old)
		s.Require().NoError(err, old)
		s.Equal(renamed, t.Name(), "%s reads as %s", old, renamed)
	}
}

func (s *modelTierSuite) TestParseKeepsAnOldNameTheSetDeclares() {
	decl := fourTiers()
	decl.Cases = []string{"economy", "routine", "design", "frontier"}
	t, err := s.tiers(decl).Parse("economy")
	s.Require().NoError(err)
	s.Equal(0, t.Rank(), "a workspace's own economy is its own case")
}

func (s *modelTierSuite) TestParseRefusesAnOldNameWhoseNewOneIsUndeclared() {
	decl := fourTiers()
	decl.Cases = []string{"cheap", "bounded", "design", "frontier"}
	_, err := s.tiers(decl).Parse("economy")
	s.Require().Error(err, "economy reads as routine, which this set does not declare")
}

func (s *modelTierSuite) TestRenamedModelTierNamesOnlyTheOldShippedNames() {
	renamed, ok := RenamedModelTier("principal")
	s.True(ok)
	s.Equal("design", renamed)
	_, ok = RenamedModelTier("design")
	s.False(ok, "a current name is not renamed")
	_, ok = RenamedModelTier("frontier")
	s.False(ok)
}

func (s *modelTierSuite) TestAboveComparesOnlyWithinOneSet() {
	set := s.tiers(fourTiers())
	routine, design := s.at(set, 0), s.at(set, 2)

	above, err := design.Above(routine)
	s.Require().NoError(err)
	s.True(above)
	above, err = routine.Above(design)
	s.Require().NoError(err)
	s.False(above)
	above, err = routine.Above(routine)
	s.Require().NoError(err)
	s.False(above)

	_, err = design.Above(s.at(s.tiers(fourTiers()), 0))
	s.Require().Error(err, "an equal declaration is still a different set")
	s.Contains(err.Error(), "different declarations")

	_, err = design.Above(ModelTier{})
	s.Require().Error(err)
}

func (s *modelTierSuite) TestWithinAllowsEqualAndLowerOnly() {
	set := s.tiers(fourTiers())
	routine, bounded, design := s.at(set, 0), s.at(set, 1), s.at(set, 2)

	s.Require().NoError(bounded.Within(bounded), "a child may run at its parent's tier")
	s.Require().NoError(routine.Within(bounded), "a child may run below its parent's tier")

	err := design.Within(bounded)
	s.Require().Error(err)
	s.Equal("model tier design (rank 2) ranks above bounded (rank 1)", err.Error())

	err = routine.Within(s.at(s.tiers(fourTiers()), 2))
	s.Require().Error(err, "a ceiling from another declaration bounds nothing")
	s.Contains(err.Error(), "different declarations")

	err = routine.Within(ModelTier{})
	s.Require().Error(err, "an unassigned ceiling bounds nothing")
	s.Contains(err.Error(), "unassigned")
}

func (s *modelTierSuite) TestTheZeroValueIsUnassigned() {
	var zero ModelTier
	s.True(zero.IsZero())
	s.Empty(zero.Name())
	s.Equal(-1, zero.Rank())

	set := s.tiers(fourTiers())
	s.False(set.Default().IsZero())
	s.Equal(ModelTierBounds{}, set.Bounds(zero))
	s.Equal(SkillVariant(""), set.Skills(zero))
}

func (s *modelTierSuite) TestATierMarshalsItsName() {
	out, err := json.Marshal(map[string]ModelTier{"tier": s.tiers(fourTiers()).Default()})
	s.Require().NoError(err)
	s.JSONEq(`{"tier":"bounded"}`, string(out))
}

func (s *modelTierSuite) TestAliasAndResolveRoundTrip() {
	set := s.tiers(fourTiers())

	for _, tier := range set.All() {
		alias, err := set.Alias(testHost, tier)
		s.Require().NoError(err)
		got, ok := set.Resolve(testHost, alias)
		s.Require().Truef(ok, "alias %q resolves", alias)
		s.Equal(tier, got)
	}

	_, ok := set.Resolve(testHost, "unmapped-model")
	s.False(ok)
	_, ok = set.Resolve("other", "small")
	s.False(ok)

	_, err := set.Alias("other", set.Default())
	s.Require().Error(err)
	s.Contains(err.Error(), "test-host", "the error names the hosts the declaration maps")

	_, err = set.Alias(testHost, s.tiers(fourTiers()).Default())
	s.Require().Error(err)
}

func (s *modelTierSuite) TestAdjacentTiersMayShareAnAlias() {
	decl := fourTiers()
	decl.Hosts[testHost] = []string{"small", "medium", "large", "large"}
	set := s.tiers(decl)

	frontier := s.at(set, 3)
	alias, err := set.Alias(testHost, frontier)
	s.Require().NoError(err)
	s.Equal("large", alias)
	got, ok := set.Resolve(testHost, "large")
	s.Require().True(ok)
	s.Equal("design", got.Name(), "a shared alias resolves to the cheapest tier on it")
}

func (s *modelTierSuite) TestSharedAliasesNamesEachHostThatRepeatsAnID() {
	decl := fourTiers()
	decl.Hosts = map[string][]string{
		"zed":    {"a", "b", "c", "c"},
		"claude": {"d", "e", "f", "g"},
	}
	set := s.tiers(decl)

	s.Equal([]string{"zed runs design and frontier on one id, c"}, set.SharedAliases(s.at(set, 3)))
	s.Equal([]string{"zed runs design and frontier on one id, c"}, set.SharedAliases(s.at(set, 2)))
	s.Empty(set.SharedAliases(s.at(set, 1)), "no host repeats bounded's id")
	s.Empty(set.SharedAliases(s.at(s.tiers(fourTiers()), 3)), "a tier of another set shares nothing here")
}

func (s *modelTierSuite) TestDeclarationWithNoCasesIsRefused() {
	err := s.refused(func(d *ModelTierDeclaration) { d.Cases, d.Bounds, d.Skills, d.Hosts = nil, nil, nil, nil })
	s.Contains(err.Error(), "declares no cases")
}

func (s *modelTierSuite) TestDeclarationWithNoEnumNameIsRefused() {
	s.Contains(s.refused(func(d *ModelTierDeclaration) { d.Enum = "" }).Error(), "has no name")
}

func (s *modelTierSuite) TestADuplicateCaseIsRefused() {
	s.Contains(s.refused(func(d *ModelTierDeclaration) { d.Cases[2] = "routine" }).Error(), `"routine" is declared twice`)
}

func (s *modelTierSuite) TestADefaultPastTheCasesIsRefused() {
	s.Contains(s.refused(func(d *ModelTierDeclaration) { d.Default = 4 }).Error(), "default rank 4 is outside 0..3")
}

func (s *modelTierSuite) TestANegativeDefaultIsRefused() {
	s.Contains(s.refused(func(d *ModelTierDeclaration) { d.Default = -1 }).Error(), "default rank -1")
}

func (s *modelTierSuite) TestShortBoundsAreRefused() {
	s.Contains(s.refused(func(d *ModelTierDeclaration) { d.Bounds = d.Bounds[:2] }).Error(), "bounds covers 2 cases")
}

func (s *modelTierSuite) TestANegativeBoundIsRefused() {
	s.Contains(s.refused(func(d *ModelTierDeclaration) { d.Bounds[1].MaxLive = -1 }).Error(), "bounds for rank 1 are negative")
}

func (s *modelTierSuite) TestShortSkillsAreRefused() {
	s.Contains(s.refused(func(d *ModelTierDeclaration) { d.Skills = d.Skills[:1] }).Error(), "skills covers 1 cases")
}

func (s *modelTierSuite) TestAnUnsetSkillIsRefused() {
	s.Contains(s.refused(func(d *ModelTierDeclaration) { d.Skills[0] = "" }).Error(), "skills for rank 0 is unset")
}

func (s *modelTierSuite) TestAnUnknownSkillIsRefused() {
	s.Contains(s.refused(func(d *ModelTierDeclaration) { d.Skills[0] = "medium" }).Error(), "the variants are short, full")
}

func (s *modelTierSuite) TestAShortHostListIsRefused() {
	err := s.refused(func(d *ModelTierDeclaration) { d.Hosts[testHost] = []string{"small", "large"} })
	s.Contains(err.Error(), "host test-host maps 2 cases and the enum declares 4")
}

func (s *modelTierSuite) TestALongHostListIsRefused() {
	err := s.refused(func(d *ModelTierDeclaration) { d.Hosts[testHost] = []string{"a", "b", "c", "d", "e"} })
	s.Contains(err.Error(), "host test-host maps 5 cases")
}

func (s *modelTierSuite) TestAnEmptyAliasIsRefused() {
	s.Contains(s.refused(func(d *ModelTierDeclaration) { d.Hosts[testHost][1] = "" }).Error(), "maps rank 1 to an empty alias")
}

func (s *modelTierSuite) TestAnAliasTwoTiersApartShareIsRefused() {
	err := s.refused(func(d *ModelTierDeclaration) { d.Hosts[testHost][2] = "small" })
	s.Contains(err.Error(), `maps ranks 0 and 2 to "small"`)
}

func (s *modelTierSuite) TestAnAliasThatReturnsAfterAnotherIsRefused() {
	err := s.refused(func(d *ModelTierDeclaration) { d.Hosts[testHost] = []string{"a", "a", "b", "a"} })
	s.Contains(err.Error(), `maps ranks 0 and 3 to "a"`, "the run of a broke at rank 2")
}

func (s *modelTierSuite) TestAnEmptyHostIDIsRefused() {
	s.Contains(s.refused(func(d *ModelTierDeclaration) { d.Hosts[""] = []string{"a", "b", "c", "d"} }).Error(), "has no harness id")
}

func (s *modelTierSuite) TestEveryProblemIsReported() {
	decl := fourTiers()
	decl.Default = 9
	decl.Skills = nil
	_, err := NewModelTierSet(decl)
	s.Require().Error(err)
	s.Contains(err.Error(), "default rank 9")
	s.Contains(err.Error(), "skills covers 0 cases")
}

func (s *modelTierSuite) TestTheVocabulariesCloseTheirSets() {
	s.Equal([]string{"short", "full"}, SkillVariant("").Values())
	s.Equal([]string{"job", "matrix", "user-matrix", "default"}, ModelTierSource("").Values())
	s.False(SkillVariant("medium").Valid())
	s.True(SkillVariantFull.Full())
	s.False(SkillVariantFull.Short())
	s.True(SkillVariantShort.Short())
	s.Equal("short", SkillVariantShort.String(), "the skill-variant stamp prints this")
}

func (s *modelTierSuite) TestTheSetNamesItsSiteAndHosts() {
	decl := fourTiers()
	decl.Hosts = map[string][]string{
		"zed":     {"a", "b", "c", "d"},
		"claude":  {"e", "f", "g", "h"},
		"bravado": {"i", "j", "k", "l"},
	}
	set := s.tiers(decl)
	s.Equal("magusfile.buzz:3", set.Site())
	s.Equal([]string{"bravado", "claude", "zed"}, set.Hosts())

	bare := fourTiers()
	bare.Hosts = nil
	s.Empty(s.tiers(bare).Hosts())
}

func (s *modelTierSuite) TestTheForkLineNamesTheReasonAndSite() {
	tier := JobModelTier{Name: "routine", Source: ModelTierSourceMatrix, Reason: "small", Site: "magusfile.buzz:20"}
	s.Equal(`routine (rank 0), matrix: "small" at magusfile.buzz:20`, tier.String())
}

func (s *modelTierSuite) TestTheForkLineNamesAReasonAlone() {
	tier := JobModelTier{Name: "bounded", Rank: 1, Source: ModelTierSourceDefault, Reason: "the declared default"}
	s.Equal(`bounded (rank 1), default: "the declared default"`, tier.String())
}

func (s *modelTierSuite) TestTheForkLineNamesTheSourceAlone() {
	s.Equal("design (rank 2), job", JobModelTier{Name: "design", Rank: 2, Source: ModelTierSourceJob}.String())
}

func (s *modelTierSuite) TestTheForkLineSaysWhichTiersShareAnID() {
	tier := JobModelTier{Name: "frontier", Rank: 3, Source: ModelTierSourceJob, Shared: []string{"codex runs design and frontier on one id, gpt-6-astra"}}
	s.Equal("frontier (rank 3), job; codex runs design and frontier on one id, gpt-6-astra", tier.String())
}

type modelTierStateSuite struct {
	suite.Suite
}

func TestModelTierState(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(modelTierStateSuite))
}

func (s *modelTierStateSuite) named(enum string) *ModelTierSet {
	decl := fourTiers()
	decl.Enum = enum
	set, err := NewModelTierSet(decl)
	s.Require().NoError(err)
	return set
}

func (s *modelTierStateSuite) TestNoDeclarationAndNoDefaultIsNil() {
	var state ModelTierState
	set, err := state.Tiers()
	s.Require().NoError(err)
	s.Nil(set)
}

func (s *modelTierStateSuite) TestTheDefaultBuildsOnceOnTheFirstAsk() {
	var state ModelTierState
	builds := 0
	shipped := s.named("Shipped")
	state.SetDefault(func() (*ModelTierSet, error) { builds++; return shipped, nil })
	s.Zero(builds, "setting the default builds nothing")
	for range 2 {
		set, err := state.Tiers()
		s.Require().NoError(err)
		s.Same(shipped, set)
	}
	s.Equal(1, builds)
}

func (s *modelTierStateSuite) TestADeclarationReplacesTheDefaultAndARerunReplacesIt() {
	var state ModelTierState
	state.SetDefault(func() (*ModelTierSet, error) { return s.named("Shipped"), nil })
	first, second := s.named("First"), s.named("Second")
	state.Declare(first)
	set, err := state.Tiers()
	s.Require().NoError(err)
	s.Same(first, set)
	state.Declare(second)
	set, err = state.Tiers()
	s.Require().NoError(err)
	s.Same(second, set)
}

func (s *modelTierStateSuite) TestADeclarationOfNilTurnsTiersOff() {
	var state ModelTierState
	state.SetDefault(func() (*ModelTierSet, error) {
		return nil, errors.New("the default must not build when tiers are off")
	})
	state.Declare(nil)
	set, err := state.Tiers()
	s.Require().NoError(err)
	s.Nil(set)
}

func (s *modelTierStateSuite) TestADefaultThatFailsReturnsItsErrorAndBuildsAgain() {
	var state ModelTierState
	builds := 0
	shipped := s.named("Shipped")
	state.SetDefault(func() (*ModelTierSet, error) {
		builds++
		if builds == 1 {
			return nil, errors.New("broken")
		}
		return shipped, nil
	})
	_, err := state.Tiers()
	s.Require().ErrorContains(err, "broken")
	set, err := state.Tiers()
	s.Require().NoError(err)
	s.Same(shipped, set)
}

func (s *modelTierStateSuite) TestTheContextCarriesTheState() {
	state := &ModelTierState{}
	s.Same(state, ModelTierStateFromContext(WithModelTierState(s.T().Context(), state)))
	s.Nil(ModelTierStateFromContext(s.T().Context()))
}
