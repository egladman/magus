package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHarnessSourceLinksInstallAndAreIdempotent(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "source"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "source", "file.txt"), []byte("source"), 0o644))

	links := map[string]string{".claude/skills/shared": "source"}
	files, err := installHarnessSourceLinks(context.Background(), root, links, false)
	require.NoError(t, err)
	assert.Equal(t, []string{".claude/skills/shared"}, files)
	link, err := os.Readlink(filepath.Join(root, ".claude/skills/shared"))
	require.NoError(t, err)
	assert.False(t, filepath.IsAbs(link), "new links use relative targets")
	assert.FileExists(t, filepath.Join(root, ".claude/skills/shared/file.txt"))

	files, err = installHarnessSourceLinks(context.Background(), root, links, false)
	require.NoError(t, err)
	assert.Empty(t, files)
}

func TestHarnessSourceLinksAcceptMatchingRelativeAndAbsoluteSymlinks(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "dest"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src", "file"), []byte("ok"), 0o644))
	require.NoError(t, os.Symlink("../src", filepath.Join(root, "dest", "relative")))
	require.NoError(t, os.Symlink(filepath.Join(root, "src"), filepath.Join(root, "dest", "absolute")))

	files, err := installHarnessSourceLinks(context.Background(), root, map[string]string{
		"dest/relative": "src",
		"dest/absolute": "src",
	}, false)
	require.NoError(t, err)
	assert.Empty(t, files)
}

func TestHarnessSourceLinksKeepEqualLegacyFiles(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "dest"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "source"), []byte("same"), 0o644))
	destination := filepath.Join(root, "dest", "source")
	require.NoError(t, os.WriteFile(destination, []byte("same"), 0o600))

	files, err := installHarnessSourceLinks(context.Background(), root, map[string]string{"dest/source": "source"}, false)
	require.NoError(t, err)
	assert.Empty(t, files)
	info, err := os.Lstat(destination)
	require.NoError(t, err)
	assert.True(t, info.Mode().IsRegular())
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestHarnessSourceLinksRefuseConflictingDestinations(t *testing.T) {
	tests := []struct {
		name  string
		make  func(*testing.T, string, string)
		check func(*testing.T, string)
	}{
		{
			name: "file",
			make: func(t *testing.T, root, dest string) {
				require.NoError(t, os.WriteFile(dest, []byte("keep"), 0o644))
			},
			check: func(t *testing.T, dest string) {
				data, err := os.ReadFile(dest)
				require.NoError(t, err)
				assert.Equal(t, "keep", string(data))
			},
		},
		{
			name: "link",
			make: func(t *testing.T, root, dest string) {
				require.NoError(t, os.Symlink("other", dest))
			},
			check: func(t *testing.T, dest string) {
				link, err := os.Readlink(dest)
				require.NoError(t, err)
				assert.Equal(t, "other", link)
			},
		},
		{
			name: "dangling link",
			make: func(t *testing.T, root, dest string) {
				require.NoError(t, os.Symlink("missing", dest))
			},
			check: func(t *testing.T, dest string) {
				link, err := os.Readlink(dest)
				require.NoError(t, err)
				assert.Equal(t, "missing", link)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, "dest"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(root, "source"), []byte("new"), 0o644))
			dest := filepath.Join(root, "dest", "entry")
			tt.make(t, root, dest)

			_, err := installHarnessSourceLinks(context.Background(), root, map[string]string{"dest/entry": "source"}, false)
			require.Error(t, err)
			tt.check(t, dest)
		})
	}
}

func TestHarnessSourceLinksRefuseUnsafeOrMissingPaths(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "source"), []byte("ok"), 0o644))
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "escape")))
	tests := []struct {
		name  string
		links map[string]string
	}{
		{name: "parent traversal destination", links: map[string]string{"../outside": "source"}},
		{name: "parent traversal source", links: map[string]string{"safe": "../outside"}},
		{name: "absolute destination", links: map[string]string{filepath.Join(root, "safe"): "source"}},
		{name: "git component", links: map[string]string{".git/hooks/link": "source"}},
		{name: "symlink escape", links: map[string]string{"safe": "escape"}},
		{name: "destination parent symlink escape", links: map[string]string{"escape-parent/link": "source"}},
		{name: "missing source", links: map[string]string{"safe": "missing"}},
	}
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "escape-parent")))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := installHarnessSourceLinks(context.Background(), root, tt.links, false)
			assert.Error(t, err)
		})
	}
}

func TestHarnessSourceLinksRejectSpecialFileSources(t *testing.T) {
	device := filepath.Join(string(filepath.Separator), "dev", "null")
	info, err := os.Stat(device)
	if err != nil || info.Mode().IsRegular() || info.IsDir() {
		t.Skip("platform has no /dev/null device file")
	}
	root := t.TempDir()
	require.NoError(t, os.Symlink(device, filepath.Join(root, "device")))
	_, err = installHarnessSourceLinks(context.Background(), root, map[string]string{"destination": "device"}, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `source "device" must be a regular file or directory`)
	assert.NoFileExists(t, filepath.Join(root, "destination"))
}

func TestHarnessSourceLinksPreflightWholeBatchBeforeWriting(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "source"), []byte("new"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "existing"), []byte("keep"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "z"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "z", "conflict"), []byte("keep"), 0o644))
	_, err := installHarnessSourceLinks(context.Background(), root, map[string]string{
		"a/new/link": "source",
		"z/conflict": "source",
	}, false)
	require.Error(t, err)
	assert.NoDirExists(t, filepath.Join(root, "a"))
	data, err := os.ReadFile(filepath.Join(root, "existing"))
	require.NoError(t, err)
	assert.Equal(t, "keep", string(data))
}

func TestHarnessSourceLinksDryRunWritesNothing(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "source"), []byte("ok"), 0o644))
	files, err := installHarnessSourceLinks(context.Background(), root, map[string]string{"new/dir/link": "source"}, true)
	require.NoError(t, err)
	assert.Equal(t, []string{"new/dir/link"}, files)
	assert.NoDirExists(t, filepath.Join(root, "new"))
}

func TestHarnessSourceLinksRefuseBoundCommandBeforeReadingStdin(t *testing.T) {
	saved := globalCfg
	t.Cleanup(func() { globalCfg = saved })
	globalCfg = config.Config{}
	t.Setenv(trail.EnvBaggage, "")
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("version: 1\n"), 0o644))
	cacheDir, err := magus.ResolveCacheDir(root, workspace.WithLoadedConfig(globalCfg))
	require.NoError(t, err)
	bindCheckout(t, cacheDir, "fleet/bound")

	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	defer reader.Close()
	defer writer.Close()
	previous := os.Stdin
	os.Stdin = reader
	t.Cleanup(func() { os.Stdin = previous })
	err = agentHarnessLinksCommand(context.Background(), root, []string{"--stdin"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `bound job "fleet/bound"`)
	_, err = writer.Write([]byte(`{"links":{}}`))
	assert.NoError(t, err, "stdin remains unread while the checkout is bound")
}

func TestHarnessSourceLinksRespectCancellationBeforeWrites(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "source"), []byte("ok"), 0o644))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := installHarnessSourceLinks(ctx, root, map[string]string{"new/link": "source"}, false)
	require.Error(t, err)
	assert.NoDirExists(t, filepath.Join(root, "new"))
}

func TestHarnessSourceLinksSkipSameActualDirectory(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "dir"), 0o755))
	files, err := installHarnessSourceLinks(context.Background(), root, map[string]string{"dir": "dir"}, false)
	require.NoError(t, err)
	assert.Empty(t, files)
	info, err := os.Lstat(filepath.Join(root, "dir"))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestHarnessSourceLinksRejectDestinationAncestorOverlap(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
	_, err := installHarnessSourceLinks(context.Background(), root, map[string]string{
		"dest":       "src",
		"dest/child": "src",
	}, false)
	assert.Error(t, err)
	assert.NoDirExists(t, filepath.Join(root, "dest"))
}

func TestHarnessSourceLinksPreserveProtectedPolicyBoundary(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "source"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "source", "skill.md"), []byte("skill"), 0o644))
	policy := filesystem.Ruleset{Rules: []filesystem.Rule{{Path: filesystem.ResolveRulePath(filepath.Join(root, "source")), Read: true, Write: true}}}
	destination := filepath.Join(root, ".claude/skills/team")
	assert.ErrorIs(t, policy.Check(destination, filesystem.Write), filesystem.ErrDenied,
		"ordinary filesystem writes stay inside the policy's declared source path")
	files, err := installHarnessSourceLinks(context.Background(), root, map[string]string{".claude/skills/team": "source"}, false)
	require.NoError(t, err)
	assert.Equal(t, []string{".claude/skills/team"}, files)
	assert.FileExists(t, filepath.Join(root, ".claude/skills/team/skill.md"))
	expected, err := filepath.EvalSymlinks(filepath.Join(root, "source"))
	require.NoError(t, err)
	resolved, err := filepath.EvalSymlinks(destination)
	require.NoError(t, err)
	assert.Equal(t, expected, resolved, "the explicit installer links only to the in-root source")
}

func TestHarnessSourceLinksRefuseGitAliasesBeforeWriting(t *testing.T) {
	t.Run("source alias", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, ".git", "private"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, ".git", "private", "source"), []byte("private"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(root, "source"), []byte("safe"), 0o644))
		require.NoError(t, os.Symlink(filepath.Join(".git", "private", "source"), filepath.Join(root, "git-source")))

		_, err := installHarnessSourceLinks(context.Background(), root, map[string]string{
			"a/new/link":      "source",
			"z/hidden-source": "git-source",
		}, false)
		require.Error(t, err)
		assert.NoDirExists(t, filepath.Join(root, "a"))
	})

	t.Run("destination parent alias", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, ".git", "private"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, "source"), []byte("safe"), 0o644))
		require.NoError(t, os.Symlink(filepath.Join(".git", "private"), filepath.Join(root, "git-parent")))

		_, err := installHarnessSourceLinks(context.Background(), root, map[string]string{
			"a/new/link":      "source",
			"git-parent/link": "source",
		}, false)
		require.Error(t, err)
		assert.NoDirExists(t, filepath.Join(root, "a"))
		assert.NoFileExists(t, filepath.Join(root, ".git", "private", "link"))
	})
}
