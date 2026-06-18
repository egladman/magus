package file

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolve(t *testing.T) {
	ok := func(t *testing.T, input, anchor, want string) {
		got, err := Resolve(input, anchor)
		require.NoError(t, err, "Resolve(%q, %q)", input, anchor)
		assert.Equal(t, want, got)
	}
	fail := func(t *testing.T, input, anchor, wantErr string) {
		got, err := Resolve(input, anchor)
		require.Error(t, err, "Resolve(%q, %q) = %q, want error containing %q", input, anchor, got, wantErr)
		assert.ErrorContains(t, err, wantErr)
	}

	// Repo-relative inputs (no dot prefix) ignore the anchor.
	t.Run("bare", func(t *testing.T) { ok(t, "api", "extensions/drape", "api") })
	t.Run("nested", func(t *testing.T) { ok(t, "web/studio", "extensions/drape", "web/studio") })
	t.Run("root project", func(t *testing.T) { ok(t, ".", "extensions/drape", "extensions/drape") })

	// Explicit relative markers resolve against the anchor.
	t.Run("sibling sub", func(t *testing.T) { ok(t, "./peer", "extensions/drape", "extensions/drape/peer") })
	t.Run("up one", func(t *testing.T) { ok(t, "../api", "extensions/drape", "extensions/api") })
	t.Run("up two to root", func(t *testing.T) { ok(t, "../../api", "extensions/drape", "api") })
	t.Run("up to repo root", func(t *testing.T) { ok(t, "..", "extensions/drape", "extensions") })
	t.Run("deep up", func(t *testing.T) { ok(t, "../../../web/studio", "a/b/c", "web/studio") })

	// Errors.
	t.Run("empty", func(t *testing.T) { fail(t, "", "x", "empty project path") })
	t.Run("abs unix", func(t *testing.T) { fail(t, "/etc", "x", "must be repo-relative") })
	t.Run("drive letter", func(t *testing.T) { fail(t, `C:\foo`, "x", "must be repo-relative") })
	t.Run("lowercase drive", func(t *testing.T) { fail(t, `c:/foo`, "x", "must be repo-relative") })
	t.Run("escapes anchor at root", func(t *testing.T) { fail(t, "../foo", ".", "escapes workspace root") })
	t.Run("escapes deep", func(t *testing.T) { fail(t, "../../../foo", "a/b", "escapes workspace root") })

	// Bare (non-dot-prefixed) inputs that clean to an escape must also be
	// rejected — they bypass the relative-marker branch (CRIT-3).
	t.Run("bare embedded escape", func(t *testing.T) { fail(t, "foo/../../bar", "a/b", "escapes workspace root") })
	t.Run("bare escape to parent", func(t *testing.T) { fail(t, "foo/../..", "a/b", "escapes workspace root") })
	t.Run("bare internal dotdot ok", func(t *testing.T) { ok(t, "foo/../bar", "a/b", "bar") })
}
