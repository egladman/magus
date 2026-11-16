package server

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// TestServerChildArgsDropsStart pins the detached server's argv: its role, with no
// `start` in ps, and every flag the person passed so it binds where the parent waits.
func TestServerChildArgsDropsStart(t *testing.T) {
	assert.Equal(t, []string{"server", "--foreground"}, ServerChildArgs([]string{"server", "start"}))
	assert.Equal(t,
		[]string{"--server-address", "unix:///tmp/m.sock", "server", "--foreground", "-v"},
		ServerChildArgs([]string{"--server-address", "unix:///tmp/m.sock", "server", "start", "-v"}))
}

func TestIsServerRun(t *testing.T) {
	assert.True(t, IsServerRun([]string{"start"}))
	assert.True(t, IsServerRun([]string{"start", "--foreground"}))
	assert.True(t, IsServerRun([]string{"--foreground"}), "the detached child runs the server")
	assert.False(t, IsServerRun([]string{"start", "-h"}), "help prints usage and builds nothing")
	assert.False(t, IsServerRun([]string{"stop"}))
	assert.False(t, IsServerRun(nil))
}

func TestIsServerStartHelpSkipsTheSubcommand(t *testing.T) {
	assert.True(t, IsServerStartHelp([]string{"start", "-h"}))
	assert.True(t, IsServerStartHelp([]string{"start", "--foreground", "--help"}))
	assert.True(t, IsServerStartHelp([]string{"start", "help"}))
	assert.False(t, IsServerStartHelp([]string{"start"}))
	assert.False(t, IsServerStartHelp([]string{"start", "--foreground"}))
	assert.False(t, IsServerStartHelp([]string{"help"}), "the subcommand itself is not a help flag")
}

func TestWantsForeground(t *testing.T) {
	assert.True(t, WantsForeground([]string{"start", "--foreground"}))
	assert.True(t, WantsForeground([]string{"start", "-foreground"}))
	// A pre-parse scanner takes only the bare spellings; an =value form is not a bool.
	assert.False(t, WantsForeground([]string{"start", "--foreground=true"}))
	assert.False(t, WantsForeground([]string{"start"}))
}

// The message exists because "already running" answered a question nobody asked. A second
// worktree's `server start` returns 0 having loaded nothing from that tree, and the console then
// shows the tree the server was started in, which reads as success.
func TestServingSuffixNamesTheLoadedWorkspaces(t *testing.T) {
	st := &proc.StatusReply{Workspaces: []proc.Workspace{
		{Root: "/repo/worktrees/b"},
		{Root: "/repo"},
	}}

	// Sorted, so two runs of the same server do not print the list two ways.
	assert.Equal(t, ", serving /repo, /repo/worktrees/b", ServingSuffix(st))

	// A server that has loaded nothing yet says nothing rather than "serving " with an empty
	// list, which would read as a server that is serving something unnameable.
	assert.Empty(t, ServingSuffix(&proc.StatusReply{}))
}

func hookSections(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, err := os.ReadDir(filepath.Join(dir, ".git", "hooks"))
	require.NoError(t, err)
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, ".git", "hooks", e.Name()))
		require.NoError(t, err)
		if strings.Contains(string(data), "# BEGIN magus-") {
			out[e.Name()] = string(data)
		}
	}
	return out
}

func captureNotices(t *testing.T) *strings.Builder {
	t.Helper()
	var buf strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestRefreshVCSHooksInstallsNothingWithoutAFirstInstall(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, exec.Command("git", "init", "-q", dir).Run())
	t.Chdir(dir)
	logs := captureNotices(t)

	RefreshVCSHooks(t.Context())

	assert.Empty(t, hookSections(t, dir), "server start wrote a hook into a repository that had none")
	assert.Equal(t, 1, strings.Count(logs.String(), "magus init --vcs git"), "want one notice naming the command:\n%s", logs)
}

func TestRefreshVCSHooksRewritesAnOlderSection(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, exec.Command("git", "init", "-q", dir).Run())
	t.Chdir(dir)
	hook := filepath.Join(dir, ".git", "hooks", "post-checkout")
	older := "#!/bin/sh\n\n# BEGIN magus-refresh - do not edit this section manually\n" +
		"[ \"$3\" = \"1\" ] || exit 0\nold-magus job run sync-graph >/dev/null 2>&1 || true\n# END magus-refresh\n"
	require.NoError(t, os.WriteFile(hook, []byte(older), 0o755))
	logs := captureNotices(t)

	RefreshVCSHooks(t.Context())

	data, err := os.ReadFile(hook)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "old-magus", "the older refresh command survived")
	assert.Contains(t, string(data), "# BEGIN magus-refresh")
	assert.NotContains(t, hookSections(t, dir), "post-commit", "server start first-installed the drift hook")
	assert.NotContains(t, logs.String(), "magus init --vcs", "a repository with hooks gets no first-install notice")
}

func TestRefreshVCSHooksRewritesADriftSectionAlone(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, exec.Command("git", "init", "-q", dir).Run())
	t.Chdir(dir)
	res, err := vcs.Resolve(t.Context(), dir, "", types.VCSOptions{})
	require.NoError(t, err)
	_, err = res.VCS.InstallDriftHook(t.Context(), dir, "old-magus job run check-drift")
	require.NoError(t, err)
	_, ok, err := vcs.ReadGitDriftHook(t.Context(), dir)
	require.NoError(t, err)
	require.True(t, ok, "the fixture must read as an installed drift section")
	logs := captureNotices(t)

	RefreshVCSHooks(t.Context())

	data, err := os.ReadFile(filepath.Join(dir, ".git", "hooks", "post-commit"))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "old-magus", "the older drift command survived")
	assert.NotContains(t, hookSections(t, dir), "post-checkout", "server start first-installed the refresh hook")
	assert.NotContains(t, logs.String(), "magus init --vcs", "a repository with hooks gets no first-install notice")
}

func TestRefreshVCSHooksRefreshesHgFamilySections(t *testing.T) {
	for _, tc := range []struct {
		prog, config           string
		readRefresh, readDrift func(string) (string, bool, error)
	}{
		{"hg", filepath.Join(".hg", "hgrc"), vcs.ReadHgRefreshHook, vcs.ReadHgDriftHook},
		{"sl", filepath.Join(".sl", "config"), vcs.ReadSaplingRefreshHook, vcs.ReadSaplingDriftHook},
	} {
		t.Run(tc.prog, func(t *testing.T) {
			if _, err := exec.LookPath(tc.prog); err != nil {
				t.Skip(tc.prog + " not available")
			}
			t.Setenv("HGRCPATH", "")
			t.Setenv("HGPLAIN", "1")
			dir := t.TempDir()
			require.NoError(t, exec.Command(tc.prog, "init", dir).Run())
			t.Chdir(dir)
			logs := captureNotices(t)

			RefreshVCSHooks(t.Context())

			_, refresh, err := tc.readRefresh(dir)
			require.NoError(t, err)
			_, drift, err := tc.readDrift(dir)
			require.NoError(t, err)
			assert.False(t, refresh || drift, "server start first-installed a hook")
			assert.Equal(t, 1, strings.Count(logs.String(), "magus init --vcs "+tc.prog), "want one notice naming the command:\n%s", logs)

			res, err := vcs.Resolve(t.Context(), dir, "", types.VCSOptions{})
			require.NoError(t, err)
			_, err = res.VCS.InstallDriftHook(t.Context(), dir, "old-magus job run check-drift")
			require.NoError(t, err)
			_, drift, err = tc.readDrift(dir)
			require.NoError(t, err)
			require.True(t, drift, "the fixture must read as an installed drift section")

			RefreshVCSHooks(t.Context())

			data, err := os.ReadFile(filepath.Join(dir, tc.config))
			require.NoError(t, err)
			assert.NotContains(t, string(data), "old-magus", "the older drift command survived")
			_, refresh, err = tc.readRefresh(dir)
			require.NoError(t, err)
			assert.False(t, refresh, "server start first-installed the refresh hook")
		})
	}
}
