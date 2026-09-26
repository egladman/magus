package client

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/queue/types"
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
// its regeneration runs, whatever their extension, and so is anything that reaches it
// through the project graph; an edit to the declarations proves nothing.
func TestWorkspaceSaysWhatIsGeneratedAndWhatItsRegenerationRuns(t *testing.T) {
	root := t.TempDir()
	for rel, body := range map[string]string{
		"magusfile.buzz":     "",
		"api/magusfile.buzz": "import \"magus\";\nmagus\\project({\"outputs\": [\"gen/**\"]});\n",
		"web/magusfile.buzz": "", "api/main.go": "package main\n", "api/gen/out.go": "package gen\n",
		"api/notes.md": "notes\n", "api/CMakeLists.txt": "project(api)\n", "api/requirements.txt": "requests\n",
		"web/notes.md": "notes\n", "web/app.go": "package web\n", "vendor/blob.go": "package blob\n",
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

	g, err := w.Generation(t.Context(), []string{"api/gen/out.go"}, []string{"web/notes.md", "web/app.go"})
	require.NoError(t, err)
	assert.Equal(t, types.Generation{Units: []string{"api"}}, g, "another project's document and code prove it")

	g, err = w.Generation(t.Context(), []string{"api/gen/out.go"}, []string{"api/main.go", "web/app.go"})
	require.NoError(t, err)
	assert.Equal(t, []string{"api/main.go"}, g.Code)

	// No extension makes a file data: CMakeLists.txt and requirements.txt are code to
	// the tools that read them, and a generator may run what a document holds.
	g, err = w.Generation(t.Context(), []string{"api/gen/out.go"}, []string{"api/CMakeLists.txt", "api/notes.md", "api/requirements.txt", "web/app.go"})
	require.NoError(t, err)
	assert.Equal(t, []string{"api/CMakeLists.txt", "api/notes.md", "api/requirements.txt"}, g.Code, "the generating project's files, whatever their extension")

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

// A merge is allowed by magus's change classifier: prose by the built-in markdown globs,
// code only where a project's merge_low_risk opts it in.
func TestWorkspaceAutoResolvableIsTheChangeClassifier(t *testing.T) {
	root := t.TempDir()
	for rel, body := range map[string]string{
		"magusfile.buzz":     "",
		"api/magusfile.buzz": "import \"magus\";\nmagus\\project({\"merge_low_risk\": [\"fixtures/**\"]});\n",
	} {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
	}
	w, err := OpenWorkspace(t.Context(), root, "ci")
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })
	for path, want := range map[string]struct {
		verdict string
		ok      bool
	}{
		"CHANGELOG.md":        {`CHANGELOG.md: prose (matches "**/*.md" (built-in default))`, true},
		"api/fixtures/a.json": {`api/fixtures/a.json: code (matches "fixtures/**" (merge_low_risk of project api))`, true},
		"api/handler.json":    {"api/handler.json: code (no comment syntax is declared for this language; classified as code)", false},
	} {
		verdict, ok, err := w.AutoResolvable(t.Context(), path, []byte("a\n"), []byte("a\nb\n"))
		require.NoError(t, err)
		assert.Equal(t, want.ok, ok, path)
		assert.Equal(t, want.verdict, verdict, path)
	}
}

// The hooks get the grants of the spells the base's projects resolved, not of every
// spell this magus has registered.
func TestWorkspaceSpellSandboxesAreTheResolvedSpells(t *testing.T) {
	root := t.TempDir()
	for rel, body := range map[string]string{
		"magusfile.buzz":     "",
		"api/magusfile.buzz": "import \"magus\";\nimport \"magus/spell/go\";\nmagus\\project({\"spells\": [go]});\n",
		"api/go.mod":         "module example.com/api\n\ngo 1.25\n",
		"api/main.go":        "package main\n",
	} {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
	}
	w, err := OpenWorkspace(t.Context(), root, "ci")
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })

	assert.Equal(t, []string{"go"}, slices.Sorted(maps.Keys(w.SpellSandboxes())))
}

func TestOpenWorkspaceNeedsATarget(t *testing.T) {
	_, err := OpenWorkspace(t.Context(), t.TempDir(), "")
	require.Error(t, err)
	_, err = OpenWorkspace(t.Context(), t.TempDir(), "ci", WithRegenerateTarget(""))
	require.EqualError(t, err, "workspace needs the target the queue's regeneration runs")
}
