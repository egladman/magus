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
	c, err := Open(cdir)
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

	c, err := Open(cdir, WithMutable(true), WithLogger(discardLogger))
	require.NoError(t, err)
	_, err = c.Run(context.Background(), step, fn)
	require.NoError(t, err)

	m, logPath, err := c.LastEntry("myservice")
	require.NoError(t, err)
	assert.Equal(t, "myservice", m.ProjectPath)
	assert.NotEmpty(t, logPath, "expected non-empty log path")
	assert.Equal(t, "build", m.Target)
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

	c, err := Open(cdir, WithMutable(true), WithLogger(discardLogger))
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
