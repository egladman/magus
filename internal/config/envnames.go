package config

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// EnvName returns the env var name: parts joined with "_", uppercased, prefixed with prefix+"_" when non-empty.
func EnvName(prefix string, parts ...string) string {
	body := strings.Join(parts, "_")
	if prefix != "" {
		body = prefix + "_" + body
	}
	body = strings.ReplaceAll(body, "-", "_")
	return strings.ToUpper(body)
}

// FlagName returns the CLI flag name: parts joined with "-" with "_" replaced by "-".
func FlagName(parts ...string) string {
	return strings.ReplaceAll(strings.Join(parts, "-"), "_", "-")
}

const envPrefix = "MAGUS_"

// inheritedEnv holds variables an older magus exports to the processes it spawns and this
// one ignores. Nobody sets them by hand, so refusing one would fail a nested run over a
// variable its person never saw.
//
// compat(until: no magus older than v0.5.0 spawns this one; observe that no workspace
// pins a required_version floor below v0.5.0): v0.4.x exports its proc-server socket
// under this name, and a child that ignores it runs self-contained, which is correct
// against a parent it cannot adopt anyway.
var inheritedEnv = map[string]bool{"MAGUS_DAEMON_SOCKET": true}

var registeredEnv = sync.OnceValue(func() map[string]bool {
	docs := EnvVarDocs()
	out := make(map[string]bool, len(docs))
	for _, d := range docs {
		out[d.EnvVar] = true
	}
	return out
})

// registeredSuffixes are the registered names without the prefix, sorted so a tie in
// distance resolves the same way every run.
var registeredSuffixes = sync.OnceValue(func() []string {
	out := make([]string, 0, len(registeredEnv()))
	for name := range registeredEnv() {
		if !strings.Contains(name, "<") {
			out = append(out, strings.TrimPrefix(name, envPrefix))
		}
	}
	slices.Sort(out)
	return out
})

// KnownEnvVar reports whether magus reads name from its environment: a variable
// EnvVarDocs registers, a per-VCS MAGUS_VCS_<NAME>_BASE_REF override, or one an older
// magus parent passes down. A retired variable is not known. An unknown name is not
// necessarily wrong (see EnvVarProblem); doctor reports every one.
func KnownEnvVar(name string) bool {
	if registeredEnv()[name] || inheritedEnv[name] {
		return true
	}
	vcsName, ok := strings.CutPrefix(name, "MAGUS_VCS_")
	if !ok {
		return false
	}
	vcsName, ok = strings.CutSuffix(vcsName, "_BASE_REF")
	return ok && vcsName != "" && !strings.Contains(vcsName, "_")
}

// EnvVarProblem reports whether setting the environment variable name is provably
// misconfiguration, and if so returns the MGS1046 line that says what to do instead. Two
// cases qualify: a name magus retired, answered with its replacement or removal, and a
// near miss of a registered name, answered with that name. Any other name, known or not,
// returns ok false: an unknown MAGUS_* variable may belong to a newer magus or to a
// repository's own tooling, and magus cannot tell which.
//
// It judges the name alone; whether the variable is set, and to what, is the caller's.
func EnvVarProblem(name string) (msg string, ok bool) {
	if r, retired := retiredEnv[name]; retired {
		return r.describe(name), true
	}
	if !strings.HasPrefix(name, envPrefix) || KnownEnvVar(name) {
		return "", false
	}
	if near := nearestRegistered(strings.TrimPrefix(name, envPrefix)); near != "" {
		return fmt.Sprintf("%s is set, but magus does not read it; did you mean %s%s?", name, envPrefix, near), true
	}
	return "", false
}

// nearestRegistered returns the registered suffix within typo distance of suffix, or "".
//
// Tighter than hint.Nearest's tolerance, because a suggestion here stops every command:
// one edit for a short name and two for a longer one covers a dropped, doubled or swapped
// character, while three already joins distinct names such as REGISTRY_KEY and
// REGISTRY_URL.
func nearestRegistered(suffix string) string {
	budget := 1
	if len(suffix) >= 6 {
		budget = 2
	}
	best, bestDist := "", budget+1
	for _, known := range registeredSuffixes() {
		if d := hint.Distance(suffix, known); d < bestDist {
			best, bestDist = known, d
		}
	}
	return best
}

// MisconfiguredEnv returns an MGS1046 error with one line per variable in environ
// (KEY=VALUE pairs, as os.Environ returns them) that EnvVarProblem flags, or nil when
// there is none. A variable set to the empty string counts as unset.
func MisconfiguredEnv(environ []string) error {
	var lines []string
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || value == "" {
			continue
		}
		if msg, bad := EnvVarProblem(name); bad {
			lines = append(lines, msg)
		}
	}
	if len(lines) == 0 {
		return nil
	}
	slices.Sort(lines)
	return types.DiagnosticErrorf(types.MisconfiguredEnvVar, "%s", strings.Join(slices.Compact(lines), "\n"))
}
