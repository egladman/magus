package cache

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLastEntryFor_NoEntries(t *testing.T) {
	cdir := t.TempDir()
	c, err := Open(t.Context(), cdir)
	require.NoError(t, err)
	_, _, err = c.LastEntry("nonexistent/project")
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

func TestLastEntryFor_ReturnsLatest(t *testing.T) {
	root := t.TempDir()
	cdir := t.TempDir()

	// Set up a minimal project with one source and one declared output.
	src := filepath.Join(root, "myservice", "main.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(src), 0o755))
	require.NoError(t, os.WriteFile(src, []byte("package main\nfunc main() {}\n"), 0o644))
	outRel := filepath.Join("myservice", "out.bin")
	step := Step{
		ProjectPath:   "myservice",
		Sources:       []string{"myservice/*.go"},
		Outputs:       []string{outRel},
		WorkspaceRoot: root,
		Target:        "build",
	}
	fn := func(_ context.Context) error {
		abs := filepath.Join(root, outRel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		return os.WriteFile(abs, []byte("binary"), 0o755)
	}

	c, err := Open(t.Context(), cdir, WithMutable(true), WithLogger(discardLogger))
	require.NoError(t, err)
	_, err = c.Run(context.Background(), step, fn)
	require.NoError(t, err)

	m, logPath, err := c.LastEntry("myservice")
	require.NoError(t, err)
	assert.Equal(t, "myservice", m.ProjectPath)
	assert.NotEmpty(t, logPath, "expected non-empty log path")
	assert.Equal(t, "build", m.Target)
}

// TestLastRecordedRun_NothingRecorded: with no entry there is no comparison to make,
// and the error has to say so and name the step that would create one.
func TestLastRecordedRun_NothingRecorded(t *testing.T) {
	c, err := Open(t.Context(), t.TempDir())
	require.NoError(t, err)

	_, err = c.LastRecordedRun("svc", "build")
	require.ErrorIs(t, err, fs.ErrNotExist)
	assert.Contains(t, err.Error(), "nothing to compare against")
	assert.Contains(t, err.Error(), "run the target once")
}

// TestLastRecordedRun_ExplainsChangedSource walks the whole negative lens: run a target,
// edit one of its sources, and the recorded run plus CompareKeyInputs must name that file
// as the reason a run now would miss.
func TestLastRecordedRun_ExplainsChangedSource(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	step := makeStep(root)
	step.Target = "build"

	_, err := c.Run(t.Context(), step, func(context.Context) error { return nil })
	require.NoError(t, err)

	liveKey, liveLines, err := c.StepKey(t.Context(), &step)
	require.NoError(t, err)
	rec, err := c.LastRecordedRun("test/pkg", "build")
	require.NoError(t, err)
	assert.Equal(t, rec.Key, liveKey, "nothing moved yet, so the recorded key is still what a run would mint")
	require.NotEmpty(t, rec.KeyInputs, "the run recorded its key inputs")
	assert.Equal(t, 0, CompareKeyInputs(rec.KeyInputs, MaskKeyInputs(liveLines)).Differences)

	writeMain(t, root, "package main // edited")

	liveKey, liveLines, err = c.StepKey(t.Context(), &step)
	require.NoError(t, err)
	assert.NotEqual(t, rec.Key, liveKey, "the edit moved the key, so a run now misses")

	cmp := CompareKeyInputs(rec.KeyInputs, MaskKeyInputs(liveLines))
	assert.Equal(t, 1, cmp.Differences, "one file changed: %+v", cmp)
	require.NotNil(t, cmp.First)
	assert.Equal(t, "src:test/pkg/main.go", cmp.First.Input)
	assert.NotEqual(t, cmp.First.Recorded, cmp.First.Live, "both content hashes are shown")
}

func TestLastEntryForTarget_FiltersTarget(t *testing.T) {
	root := t.TempDir()
	cdir := t.TempDir()

	writeOutput := func(name string) func(context.Context) error {
		return func(_ context.Context) error {
			abs := filepath.Join(root, "svc", name)
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				return err
			}
			return os.WriteFile(abs, []byte("x"), 0o644)
		}
	}

	src := filepath.Join(root, "svc", "main.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(src), 0o755))
	require.NoError(t, os.WriteFile(src, []byte("package main\nfunc main(){}\n"), 0o644))

	c, err := Open(t.Context(), cdir, WithMutable(true), WithLogger(discardLogger))
	require.NoError(t, err)

	buildStep := Step{
		ProjectPath:   "svc",
		Sources:       []string{"svc/*.go"},
		Outputs:       []string{"svc/build.out"},
		WorkspaceRoot: root,
		Target:        "build",
	}
	testStep := Step{
		ProjectPath:   "svc",
		Sources:       []string{"svc/*.go"},
		Outputs:       []string{"svc/test.out"},
		WorkspaceRoot: root,
		Target:        "test",
	}

	_, err = c.Run(context.Background(), buildStep, writeOutput("build.out"))
	require.NoError(t, err)
	_, err = c.Run(context.Background(), testStep, writeOutput("test.out"))
	require.NoError(t, err)

	// Filtering by "test" should return only the test entry.
	m, _, err := c.LastEntryForTarget("svc", "test")
	require.NoError(t, err, "LastEntryForTarget(test)")
	assert.Equal(t, "test", m.Target, "LastEntryForTarget(test)")

	// Filtering by "build" should return only the build entry.
	m, _, err = c.LastEntryForTarget("svc", "build")
	require.NoError(t, err, "LastEntryForTarget(build)")
	assert.Equal(t, "build", m.Target, "LastEntryForTarget(build)")

	// Filtering by an unknown target returns ErrNotExist.
	_, _, err = c.LastEntryForTarget("svc", "format")
	assert.ErrorIs(t, err, fs.ErrNotExist, "LastEntryForTarget(format): expected fs.ErrNotExist")
}
