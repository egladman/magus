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
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
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
		Models:     []tier.Pricing{{ID: "claude-opus-test", Alias: "opus", Tier: "design"}},
	})
	root := writeWorkspace(t, map[string]string{
		"magus.yaml":                  "",
		"magusfile.buzz":              "import \"magus\";\nimport \"spells/opus-host\" as host;\nmagus\\harness.provider(host);\n",
		"spells/opus-host/spell.buzz": opusHostSpell,
	})
	ws, err := Inspect(t.Context(), root, WithSpawnRule(tier.Ceiling))
	require.NoError(t, err)

	got, err := std.MagusPutJob(ws.(*Magus).Context(t.Context()), "w1", job.Put{Agent: &job.Agent{Instructions: "ship it", Model: job.Model{ID: "opus"}, Worker: job.Worker{Type: job.WorkerTypeResumed}}, ReadOnly: new(true)})
	require.NoError(t, err)
	require.NotNil(t, got.ModelTier)
	assert.Equal(t, "design", got.ModelTier.Name)
}

type modelTiersSuite struct {
	suite.Suite
}

func TestModelTiersSuite(t *testing.T) { suite.Run(t, new(modelTiersSuite)) }

func (s *modelTiersSuite) matrix(reason string) types.ModelTierMatrix {
	return func(context.Context, types.ModelTierRequest) (*tier.Assignment, error) {
		return &tier.Assignment{Name: "high", Rank: 1, Reason: reason}, nil
	}
}

func (s *modelTiersSuite) TestAZeroMagusHoldsNone() {
	m := &Magus{}
	s.Nil(m.ModelTiers(s.T().Context()))
	s.Nil(m.ModelTierMatrix())
}

func (s *modelTiersSuite) TestTheTiersAreWhatTheMagusfileDeclared() {
	set, err := tier.NewSet(tier.Declaration{
		Enum:   "Tier",
		Site:   "magusfile.buzz:1",
		Cases:  []string{"low", "high"},
		Bounds: make([]tier.Bounds, 2),
		Skills: []tier.SkillVariant{tier.SkillVariantFull, tier.SkillVariantShort},
	})
	s.Require().NoError(err)
	m := &Magus{}
	m.modelTiers.Declare(set)
	s.Same(set, m.ModelTiers(s.T().Context()))
}

func (s *modelTiersSuite) TestADefaultThatDoesNotBuildIsNil() {
	m := &Magus{}
	m.modelTiers.SetDefault(func() (*tier.Set, error) { return nil, errors.New("broken") })
	s.Nil(m.ModelTiers(s.T().Context()))
}

func (s *modelTiersSuite) TestTheMatrixIsTheRegistrysLatest() {
	reg := workspace.NewWorkspaceRegistry()
	reg.SetModelTierMatrix(s.matrix("first"))
	reg.SetModelTierMatrix(s.matrix("second"))
	m := &Magus{wsReg: reg}

	got, err := m.ModelTierMatrix()(s.T().Context(), types.ModelTierRequest{})
	s.Require().NoError(err)
	s.Equal(&tier.Assignment{Name: "high", Rank: 1, Reason: "second"}, got)
}

func (s *modelTiersSuite) TestTheLoadContextCarriesTheTiers() {
	m := &Magus{wsReg: workspace.NewWorkspaceRegistry()}
	ctx := m.installWorkspaceRegistry(s.T().Context())
	s.Same(&m.modelTiers, tier.StateFromContext(ctx))
	s.Same(m.wsReg, workspace.WorkspaceRegistryFromContext(ctx))
}
