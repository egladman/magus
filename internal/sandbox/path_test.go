package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/sandbox/env"
	"github.com/egladman/magus/internal/sandbox/filesystem"
)

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
		err := p.CheckRead(path)
		if err == nil {
			t.Errorf("CheckRead(%q) = nil, want ErrDenied", path)
		} else if !errors.Is(err, filesystem.ErrDenied) {
			t.Errorf("CheckRead(%q) error = %v, want ErrDenied", path, err)
		}
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
		if err := p.CheckRead(path); err != nil {
			t.Errorf("CheckRead(%q) = %v, want nil", path, err)
		}
	}
}

func TestCheckWriteRequiresWriteRule(t *testing.T) {
	// System paths are read-only; writes there should be denied even when
	// reads pass.
	p := BuildPolicy("/workspace", nil, nil, nil, nil)

	if err := p.CheckRead("/usr/lib/libc.so.6"); err != nil {
		t.Errorf("CheckRead(/usr/lib/...) should be allowed (read-only system path), got %v", err)
	}
	if err := p.CheckWrite("/usr/lib/poisoned.so"); err == nil {
		t.Error("CheckWrite(/usr/lib/...) should be denied — read-only rule")
	}
}

func TestCheckWriteAllowsWorkspaceAndTmp(t *testing.T) {
	p := BuildPolicy("/workspace", nil, nil, nil, nil)
	for _, path := range []string{"/workspace/dist/out", filepath.Join(os.TempDir(), "staging")} {
		if err := p.CheckWrite(path); err != nil {
			t.Errorf("CheckWrite(%q) = %v, want nil", path, err)
		}
	}
}

func TestUserAllowExtension(t *testing.T) {
	cargo, err := filesystem.ExpandUserRule("/home/u/.cargo", true, true)
	if err != nil {
		t.Fatal(err)
	}
	p := BuildPolicy("/workspace", []filesystem.Rule{cargo}, nil, nil, nil)
	if err := p.CheckWrite("/home/u/.cargo/registry/cache/foo"); err != nil {
		t.Errorf("user-extended path should be writable, got %v", err)
	}
	if err := p.CheckRead("/home/u/.aws/credentials"); err == nil {
		t.Error("user-extension of one path should not open another")
	}
}

func TestNilPolicyAllowsEverything(t *testing.T) {
	var p *Policy
	if err := p.CheckRead("/etc/shadow"); err != nil {
		t.Errorf("nil policy CheckRead should pass, got %v", err)
	}
	if err := p.CheckWrite("/etc/passwd"); err != nil {
		t.Errorf("nil policy CheckWrite should pass, got %v", err)
	}
}

func TestSymlinkEscapeRejected(t *testing.T) {
	ws := t.TempDir()
	// Create a symlink inside the workspace pointing at /etc/passwd.
	link := ws + "/evil"
	if err := os.Symlink("/etc/passwd", link); err != nil {
		t.Fatal(err)
	}
	p := BuildPolicy(ws, nil, nil, nil, nil)
	// CheckRead must deny the symlink because it resolves to /etc/passwd,
	// which is outside the workspace allowlist.
	err := p.CheckRead(link)
	if err == nil {
		t.Error("CheckRead on a symlink pointing outside workspace should be denied")
	} else if !errors.Is(err, filesystem.ErrDenied) {
		t.Errorf("CheckRead symlink escape = %v, want ErrDenied", err)
	}
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
		if err := p.CheckRead(path); err == nil {
			t.Errorf("CheckRead(%q) should be denied (resolves outside workspace)", path)
		}
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
		if err := p.CheckRead(path); err == nil {
			t.Errorf("CheckRead(%q) should be denied — self-introspection bypasses env scrubbing", path)
		}
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
			if rule.Path == sys && rule.Exec {
				t.Errorf("system path %q has Exec=true; it should be read-only (no execve)", sys)
			}
		}
	}
}

// TestWorkspaceHasExec verifies the workspace rule permits execve so spells
// can build and run binaries under the workspace root.
func TestWorkspaceHasExec(t *testing.T) {
	ws := t.TempDir()
	p := BuildPolicy(ws, nil, nil, nil, nil)
	for _, rule := range p.FS.Rules {
		if rule.Path == ws {
			if !rule.Exec {
				t.Error("workspace rule must have Exec=true so spells can run built binaries")
			}
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
	if a.Fingerprint() != b.Fingerprint() {
		t.Errorf("identical policies produced different fingerprints: %q vs %q",
			a.Fingerprint(), b.Fingerprint())
	}
	extra, _ := filesystem.ExpandUserRule("/tmp/extra", true, false)
	c := BuildPolicy("/ws", []filesystem.Rule{extra}, nil, nil, nil)
	if a.Fingerprint() == c.Fingerprint() {
		t.Error("policies with different rules must have different fingerprints")
	}
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
	if p.BaseEnv == nil {
		t.Error("BaseEnv must be a non-nil slice")
	}
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
	if shared == nil {
		t.Fatalf("union missing /shared; got %+v", u.FS.Rules)
	}
	if !shared.Read || !shared.Write || !shared.Exec {
		t.Errorf("union of /shared should be Read+Write+Exec, got %+v", *shared)
	}
	for _, p := range []string{"/only-in-a", "/only-in-b"} {
		if !paths[p] {
			t.Errorf("union missing exclusive path %q", p)
		}
	}

	envSet := map[string]bool{}
	for _, n := range u.Env.Allow {
		envSet[n] = true
	}
	for _, n := range []string{"PATH", "HOME", "GOPATH"} {
		if !envSet[n] {
			t.Errorf("union missing env %q", n)
		}
	}
	if len(u.Env.Globs) != 1 || u.Env.Globs[0] != "MISE_*" {
		t.Errorf("union globs = %v, want [MISE_*]", u.Env.Globs)
	}
}

// TestUnionSkipsNil ensures nil inputs are silently ignored.
func TestUnionSkipsNil(t *testing.T) {
	a := &Policy{FS: filesystem.Ruleset{Rules: []filesystem.Rule{{Path: "/a", Read: true}}}}
	u := UnionPolicies(nil, a, nil)
	if len(u.FS.Rules) != 1 || u.FS.Rules[0].Path != "/a" {
		t.Errorf("union of (nil, a, nil) should equal a; got %+v", u.FS.Rules)
	}
}

// TestUnionDeduplicatesBaseEnv ensures identical NAME=VALUE entries in BaseEnv
// are not duplicated in the union (a child sees each var once).
func TestUnionDeduplicatesBaseEnv(t *testing.T) {
	a := &Policy{BaseEnv: []string{"PATH=/usr/bin", "HOME=/home/u"}}
	b := &Policy{BaseEnv: []string{"PATH=/usr/bin", "GOPATH=/home/u/go"}}
	u := UnionPolicies(a, b)
	if len(u.BaseEnv) != 3 {
		t.Errorf("expected 3 unique BaseEnv entries, got %d: %v", len(u.BaseEnv), u.BaseEnv)
	}
}

// TestUnionRespectsSingleWorkspace verifies the union of a single policy is
// equivalent to that policy (no duplication, no loss).
func TestUnionRespectsSingleWorkspace(t *testing.T) {
	p := BuildPolicy(t.TempDir(), nil, nil, nil, nil)
	u := UnionPolicies(p)
	if u.Fingerprint() != p.Fingerprint() {
		t.Errorf("union of one policy must equal that policy; fingerprints %q vs %q",
			u.Fingerprint(), p.Fingerprint())
	}
}
