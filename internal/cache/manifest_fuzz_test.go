package cache

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzReadManifest verifies that readManifest never panics on
// arbitrary manifest file contents. Truncated, garbage, or empty JSON
// should always return a typed error, never a nil-manifest with a nil
// error and never a panic.
func FuzzReadManifest(f *testing.F) {
	// Valid manifest JSON.
	f.Add(`{"outputs":[{"path":"bin/api","blob":"abc123"}],"created_at":"2024-01-01T00:00:00Z"}`)
	// Truncated JSON.
	f.Add(`{"outputs":[{"path":"bin/`)
	// Empty.
	f.Add(``)
	// Valid JSON but wrong type.
	f.Add(`[]`)
	// Null.
	f.Add(`null`)
	// Garbage binary-ish data.
	f.Add("\x00\x01\x02\x03")

	f.Fuzz(func(t *testing.T, data string) {
		dir := t.TempDir()
		c := &Cache{dir: dir}

		path := filepath.Join(dir, "manifests", flattenPath("proj"), "hash.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}

		m, err := c.readManifest("proj", "hash")
		if err != nil {
			// Any error is acceptable; the important invariant is no panic.
			return
		}
		// If no error, manifest must be non-nil.
		if m == nil {
			t.Error("readManifest: (nil, nil) — callers cannot distinguish from a valid miss")
		}
	})
}
