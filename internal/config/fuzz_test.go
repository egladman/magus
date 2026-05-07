package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/config"
)

// FuzzLoadFile feeds arbitrary bytes through the YAML loader. The
// invariant is "no panic": malformed YAML and unknown-key strict
// failures must return errors, not crash. Strict mode also enforces
// schema validation, exercising the validate package via Validate().
func FuzzLoadFile(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(""),
		[]byte("cache:\n  mode: auto\n"),
		[]byte("cache:\n  mode: bogus\n"),
		[]byte("vcs:\n  enabled: true\n  command_name: git\n"),
		[]byte("concurrency: -1\n"),
		[]byte("log:\n  format: pretty\n"),
		[]byte("\xff\xfe\x00\x00binary garbage"),
		[]byte("a: !!binary unparsable"),
		[]byte("---\n---\nmultiple-docs"),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		// LoadFile is path-based; persist the fuzz input and let the
		// loader read it. t.TempDir handles cleanup.
		path := filepath.Join(t.TempDir(), "magus.yaml")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		// Both modes; strict adds KnownFields + Validate. Either path
		// must terminate in (Config, error) and never panic.
		_, _ = config.LoadFile(path, false)
		_, _ = config.LoadFile(path, true)
	})
}
