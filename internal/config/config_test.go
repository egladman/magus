package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Note: ApplyEnv tests moved to internal/config/gen/env_test.go.

func TestDefaults_VolatilityEnabled(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	assert.True(t, cfg.Volatility.Enabled, "Defaults().Volatility.Enabled should be true")
}

// TestCacheIncludeDefaultsOff pins the OFF default deliberately, against the pull to
// "fix" it toward the safe-looking direction. Keying the host platform is not what makes
// a replay safe - Manifest.Platform refuses a cross-platform hit whatever these say - and
// a key free of host facts is what lets an output ref name the same run on every machine
// (see internal/cache's TestCacheKeyUnaffectedByPlatform). Turning these on by default
// would break that and rekey every existing entry.
func TestCacheIncludeDefaultsOff(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	assert.False(t, cfg.Cache.IncludeOS(), "cache.include.os.enabled must default off")
	assert.False(t, cfg.Cache.IncludeArch(), "cache.include.arch.enabled must default off")
}

func TestCacheIncludeExplicit(t *testing.T) {
	t.Parallel()
	on := Cache{Include: CacheInclude{
		OS:   CacheIncludeFlag{Enabled: boolPtr(true)},
		Arch: CacheIncludeFlag{Enabled: boolPtr(true)},
	}}
	assert.True(t, on.IncludeOS())
	assert.True(t, on.IncludeArch())

	off := Cache{Include: CacheInclude{
		OS:   CacheIncludeFlag{Enabled: boolPtr(false)},
		Arch: CacheIncludeFlag{Enabled: boolPtr(false)},
	}}
	assert.False(t, off.IncludeOS())
	assert.False(t, off.IncludeArch())
}

// TestSave_Concurrent verifies that 10 goroutines concurrently calling Save
// on the same path do not panic and leave a valid YAML file behind.
func TestSave_Concurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magus.yaml")

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make([]error, goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			errs[i] = Save(path, "concurrency", fmt.Sprintf("%d", i+1))
		}()
	}
	wg.Wait()

	// At least one write must have succeeded.
	anyOK := false
	for _, err := range errs {
		if err == nil {
			anyOK = true
			break
		}
	}
	require.True(t, anyOK, "all concurrent Save calls failed")

	// Final file must be valid YAML.
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var m map[string]interface{}
	assert.NoError(t, yaml.Unmarshal(data, &m), "final file is not valid YAML:\n%s", data)
}
