package cache

import (
	"context"
	"testing"
)

// FuzzHashStep verifies that hashStep never panics on arbitrary Step
// field values and always returns a non-empty hex string on success.
// The seed corpus covers the common shapes: empty step, single source
// glob, env vars, and upstream dependency paths.
func FuzzHashStep(f *testing.F) {
	f.Add("api", "build", "", "")
	f.Add("web/studio", "test", "*.ts", "GOPATH")
	f.Add(".", "lint", "**/*.go", "HOME")
	f.Add("extensions/drape", "ci", "src/**/*.rs", "CARGO_HOME")

	f.Fuzz(func(t *testing.T, projectPath, target, source, envKey string) {
		s := &Step{
			ProjectPath:   projectPath,
			WorkspaceRoot: t.TempDir(),
			Target:        target,
		}
		if source != "" {
			s.Sources = []string{source}
		}
		if envKey != "" {
			s.EnvAllow = []string{envKey}
		}

		c := &Cache{mtimes: newMtimeStore(t.TempDir(), nil)}
		h, err := c.hashStep(context.Background(), s)
		if err != nil {
			// Errors are expected when source globs resolve to nothing or
			// the workspace root is invalid — not a bug.
			return
		}
		if len(h) == 0 {
			t.Errorf("hashStep returned empty hash for step %+v", s)
		}
	})
}
