package vcs

import (
	"strings"
	"testing"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const projectMagusfile = `import "magus";

fun helper() > void {
}

// The project.
magus\project({
    "name": "demo",
    "sources": [
        "a.txt",
    ],
    "targets": {
        // Runs the suite.
        "test": {
            "timeout": "25m",
            "slots": 4,
        },
        "go-test": {"slots": 4},
    },
});

export fun build(ctx: magus\Context, args: [str]) > void {
}
`

func TestPlaceProjectKeys(t *testing.T) {
	var funcnames []string
	for _, f := range gitFuncnames {
		funcnames = append(funcnames, "-c", f.key()+"="+f.pattern)
	}
	for _, tc := range []struct {
		name string
		body string
		want []string
	}{
		{
			name: "keys and the entries of a map key",
			body: projectMagusfile,
			want: []string{
				"", "", "fun helper() > void {", "fun helper() > void {", "fun helper() > void {",
				"project", "project",
				"project.name",
				"project.sources", "project.sources", "project.sources",
				"project.targets",
				"project.targets.test", "project.targets.test", "project.targets.test", "project.targets.test", "project.targets.test",
				"project.targets.go-test",
				"project.targets",
				"project",
				"project",
				"export fun build(ctx: magus\\Context, args: [str]) > void {", "export fun build(ctx: magus\\Context, args: [str]) > void {",
			},
		},
		{
			name: "an aliased import and a one-line call",
			body: "import \"magus\" as m;\nm\\project({\"name\": \"x\"});\n",
			want: []string{"", "project"},
		},
		{
			name: "braces inside strings and comments are not counted",
			body: "magus\\project({\n    \"name\": \"{\", // }\n    \"tools\": [],\n});\n",
			want: []string{"project", "project.name", "project.tools", "project"},
		},
		{
			name: "a file with no project call keeps git's names",
			body: "fun a() > void {\n}\n",
			want: []string{"fun a() > void {", "fun a() > void {"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := gitPlaceLines(t.Context(), t.TempDir(), "buzz", funcnames, []byte(tc.body))
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// A footprint names an edit inside one target's settings by its key path, which is what a
// `magusfile.buzz#project.targets.test` claim names.
func TestRegionsBetweenNamesProjectKeys(t *testing.T) {
	dir := testkit.NewRepo(t, map[string]string{".gitattributes": "*.buzz diff=buzz\n"}).Root()
	after := []byte(strings.Replace(projectMagusfile, `"timeout": "25m"`, `"timeout": "30m"`, 1))
	got, err := gitVCS{}.RegionsBetween(t.Context(), dir, "magusfile.buzz", []byte(projectMagusfile), after)
	require.NoError(t, err)
	assert.Equal(t, []types.RegionChange{
		region("magusfile.buzz", types.RegionOld, 15, 15, "project.targets.test", "buzz"),
		region("magusfile.buzz", types.RegionNew, 15, 15, "project.targets.test", "buzz"),
	}, got)
	for _, r := range got {
		assert.True(t, types.NamesDeclaration("project.targets.test", r.Declaration))
		assert.False(t, types.NamesDeclaration("project.targets.go-test", r.Declaration))
	}
}
