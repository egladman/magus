package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

// Note: ApplyEnv tests moved to internal/config/gen/env_test.go.

func TestDefaults_FlakeEnabled(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	if !cfg.Flake.Enabled {
		t.Error("Defaults().Flake.Enabled = false, want true")
	}
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
	if !anyOK {
		t.Fatal("all concurrent Save calls failed")
	}

	// Final file must be valid YAML.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var m map[string]interface{}
	if err := yaml.Unmarshal(data, &m); err != nil {
		t.Errorf("final file is not valid YAML: %v\n%s", err, data)
	}
}
