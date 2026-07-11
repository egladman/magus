package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/egladman/magus/internal/sandbox/env"
	"github.com/egladman/magus/internal/sandbox/filesystem"
)

// BuildPolicy assembles a Policy from a workspace root, user/spell extras, and env passthrough.
// Grants read+write on workspace and $TMPDIR, read-only on system paths, exec on the magus binary.
func BuildPolicy(workspace string, userExtras, spellExtras []filesystem.Rule, extraEnvAllow, extraEnvGlobs []string) *Policy {
	rules := make([]filesystem.Rule, 0, 16+len(userExtras)+len(spellExtras))

	if workspace != "" {
		// Workspace is writable and executable: spells build binaries then run them.
		rules = append(rules, filesystem.Rule{Path: workspace, Read: true, Write: true, Exec: true})
	}

	tmp := os.TempDir()
	if tmp != "" {
		// /tmp: read+write but NOT exec — world-shared on multiuser hosts, so exec would allow running
		// payloads planted by other users. Add via sandbox.allow if a spell needs exec there.
		rules = append(rules, filesystem.Rule{Path: tmp, Read: true, Write: true, Exec: false})
	}

	rules = append(rules, systemReadRules()...)
	rules = append(rules, userExtras...)
	rules = append(rules, spellExtras...)

	// Resolve paths and merge duplicates (e.g. /lib and /usr/lib on merged-/usr hosts).
	for i := range rules {
		rules[i].Path = filesystem.ResolveRulePath(rules[i].Path)
	}
	rules = mergeRulesByPath(rules)

	envAllow := env.DefaultAllow()
	envAllow = append(envAllow, extraEnvAllow...)

	envGlobs := env.DefaultGlobs()
	envGlobs = append(envGlobs, extraEnvGlobs...)

	p := &Policy{
		Workspace: workspace,
		FS:        filesystem.Ruleset{Rules: rules},
		Env:       env.Allowlist{Allow: envAllow, Globs: envGlobs},
	}
	kept, dropped := p.Env.Scrub(os.Environ())
	slices.Sort(kept)
	p.BaseEnv = kept
	p.EnvDropped = dropped
	return p
}

// mergeRulesByPath collapses same-path rules by OR-ing R/W/X flags, preserving first-seen order.
func mergeRulesByPath(rules []filesystem.Rule) []filesystem.Rule {
	seen := make(map[string]int, len(rules))
	out := make([]filesystem.Rule, 0, len(rules))
	for _, r := range rules {
		if idx, ok := seen[r.Path]; ok {
			out[idx].Read = out[idx].Read || r.Read
			out[idx].Write = out[idx].Write || r.Write
			out[idx].Exec = out[idx].Exec || r.Exec
			continue
		}
		seen[r.Path] = len(out)
		out = append(out, r)
	}
	return out
}

// systemReadRules returns read-only entries for system libraries and certs; /proc/self omitted intentionally.
func systemReadRules() []filesystem.Rule {
	readOnlyPaths := []string{
		"/etc/resolv.conf",
		"/etc/hosts",
		"/etc/nsswitch.conf",
		"/etc/ssl",
		"/etc/ca-certificates",
		"/etc/pki",
		"/usr/lib",
		"/usr/local/lib",
		"/lib",
		"/lib64",
		"/usr/share/ca-certificates",
	}
	if runtime.GOOS == "linux" {
		if _, err := os.Stat("/nix/store"); err == nil {
			readOnlyPaths = append(readOnlyPaths, "/nix/store")
		}
	}
	out := make([]filesystem.Rule, 0, len(readOnlyPaths)+1)
	for _, p := range readOnlyPaths {
		out = append(out, filesystem.Rule{Path: p, Read: true, Exec: false})
	}
	// The magus binary itself needs to be executable for recursive invocations.
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			out = append(out, filesystem.Rule{Path: resolved, Read: true, Exec: true})
		}
	}
	return out
}
