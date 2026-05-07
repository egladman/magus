package schema

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestSchemaNotDrifted re-runs the schema generator into temp files and
// diffs them against the committed fields.go, bind.go, and env.go.
// Fails if any committed file is out of date, meaning a Config change
// requires re-running:
//
//	go generate ./magus/cmd/magus/...
func TestSchemaNotDrifted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping drift check in short mode")
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	schemaDir := filepath.Dir(thisFile)
	// e.g. magus/schema

	tmp := t.TempDir()
	fieldsOut := filepath.Join(tmp, "fields.go")
	bindOut := filepath.Join(tmp, "bind.go")
	envOut := filepath.Join(tmp, "env.go")

	generatorPath := filepath.Join(schemaDir, "..", "cmd", "magus-config-gen")
	configPath := filepath.Join(schemaDir, "..", "internal", "config", "config.go")

	cmd := exec.Command(
		"go", "run", generatorPath,
		"-config", configPath,
		"-fields-out", fieldsOut,
		"-bind-out", bindOut,
		"-apply-env-out", envOut,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("schema generator failed: %v\n%s", err, out)
	}

	checks := []struct {
		name      string
		committed string
		generated string
	}{
		{
			"fields.go",
			filepath.Join(schemaDir, "gen", "fields.go"),
			fieldsOut,
		},
		{
			"bind.go",
			filepath.Join(schemaDir, "..", "cmd", "magus", "gen", "bind.go"),
			bindOut,
		},
		{
			"env.go",
			filepath.Join(schemaDir, "..", "internal", "config", "gen", "env.go"),
			envOut,
		},
	}

	for _, c := range checks {
		want, err := os.ReadFile(c.committed)
		if err != nil {
			t.Fatalf("read committed %s: %v", c.name, err)
		}
		got, err := os.ReadFile(c.generated)
		if err != nil {
			t.Fatalf("read generated %s: %v", c.name, err)
		}
		if !bytes.Equal(want, got) {
			t.Errorf("%s is out of date — run: go generate ./magus/cmd/magus/...", c.name)
		}
	}
}
