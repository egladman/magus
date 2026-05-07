// Package env holds the environment half of a sandbox policy: the allowlist
// of variable names a child process may inherit and the scrubbing logic.
package env

import "strings"

// commonEnvAllow is the cross-platform baseline. Secret-bearing names (AWS_*, GITHUB_TOKEN, VAULT_*, …)
// are intentionally absent; users opt in via sandbox.env.passthrough.
var commonEnvAllow = []string{
	"HOME",
	"USER",
	"PATH",
	"LANG",
	"LC_ALL",
	"LC_CTYPE",
	"LC_MESSAGES",
	"LC_NUMERIC",
	"LC_TIME",
	"LC_COLLATE",
	"LC_MONETARY",
	"TZ",
	"TERM",
}

// magusRuntimeEnv lists MAGUS_* vars passed to sandbox children.
// MAGUS_DAEMON_SOCKET/ADDRESS are absent: they're unauthenticated and would let spells escape the sandbox.
var magusRuntimeEnv = []string{
	"MAGUS_RUN_ID",
}

// DefaultAllow returns the base env-var allowlist (cross-platform + platform-specific + magus runtime vars).
func DefaultAllow() []string {
	out := make([]string, 0, len(commonEnvAllow)+len(platformEnvAllow)+len(magusRuntimeEnv))
	out = append(out, commonEnvAllow...)
	out = append(out, platformEnvAllow...)
	out = append(out, magusRuntimeEnv...)
	return out
}

// DefaultGlobs returns the universal env-var glob allowlist (currently empty).
func DefaultGlobs() []string {
	return nil
}

// Allowlist is the set of env-var names and suffix-glob patterns a child process may inherit.
type Allowlist struct {
	Allow []string // exact variable names
	Globs []string // prefix patterns ending in "*", e.g. "MISE_*"
}

// Scrub returns a filtered copy of env keeping only entries in Allow or matching Globs.
// Malformed entries (no '=') are dropped. The second return value lists dropped names.
func (a Allowlist) Scrub(env []string) (kept, dropped []string) {
	exact := make(map[string]struct{}, len(a.Allow))
	for _, n := range a.Allow {
		exact[n] = struct{}{}
	}
	kept = make([]string, 0, len(env))
	for _, kv := range env {
		i := strings.IndexByte(kv, '=')
		if i < 0 {
			continue
		}
		name := kv[:i]
		if _, ok := exact[name]; ok {
			kept = append(kept, kv)
			continue
		}
		if matchGlobs(name, a.Globs) {
			kept = append(kept, kv)
			continue
		}
		dropped = append(dropped, name)
	}
	return kept, dropped
}

// Allows reports whether name is permitted by the allowlist.
func (a Allowlist) Allows(name string) bool {
	for _, n := range a.Allow {
		if n == name {
			return true
		}
	}
	return matchGlobs(name, a.Globs)
}

// matchGlobs reports whether name matches any suffix-wildcard pattern (e.g. "MISE_*").
// Bare "*" is never matched to avoid leaking the whole environment.
func matchGlobs(name string, globs []string) bool {
	for _, g := range globs {
		if !strings.HasSuffix(g, "*") {
			continue
		}
		prefix := g[:len(g)-1]
		if prefix == "" {
			continue // bare "*": would leak whole env
		}
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// ValidateGlobs returns the first invalid pattern (must end in exactly one "*" with non-empty prefix), or "".
func ValidateGlobs(globs []string) string {
	for _, g := range globs {
		if len(g) < 2 || strings.Count(g, "*") != 1 || !strings.HasSuffix(g, "*") {
			return g
		}
	}
	return ""
}
