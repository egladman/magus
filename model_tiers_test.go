package magus

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
		Models:     []types.ModelPricing{{ID: "claude-opus-test", Alias: "opus", Tier: "principal"}},
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
	assert.Equal(t, "principal", got.ModelTier.Name)
}

func TestModelTiersWithoutARegistry(t *testing.T) {
	m := &Magus{}
	assert.Nil(t, m.ModelTiers(t.Context()))
	assert.Nil(t, m.ModelTierMatrix())
}

func TestModelTiersReadTheRegistry(t *testing.T) {
	set, err := typesagent.NewModelTierSet(typesagent.ModelTierDeclaration{
		Enum:   "Tier",
		Site:   "magusfile.buzz:1",
		Cases:  []string{"low", "high"},
		Bounds: make([]typesagent.ModelTierBounds, 2),
		Skills: []typesagent.SkillVariant{typesagent.SkillVariantFull, typesagent.SkillVariantShort},
	})
	require.NoError(t, err)

	reg := workspace.NewWorkspaceRegistry()
	reg.SetModelTiers(set)
	reg.SetModelTierMatrix(func(context.Context, types.ModelTierRequest) (*typesagent.JobModelTier, error) {
		return &typesagent.JobModelTier{Name: "high", Rank: 1}, nil
	})
	m := &Magus{wsReg: reg}

	assert.Same(t, set, m.ModelTiers(t.Context()))
	got, err := m.ModelTierMatrix()(context.Background(), types.ModelTierRequest{})
	require.NoError(t, err)
	assert.Equal(t, &typesagent.JobModelTier{Name: "high", Rank: 1}, got)
}
