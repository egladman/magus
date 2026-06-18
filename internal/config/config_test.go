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

func TestDefaults_FlakeEnabled(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	assert.True(t, cfg.Flake.Enabled, "Defaults().Flake.Enabled should be true")
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
