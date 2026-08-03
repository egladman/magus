package bindings

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/spells"
)

func TestBuildWorkspaceNS(t *testing.T) {
	t.Run("valid spell handle records the provider", func(t *testing.T) {
		reg := workspace.NewWorkspaceRegistry()
		ns := buildWorkspaceNS(workspace.ContextWithRegistry(context.Background(), reg), nil)

		handle := vm.NewMap()
		handle.MapSet("name", vm.StrValue("nx"))
		require.NoError(t, callVoidDirect(t, requireDirect(t, ns, "provider"), handle))
		assert.Equal(t, []string{"nx"}, reg.Providers())
	})

	t.Run("wiring order is preserved and repeats collapse", func(t *testing.T) {
		reg := workspace.NewWorkspaceRegistry()
		ns := buildWorkspaceNS(workspace.ContextWithRegistry(context.Background(), reg), nil)
		provider := requireDirect(t, ns, "provider")

		for _, name := range []string{"nx", "cargo", "nx"} {
			handle := vm.NewMap()
			handle.MapSet("name", vm.StrValue(name))
			require.NoError(t, callVoidDirect(t, provider, handle))
		}
		assert.Equal(t, []string{"nx", "cargo"}, reg.Providers(),
			"first-wired wins the tiebreak, so order is identity and a repeat must not reorder it")
	})

	t.Run("no registry in context is a silent no-op", func(t *testing.T) {
		ns := buildWorkspaceNS(context.Background(), nil)
		handle := vm.NewMap()
		handle.MapSet("name", vm.StrValue("nx"))
		require.NoError(t, callVoidDirect(t, requireDirect(t, ns, "provider"), handle))
	})

	t.Run("non-map argument is rejected", func(t *testing.T) {
		ns := buildWorkspaceNS(context.Background(), nil)
		err := callVoidDirect(t, requireDirect(t, ns, "provider"), vm.StrValue("nx"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "imported spell handle")
	})

	t.Run("map without a name is rejected", func(t *testing.T) {
		ns := buildWorkspaceNS(context.Background(), nil)
		err := callVoidDirect(t, requireDirect(t, ns, "provider"), vm.NewMap())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no name")
	})
}

func TestDecodeProvidedProject(t *testing.T) {
	t.Run("every field is carried", func(t *testing.T) {
		pp, err := decodeProvidedProject("nx", 0, map[string]any{
			"path":       "libs/foo",
			"name":       "@acme/foo",
			"spells":     []any{"go"},
			"depends_on": []any{"libs/shared"},
			"sources":    []any{"**/*.ts"},
			"outputs":    []any{"dist/**"},
			"exclusive":  true,
		})
		require.NoError(t, err)

		assert.Equal(t, spells.ProvidedProject{
			Path:      "libs/foo",
			Name:      "@acme/foo",
			Spells:    []string{"go"},
			DependsOn: []string{"libs/shared"},
			Sources:   []string{"**/*.ts"},
			Outputs:   []string{"dist/**"},
			Exclusive: true,
		}, pp)
	})

	t.Run("a null field reads as absent, like every other field", func(t *testing.T) {
		// One record must not have two null policies: nil is "not declared" for the
		// string, list and bool fields alike.
		pp, err := decodeProvidedProject("nx", 0, map[string]any{
			"path": "libs/foo", "name": nil, "spells": nil, "exclusive": nil,
		})
		require.NoError(t, err)
		assert.Equal(t, spells.ProvidedProject{Path: "libs/foo"}, pp)
	})

	t.Run("a record of defaults yields a bare record", func(t *testing.T) {
		// A Buzz object marshals every declared field, so the empty case arrives as
		// zero values rather than absent keys.
		pp, err := decodeProvidedProject("nx", 0, map[string]any{
			"path": "libs/foo", "name": "", "spells": []any{},
			"depends_on": []any{}, "sources": []any{}, "outputs": []any{}, "exclusive": false,
		})
		require.NoError(t, err)
		assert.Equal(t, spells.ProvidedProject{Path: "libs/foo"}, pp)
	})

	t.Run("path is carried, not validated here", func(t *testing.T) {
		// Path validity is AddProvidedProjects' job (MGS1018): it needs the workspace
		// root, which the decoder does not have. An unregistered spell is likewise left
		// to WithRegisteredSpell, which already rejects one.
		pp, err := decodeProvidedProject("nx", 0, map[string]any{"path": "../outside", "spells": []any{"nope"}})
		require.NoError(t, err)
		assert.Equal(t, "../outside", pp.Path)
		assert.Equal(t, []string{"nope"}, pp.Spells)
	})

	for _, tc := range []struct {
		name string
		item any
		want string
	}{
		{"not a record", "libs/foo", "want a Project"},
		{"path of the wrong type", map[string]any{"path": 7}, `field "path" is int, want str`},
		{"list field of the wrong type", map[string]any{"path": "libs/foo", "sources": "glob"}, `field "sources" is string, want [str]`},
		{"list element of the wrong type", map[string]any{"path": "libs/foo", "sources": []any{"ok", 7}}, `field "sources"[1] is int, want str`},
		{"exclusive of the wrong type", map[string]any{"path": "libs/foo", "exclusive": "yes"}, `field "exclusive" is string, want bool`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeProvidedProject("nx", 3, tc.item)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Contains(t, err.Error(), `spell "nx"`, "the error must name the provider")
			assert.Contains(t, err.Error(), "list_projects[3]", "and which record it came from")
		})
	}
}

func TestRunWorkspaceProviderUnregisteredSpell(t *testing.T) {
	_, err := runWorkspaceProvider(context.Background(), "not-a-spell", t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not registered")
}

// TestDecodeCoversEveryProvidedProjectField is the drift guard for the one seam this
// decoder cannot type-check: the record crosses from Buzz as a plain map, so a field
// renamed on spells.ProvidedProject (and duly regenerated into the Buzz mirror) would
// otherwise decode to its zero value in silence - a provider's dependency edges or
// source globs vanishing with no error, which is exactly the failure the explicit
// decoding exists to prevent.
func TestDecodeCoversEveryProvidedProjectField(t *testing.T) {
	decoded := map[string]bool{
		fieldPath: true, fieldName: true, fieldSpells: true, fieldDependsOn: true,
		fieldSources: true, fieldOutputs: true, fieldExclusive: true,
	}
	rt := reflect.TypeFor[spells.ProvidedProject]()
	for i := range rt.NumField() {
		f := rt.Field(i)
		key := f.Tag.Get("buzz")
		if key == "" {
			// The generator lowerCamels an untagged field name, which equals its
			// lowercase form only for a single word. A multi-word field must therefore
			// carry an explicit tag - DependsOn crossing as "dependsOn" while the
			// decoder read "depends_on" is precisely the drift this guards.
			require.Equalf(t, strings.ToLower(f.Name), strings.ToLower(f.Name[:1])+f.Name[1:],
				"spells.ProvidedProject.%s is multi-word and needs an explicit buzz tag", f.Name)
			key = strings.ToLower(f.Name)
		}
		assert.Truef(t, decoded[key],
			"spells.ProvidedProject.%s crosses as %q, which decodeProvidedProject never reads", f.Name, key)
	}
	assert.Len(t, decoded, rt.NumField(), "decodeProvidedProject reads a field the record no longer has")
}
