package vcs

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tarball builds an in-memory tar stream from name->content entries (a directory entry
// has empty content and a trailing slash in name).
func tarball(t *testing.T, entries map[string]string) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, content := range entries {
		if content == "" && strings.HasSuffix(name, "/") {
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

// gitInitRepo makes a throwaway repo at dir with files committed. Skips if git is absent.
func gitInitRepo(t *testing.T, dir string, files map[string]string) {
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

// TestExportRevision exercises the real git-archive -> tar -> temp-tree path, including
// the subdir re-rooting (a workspace root nested below the git root).
func TestExportRevision(t *testing.T) {
	repo := t.TempDir()
	gitInitRepo(t, repo, map[string]string{
		"magus.yaml":       "version: 1\n",
		"pkg/service.buzz": "target build {}\n",
		"sub/proj/app.txt": "nested\n",
	})
	ctx := context.Background()

	// From the git root: the whole tree is exported, re-rooted at repo.
	dst := t.TempDir()
	require.NoError(t, gitVCS{}.ExportRevision(ctx, repo, "HEAD", dst))
	got, err := os.ReadFile(filepath.Join(dst, "pkg", "service.buzz"))
	require.NoError(t, err)
	assert.Equal(t, "target build {}\n", string(got))

	// From a subdir: only that subtree is exported, re-rooted so the subdir's own files
	// sit at the destination top level (app.txt, not sub/proj/app.txt).
	sub := filepath.Join(repo, "sub", "proj")
	dstSub := t.TempDir()
	require.NoError(t, gitVCS{}.ExportRevision(ctx, sub, "HEAD", dstSub))
	got, err = os.ReadFile(filepath.Join(dstSub, "app.txt"))
	require.NoError(t, err)
	assert.Equal(t, "nested\n", string(got))
	_, statErr := os.Stat(filepath.Join(dstSub, "magus.yaml"))
	assert.True(t, os.IsNotExist(statErr), "subdir export must not include repo-root files")
}

// TestExportRevisionBadRev reports a clear error (not a panic or hang) for an unknown rev.
func TestExportRevisionBadRev(t *testing.T) {
	repo := t.TempDir()
	gitInitRepo(t, repo, map[string]string{"magus.yaml": "version: 1\n"})
	err := gitVCS{}.ExportRevision(context.Background(), repo, "no-such-rev", t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no-such-rev")
}

func TestWriteManagedHookNewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "post-checkout")
	changed, err := writeManagedHook(path, gitHookBody("post-checkout", "magus server sync"))
	require.NoError(t, err)
	assert.True(t, changed)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	s := string(body)
	assert.True(t, strings.HasPrefix(s, "#!/bin/sh"), "a new hook gets a shebang")
	assert.Contains(t, s, gitHookBegin)
	assert.Contains(t, s, "magus server sync")
	assert.Contains(t, s, `[ "$3" = "1" ]`, "post-checkout guards on the branch-checkout flag")

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&0o100, "the hook is executable")
}

func TestWriteManagedHookIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "post-merge")
	_, err := writeManagedHook(path, gitHookBody("post-merge", "magus server sync"))
	require.NoError(t, err)

	changed, err := writeManagedHook(path, gitHookBody("post-merge", "magus server sync"))
	require.NoError(t, err)
	assert.False(t, changed, "re-installing an unchanged section is a no-op")
}

func TestWriteManagedHookPreservesUserContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "post-rewrite")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho 'my own hook'\n"), 0o755))

	changed, err := writeManagedHook(path, gitHookBody("post-rewrite", "magus server sync"))
	require.NoError(t, err)
	assert.True(t, changed)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	s := string(body)
	assert.Contains(t, s, "echo 'my own hook'", "the user's hook body is preserved")
	assert.Contains(t, s, gitHookBegin, "the managed section is appended")
}

// TestParseChangesByCommit verifies the NUL-delimited `git log --name-only` parse:
// a NUL line opens a commit (hash, author, date); following non-empty lines are files.
func TestParseChangesByCommit(t *testing.T) {
	out := "\x00abc123\x00Ada\x002026-06-20T10:00:00Z\n\napi/main.go\napi/util.go\n" +
		"\x00def456\x00Babbage\x002026-06-19T09:00:00Z\n\nweb/app.ts\n"

	got := parseChangesByCommit(out)
	require.Len(t, got, 2)

	assert.Equal(t, "abc123", got[0].ID)
	assert.Equal(t, "Ada", got[0].Author)
	assert.Equal(t, time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC), got[0].Date.UTC())
	assert.Equal(t, []string{"api/main.go", "api/util.go"}, got[0].Files)

	assert.Equal(t, "def456", got[1].ID)
	assert.Equal(t, "Babbage", got[1].Author)
	assert.Equal(t, []string{"web/app.ts"}, got[1].Files)
}

// TestParseChangesByCommitEmpty covers a commit that touched no files and a bad date.
func TestParseChangesByCommitEmpty(t *testing.T) {
	got := parseChangesByCommit("\x00abc123\x00Ada\x00not-a-date\n\n")
	require.Len(t, got, 1)
	assert.Equal(t, "abc123", got[0].ID)
	assert.True(t, got[0].Date.IsZero(), "unparseable date is zero, not an error")
	assert.Empty(t, got[0].Files)
}
