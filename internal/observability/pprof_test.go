package observability

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The guard hook's `magus buzz` forks `magus shell`, which inherits MAGUS_PPROF. Each
// process profiles to its own file, so neither overwrites the profile the other is
// still writing.
func TestProfilesEachMagusApart(t *testing.T) {
	for spec, want := range map[string]string{
		"cpu:/tmp/c":               "cpu:/tmp/c.%p",
		"cpu:/tmp/c,mem:/tmp/m":    "cpu:/tmp/c.%p,mem:/tmp/m.%p",
		"cpu: /tmp/c ":             "cpu:/tmp/c.%p",
		"trace:/tmp/t.%p":          "trace:/tmp/t.%p",
		"cpu:/tmp/c.%p,mem:/tmp/m": "cpu:/tmp/c.%p,mem:/tmp/m.%p",
		"cpu":                      "cpu",
		"cpu:":                     "cpu:",
	} {
		assert.Equal(t, want, pprofDescendantSpec(spec), spec)
	}

	dir := t.TempDir()
	t.Setenv(pprofEnv, fmt.Sprintf("cpu:%s,mem:%s", filepath.Join(dir, "top.pprof"), filepath.Join(dir, "own.%p")))
	stop := StartProfiling()
	inherited := os.Getenv(pprofEnv)
	stop()
	assert.Equal(t, fmt.Sprintf("cpu:%s,mem:%s", filepath.Join(dir, "top.pprof.%p"), filepath.Join(dir, "own.%p")), inherited)
	for _, name := range []string{"top.pprof", fmt.Sprintf("own.%d", os.Getpid())} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		assert.True(t, bytes.HasPrefix(data, []byte{0x1f, 0x8b}), "%s is a gzipped profile", name)
	}
}

func TestStartProfilingWithoutASpecIsANoOp(t *testing.T) {
	t.Setenv(pprofEnv, "  ")
	stop := StartProfiling()
	require.NotNil(t, stop)
	stop()
	assert.Equal(t, "  ", os.Getenv(pprofEnv), "an empty spec is left alone")
}

func TestStartOneProfileRefusesAnEmptyPath(t *testing.T) {
	assert.Nil(t, startOneProfile("cpu", ""))
}

func TestStartOneProfileRefusesAnUnknownKindAfterCreatingTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p")
	assert.Nil(t, startOneProfile("bogus", path))
}

func TestStartOneProfileCreatesTheParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "heap.pprof")
	stop := startOneProfile("mem", path)
	require.NotNil(t, stop)
	stop()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotEmpty(t, data)
}

func TestStartOneProfileWritesAnExecutionTrace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.trace")
	stop := startOneProfile("trace", path)
	require.NotNil(t, stop)
	stop()
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.NotZero(t, info.Size())
}
