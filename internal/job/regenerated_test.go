package job

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// regeneratedWorkspace is a flag registry the root project and a docs project both
// generate from, the shape that sent a worker into a manpage outside its write paths.
func regeneratedWorkspace(t *testing.T) (string, []*types.Project) {
	t.Helper()
	root := t.TempDir()
	for _, rel := range []string{
		"cli/registry.go",
		"cli/testdata/api.lock",
		"cmd/main.go",
		"cmd/gen/flags.go",
		"docs/manpage/magus-shell.md",
		"docs/rules/guard.md",
		"docs/site/page.buzz",
	} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(rel), 0o644))
	}
	projects := []*types.Project{
		{
			Path:    ".",
			Sources: types.MustParseGlobs("**/*.go"),
			TargetInputs: map[string][]types.InputRef{
				"man-generate":    {{Glob: "cli/**/*.go"}},
				"config-generate": {{Glob: "**/*.go"}},
				"test":            {{Glob: "**/*.go"}},
			},
			TargetOutputs: map[string][]types.OutputRef{
				"man-generate":    {{Glob: "cli/testdata/api.lock"}},
				"config-generate": {{Glob: "cmd/gen/*.go"}},
				"test":            {{Glob: "coverage/*.lcov"}},
				"types-generate":  {{Glob: "types/gen.go"}},
			},
		},
		{
			Path:    "docs",
			Sources: types.MustParseGlobs("**/*.md"),
			TargetInputs: map[string][]types.InputRef{
				"content-generate": {{Project: ".", Glob: "cli/**/*.go"}},
				"site-generate":    {{Glob: "site/*.buzz"}},
			},
			TargetOutputs: map[string][]types.OutputRef{
				"content-generate": {{Glob: "manpage/*.md"}, {Glob: "rules/*.md"}},
				"site-generate":    {{Glob: "gen/**"}},
			},
		},
	}
	return root, projects
}

func ignoresCoverage(paths []string) []string {
	var out []string
	for _, p := range paths {
		if strings.HasPrefix(p, "coverage/") {
			out = append(out, p)
		}
	}
	return out
}

func TestRegeneratedOutside(t *testing.T) {
	t.Parallel()

	manpage := RegeneratedOutput{Output: "docs/manpage/*.md", Target: "docs:content-generate", Source: "cli/**/*.go"}
	apiLock := RegeneratedOutput{Output: "cli/testdata/api.lock", Target: ".:man-generate", Source: "cli/**/*.go"}
	cliFlags := RegeneratedOutput{Output: "cmd/gen/*.go", Target: ".:config-generate", Source: "**/*.go"}
	coverage := RegeneratedOutput{Output: "coverage/*.lcov", Target: ".:test", Source: "**/*.go"}
	typesGen := RegeneratedOutput{Output: "types/gen.go", Target: ".:types-generate", Source: "**/*.go"}

	tests := []struct {
		name    string
		row     types.Job
		ignored func([]string) []string
		want    []RegeneratedOutput
	}{
		{
			name:    "outputs outside the paths, most specific read first, project baseline last, ignored dropped",
			row:     types.Job{WritePaths: []string{"cmd/*.go", "cli/*.go", "docs/rules/*.md"}},
			ignored: ignoresCoverage,
			want:    []RegeneratedOutput{apiLock, manpage, cliFlags, typesGen},
		},
		{
			name: "no ignore answer drops nothing",
			row:  types.Job{WritePaths: []string{"cmd/*.go", "cli/*.go", "docs/rules/*.md"}},
			want: []RegeneratedOutput{apiLock, manpage, cliFlags, coverage, typesGen},
		},
		{
			name:    "a literal path the job will create counts as written",
			row:     types.Job{WritePaths: []string{"cli/shell_flags.go"}},
			ignored: ignoresCoverage,
			want: []RegeneratedOutput{
				apiLock,
				manpage,
				{Output: "docs/rules/*.md", Target: "docs:content-generate", Source: "cli/**/*.go"},
				cliFlags,
				typesGen,
			},
		},
		{
			name:    "a deny path takes an output back out of a covering write path",
			row:     types.Job{WritePaths: []string{"docs/**", "cli/registry.go"}, DenyPaths: []string{"docs/manpage/*.md"}},
			ignored: ignoresCoverage,
			want:    []RegeneratedOutput{apiLock, manpage, cliFlags, typesGen},
		},
		{
			name:    "sources outside the paths name nothing",
			row:     types.Job{WritePaths: []string{"docs/rules/guard.md"}},
			ignored: ignoresCoverage,
		},
		{
			name: "a read-only row writes nothing",
			row:  types.Job{ReadOnly: true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root, projects := regeneratedWorkspace(t)
			assert.Equal(t, tc.want, RegeneratedOutside(root, tc.row, projects, tc.ignored))
		})
	}
}

// The text lists outputs in rank order up to the cap, counts the rest with the targets
// behind them, and ends with who regenerates them.
func TestRenderRegeneratedCapsTheListing(t *testing.T) {
	t.Parallel()

	var outputs []RegeneratedOutput
	for i := range RegeneratedLimit {
		outputs = append(outputs, RegeneratedOutput{Output: fmt.Sprintf("docs/ref/%d.md", i), Target: "docs:content-generate", Source: "cli/**/*.go"})
	}
	outputs = append(outputs,
		RegeneratedOutput{Output: "cli/api.lock", Target: ".:man-generate", Source: "cli/**/*.go"},
		RegeneratedOutput{Output: "manpage/*.1", Target: ".:man-generate", Source: "cli/**/*.go"},
		RegeneratedOutput{Output: "MAGUS.md", Target: ".:index-generate", Source: "**/*.go"},
	)
	var b strings.Builder
	RenderRegenerated(&b, outputs)

	want := "regenerated outside the write paths, from sources inside them\n"
	for i := range RegeneratedLimit {
		want += fmt.Sprintf("  docs/ref/%d.md (docs:content-generate)\n", i)
	}
	want += "  and 3 more from 2 target(s); -o json lists every one\n" +
		"  the root regenerates these after integration: never hand-edit them; if this job must regenerate them itself, ask the root to widen the write paths\n"
	assert.Equal(t, want, b.String())

	b.Reset()
	RenderRegenerated(&b, nil)
	assert.Empty(t, b.String(), "nothing to say prints nothing")
}
