package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/dry"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckManifestScripts(t *testing.T) {
	declaring := []*spells.Spell{
		spells.NewSpell("golang"),
		spells.NewSpell("typescript", spells.WithScriptRunners(
			spells.Command{Bin: "pnpm", Args: []string{"run"}},
			spells.Command{Bin: "npm", Args: []string{"test"}},
		)),
		spells.NewSpell("python", spells.WithScriptRunners(spells.Command{Bin: "poe"})),
	}
	const delegating = `
import "magus";
import "proc";

fun pnpm(script: str) > void !> any { proc\exec("pnpm", ["run", script]); }

export fun build(ctx: magus\Context, args: [str]) > void !> any { pnpm("build"); }
export fun test(ctx: magus\Context, args: [str]) > void !> any {
    final sh = proc\shell("npm ci && npm test");
    proc\exec(sh.bin, sh.args);
}
export fun lint(ctx: magus\Context, args: [str]) > void !> any {
    proc\exec("pnpm", ["exec", "eslint", "."]);
    proc\exec("poe", ["lint"]);
}
`
	const clean = `
import "magus";
import "proc";

export fun build(ctx: magus\Context, args: [str]) > void !> any { proc\exec("go", ["build", "./..."]); }
`
	workspace := func(t *testing.T, files map[string]string) (string, []*types.Project) {
		t.Helper()
		root := t.TempDir()
		var projects []*types.Project
		for rel, src := range files {
			dir := filepath.Join(root, filepath.Dir(rel))
			require.NoError(t, os.MkdirAll(dir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(root, rel), []byte(src), 0o644))
			projects = append(projects, &types.Project{Path: filepath.Dir(rel), Dir: dir})
		}
		return root, projects
	}

	t.Run("no spell declares a runner", func(t *testing.T) {
		root, projects := workspace(t, map[string]string{"magusfile.buzz": delegating})
		got := checkManifestScripts(context.Background(), root, projects, []*spells.Spell{spells.NewSpell("golang")})
		assert.Equal(t, types.Check{
			Name: "manifest-scripts", Status: types.CheckOK, Evidence: types.EvidenceDeclared,
			Message: "no registered spell declares a script runner",
		}, got)
	})

	t.Run("delegating targets fail", func(t *testing.T) {
		root, projects := workspace(t, map[string]string{"web/magusfile.buzz": delegating})
		got := checkManifestScripts(context.Background(), root, projects, declaring)
		assert.Equal(t, types.Check{
			Name:   "manifest-scripts",
			Status: types.CheckFail,
			Message: "3 call(s) run a script a manifest defines, so its steps, inputs and outputs are " +
				"invisible to the cache key; declare those steps in the magusfile (see " +
				types.CodeURL(types.ManifestScriptDelegation) + ")",
			Details: []string{
				"web: target \"build\" runs `pnpm run build`, script \"build\" its typescript manifest defines (runner `pnpm run`) (web/magusfile.buzz)",
				"web: target \"lint\" runs `poe lint`, script \"lint\" its python manifest defines (runner `poe`) (web/magusfile.buzz)",
				"web: target \"test\" runs `npm test`, a script its typescript manifest defines (runner `npm test`) (web/magusfile.buzz)",
			},
		}, got)
	})

	t.Run("clean workspace", func(t *testing.T) {
		root, projects := workspace(t, map[string]string{"magusfile.buzz": clean})
		got := checkManifestScripts(context.Background(), root, projects, declaring)
		assert.Equal(t, types.Check{
			Name: "manifest-scripts", Status: types.CheckOK,
			Message: "no target in 1 magusfile(s) runs a manifest-defined script",
		}, got)
	})

	t.Run("an untraceable magusfile is unknown, not ok", func(t *testing.T) {
		const broken = "import \"magus\";\nexport fun a(ctx: magus\\Context, args: [str]) > void { var x = ; }"
		_, diag := dry.Execs(context.Background(), broken)
		require.NotNil(t, diag)
		root, projects := workspace(t, map[string]string{"magusfile.buzz": broken})
		got := checkManifestScripts(context.Background(), root, projects, declaring)
		assert.Equal(t, types.Check{
			Name: "manifest-scripts", Status: types.CheckOK, Evidence: types.EvidenceUnknown,
			Message: "could not trace 1 of 1 magusfile(s)",
			Details: []string{fmt.Sprintf("magusfile.buzz:%d: not traced: %s", diag.Line, diag.Msg)},
		}, got)
	})
}

func TestCommandLines(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want [][]string
	}{
		{name: "plain argv", argv: []string{"pnpm", "run", "build"},
			want: [][]string{{"pnpm", "run", "build"}}},
		{name: "shell line split on operators", argv: []string{"/bin/sh", "-c", "npm ci && npm run build; echo ok | tee log"},
			want: [][]string{
				{"/bin/sh", "-c", "npm ci && npm run build; echo ok | tee log"},
				{"npm", "ci"}, {"npm", "run", "build"}, {"echo", "ok"}, {"tee", "log"},
			}},
		{name: "not a shell", argv: []string{"python", "-c", "print(1)"},
			want: [][]string{{"python", "-c", "print(1)"}}},
		{name: "shell without -c", argv: []string{"bash", "script.sh"},
			want: [][]string{{"bash", "script.sh"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, commandLines(tt.argv))
		})
	}
}
