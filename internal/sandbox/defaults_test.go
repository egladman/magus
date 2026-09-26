package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/sandbox/env"
	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/egladman/magus/spells"
)

// goDecl and cargoDecl stand in for the go and rust spells' declarations; the real ones
// are tested where the spells load (internal/spell).
var (
	goDecl = spells.Sandbox{
		Allow: []spells.SandboxAllow{
			{Env: "GOROOT", Mode: spells.SandboxAccessRX},
			{Env: "GOCACHE", Base: spells.SandboxBaseUserCache, Path: "go-build", Mode: spells.SandboxAccessRWX},
		},
		Env: spells.SandboxEnv{Passthrough: []string{"GOCACHE", "GOFLAGS"}},
	}
	cargoDecl = spells.Sandbox{Allow: []spells.SandboxAllow{
		{Base: "$CARGO_HOME", Path: "registry", Mode: spells.SandboxAccessRW},
		{Base: spells.SandboxBaseHome, Path: ".cargo/registry", Mode: spells.SandboxAccessRW},
	}}
	miseDecl = spells.Sandbox{Allow: []spells.SandboxAllow{
		{Env: "MISE_DATA_DIR", Base: spells.SandboxBaseXDGData, Path: "mise", Mode: spells.SandboxAccessRX},
	}}
)

// hostOptions is a realistic linux host: a workspace in a linked worktree, a home
// holding secrets and tool caches, a PATH mixing system, home and relative dirs, the go
// and rust spells loaded and mise allowed by the workspace.
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
			"GOROOT=/usr/local/go",
			"GOCACHE=" + root + "/gocache",
			"MISE_DATA_DIR=/opt/mise",
		},
		Sandbox: miseDecl,
		Spells:  map[string]spells.Sandbox{"go": goDecl, "rust": cargoDecl},
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

// The core knows no toolchain. With every toolchain's variable pointing somewhere and
// no declaration loaded, nothing a toolchain keeps is granted and none of those
// variables reaches a child: each arrives only through a spell or the workspace.
func TestBuildPolicyKnowsNoToolchain(t *testing.T) {
	tc := filesystem.ResolveRulePath(t.TempDir())
	home := filepath.Join(tc, "home")
	names := []string{
		"GOROOT", "GOPATH", "GOCACHE", "GOMODCACHE", "GOENV", "GOLANGCI_LINT_CACHE", "GOFLAGS",
		"CARGO_HOME", "RUSTUP_HOME", "PNPM_HOME", "npm_config_cache", "YARN_CACHE_FOLDER", "COREPACK_HOME",
		"PIP_CACHE_DIR", "UV_CACHE_DIR", "MISE_DATA_DIR", "MISE_CACHE_DIR", "MISE_STATE_DIR", "ASDF_DATA_DIR",
		"BUF_CACHE_DIR", "TRIVY_CACHE_DIR",
	}
	environ := []string{"PATH=/usr/bin", "HOME=" + home}
	for _, n := range names {
		environ = append(environ, n+"="+filepath.Join(tc, "tools", n))
	}
	p := BuildPolicy(PolicyOptions{Workspace: filepath.Join(tc, "ws"), Home: home, GOOS: "linux", Environ: environ})

	for _, r := range p.FS.Rules {
		assert.False(t, filesystem.Under(r.Path, filepath.Join(tc, "tools")), "toolchain path granted: %s", r.Path)
		assert.False(t, filesystem.Under(r.Path, home), "home path granted: %s", r.Path)
	}
	for _, n := range names {
		assert.False(t, p.AllowsEnv(n), "%s reaches a child", n)
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
	p := BuildPolicy(o)

	assert.Contains(t, p.BaseEnv, "TMPDIR="+filepath.Join(root, "tmp"))
	assert.Contains(t, p.BaseEnv, "GOCACHE="+root+"/gocache", "the go spell passes GOCACHE through")
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
	o.Sandbox = spells.Sandbox{
		Allow: []spells.SandboxAllow{{Path: extra, Mode: spells.SandboxAccessRW}},
		Env:   spells.SandboxEnv{Passthrough: []string{"MISE_*"}},
	}
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

func TestBuildPolicyMergesDuplicatePaths(t *testing.T) {
	ws := t.TempDir()
	p := BuildPolicy(PolicyOptions{Workspace: ws, Sandbox: spells.Sandbox{Allow: []spells.SandboxAllow{{Path: ws, Mode: spells.SandboxAccessRX}}}})
	var n int
	for _, r := range p.FS.Rules {
		if r.Path == filesystem.ResolveRulePath(ws) {
			n++
			assert.Equal(t, rwx(r.Path), r)
		}
	}
	assert.Equal(t, 1, n)
}

// mergeLayers is the one merge every layer goes through. Entries union, the same path
// at two layers gets both modes, passthrough unions, and an entry reads its variable
// before its base.
func TestMergeLayers(t *testing.T) {
	entry := func(env, base, path string, mode spells.SandboxAccess) spells.SandboxAllow {
		return spells.SandboxAllow{Env: env, Base: base, Path: path, Mode: mode}
	}
	layer := func(pass []string, allow ...spells.SandboxAllow) spells.Sandbox {
		return spells.Sandbox{Allow: allow, Env: spells.SandboxEnv{Passthrough: pass}}
	}
	for name, tc := range map[string]struct {
		vars      map[string]string
		layers    []spells.Sandbox
		want      []filesystem.Rule
		wantNames []string
		wantPfx   []string
	}{
		"no layers": {},
		"rx and rw at two layers make rwx": {
			layers: []spells.Sandbox{
				layer(nil, entry("", "", "/opt/tool", spells.SandboxAccessRX)),
				layer(nil, entry("", "", "/opt/tool", spells.SandboxAccessRW)),
			},
			want: []filesystem.Rule{rwx("/opt/tool")},
		},
		"a later ro layer never narrows": {
			layers: []spells.Sandbox{
				layer(nil, entry("", "", "/opt/tool", spells.SandboxAccessRW)),
				layer(nil, entry("", "", "/opt/tool", spells.SandboxAccessRO)),
			},
			want: []filesystem.Rule{rw("/opt/tool")},
		},
		"an empty mode is ro": {
			layers: []spells.Sandbox{layer(nil, entry("", "", "/opt/tool", ""))},
			want:   []filesystem.Rule{ro("/opt/tool")},
		},
		"the variable wins over the base": {
			vars:   map[string]string{"GOCACHE": "/fast/gocache"},
			layers: []spells.Sandbox{layer(nil, entry("GOCACHE", "userCache", "go-build", spells.SandboxAccessRWX))},
			want:   []filesystem.Rule{rwx("/fast/gocache")},
		},
		"the base when the variable is unset": {
			vars:   map[string]string{"XDG_CACHE_HOME": "/xdg"},
			layers: []spells.Sandbox{layer(nil, entry("GOCACHE", "userCache", "go-build", spells.SandboxAccessRWX))},
			want:   []filesystem.Rule{rwx("/xdg/go-build")},
		},
		"a $VAR base takes the first entry of a list": {
			vars:   map[string]string{"GOPATH": "/a:/b"},
			layers: []spells.Sandbox{layer(nil, entry("", "$GOPATH", "pkg/mod", spells.SandboxAccessRW))},
			want:   []filesystem.Rule{rw("/a/pkg/mod")},
		},
		"an unset $VAR base grants nothing": {
			layers: []spells.Sandbox{layer(nil, entry("", "$CARGO_HOME", "registry", spells.SandboxAccessRW))},
		},
		"a variable naming home, an ancestor or a relative path grants nothing": {
			vars: map[string]string{"GOCACHE": "off", "GOMODCACHE": "/", "XDG_CACHE_HOME": "/home/u"},
			layers: []spells.Sandbox{layer(nil,
				entry("GOCACHE", "userCache", "go-build", spells.SandboxAccessRWX),
				entry("GOMODCACHE", "home", "go/pkg/mod", spells.SandboxAccessRW),
				entry("", "$XDG_CACHE_HOME", "", spells.SandboxAccessRW),
			)},
		},
		"passthrough unions across layers": {
			layers:    []spells.Sandbox{layer([]string{"GOFLAGS"}), layer([]string{"CARGO_HOME", "MISE_*"})},
			wantNames: []string{"GOFLAGS", "CARGO_HOME"},
			wantPfx:   []string{"MISE_*"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := hostDirs{vars: tc.vars, home: "/home/u", goos: "linux"}
			rules, allow := h.mergeLayers(tc.layers...)
			assert.Equal(t, append([]filesystem.Rule{}, tc.want...), rules)
			assert.Equal(t, append(env.DefaultAllow(), tc.wantNames...), allow.Names)
			assert.Equal(t, tc.wantPfx, allow.Prefixes)
		})
	}
}

// userCache and userConfig follow the OS the way Go's os.UserCacheDir does; the xdg
// bases do not.
func TestBasesFollowTheOS(t *testing.T) {
	darwin := hostDirs{vars: map[string]string{"XDG_CACHE_HOME": "/xdg"}, home: "/Users/u", goos: "darwin"}
	assert.Equal(t, "/Users/u/Library/Caches", darwin.base(spells.SandboxAllow{Base: "userCache"}))
	assert.Equal(t, "/Users/u/Library/Application Support", darwin.base(spells.SandboxAllow{Base: "userConfig"}))
	assert.Equal(t, "/xdg", darwin.base(spells.SandboxAllow{Base: "xdgCache"}))
	linux := hostDirs{home: "/home/u", goos: "linux"}
	assert.Equal(t, "/home/u/.cache", linux.base(spells.SandboxAllow{Base: "userCache"}))
	assert.Equal(t, "/home/u/.local/share", linux.base(spells.SandboxAllow{Base: "xdgData"}))
	assert.Equal(t, "/home/u/.local/state", linux.base(spells.SandboxAllow{Base: "xdgState"}))
	assert.Empty(t, hostDirs{goos: "linux"}.base(spells.SandboxAllow{Base: "userCache"}), "no home, no base")
}

// A binRoot base is the install root of a binary on PATH, found through its symlinks,
// and only a real install counts: requires tells a toolchain from a shim of that name.
func TestBinRootFindsTheInstallBehindPath(t *testing.T) {
	dir := filesystem.ResolveRulePath(t.TempDir())
	install := filepath.Join(dir, "go")
	for _, d := range []string{"go/bin", "go/pkg/tool", "links", "shims"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, d), 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(install, "bin/go"), []byte("#!/bin/sh\n"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join(install, "bin/go"), filepath.Join(dir, "links/go")))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "shims/go"), []byte("#!/bin/sh\n"), 0o755))

	goroot := spells.SandboxAllow{Base: "binRoot", Bin: "go", Requires: "pkg/tool", Mode: spells.SandboxAccessRX}
	resolve := func(path string) []filesystem.Rule {
		rules, _ := hostDirs{vars: map[string]string{"PATH": path}, goos: "linux"}.mergeLayers(spells.Sandbox{Allow: []spells.SandboxAllow{goroot}})
		return rules
	}
	assert.Equal(t, []filesystem.Rule{rx(install)}, resolve(filepath.Join(dir, "links")), "through a symlink")
	assert.Empty(t, resolve(filepath.Join(dir, "shims")), "a shim outside a bin/ has no root")
	require.NoError(t, os.Remove(filepath.Join(install, "pkg/tool")))
	assert.Empty(t, resolve(filepath.Join(install, "bin")), "a root without pkg/tool is not a Go install")
}

// Scoped keeps the core and the workspace layer, cuts the spell layer to the named
// spells and adds the target's declaration. The go spell's cache and environment reach
// only a scope that includes it.
func TestScopedCutsTheSpellLayer(t *testing.T) {
	o, root := hostOptions(t)
	p := BuildPolicy(o)
	gocache := filepath.Join(root, "gocache/ab/x")
	registry := filepath.Join(o.Home, ".cargo/registry/x.crate")

	goOnly := p.Scoped([]string{"go"}, nil)
	assert.NoError(t, check(goOnly, filesystem.Exec, gocache))
	assert.ErrorIs(t, check(goOnly, filesystem.Write, registry), filesystem.ErrDenied)
	assert.True(t, goOnly.AllowsEnv("GOFLAGS"))
	assert.NoError(t, check(goOnly, filesystem.Exec, "/opt/mise/installs/x"), "the workspace layer stays")

	rustOnly := p.Scoped([]string{"rust"}, nil)
	assert.ErrorIs(t, check(rustOnly, filesystem.Read, gocache), filesystem.ErrDenied)
	assert.NoError(t, check(rustOnly, filesystem.Write, registry))
	assert.False(t, rustOnly.AllowsEnv("GOFLAGS"))
	assert.NotContains(t, rustOnly.BaseEnv, "GOCACHE="+root+"/gocache")

	target := &spells.Sandbox{Allow: []spells.SandboxAllow{{Path: filepath.Join(root, "fixtures"), Mode: spells.SandboxAccessRW}}}
	withTarget := p.Scoped([]string{"rust"}, target)
	assert.NoError(t, check(withTarget, filesystem.Write, filepath.Join(root, "fixtures/x")))
	assert.NoError(t, check(withTarget, filesystem.Write, registry))
	assert.ErrorIs(t, check(p, filesystem.Write, filepath.Join(root, "fixtures/x")), filesystem.ErrDenied,
		"a target's declaration is its own")

	assert.Same(t, goOnly, p.Scoped([]string{"go", "go"}, nil), "memoized by the sorted names")
	assert.Same(t, goOnly, rustOnly.Scoped([]string{"go"}, nil), "built from every spell, whatever the receiver kept")
	all := p.Scoped(nil, nil)
	assert.NoError(t, check(all, filesystem.Exec, gocache))
	assert.NoError(t, check(all, filesystem.Write, registry))
}

// A lease-narrowed policy stays narrowed through Scoped.
func TestScopedKeepsTheLeaseBoundary(t *testing.T) {
	o, root := hostOptions(t)
	b := &leaseBoundary{id: "l1", root: filesystem.ResolveRulePath(o.Workspace), granted: []string{filepath.Join(o.Workspace, "pkg")}}
	p := b.apply(BuildPolicy(o))
	scoped := p.Scoped([]string{"go"}, nil)
	assert.ErrorIs(t, check(scoped, filesystem.Write, filepath.Join(root, "ws/main.go")), filesystem.ErrDenied)
	assert.NoError(t, check(scoped, filesystem.Write, filepath.Join(root, "ws/pkg/x.go")))
	assert.Equal(t, "l1", scoped.Lease)
}

// A step records its project's spells: a spell op started under it gets those and its
// own spell's grants, the step's own processes keep every spell's, and without a
// recorded step nothing is narrowed.
func TestScopeToSpellNarrowsOnlyUnderAStep(t *testing.T) {
	o, root := hostOptions(t)
	p := BuildPolicy(o)
	gocache := filepath.Join(root, "gocache/ab/x")
	ctx := WithPolicy(context.Background(), p)

	assert.Same(t, p, PolicyFromContext(ScopeToSpell(ctx, "rust")), "no step, no narrowing")

	step := WithStep(ctx, []string{"rust"}, nil)
	assert.NoError(t, check(PolicyFromContext(step), filesystem.Exec, gocache), "the step's own processes keep every spell")
	assert.ErrorIs(t, check(PolicyFromContext(ScopeToSpell(step, "rust")), filesystem.Read, gocache), filesystem.ErrDenied)
	assert.NoError(t, check(PolicyFromContext(ScopeToSpell(step, "go")), filesystem.Exec, gocache), "an op gets its own spell")

	target := &spells.Sandbox{Allow: []spells.SandboxAllow{{Path: filepath.Join(root, "fixtures"), Mode: spells.SandboxAccessRW}}}
	targetStep := WithStep(ctx, []string{"rust"}, target)
	for _, q := range []*Policy{PolicyFromContext(targetStep), PolicyFromContext(ScopeToSpell(targetStep, "rust"))} {
		assert.NoError(t, check(q, filesystem.Write, filepath.Join(root, "fixtures/x")), "the target layer reaches both")
	}
	inner := WithStep(targetStep, []string{"go"}, nil)
	assert.ErrorIs(t, check(PolicyFromContext(inner), filesystem.Write, filepath.Join(root, "fixtures/x")), filesystem.ErrDenied,
		"a step reached from inside another does not inherit its target's grants")
}

// A spell's or a target's declaration that no host could honor is refused with every
// bad entry named.
func TestCheckDeclarationRefuses(t *testing.T) {
	for name, sb := range map[string]spells.Sandbox{
		"unknown mode":      {Allow: []spells.SandboxAllow{{Base: "home", Path: ".x", Mode: "RW"}}},
		"unknown base":      {Allow: []spells.SandboxAllow{{Base: "tmp", Path: "x"}}},
		"escaping path":     {Allow: []spells.SandboxAllow{{Base: "xdgCache", Path: "../x"}}},
		"absolute sub-path": {Allow: []spells.SandboxAllow{{Base: "xdgCache", Path: "/x"}}},
		"whole base":        {Allow: []spells.SandboxAllow{{Base: "userCache"}}},
		"empty entry":       {Allow: []spells.SandboxAllow{{Mode: spells.SandboxAccessRW}}},
		"bad env":           {Allow: []spells.SandboxAllow{{Env: "GO*", Base: "home", Path: "go"}}},
		"bad var base":      {Allow: []spells.SandboxAllow{{Base: "$", Path: "x"}}},
		"binRoot path bin":  {Allow: []spells.SandboxAllow{{Base: "binRoot", Bin: "/usr/bin/go"}}},
		"variable in path":  {Allow: []spells.SandboxAllow{{Path: "$HOME/.cache"}}},
		"relative literal":  {Allow: []spells.SandboxAllow{{Path: "build"}}},
		"bad passthrough":   {Env: spells.SandboxEnv{Passthrough: []string{"GO*"}}},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Error(t, CheckDeclaration(sb))
		})
	}
	assert.NoError(t, CheckDeclaration(spells.Sandbox{
		Allow: []spells.SandboxAllow{
			{Env: "GOROOT", Mode: spells.SandboxAccessRX},
			{Env: "GOMODCACHE", Base: "$GOPATH", Path: "pkg/mod", Mode: spells.SandboxAccessRW},
			{Base: "binRoot", Bin: "go", Requires: "pkg/tool", Mode: spells.SandboxAccessRX},
			{Path: "/opt/tool", Mode: spells.SandboxAccessRX},
			{Path: "~/.local/share/tool"},
		},
		Env: spells.SandboxEnv{Passthrough: []string{"GOFLAGS", "MISE_*"}},
	}))
}
