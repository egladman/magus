//go:build windows

// These test cases exercise the Windows-specific backslash normalisation in
// Resolve. No separate resolve_windows.go is needed because the implementation
// uses filepath.ToSlash unconditionally; on non-Windows hosts ToSlash is a
// no-op, so these cases are only meaningful under GOOS=windows.

package file

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveWindows(t *testing.T) {
	ok := func(t *testing.T, input, anchor, want string) {
		got, err := Resolve(input, anchor)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}

	t.Run("backslash relative", func(t *testing.T) { ok(t, `..\api`, "extensions/drape", "extensions/api") })
	t.Run("mixed sep", func(t *testing.T) { ok(t, `..\..\web/studio`, "a/b", "web/studio") })
}
