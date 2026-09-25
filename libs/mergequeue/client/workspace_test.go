package client

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/mergequeue/types"
)

func TestWorkspaceAnswersFromTheProjectGraph(t *testing.T) {
	root := t.TempDir()
	for rel, body := range map[string]string{
		"magusfile.buzz": "", "api/magusfile.buzz": "", "web/magusfile.buzz": "", "api/main.go": "package main\n",
	} {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
	}
	w, err := OpenWorkspace(t.Context(), root, "ci")
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })
	c := types.Change{ID: "1"}

	affected, unboundedBy, err := w.Affected(t.Context(), c, []string{"api/main.go"})
	require.NoError(t, err)
	assert.Equal(t, []string{"api"}, affected)
	assert.Empty(t, unboundedBy, "a source edit is a proof")

	_, unboundedBy, err = w.Affected(t.Context(), c, []string{"api/main.go", "api/magusfile.buzz"})
	require.NoError(t, err)
	assert.Contains(t, unboundedBy, "api/magusfile.buzz", "an edit to the declarations is not")

	affected, unboundedBy, err = w.Affected(t.Context(), c, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{}, affected, "a change touching nothing reaches nothing")
	assert.Empty(t, unboundedBy)

	all, err := w.AllUnits(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []string{"/"}, all, "magus run reads / as every project")
}

// Generated means declared as an output. The generating project's own sources are code
// its regeneration runs, and so is anything that reaches it through the project graph;
// a document it reads is not, and an edit to the declarations proves nothing.
func TestWorkspaceSaysWhatIsGeneratedAndWhatItsRegenerationRuns(t *testing.T) {
	root := t.TempDir()
	for rel, body := range map[string]string{
		"magusfile.buzz":     "",
		"api/magusfile.buzz": "import \"magus\";\nmagus\\project({\"outputs\": [\"gen/**\"]});\n",
		"web/magusfile.buzz": "", "api/main.go": "package main\n", "api/gen/out.go": "package gen\n",
		"api/notes.md": "notes\n", "web/app.go": "package web\n", "vendor/blob.go": "package blob\n",
	} {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
	}
	w, err := OpenWorkspace(t.Context(), root, "ci")
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })

	out, err := w.Classify(t.Context(), []string{"api/gen/out.go", "api/main.go", "vendor/blob.go"})
	require.NoError(t, err)
	assert.Equal(t, map[string]types.Writes{"api/gen/out.go": {Output: true}}, out, "an undeclared file is source, whatever marks it")

	g, err := w.Generation(t.Context(), []string{"api/gen/out.go"}, []string{"api/notes.md", "web/app.go"})
	require.NoError(t, err)
	assert.Equal(t, types.Generation{Units: []string{"api"}}, g, "a document and another project's code prove it")

	g, err = w.Generation(t.Context(), []string{"api/gen/out.go"}, []string{"api/main.go", "web/app.go"})
	require.NoError(t, err)
	assert.Equal(t, []string{"api/main.go"}, g.Code)

	g, err = w.Generation(t.Context(), []string{"api/gen/out.go"}, []string{"web/magusfile.buzz"})
	require.NoError(t, err)
	assert.NotEmpty(t, g.Unbounded, "a declaration edit is never a proof")

	g, err = w.Generation(t.Context(), []string{"vendor/blob.go"}, nil)
	require.NoError(t, err)
	assert.Equal(t, "no project declares vendor/blob.go as its output", g.Unbounded)
}

// An in-place update is the regeneration's to write only when a target the regeneration
// runs declares it: a formatter's claim on every Go file must not open them all to it.
// magus rewrites the file it maintains whatever anyone declares.
func TestWorkspaceClassifyCountsOnlyTheRegenerationsUpdates(t *testing.T) {
	root := t.TempDir()
	for rel, body := range map[string]string{
		"magusfile.buzz": "",
		"api/magusfile.buzz": `export fun generate(ctx: magus\Context, args: [str]) > void {
    ctx.needs(stamp);
}
export fun stamp(ctx: magus\Context, args: [str]) > void {
    ctx.modifiesExistingFiles("README.md");
}
export fun format(ctx: magus\Context, args: [str]) > void {
    ctx.modifiesExistingFiles("**/*.go");
}
`,
		"api/README.md": "# api\n", "api/main.go": "package main\n",
	} {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
	}
	paths := []string{"api/README.md", "api/main.go", ".gitattributes"}
	for name, tc := range map[string]struct {
		opts []WorkspaceOption
		want map[string]types.Writes
	}{
		"generate's closure by default": {want: map[string]types.Writes{"api/README.md": {Updated: true}, ".gitattributes": {Maintained: true}}},
		"the named regeneration target": {opts: []WorkspaceOption{WithRegenerateTarget("format")},
			want: map[string]types.Writes{"api/main.go": {Updated: true}, ".gitattributes": {Maintained: true}}},
	} {
		t.Run(name, func(t *testing.T) {
			w, err := OpenWorkspace(t.Context(), root, "ci", tc.opts...)
			require.NoError(t, err)
			t.Cleanup(func() { _ = w.Close() })
			got, err := w.Classify(t.Context(), paths)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// Auto-resolution is opted into by path in magus.yaml and changes nothing about how a
// file is written: an opted-in source stays source.
func TestWorkspaceClassifyReadsAutoResolveFromTheConfig(t *testing.T) {
	root := t.TempDir()
	for rel, body := range map[string]string{
		"magus.yaml":     "vcs:\n  auto_resolve: [\"CHANGELOG.md\", \"docs/**/*.md\"]\n",
		"magusfile.buzz": "", "CHANGELOG.md": "# log\n", "docs/a/b.md": "b\n", "main.go": "package main\n",
	} {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
	}
	w, err := OpenWorkspace(t.Context(), root, "ci")
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })
	got, err := w.Classify(t.Context(), []string{"CHANGELOG.md", "docs/a/b.md", "main.go"})
	require.NoError(t, err)
	assert.Equal(t, map[string]types.Writes{"CHANGELOG.md": {AutoResolve: true}, "docs/a/b.md": {AutoResolve: true}}, got)
}

func TestOpenWorkspaceNeedsATarget(t *testing.T) {
	_, err := OpenWorkspace(t.Context(), t.TempDir(), "")
	require.Error(t, err)
	_, err = OpenWorkspace(t.Context(), t.TempDir(), "ci", WithRegenerateTarget(""))
	require.EqualError(t, err, "workspace needs the target the queue's regeneration runs")
}
