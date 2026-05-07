package gen

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestConfigFlagsNotDrifted re-runs the flag generator into a temp file and
// diffs it against the committed config_flags.go. The test fails if the
// committed file is out of date, which means a Config change requires
// re-running:
//
//	go generate ./cmd/magus/...
func TestConfigFlagsNotDrifted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping drift check in short mode")
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	genDir := filepath.Dir(thisFile)

	tmp := t.TempDir()
	flagsGen := filepath.Join(tmp, "config_flags.go")

	cmd := exec.Command("go", "run",
		"../../magus-config-gen",
		"-config", "../../../internal/config/config.go",
		"-out", flagsGen,
	)
	cmd.Dir = genDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("config/flag generator failed: %v\n%s", err, out)
	}

	checks := []struct {
		name      string
		committed string
		generated string
	}{
		{"config_flags.go", filepath.Join(genDir, "config_flags.go"), flagsGen},
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
			t.Errorf("%s is out of date — run: go generate ./cmd/magus/...", c.name)
		}
	}
}
