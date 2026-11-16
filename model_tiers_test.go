package magus

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
	typesagent "github.com/egladman/magus/types/agent"
)

const opusHostSpell = `import "magus/spell";

export fun mgs_getName() > str { return "opus-host"; }

export fun mgs_getHarness() > Harness {
    return Harness{config = HarnessConfig{path = "hooks.json"}, guard = ""};
}

export fun mgs_getSkills() > HarnessSkills {
    return HarnessSkills{form = SkillForm.short};
}

export fun mgs_renderPrompts(humanOnly: {str: str}) > [Prompt] { return []; }
`

func TestModelRowsLetAJobPutResolveAnAliasThroughARealMagus(t *testing.T) {
	testkit.Isolate(t)
	fixture.WireHarness(t, agent.HarnessDescriptor{
		APIVersion: agent.HarnessAPIVersion,
		ID:         "opus-host",
		Display:    agent.HarnessDisplay{Name: "Opus host"},
		Skills:     types.HarnessSkills{Form: types.SkillFormShort},
		Config:     types.HarnessConfig{Path: "hooks.json"},
		Models:     []types.ModelPricing{{ID: "claude-opus-test", Alias: "opus", Tier: "design"}},
	})
	root := writeWorkspace(t, map[string]string{
		"magus.yaml":                  "",
		"magusfile.buzz":              "import \"magus\";\nimport \"spells/opus-host\" as host;\nmagus\\harness.provider(host);\n",
		"spells/opus-host/spell.buzz": opusHostSpell,
	})
	ws, err := Inspect(t.Context(), root, WithSpawnRule(TierCeiling))
	require.NoError(t, err)

	got, err := std.MagusPutJob(ws.(*Magus).Context(t.Context()), "w1", types.JobPut{Agent: &typesagent.Spec{Instructions: "ship it", Model: typesagent.Model{ID: "opus"}, Worker: typesagent.Worker{Type: typesagent.WorkerTypeResumed}}, ReadOnly: new(true)})
	require.NoError(t, err)
	require.NotNil(t, got.ModelTier)
	assert.Equal(t, "design", got.ModelTier.Name)
}

type modelTiersSuite struct {
	suite.Suite
}

func TestModelTiersSuite(t *testing.T) { suite.Run(t, new(modelTiersSuite)) }

func (s *modelTiersSuite) matrix(reason string) types.ModelTierMatrix {
	return func(context.Context, types.ModelTierRequest) (*typesagent.JobModelTier, error) {
		return &typesagent.JobModelTier{Name: "high", Rank: 1, Reason: reason}, nil
	}
}

func (s *modelTiersSuite) TestAZeroMagusHoldsNone() {
	m := &Magus{}
	s.Nil(m.ModelTiers(s.T().Context()))
	s.Nil(m.ModelTierMatrix())
}

func (s *modelTiersSuite) TestTheTiersAreWhatTheMagusfileDeclared() {
	set, err := typesagent.NewModelTierSet(typesagent.ModelTierDeclaration{
		Enum:   "Tier",
		Site:   "magusfile.buzz:1",
		Cases:  []string{"low", "high"},
		Bounds: make([]typesagent.ModelTierBounds, 2),
		Skills: []typesagent.SkillVariant{typesagent.SkillVariantFull, typesagent.SkillVariantShort},
	})
	s.Require().NoError(err)
	m := &Magus{}
	m.modelTiers.Declare(set)
	s.Same(set, m.ModelTiers(s.T().Context()))
}

func (s *modelTiersSuite) TestADefaultThatDoesNotBuildIsNil() {
	m := &Magus{}
	m.modelTiers.SetDefault(func() (*typesagent.ModelTierSet, error) { return nil, errors.New("broken") })
	s.Nil(m.ModelTiers(s.T().Context()))
}

func (s *modelTiersSuite) TestTheMatrixIsTheRegistrysLatest() {
	reg := workspace.NewWorkspaceRegistry()
	reg.SetModelTierMatrix(s.matrix("first"))
	reg.SetModelTierMatrix(s.matrix("second"))
	m := &Magus{wsReg: reg}

	got, err := m.ModelTierMatrix()(s.T().Context(), types.ModelTierRequest{})
	s.Require().NoError(err)
	s.Equal(&typesagent.JobModelTier{Name: "high", Rank: 1, Reason: "second"}, got)
}

func (s *modelTiersSuite) TestTheLoadContextCarriesTheTiers() {
	m := &Magus{wsReg: workspace.NewWorkspaceRegistry()}
	ctx := m.installWorkspaceRegistry(s.T().Context())
	s.Same(&m.modelTiers, typesagent.ModelTierStateFromContext(ctx))
	s.Same(m.wsReg, workspace.WorkspaceRegistryFromContext(ctx))
}
