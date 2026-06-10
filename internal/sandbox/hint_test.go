package sandbox

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/sandbox/env"
	"github.com/egladman/magus/internal/sandbox/filesystem"
)

func TestDenyHint(t *testing.T) {
	t.Parallel()

	got := denyHint("ro", "/usr/bin/curl")
	for _, want := range []string{
		"sandbox blocked access to /usr/bin/curl",
		"magus config set key=sandbox.allow.curl.path,value=/usr/bin/curl",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("denyHint(ro) missing %q in:\n%s", want, got)
		}
	}
	// ro must not emit a mode command (mode defaults to ro).
	if strings.Contains(got, "mode") {
		t.Errorf("denyHint(ro) should not set mode:\n%s", got)
	}

	w := denyHint("rw", "/data/out")
	if !strings.Contains(w, "sandbox.allow.out.path,value=/data/out") || !strings.Contains(w, "sandbox.allow.out.mode,value=rw") {
		t.Errorf("denyHint(rw) wrong:\n%s", w)
	}
}

// TestEmitDenyHint verifies the hint reaches stderr as a "hint:" line, and that
// the hints toggle silences it. Not parallel: it redirects os.Stderr and flips
// the process-wide hints switch.
func TestEmitDenyHint(t *testing.T) {
	capture := func() string {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
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
	for _, want := range []string{"hint:", "magus config set key=sandbox.allow.curl.path,value=/usr/bin/curl"} {
		if !strings.Contains(got, want) {
			t.Errorf("EmitDenyHint stderr missing %q:\n%s", want, got)
		}
	}

	interactive.SetEnabled(false)
	if got := capture(); got != "" {
		t.Errorf("EmitDenyHint should be silent when hints are disabled, got: %q", got)
	}
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
	err := Apply(p)
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("Apply on %s = %v, want ErrUnsupported", runtime.GOOS, err)
	}
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
	err := Apply(p)
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("Supported=false but Apply returned %v, want ErrUnsupported", err)
	}
}

// TestApplyNilPolicyNoop confirms calling Apply with a nil policy is a
// no-op on every platform — needed so that the orchestrator can call
// Apply unconditionally without first checking cfg.Sandbox.Enabled.
func TestApplyNilPolicyNoop(t *testing.T) {
	if err := Apply(nil); err != nil {
		t.Errorf("Apply(nil) = %v, want nil", err)
	}
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
		if r.Exec {
			t.Errorf("temp-dir rule %q has Exec=true; world-shared temp must not be executable", tmp)
		}
		if !r.Read || !r.Write {
			t.Errorf("temp-dir rule %q should be read+write, got read=%v write=%v", tmp, r.Read, r.Write)
		}
	}
	if !found {
		t.Fatalf("no temp-dir rule (%q) found in default policy", tmp)
	}
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
	if err := p.CheckRead(realDir + "/file.txt"); err != nil {
		t.Errorf("CheckRead under resolved workspace should pass, got %v", err)
	}
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

	want := map[string]bool{
		"PATH=/usr/bin": true,
		"HOME=/home/u":  true,
		"USER=u":        true,
	}
	if len(kept) != len(want) {
		t.Fatalf("kept = %v, want exactly %d entries", kept, len(want))
	}
	for _, kv := range kept {
		if !want[kv] {
			t.Errorf("kept unexpected entry %q", kv)
		}
	}
	for _, secret := range []string{
		"AWS_ACCESS_KEY_ID", "GITHUB_TOKEN", "VAULT_TOKEN",
		"OP_SESSION_my_team", "NPM_TOKEN", "ANTHROPIC_API_KEY",
	} {
		if !slices.Contains(dropped, secret) {
			t.Errorf("expected %q in dropped, got %v", secret, dropped)
		}
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
	for _, want := range []string{
		"MISE_DATA_DIR=/home/u/.local/share/mise",
		"MISE_SHIMS=/home/u/.local/share/mise/shims",
	} {
		if !slices.Contains(kept, want) {
			t.Errorf("expected %q to pass through, kept=%v", want, kept)
		}
	}
	if !slices.Contains(dropped, "GITHUB_TOKEN") {
		t.Errorf("expected GITHUB_TOKEN dropped, got %v", dropped)
	}
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
	if !slices.Contains(kept, "GOPATH=/home/u/go") {
		t.Errorf("GOPATH should pass through, kept=%v", kept)
	}
	if !slices.Contains(kept, "GOCACHE=/home/u/.cache/go-build") {
		t.Errorf("GOCACHE should pass through, kept=%v", kept)
	}
	for _, denied := range []string{"GOROOT_BOOTSTRAP", "GITHUB_TOKEN"} {
		for _, kv := range kept {
			if got := kv[:len(denied)+1]; got == denied+"=" {
				t.Errorf("%s should not pass through, kept=%v", denied, kept)
			}
		}
	}
}

func TestScrubEnvNilPolicyPassthrough(t *testing.T) {
	var p *Policy
	in := []string{"GITHUB_TOKEN=ghp_xxx"}
	kept, dropped := p.ScrubEnv(in)
	if len(dropped) != 0 {
		t.Errorf("nil policy should not drop, got %v", dropped)
	}
	if len(kept) != 1 || kept[0] != "GITHUB_TOKEN=ghp_xxx" {
		t.Errorf("nil policy should pass through unchanged, got %v", kept)
	}
}

func TestAllowEnv(t *testing.T) {
	p := BuildPolicy("/ws", nil, nil, []string{"GOPATH"}, []string{"NPM_CONFIG_*"})
	cases := []struct {
		name string
		want bool
	}{
		{"PATH", true},
		{"HOME", true},
		{"GOPATH", true},
		{"NPM_CONFIG_CACHE", true},
		{"NPM_CONFIG_REGISTRY", true},
		{"GITHUB_TOKEN", false},
		{"AWS_ACCESS_KEY_ID", false},
		{"NPM_TOKEN", false}, // exact name; not matched by NPM_CONFIG_*
	}
	for _, c := range cases {
		if got := p.AllowEnv(c.name); got != c.want {
			t.Errorf("AllowEnv(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestMagusRuntimeEnvPassesThrough(t *testing.T) {
	p := BuildPolicy("/ws", nil, nil, nil, nil)
	// MAGUS_RUN_ID is a plain identifier; it passes through to all children.
	if !p.AllowEnv("MAGUS_RUN_ID") {
		t.Error("MAGUS_RUN_ID must pass through")
	}
	// MAGUS_DAEMON_SOCKET and MAGUS_DAEMON_ADDRESS are intentionally withheld
	// from the general allowlist: the daemon socket is unauthenticated. They
	// are injected per-spawn only by MagusCmd (recursive magus invocations).
	if p.AllowEnv("MAGUS_DAEMON_SOCKET") {
		t.Error("MAGUS_DAEMON_SOCKET must NOT be in the general allowlist (unauthenticated socket)")
	}
	if p.AllowEnv("MAGUS_DAEMON_ADDRESS") {
		t.Error("MAGUS_DAEMON_ADDRESS must NOT be in the general allowlist (unauthenticated socket)")
	}
}

func TestValidateGlobs(t *testing.T) {
	if bad := env.ValidateGlobs([]string{"MISE_*", "NPM_CONFIG_*"}); bad != "" {
		t.Errorf("valid globs failed: %q", bad)
	}
	for _, bad := range []string{"*_TOKEN", "FOO*BAR", "EXACT"} {
		if got := env.ValidateGlobs([]string{bad}); got == "" {
			t.Errorf("expected %q to be invalid, but ValidateGlobs passed it", bad)
		}
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
