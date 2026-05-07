package sandbox

import (
	"slices"
	"testing"

	"github.com/egladman/magus/internal/sandbox/env"
)

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
