package magus

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// TestADispatchOnlyInstallTargetSchedulesAsItsInstalls pins the conventional `install`
// target, whose body only calls the spell's install op, to the step a spell install
// gets: never replayed, keyed on no source or tool, and not ordered after the installs
// of the projects it depends on. A target that does anything else keeps its own step.
func TestADispatchOnlyInstallTargetSchedulesAsItsInstalls(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
	write("magusfile.buzz", "")
	for _, p := range []string{"lib", "web"} {
		write(p+"/package.json", "{}\n")
		write(p+"/pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
		write(p+"/magusfile.buzz", `import "magus/spell/typescript";
export fun install(ctx: magus\Context, args: [str]) > void !> any { typescript["pnpm-install"](ctx); }
export fun lint(ctx: magus\Context, args: [str]) > void !> any { typescript["pnpm-install"](ctx); ctx.needs(install); }
`)
	}
	reg := NewWorkspaceRegistry()
	reg.RegisterProject("web", WithDependsOn("../lib"))

	m, err := Open(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })
	web := m.Get("web")
	require.NotNil(t, web)
	// This package links no spell registry, so the resolved typescript spell is stood in
	// by one carrying just the install op the magusfile calls.
	web.ResolvedSpells = []*spells.Spell{spells.NewSpell("typescript",
		spells.WithOps(map[string]spells.Op{"pnpm-install": {Kind: spells.OpKindInstall}}))}

	assert.Equal(t, []string{"install"}, web.DispatchOnlyTargets)
	ops := installOpsOf(web, "install")
	require.Len(t, ops, 1)
	assert.Equal(t, "pnpm-install", ops[0].name)
	assert.Equal(t, "typescript", ops[0].spell.Name())
	assert.False(t, keysTools(web, "install"))

	install := m.buildStep(web, "install")
	assert.True(t, install.NoCache)
	assert.Empty(t, install.Sources)
	assert.Empty(t, install.DependsOn, "an install reads its dependencies' manifests, never their installed trees")

	assert.Nil(t, installOpsOf(web, "lint"), "a body doing anything else is not an install")
	assert.Equal(t, []string{"lib"}, m.buildStep(web, "lint").DependsOn)
	assert.True(t, keysTools(web, "lint"))
}

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
