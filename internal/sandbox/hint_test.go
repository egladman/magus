package sandbox

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/sandbox/env"
	"github.com/egladman/magus/internal/sandbox/filesystem"
)

func TestDenyHint(t *testing.T) {
	t.Parallel()

	got := denyHint("ro", "/usr/bin/curl")
	assert.Contains(t, got, "sandbox blocked access to /usr/bin/curl")
	assert.Contains(t, got, "magus config set key=sandbox.allow.curl.path,value=/usr/bin/curl")
	// ro must not emit a mode command (mode defaults to ro).
	assert.NotContains(t, got, "mode", "denyHint(ro) should not set mode")

	w := denyHint("rw", "/data/out")
	assert.Contains(t, w, "sandbox.allow.out.path,value=/data/out")
	assert.Contains(t, w, "sandbox.allow.out.mode,value=rw")
}

// TestEmitDenyHint verifies the hint reaches stderr as a "hint:" line, and that
// the hints toggle silences it. Not parallel: it redirects os.Stderr and flips
// the process-wide hints switch.
func TestEmitDenyHint(t *testing.T) {
	capture := func() string {
		r, w, err := os.Pipe()
		require.NoError(t, err)
		orig := os.Stderr
		os.Stderr = w
		EmitDenyHint("ro", "/usr/bin/curl")
		os.Stderr = orig
		_ = w.Close()
		out, _ := io.ReadAll(r)
		return string(out)
	}

	interactive.SetEnabled(true)
	defer interactive.SetEnabled(true)
	got := capture()
	assert.Contains(t, got, "hint:")
	assert.Contains(t, got, "magus config set key=sandbox.allow.curl.path,value=/usr/bin/curl")

	interactive.SetEnabled(false)
	assert.Empty(t, capture(), "EmitDenyHint should be silent when hints are disabled")
}

// TestApplyNonLinuxReportsUnsupported verifies the !linux build tag's stub
// returns ErrUnsupported so the caller can fall back. Compiled on every
// platform; the assertion is only meaningful off-Linux but the call must
// not panic on Linux either.
func TestApplyNonLinuxReportsUnsupported(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("Linux uses the real landlock implementation")
	}
	p := BuildPolicy(t.TempDir(), nil, nil, nil, nil)
	assert.ErrorIs(t, Apply(p), ErrUnsupported)
}

// TestSupportedConsistentWithApply ensures Supported() does not lie:
// a true return must mean Apply has a chance to succeed, and false must
// guarantee Apply returns ErrUnsupported.
func TestSupportedConsistentWithApply(t *testing.T) {
	if Supported() {
		return
	}
	// Supported() == false: Apply must report ErrUnsupported, never a
	// success and never a different error type.
	p := BuildPolicy(t.TempDir(), nil, nil, nil, nil)
	assert.ErrorIs(t, Apply(p), ErrUnsupported)
}

// TestApplyNilPolicyNoop confirms calling Apply with a nil policy is a
// no-op on every platform — needed so that the orchestrator can call
// Apply unconditionally without first checking cfg.Sandbox.Enabled.
func TestApplyNilPolicyNoop(t *testing.T) {
	assert.NoError(t, Apply(nil), "Apply(nil) should be a no-op")
}

// TestTmpRuleNotExecutable verifies the default temp-dir rule grants read+write
// but NOT execute. On a multiuser host the temp dir is world-shared, so granting
// execve there would let a confined spell run payloads planted by other users.
func TestTmpRuleNotExecutable(t *testing.T) {
	if os.TempDir() == "" {
		t.Skip("no temp dir on this platform")
	}
	p := BuildPolicy(t.TempDir(), nil, nil, nil, nil)
	tmp := filesystem.ResolveRulePath(os.TempDir())

	var found bool
	for _, r := range p.FS.Rules {
		if r.Path != tmp {
			continue
		}
		found = true
		assert.False(t, r.Exec, "temp-dir rule %q has Exec=true; world-shared temp must not be executable", tmp)
		assert.True(t, r.Read && r.Write, "temp-dir rule %q should be read+write, got read=%v write=%v", tmp, r.Read, r.Write)
	}
	require.True(t, found, "no temp-dir rule (%q) found in default policy", tmp)
}

// TestRulePathsNormalized verifies BuildPolicy resolves rule paths so a rule
// path that is a symlink is compared symmetrically with resolved access paths.
// A workspace reached through a symlink must still match its own rule.
func TestRulePathsNormalized(t *testing.T) {
	realDir := t.TempDir()
	link := t.TempDir() + "/link"
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	// Build the policy against the symlink; the rule path must be resolved to
	// the real directory so a read of a file under it is allowed.
	p := BuildPolicy(link, nil, nil, nil, nil)
	assert.NoError(t, p.CheckRead(realDir+"/file.txt"), "CheckRead under resolved workspace should pass")
}

func TestScrubEnvDropsSecrets(t *testing.T) {
	p := BuildPolicy("/ws", nil, nil, nil, nil)
	in := []string{
		"PATH=/usr/bin",
		"HOME=/home/u",
		"AWS_ACCESS_KEY_ID=secret",
		"GITHUB_TOKEN=ghp_abc",
		"VAULT_TOKEN=hvs.xyz",
		"OP_SESSION_my_team=abc",
		"NPM_TOKEN=npm_def",
		"ANTHROPIC_API_KEY=ak-123",
		"USER=u",
	}
	kept, dropped := p.ScrubEnv(in)

	assert.ElementsMatch(t, []string{
		"PATH=/usr/bin",
		"HOME=/home/u",
		"USER=u",
	}, kept, "only non-secret vars should be kept")

	for _, secret := range []string{
		"AWS_ACCESS_KEY_ID", "GITHUB_TOKEN", "VAULT_TOKEN",
		"OP_SESSION_my_team", "NPM_TOKEN", "ANTHROPIC_API_KEY",
	} {
		assert.Contains(t, dropped, secret, "expected %q in dropped", secret)
	}
}

func TestScrubEnvHonoursUserGlob(t *testing.T) {
	// User opts into MISE_* via sandbox.env.passthrough.
	p := BuildPolicy("/ws", nil, nil, nil, []string{"MISE_*"})
	in := []string{
		"PATH=/usr/bin",
		"MISE_DATA_DIR=/home/u/.local/share/mise",
		"MISE_SHIMS=/home/u/.local/share/mise/shims",
		"GITHUB_TOKEN=ghp_xxx",
	}
	kept, dropped := p.ScrubEnv(in)
	assert.Contains(t, kept, "MISE_DATA_DIR=/home/u/.local/share/mise")
	assert.Contains(t, kept, "MISE_SHIMS=/home/u/.local/share/mise/shims")
	assert.Contains(t, dropped, "GITHUB_TOKEN")
}

func TestScrubEnvUserExactExtension(t *testing.T) {
	p := BuildPolicy("/ws", nil, nil, []string{"GOPATH", "GOCACHE"}, nil)
	in := []string{
		"GOPATH=/home/u/go",
		"GOCACHE=/home/u/.cache/go-build",
		"GOROOT_BOOTSTRAP=/opt/go",
		"GITHUB_TOKEN=ghp",
	}
	kept, _ := p.ScrubEnv(in)
	assert.Contains(t, kept, "GOPATH=/home/u/go", "GOPATH should pass through")
	assert.Contains(t, kept, "GOCACHE=/home/u/.cache/go-build", "GOCACHE should pass through")
	for _, denied := range []string{"GOROOT_BOOTSTRAP", "GITHUB_TOKEN"} {
		for _, kv := range kept {
			assert.NotEqual(t, denied+"=", kv[:len(denied)+1], "%s should not pass through", denied)
		}
	}
}

func TestScrubEnvNilPolicyPassthrough(t *testing.T) {
	var p *Policy
	in := []string{"GITHUB_TOKEN=ghp_xxx"}
	kept, dropped := p.ScrubEnv(in)
	assert.Empty(t, dropped, "nil policy should not drop")
	assert.Equal(t, []string{"GITHUB_TOKEN=ghp_xxx"}, kept, "nil policy should pass through unchanged")
}

func TestAllowEnv(t *testing.T) {
	p := BuildPolicy("/ws", nil, nil, []string{"GOPATH"}, []string{"NPM_CONFIG_*"})

	assert.True(t, p.AllowEnv("PATH"))
	assert.True(t, p.AllowEnv("HOME"))
	assert.True(t, p.AllowEnv("GOPATH"))
	assert.True(t, p.AllowEnv("NPM_CONFIG_CACHE"))
	assert.True(t, p.AllowEnv("NPM_CONFIG_REGISTRY"))
	assert.False(t, p.AllowEnv("GITHUB_TOKEN"))
	assert.False(t, p.AllowEnv("AWS_ACCESS_KEY_ID"))
	assert.False(t, p.AllowEnv("NPM_TOKEN"), "exact name; not matched by NPM_CONFIG_*")
}

func TestMagusRuntimeEnvPassesThrough(t *testing.T) {
	p := BuildPolicy("/ws", nil, nil, nil, nil)
	// MAGUS_RUN_ID is a plain identifier; it passes through to all children.
	assert.True(t, p.AllowEnv("MAGUS_RUN_ID"), "MAGUS_RUN_ID must pass through")
	// MAGUS_DAEMON_SOCKET and MAGUS_DAEMON_ADDRESS are intentionally withheld
	// from the general allowlist: the daemon socket is unauthenticated. They
	// are injected per-spawn only by MagusCmd (recursive magus invocations).
	assert.False(t, p.AllowEnv("MAGUS_DAEMON_SOCKET"), "MAGUS_DAEMON_SOCKET must NOT be in the general allowlist (unauthenticated socket)")
	assert.False(t, p.AllowEnv("MAGUS_DAEMON_ADDRESS"), "MAGUS_DAEMON_ADDRESS must NOT be in the general allowlist (unauthenticated socket)")
}

func TestValidateGlobs(t *testing.T) {
	assert.Empty(t, env.ValidateGlobs([]string{"MISE_*", "NPM_CONFIG_*"}), "valid globs should pass")
	for _, bad := range []string{"*_TOKEN", "FOO*BAR", "EXACT"} {
		assert.NotEmpty(t, env.ValidateGlobs([]string{bad}), "expected %q to be invalid", bad)
	}
}

func TestCheckReadRejectsOutsideWorkspace(t *testing.T) {
	p := BuildPolicy("/workspace", nil, nil, nil, nil)

	denied := []string{
		"/home/user/.aws/credentials",
		"/home/user/.vault-token",
		"/home/user/.ssh/id_rsa",
		"/etc/shadow",
		"/root/.bashrc",
	}
	for _, path := range denied {
		assert.ErrorIs(t, p.CheckRead(path), filesystem.ErrDenied, "CheckRead(%q) should be denied", path)
	}
}

func TestCheckReadAllowsWorkspaceAndTmp(t *testing.T) {
	p := BuildPolicy("/workspace", nil, nil, nil, nil)

	allowed := []string{
		"/workspace/main.go",
		"/workspace/internal/foo/bar.go",
		"/workspace",
		// Derive from os.TempDir(), not a hard-coded /tmp: BuildPolicy allowlists
		// os.TempDir(), which honors $TMPDIR (e.g. /tmp/claude-0 in CI sandboxes).
		filepath.Join(os.TempDir(), "build-cache"),
	}
	for _, path := range allowed {
		assert.NoError(t, p.CheckRead(path), "CheckRead(%q) should be allowed", path)
	}
}

func TestCheckWriteRequiresWriteRule(t *testing.T) {
	// System paths are read-only; writes there should be denied even when
	// reads pass.
	p := BuildPolicy("/workspace", nil, nil, nil, nil)

	assert.NoError(t, p.CheckRead("/usr/lib/libc.so.6"), "CheckRead(/usr/lib/...) should be allowed (read-only system path)")
	assert.Error(t, p.CheckWrite("/usr/lib/poisoned.so"), "CheckWrite(/usr/lib/...) should be denied — read-only rule")
}

func TestCheckWriteAllowsWorkspaceAndTmp(t *testing.T) {
	p := BuildPolicy("/workspace", nil, nil, nil, nil)
	for _, path := range []string{"/workspace/dist/out", filepath.Join(os.TempDir(), "staging")} {
		assert.NoError(t, p.CheckWrite(path), "CheckWrite(%q) should be allowed", path)
	}
}

func TestUserAllowExtension(t *testing.T) {
	cargo, err := filesystem.ExpandUserRule("/home/u/.cargo", true, true)
	require.NoError(t, err)
	p := BuildPolicy("/workspace", []filesystem.Rule{cargo}, nil, nil, nil)
	assert.NoError(t, p.CheckWrite("/home/u/.cargo/registry/cache/foo"), "user-extended path should be writable")
	assert.Error(t, p.CheckRead("/home/u/.aws/credentials"), "user-extension of one path should not open another")
}

func TestNilPolicyAllowsEverything(t *testing.T) {
	var p *Policy
	assert.NoError(t, p.CheckRead("/etc/shadow"), "nil policy CheckRead should pass")
	assert.NoError(t, p.CheckWrite("/etc/passwd"), "nil policy CheckWrite should pass")
}

func TestSymlinkEscapeRejected(t *testing.T) {
	ws := t.TempDir()
	// Create a symlink inside the workspace pointing at /etc/passwd.
	link := ws + "/evil"
	require.NoError(t, os.Symlink("/etc/passwd", link))
	p := BuildPolicy(ws, nil, nil, nil, nil)
	// CheckRead must deny the symlink because it resolves to /etc/passwd,
	// which is outside the workspace allowlist.
	assert.ErrorIs(t, p.CheckRead(link), filesystem.ErrDenied, "CheckRead on a symlink pointing outside workspace should be denied")
}

func TestPathTraversalRejected(t *testing.T) {
	p := BuildPolicy("/workspace", nil, nil, nil, nil)
	// A spell trying to break out via ".." should never succeed regardless
	// of how the input is shaped.
	bad := []string{
		"/workspace/../etc/passwd",
		"/workspace/sub/../../etc/shadow",
	}
	for _, path := range bad {
		// After filepath.Clean these resolve to /etc/... which is outside
		// the workspace, so the check fails — but the test is on a real
		// denial, not the ".." literal.
		assert.Error(t, p.CheckRead(path), "CheckRead(%q) should be denied (resolves outside workspace)", path)
	}
}

// TestProcSelfDenied verifies that /proc/self paths are denied by the
// sandbox policy. A spell reading /proc/self/environ would obtain the
// daemon's full environment including any secrets in it.
func TestProcSelfDenied(t *testing.T) {
	ws := t.TempDir()
	p := BuildPolicy(ws, nil, nil, nil, nil)
	for _, path := range []string{
		"/proc/self/environ",
		"/proc/self/mem",
		"/proc/self/maps",
		"/proc/self/cmdline",
		"/proc/self/fd",
	} {
		assert.Error(t, p.CheckRead(path), "CheckRead(%q) should be denied — self-introspection bypasses env scrubbing", path)
	}
}

// TestSystemPathsNotExecutable verifies that read-only system paths
// (shared libs, CA certs) do not carry the Exec flag at the Rule level.
// The kernel landlock layer uses this to prevent execve from those trees.
func TestSystemPathsNotExecutable(t *testing.T) {
	execForbidden := []string{"/usr/lib", "/lib", "/lib64", "/etc/ssl", "/nix/store"}
	ws := t.TempDir()
	p := BuildPolicy(ws, nil, nil, nil, nil)
	for _, rule := range p.FS.Rules {
		for _, sys := range execForbidden {
			if rule.Path == sys {
				assert.False(t, rule.Exec, "system path %q has Exec=true; it should be read-only (no execve)", sys)
			}
		}
	}
}

// TestWorkspaceHasExec verifies the workspace rule permits execve so spells
// can build and run binaries under the workspace root.
func TestWorkspaceHasExec(t *testing.T) {
	ws := t.TempDir()
	// BuildPolicy stores symlink-resolved rule paths; resolve ws to match on macOS.
	if resolved, err := filepath.EvalSymlinks(ws); err == nil {
		ws = resolved
	}
	p := BuildPolicy(ws, nil, nil, nil, nil)
	for _, rule := range p.FS.Rules {
		if rule.Path == ws {
			assert.True(t, rule.Exec, "workspace rule must have Exec=true so spells can run built binaries")
			return
		}
	}
	t.Errorf("no rule found for workspace %q", ws)
}

// TestFingerprintStable verifies that two identically-built policies produce
// the same fingerprint, and that a policy with a different rule differs.
func TestFingerprintStable(t *testing.T) {
	a := BuildPolicy("/ws", nil, nil, nil, nil)
	b := BuildPolicy("/ws", nil, nil, nil, nil)
	assert.Equal(t, a.Fingerprint(), b.Fingerprint(), "identical policies should produce the same fingerprint")
	extra, _ := filesystem.ExpandUserRule("/tmp/extra", true, false)
	c := BuildPolicy("/ws", []filesystem.Rule{extra}, nil, nil, nil)
	assert.NotEqual(t, a.Fingerprint(), c.Fingerprint(), "policies with different rules must have different fingerprints")
}

// TestBaseEnvFrozenAtBuildTime confirms that the frozen BaseEnv contains only
// allowlisted variables, not raw secrets that are typically in the process env.
func TestBaseEnvFrozenAtBuildTime(t *testing.T) {
	p := BuildPolicy(t.TempDir(), nil, nil, nil, nil)
	secrets := []string{"GITHUB_TOKEN", "AWS_ACCESS_KEY_ID", "VAULT_TOKEN", "NPM_TOKEN"}
	for _, kv := range p.BaseEnv {
		for _, secret := range secrets {
			prefix := secret + "="
			if len(kv) >= len(prefix) && kv[:len(prefix)] == prefix {
				t.Errorf("secret var leaked into BaseEnv: %q", kv)
			}
		}
	}
	// BaseEnv must be a non-nil slice even in a clean environment.
	assert.NotNil(t, p.BaseEnv, "BaseEnv must be a non-nil slice")
}

// TestUnionPoliciesMergesRules verifies that two workspace policies with
// overlapping paths union their Read/Write/Exec flags rather than dropping one.
func TestUnionPoliciesMergesRules(t *testing.T) {
	a := &Policy{
		FS: filesystem.Ruleset{Rules: []filesystem.Rule{
			{Path: "/shared", Read: true},
			{Path: "/only-in-a", Read: true, Write: true},
		}},
		Env: env.Allowlist{Allow: []string{"PATH", "HOME"}},
	}
	b := &Policy{
		FS: filesystem.Ruleset{Rules: []filesystem.Rule{
			{Path: "/shared", Write: true, Exec: true},
			{Path: "/only-in-b", Read: true},
		}},
		Env: env.Allowlist{
			Allow: []string{"PATH", "GOPATH"},
			Globs: []string{"MISE_*"},
		},
	}
	u := UnionPolicies(a, b)

	var shared *filesystem.Rule
	paths := map[string]bool{}
	for i := range u.FS.Rules {
		paths[u.FS.Rules[i].Path] = true
		if u.FS.Rules[i].Path == "/shared" {
			shared = &u.FS.Rules[i]
		}
	}
	require.NotNil(t, shared, "union missing /shared; got %+v", u.FS.Rules)
	assert.True(t, shared.Read && shared.Write && shared.Exec, "union of /shared should be Read+Write+Exec, got %+v", *shared)
	for _, p := range []string{"/only-in-a", "/only-in-b"} {
		assert.True(t, paths[p], "union missing exclusive path %q", p)
	}

	envSet := map[string]bool{}
	for _, n := range u.Env.Allow {
		envSet[n] = true
	}
	for _, n := range []string{"PATH", "HOME", "GOPATH"} {
		assert.True(t, envSet[n], "union missing env %q", n)
	}
	assert.Equal(t, []string{"MISE_*"}, u.Env.Globs, "union globs")
}

// TestUnionSkipsNil ensures nil inputs are silently ignored.
func TestUnionSkipsNil(t *testing.T) {
	a := &Policy{FS: filesystem.Ruleset{Rules: []filesystem.Rule{{Path: "/a", Read: true}}}}
	u := UnionPolicies(nil, a, nil)
	assert.Equal(t, []filesystem.Rule{{Path: "/a", Read: true}}, u.FS.Rules, "union of (nil, a, nil) should equal a")
}

// TestUnionDeduplicatesBaseEnv ensures identical NAME=VALUE entries in BaseEnv
// are not duplicated in the union (a child sees each var once).
func TestUnionDeduplicatesBaseEnv(t *testing.T) {
	a := &Policy{BaseEnv: []string{"PATH=/usr/bin", "HOME=/home/u"}}
	b := &Policy{BaseEnv: []string{"PATH=/usr/bin", "GOPATH=/home/u/go"}}
	u := UnionPolicies(a, b)
	assert.Len(t, u.BaseEnv, 3, "expected 3 unique BaseEnv entries, got %v", u.BaseEnv)
}

// TestUnionRespectsSingleWorkspace verifies the union of a single policy is
// equivalent to that policy (no duplication, no loss).
func TestUnionRespectsSingleWorkspace(t *testing.T) {
	p := BuildPolicy(t.TempDir(), nil, nil, nil, nil)
	u := UnionPolicies(p)
	assert.Equal(t, p.Fingerprint(), u.Fingerprint(), "union of one policy must equal that policy")
}
