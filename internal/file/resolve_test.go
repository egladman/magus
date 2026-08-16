package file

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolve(t *testing.T) {
	ok := func(t *testing.T, input, anchor, want string) {
		got, err := resolveAmbiguous(input, anchor)
		require.NoError(t, err, "resolveAmbiguous(%q, %q)", input, anchor)
		assert.Equal(t, want, got)
	}
	fail := func(t *testing.T, input, anchor, wantErr string) {
		got, err := resolveAmbiguous(input, anchor)
		require.Error(t, err, "resolveAmbiguous(%q, %q) = %q, want error containing %q", input, anchor, got, wantErr)
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

// TestResolveProjectDeprecatedScheme pins the deprecation window: workspace:// still
// resolves exactly as the bare path it wraps, and every use says so once. A silent
// acceptance would leave the scheme in circulation with nothing telling anyone it went.
func TestResolveProjectDeprecatedScheme(t *testing.T) {
	logged := func(t *testing.T, input, anchor, want string) string {
		t.Helper()
		var buf bytes.Buffer
		prev := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
		t.Cleanup(func() { slog.SetDefault(prev) })
		got, err := ResolveProject(t.Context(), input, anchor)
		require.NoError(t, err, "ResolveProject(%q, %q)", input, anchor)
		assert.Equal(t, want, got)
		return buf.String()
	}

	t.Run("nested path resolves as the bare path", func(t *testing.T) {
		out := logged(t, "workspace://pkg/api", "web/studio", "pkg/api")
		assert.Contains(t, out, "workspace:// is deprecated")
		assert.Contains(t, out, `use=pkg/api`)
	})

	// The root alias returns "." regardless of anchor - it is not the bare "." reading,
	// which would resolve to the anchor.
	t.Run("root alias stays the root", func(t *testing.T) {
		out := logged(t, "workspace://", "web/studio", ".")
		assert.Contains(t, out, "workspace:// is deprecated")
		assert.Contains(t, out, "use=.")
	})

	// "workspace://." is NOT the root alias: the "." is a dot-relative marker and
	// anchors, exactly as a bare "." does. That equivalence is the whole argument for
	// retiring the scheme - it never bought a reading the bare path did not already have.
	t.Run("explicit dot anchors like the bare dot", func(t *testing.T) {
		logged(t, "workspace://.", "web/studio", "web/studio")
		bare, err := ResolveProject(t.Context(), ".", "web/studio")
		require.NoError(t, err)
		assert.Equal(t, "web/studio", bare)
	})

	t.Run("bare path warns about nothing", func(t *testing.T) {
		assert.Empty(t, logged(t, "pkg/api", "web/studio", "pkg/api"))
	})
}

// TestResolveImport pins the `import "project/<path>"` contract: the path is
// always dot-relative to the importing magusfile, so a BARE descendant anchors
// rather than being read as workspace-relative the way Resolve reads it.
func TestResolveImport(t *testing.T) {
	ok := func(t *testing.T, input, anchor, want string) {
		got, err := ResolveImport(input, anchor)
		require.NoError(t, err, "ResolveImport(%q, %q)", input, anchor)
		assert.Equal(t, want, got)
	}

	// The regression: docs importing a nested project resolved to the bare path,
	// which matched no registered project and broke every graph build.
	t.Run("bare descendant anchors", func(t *testing.T) {
		ok(t, "guides/integrations/agents", "docs", "docs/guides/integrations/agents")
	})
	t.Run("bare sibling anchors", func(t *testing.T) { ok(t, "api", "extensions/drape", "extensions/drape/api") })
	t.Run("from root anchor", func(t *testing.T) { ok(t, "libs/gopherbuzz", ".", "libs/gopherbuzz") })

	// Dot-relative forms keep resolving exactly as before.
	t.Run("up one", func(t *testing.T) { ok(t, "../libs/textsearch", "docs", "libs/textsearch") })
	t.Run("explicit dot", func(t *testing.T) { ok(t, "./peer", "extensions/drape", "extensions/drape/peer") })
	t.Run("self", func(t *testing.T) { ok(t, ".", "docs", "docs") })

	// Errors still reject what Resolve rejects.
	t.Run("empty", func(t *testing.T) {
		_, err := ResolveImport("", "docs")
		require.Error(t, err)
	})
	t.Run("escapes root", func(t *testing.T) {
		_, err := ResolveImport("../../foo", "docs")
		require.ErrorContains(t, err, "escapes workspace root")
	})
	// A bare path that only escapes AFTER anchoring, and one that escapes only
	// relative to the root anchor.
	t.Run("bare embedded escape", func(t *testing.T) {
		_, err := ResolveImport("foo/../../../bar", "docs")
		require.ErrorContains(t, err, "escapes workspace root")
	})
	t.Run("bare internal dotdot ok", func(t *testing.T) { ok(t, "foo/../bar", "docs", "docs/bar") })

	// Absolute forms are rejected. The Windows rooted spelling only looks
	// absolute after ToSlash, so classifying the raw string would let it pass.
	t.Run("abs unix", func(t *testing.T) {
		_, err := ResolveImport("/etc", "docs")
		require.ErrorContains(t, err, "must be relative")
	})
	t.Run("drive letter", func(t *testing.T) {
		_, err := ResolveImport(`C:\foo`, "docs")
		require.ErrorContains(t, err, "must be relative")
	})
	t.Run("windows rooted", func(t *testing.T) {
		_, err := ResolveImport(`\foo`, "docs")
		require.ErrorContains(t, err, "must be relative")
	})

	// The error quotes what the magusfile wrote, never an internally synthesized path.
	t.Run("error quotes original input", func(t *testing.T) {
		_, err := ResolveImport("foo/../../../bar", "docs")
		require.ErrorContains(t, err, `"foo/../../../bar"`)
		assert.NotContains(t, err.Error(), "./foo")
	})
}
