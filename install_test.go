package magus

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// TestInstallStepKeysOnlyWhatDecidesTheInstall pins the install's key: the manifest,
// the live lock (hoisted above the project here), the settings files, the tools the
// install names and the platform. A source edit, a tool that runs from the tree (tsc),
// and a charm the command does not declare all stay out, or an unchanged install forks.
func TestInstallStepKeysOnlyWhatDecidesTheInstall(t *testing.T) {
	m, _ := openTempWorkspace(t, "web", nil)
	root := m.Root()
	p := m.Get("web")
	require.NotNil(t, p)
	choice := spells.InstallChoice{
		Manifest: filepath.Join(root, "web", "package.json"),
		Lock:     filepath.Join(root, "pnpm-lock.yaml"),
		Install: spells.Install{
			Command: spells.Command{Bin: "pnpm", Charms: map[string]spells.Charm{types.CharmUpdate: {}}},
			Stamps:  []string{"node_modules/.pnpm/lock.yaml"},
			Inputs:  []string{".npmrc"},
			Tools:   []string{"node", "pnpm"},
		},
	}
	tools := []string{"typescript:node:v24.19.0", "typescript:pnpm:10.33.0", "typescript:tsc:UNPROBED", "go:go:1.26"}

	step := m.installStep(p, "typescript", "pnpm-install", choice, tools, []string{"rw"})
	assert.Equal(t, "pnpm-install", step.Target)
	assert.Equal(t, "typescript", step.Spell)
	assert.Equal(t, []string{"web/package.json", "pnpm-lock.yaml", "web/.npmrc"}, step.Sources)
	assert.Equal(t, []string{"web/node_modules/.pnpm/lock.yaml"}, step.Stamps)
	assert.Equal(t, []string{"typescript:node:v24.19.0", "typescript:pnpm:10.33.0"}, step.ToolVersions)
	assert.Empty(t, step.Charms)
	assert.True(t, step.IncludeOS)
	assert.True(t, step.IncludeArch)
	assert.False(t, step.NoCache)
	assert.Empty(t, step.Outputs)

	update := m.installStep(p, "typescript", "pnpm-install", choice, tools, []string{"rw", types.CharmUpdate})
	assert.Equal(t, []string{types.CharmUpdate}, update.Charms)
	assert.True(t, update.NoCache, "a replayed update is an update that never happened")
	assert.Equal(t, []string{"web/package.json", "pnpm-lock.yaml"}, update.Updates)

	choice.Install.Stamps = nil
	assert.True(t, m.installStep(p, "typescript", "pnpm-install", choice, tools, nil).NoCache,
		"with no stamp nothing could notice a deleted tree, so the install always runs")
}
