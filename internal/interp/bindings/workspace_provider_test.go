package bindings

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/interp/bindings/gen/providerops"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

func TestBuildWorkspace(t *testing.T) {
	t.Run("valid spell handle records the provider", func(t *testing.T) {
		reg := workspace.NewWorkspaceRegistry()
		workspaceMap := buildWorkspace(workspace.ContextWithRegistry(context.Background(), reg), nil)

		handle := vm.NewMap()
		handle.MapSet("name", vm.StrValue("nx"))
		require.NoError(t, callVoidDirect(t, requireDirect(t, workspaceMap, "provider"), handle))
		assert.Equal(t, []string{"nx"}, reg.Providers())
	})

	t.Run("wiring order is preserved and repeats collapse", func(t *testing.T) {
		reg := workspace.NewWorkspaceRegistry()
		workspaceMap := buildWorkspace(workspace.ContextWithRegistry(context.Background(), reg), nil)
		provider := requireDirect(t, workspaceMap, "provider")

		for _, name := range []string{"nx", "cargo", "nx"} {
			handle := vm.NewMap()
			handle.MapSet("name", vm.StrValue(name))
			require.NoError(t, callVoidDirect(t, provider, handle))
		}
		assert.Equal(t, []string{"nx", "cargo"}, reg.Providers(),
			"first-wired wins the tiebreak, so order is identity and a repeat must not reorder it")
	})

	t.Run("no registry in context is a silent no-op", func(t *testing.T) {
		workspaceMap := buildWorkspace(context.Background(), nil)
		handle := vm.NewMap()
		handle.MapSet("name", vm.StrValue("nx"))
		require.NoError(t, callVoidDirect(t, requireDirect(t, workspaceMap, "provider"), handle))
	})

	t.Run("non-map argument is rejected", func(t *testing.T) {
		workspaceMap := buildWorkspace(context.Background(), nil)
		err := callVoidDirect(t, requireDirect(t, workspaceMap, "provider"), vm.StrValue("nx"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "imported spell handle")
	})

	t.Run("map without a name is rejected", func(t *testing.T) {
		workspaceMap := buildWorkspace(context.Background(), nil)
		err := callVoidDirect(t, requireDirect(t, workspaceMap, "provider"), vm.NewMap())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no name")
	})
}

func TestListProjectsHandsTheRootAndDecodesEveryField(t *testing.T) {
	drv := buzzProvider(t, `
export fun list_projects(target: Target, req: ProjectQuery) > [Project] {
    return [Project{
        path = "libs/foo", name = req.root, spells = ["go"],
        depends_on = ["libs/shared"], sources = ["**/*.ts"], outputs = ["dist/**"],
    }];
}`)
	op := providerops.ListProjects
	got, exported, err := CallOp(context.Background(), drv, op, providerTarget(context.Background(), op.Name, "/ws"), types.ProjectQuery{Root: "/ws"})
	require.NoError(t, err)
	assert.True(t, exported)
	assert.Equal(t, []spells.ProvidedProject{{
		Path:      "libs/foo",
		Name:      "/ws",
		Spells:    []string{"go"},
		DependsOn: []string{"libs/shared"},
		Sources:   []string{"**/*.ts"},
		Outputs:   []string{"dist/**"},
	}}, got)
}

// Path validity is AddProvidedProjects' job (MGS1018), which has the workspace root; the
// op carries a path through unchecked.
func TestListProjectsCarriesAPathUnvalidated(t *testing.T) {
	drv := buzzProvider(t, `
export fun list_projects(target: Target, req: ProjectQuery) > [Project] {
    return [Project{ path = "../outside", spells = ["nope"] }];
}`)
	got, _, err := CallOp(context.Background(), drv, providerops.ListProjects, types.Target{}, types.ProjectQuery{})
	require.NoError(t, err)
	assert.Equal(t, []spells.ProvidedProject{{Path: "../outside", Spells: []string{"nope"}}}, got)
}

func TestListProjectsRefusesAMalformedRecord(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"not a list":                     {`return "libs/foo";`, "list_projects"},
		"path of the wrong type":         {`return [{"path": 7}];`, ".path"},
		"list element of the wrong type": {`return [{"path": "libs/foo", "sources": ["ok", 7]}];`, ".sources[1]"},
		"a member Project does not have": {`return [{"path": "libs/foo", "owner": "x"}];`, `has no member "owner"`},
	} {
		t.Run(name, func(t *testing.T) {
			drv := buzzProvider(t, fmt.Sprintf("export fun list_projects(target: Target, req: ProjectQuery) > any {\n%s\n}", tc.body))
			_, exported, err := CallOp(context.Background(), drv, providerops.ListProjects, types.Target{}, types.ProjectQuery{})
			require.Error(t, err)
			assert.True(t, exported)
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func TestRunWorkspaceProviderUnregisteredSpell(t *testing.T) {
	_, err := runWorkspaceProvider(context.Background(), "not-a-spell", t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not registered")
}
