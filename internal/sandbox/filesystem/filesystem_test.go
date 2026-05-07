package filesystem

import "testing"

// FuzzNormalizePath exercises path-shape handling with adversarial inputs.
func FuzzNormalizePath(f *testing.F) {
	f.Add("/workspace/foo")
	f.Add("/workspace/../etc/passwd")
	f.Add("relative/path")
	f.Add("")
	f.Add("/workspace/foo\x00bar")
	f.Add("//workspace//foo")
	f.Fuzz(func(t *testing.T, path string) {
		result, err := normalizePath(path)
		if err != nil {
			return // empty or bad paths are rejected; that's fine
		}
		// Whatever comes back must be absolute and clean.
		if len(result) == 0 || result[0] != '/' {
			t.Errorf("normalizePath(%q) = %q: not absolute", path, result)
		}
	})
}
