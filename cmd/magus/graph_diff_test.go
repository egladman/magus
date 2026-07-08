package main

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tarball builds an in-memory tar stream from name->content entries (a directory entry
// has an empty content and a trailing slash in name).
func tarball(t *testing.T, entries map[string]string) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, content := range entries {
		if content == "" && name[len(name)-1] == '/' {
			require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0o755}))
			continue
		}
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content))}))
		_, err := tw.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	return bytes.NewReader(buf.Bytes())
}

func TestExtractTar(t *testing.T) {
	dst := t.TempDir()
	err := extractTar(tarball(t, map[string]string{
		"magus.yaml":       "version: 1\n",
		"pkg/":             "",
		"pkg/service.buzz": "target build {}\n",
		"docs/readme.md":   "# hi\n",
	}), dst)
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(dst, "pkg", "service.buzz"))
	require.NoError(t, err)
	assert.Equal(t, "target build {}\n", string(got))
	got, err = os.ReadFile(filepath.Join(dst, "magus.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "version: 1\n", string(got))
}

// TestExtractTarRejectsEscape locks in the defense-in-depth guard: a crafted entry whose
// path escapes the destination is refused rather than written outside dst.
func TestExtractTarRejectsEscape(t *testing.T) {
	dst := t.TempDir()
	err := extractTar(tarball(t, map[string]string{"../escape.txt": "pwned"}), dst)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes the destination")
	_, statErr := os.Stat(filepath.Join(filepath.Dir(dst), "escape.txt"))
	assert.True(t, os.IsNotExist(statErr), "escaping entry must not be written")
}

// gitInit makes a throwaway repo at dir with the given files committed, returning nothing
// (t fatals on any git error). Skips the whole test if git is unavailable.
func gitInit(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	}
	run("init", "-q")
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	run("add", "-A")
	run("commit", "-q", "-m", "init")
}

// TestGitArchiveTo exercises the real git-archive -> tar -> temp-tree path against a
// throwaway repo, including the subdir re-rooting (a magus root nested below the git root).
func TestGitArchiveTo(t *testing.T) {
	repo := t.TempDir()
	gitInit(t, repo, map[string]string{
		"magus.yaml":       "version: 1\n",
		"pkg/service.buzz": "target build {}\n",
		"sub/proj/app.txt": "nested\n",
	})
	ctx := context.Background()

	// From the git root: the whole tree is archived, re-rooted at repo.
	dst := t.TempDir()
	require.NoError(t, gitArchiveTo(ctx, repo, "HEAD", dst))
	got, err := os.ReadFile(filepath.Join(dst, "pkg", "service.buzz"))
	require.NoError(t, err)
	assert.Equal(t, "target build {}\n", string(got))

	// From a subdir (a nested root): only that subtree is archived, re-rooted so the
	// subdir's own files sit at the destination top level (app.txt, not sub/proj/app.txt).
	sub := filepath.Join(repo, "sub", "proj")
	dstSub := t.TempDir()
	require.NoError(t, gitArchiveTo(ctx, sub, "HEAD", dstSub))
	got, err = os.ReadFile(filepath.Join(dstSub, "app.txt"))
	require.NoError(t, err)
	assert.Equal(t, "nested\n", string(got))
	_, statErr := os.Stat(filepath.Join(dstSub, "magus.yaml"))
	assert.True(t, os.IsNotExist(statErr), "subdir archive must not include repo-root files")
}

// TestGitArchiveToBadRev reports a clear error (not a panic or hang) for an unknown rev.
func TestGitArchiveToBadRev(t *testing.T) {
	repo := t.TempDir()
	gitInit(t, repo, map[string]string{"magus.yaml": "version: 1\n"})
	err := gitArchiveTo(context.Background(), repo, "no-such-rev", t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no-such-rev")
}
