package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/sandbox/env"
	"github.com/egladman/magus/internal/sandbox/filesystem"
)

// hostOptions is a realistic linux host: a workspace in a linked worktree, a home
// holding secrets and tool caches, and a PATH mixing system, home and relative dirs.
func hostOptions(t *testing.T) (PolicyOptions, string) {
	t.Helper()
	root := filesystem.ResolveRulePath(t.TempDir())
	home := filepath.Join(root, "home", "u")
	for _, d := range []string{"ws", "cache", "tmp", "repo/.git/worktrees/ws", "home/u/.ssh", "home/u/.cargo/registry", "home/u/.local/bin"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, d), 0o755))
	}
	return PolicyOptions{
		Workspace:    filepath.Join(root, "ws"),
		CacheDir:     filepath.Join(root, "cache"),
		TempDir:      filepath.Join(root, "tmp"),
		Executable:   "/usr/local/bin/magus",
		GitDir:       filepath.Join(root, "repo/.git/worktrees/ws"),
		GitCommonDir: filepath.Join(root, "repo/.git"),
		Home:         home,
		GOOS:         "linux",
		Environ: []string{
			"PATH=/usr/bin:" + home + "/.local/bin:" + home + ":.:/",
			"HOME=" + home,
			"TMPDIR=/tmp/shared",
			"GITHUB_TOKEN=ghp_secret",
			"GOCACHE=" + root + "/gocache",
			"MISE_DATA_DIR=/opt/mise",
		},
		InstallDirs: []string{"/usr/local/go"},
	}, root
}

func check(p *Policy, access filesystem.Access, path string) error {
	return p.FS.Check(path, access)
}

// The grant list, access by access. Home is the boundary that matters: tool caches
// under it are granted one by one, and nothing else there is.
func TestBuildPolicyGrants(t *testing.T) {
	o, root := hostOptions(t)
	p := BuildPolicy(o)
	home := o.Home

	for _, tc := range []struct {
		path   string
		access filesystem.Access
		want   bool
	}{
		{filepath.Join(root, "ws/main.go"), filesystem.Write, true},
		{filepath.Join(root, "ws/bin/tool"), filesystem.Exec, true},
		{filepath.Join(root, "cache/x"), filesystem.Write, true},
		{filepath.Join(root, "cache/x"), filesystem.Exec, false},
		{filepath.Join(root, "tmp/go-build1/a.test"), filesystem.Exec, true},
		{filepath.Join(root, "gocache/eb/eba0-d/magus-utils"), filesystem.Exec, true},
		{"/usr/bin/git", filesystem.Exec, true},
		{"/usr/lib/git-core/git-remote-http", filesystem.Exec, true},
		{"/usr/lib/libc.so.6", filesystem.Write, false},
		{"/bin/sh", filesystem.Exec, true},
		{"/usr/local/go/pkg/tool/linux_amd64/compile", filesystem.Exec, true},
		{"/opt/mise/installs/go/1.25/bin/go", filesystem.Exec, true},
		{"/etc/ssl/certs/ca.pem", filesystem.Read, true},
		{"/etc/hosts", filesystem.Write, false},
		{"/dev/null", filesystem.Write, true},
		{filepath.Join(root, "repo/.git/worktrees/ws/index"), filesystem.Write, true},
		{filepath.Join(root, "repo/.git/objects/ab/cdef"), filesystem.Write, true},
		{filepath.Join(root, "repo/.git/config"), filesystem.Read, true},
		{filepath.Join(root, "repo/.git/config"), filesystem.Write, false},
		{filepath.Join(root, "repo/.git/hooks/pre-commit"), filesystem.Exec, false},
		{filepath.Join(root, "gocache/ab/x-d"), filesystem.Write, true},
		{filepath.Join(home, ".cargo/registry/cache/x.crate"), filesystem.Write, true},
		{filepath.Join(home, ".cargo/credentials.toml"), filesystem.Read, false},
		{filepath.Join(home, ".local/bin/tool"), filesystem.Exec, true},
		{filepath.Join(home, ".ssh/id_ed25519"), filesystem.Read, false},
		{filepath.Join(home, ".aws/credentials"), filesystem.Read, false},
		{filepath.Join(home, ".config/gh/hosts.yml"), filesystem.Read, false},
		{filepath.Join(home, "notes.txt"), filesystem.Read, false},
		{"/tmp/shared/ssh-agent.sock", filesystem.Read, false},
		{os.TempDir(), filesystem.Write, false},
		{"/var/tmp/x", filesystem.Write, false},
		{"/proc/self/environ", filesystem.Read, false},
		{"/root/.bashrc", filesystem.Read, false},
	} {
		err := check(p, tc.access, tc.path)
		if tc.want {
			assert.NoError(t, err, "%s %s", tc.access, tc.path)
		} else {
			assert.ErrorIs(t, err, filesystem.ErrDenied, "%s %s", tc.access, tc.path)
		}
	}
}

// PATH entries grant exec one directory at a time. Home and its ancestors (/ among
// them) are skipped, and so is a relative entry, or a PATH with ~ on it would grant
// the whole home tree.
func TestBuildPolicyPathEntries(t *testing.T) {
	o, _ := hostOptions(t)
	rules := pathRules("/usr/bin:"+o.Home+"/.local/bin:"+o.Home+":.:/:bin:/opt/x/", o.Home)
	assert.Equal(t, []filesystem.Rule{rx("/usr/bin"), rx(o.Home + "/.local/bin"), rx("/opt/x")}, rules)
}

// A child's environment is the host's, scrubbed, with TMPDIR moved to the private
// temp dir: the shared one holds other programs' sockets.
func TestBuildPolicyBaseEnv(t *testing.T) {
	o, root := hostOptions(t)
	o.Env = env.Allowlist{Names: []string{"GOCACHE"}}
	p := BuildPolicy(o)

	assert.Contains(t, p.BaseEnv, "TMPDIR="+filepath.Join(root, "tmp"))
	assert.Contains(t, p.BaseEnv, "GOCACHE="+root+"/gocache")
	assert.NotContains(t, p.BaseEnv, "TMPDIR=/tmp/shared")
	for _, kv := range p.BaseEnv {
		assert.False(t, strings.HasPrefix(kv, "GITHUB_TOKEN="), "secret leaked: %s", kv)
	}
	assert.Contains(t, p.EnvDropped, "GITHUB_TOKEN")
	assert.Equal(t, filepath.Join(root, "tmp"), p.TempDir)
}

// A policy's BaseEnv is never nil, so a child never falls back to the host's
// environment, however bare the host is.
func TestBuildPolicyBaseEnvIsNeverNil(t *testing.T) {
	p := BuildPolicy(PolicyOptions{})
	assert.NotNil(t, p.BaseEnv)
	assert.Empty(t, p.BaseEnv)
}

func TestBuildPolicyKeepsUserRulesAndPassthrough(t *testing.T) {
	o, root := hostOptions(t)
	extra := filepath.Join(root, "data")
	o.Allow = []filesystem.Rule{{Path: extra, Read: true, Write: true}}
	o.Env = env.Allowlist{Prefixes: []string{"MISE_*"}}
	p := BuildPolicy(o)

	assert.NoError(t, check(p, filesystem.Write, filepath.Join(extra, "out")))
	assert.ErrorIs(t, check(p, filesystem.Exec, filepath.Join(extra, "tool")), filesystem.ErrDenied,
		"a rw entry grants no exec")
	assert.True(t, p.AllowsEnv("MISE_DATA_DIR"))
	assert.False(t, p.AllowsEnv("GITHUB_TOKEN"))
}

// A rule path is resolved like a checked path, so a workspace reached through a
// symlink still matches its own rule.
func TestBuildPolicyResolvesRulePaths(t *testing.T) {
	realDir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(realDir, link))

	p := BuildPolicy(PolicyOptions{Workspace: link})
	assert.NoError(t, check(p, filesystem.Read, filepath.Join(realDir, "file.txt")))
}

func TestBuildPolicyDefaultCachesFollowTheOS(t *testing.T) {
	darwin := toolRules(map[string]string{}, "/Users/u", "darwin")
	linux := toolRules(map[string]string{"XDG_CACHE_HOME": "/xdg"}, "/home/u", "linux")
	assert.Contains(t, darwin, rwx("/Users/u/Library/Caches/go-build"))
	assert.Contains(t, linux, rwx("/xdg/go-build"))
	assert.Contains(t, linux, rw("/home/u/go/pkg/mod"))

	// No home and no variables: nothing under a relative path.
	assert.Empty(t, toolRules(map[string]string{}, "", "linux"))

	// A variable naming home, an ancestor of it, or a relative path grants nothing.
	for _, r := range toolRules(map[string]string{"GOCACHE": "off", "XDG_CACHE_HOME": "/home/u", "GOMODCACHE": "/"}, "/home/u", "linux") {
		assert.NotContains(t, []string{"off", "/home/u", "/"}, r.Path)
		assert.True(t, filepath.IsAbs(r.Path), "relative rule %q", r.Path)
	}
}

func TestBuildPolicyMergesDuplicatePaths(t *testing.T) {
	ws := t.TempDir()
	p := BuildPolicy(PolicyOptions{Workspace: ws, Allow: []filesystem.Rule{rx(ws)}})
	var n int
	for _, r := range p.FS.Rules {
		if r.Path == filesystem.ResolveRulePath(ws) {
			n++
			assert.Equal(t, rwx(r.Path), r)
		}
	}
	assert.Equal(t, 1, n)
}
