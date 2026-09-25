// Package env holds the environment half of a sandbox policy: the allowlist
// of variable names a child process may inherit and the scrubbing logic.
package env

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// commonEnvAllow is the cross-platform baseline. Secret-bearing names (AWS_*, GITHUB_TOKEN, VAULT_*, ...)
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

// DefaultAllow returns the base env-var allowlist (cross-platform + platform-specific).
// No MAGUS_* variable is on it: the ones a child magus needs are injected per spawn, and
// MAGUS_PROC_SOCKET and MAGUS_SERVER_ADDRESS stay off because they're unauthenticated and
// would let spells escape the sandbox.
func DefaultAllow() []string {
	out := make([]string, 0, len(commonEnvAllow)+len(platformEnvAllow))
	out = append(out, commonEnvAllow...)
	out = append(out, platformEnvAllow...)
	return out
}

// Allowlist is the set of env-var names and prefix patterns a child process may inherit.
type Allowlist struct {
	Names    []string // exact variable names
	Prefixes []string // prefix patterns ending in "*", e.g. "MISE_*"
}

// minPrefix is the shortest prefix a pattern may name, underscore included.
//
// A prefix pattern names one tool's namespace, and a namespace ends at a word
// boundary: "GO*" also matches GOOGLE_APPLICATION_CREDENTIALS, where "GO_*" could
// not. So the prefix must end in "_", and one letter before it (X_) is not a
// namespace anybody owns. LC_ is the shortest real one.
const minPrefix = len("LC_")

// ErrInvalidPattern is the sentinel every Parse failure wraps.
var ErrInvalidPattern = errors.New("env: invalid passthrough pattern")

// Parse sorts patterns into exact names and prefix patterns. A pattern holding "*"
// is a prefix pattern and must be exactly one trailing "*" after a prefix of at
// least three characters ending in "_" (MISE_*, LC_*). Any other shape is an error
// naming it, as is an empty pattern or a name containing "=".
func Parse(patterns []string) (Allowlist, error) {
	var a Allowlist
	var errs []error
	for _, p := range patterns {
		switch {
		case p == "" || strings.Contains(p, "="):
			errs = append(errs, fmt.Errorf("%w: %q is not a variable name", ErrInvalidPattern, p))
		case !strings.Contains(p, "*"):
			a.Names = append(a.Names, p)
		case strings.Count(p, "*") != 1 || !strings.HasSuffix(p, "*"):
			errs = append(errs, fmt.Errorf("%w: %q: \"*\" must appear once, at the end", ErrInvalidPattern, p))
		case len(p)-1 < minPrefix || !strings.HasSuffix(p, "_*"):
			errs = append(errs, fmt.Errorf("%w: %q: the prefix must be at least %d characters and end in \"_\" (write the variables out instead)", ErrInvalidPattern, p, minPrefix))
		default:
			a.Prefixes = append(a.Prefixes, p)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return Allowlist{}, err
	}
	return a, nil
}

// Scrub returns a filtered copy of environ keeping only entries a allows.
// Malformed entries (no '=') are dropped. The second return value lists dropped names.
func (a Allowlist) Scrub(environ []string) (kept, dropped []string) {
	kept = make([]string, 0, len(environ))
	for _, kv := range environ {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if a.Allows(name) {
			kept = append(kept, kv)
			continue
		}
		dropped = append(dropped, name)
	}
	return kept, dropped
}

// Allows reports whether name is permitted by the allowlist.
func (a Allowlist) Allows(name string) bool {
	if slices.Contains(a.Names, name) {
		return true
	}
	for _, p := range a.Prefixes {
		prefix := strings.TrimSuffix(p, "*")
		// A bare "*" never matches: it would pass the whole environment through.
		if prefix != "" && strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
