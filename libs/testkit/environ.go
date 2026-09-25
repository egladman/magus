package testkit

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/sandbox/env"
)

// goSettings are the Go toolchain's settings a test inherits beyond the sandbox's
// allowlist. A child `go` that misses one builds differently from the harness that ran
// the test (this repo's GOEXPERIMENT=jsonv2), resolves modules through another proxy,
// or switches toolchains.
var goSettings = []string{
	"CGO_ENABLED",
	"GOAUTH",
	"GOCOVERDIR", // where a -cover binary a test runs writes its counters
	"GOEXPERIMENT",
	"GOFLAGS",
	"GOINSECURE",
	"GONOPROXY",
	"GONOSUMDB",
	"GOPRIVATE",
	"GOPROXY",
	"GOROOT",
	"GOSUMDB",
	"GOTOOLCHAIN",
	"GOVCS",
	"GOWORK",
}

// goDirs are the Go toolchain's directories that default to somewhere under HOME or
// XDG_*. Environ pins each to the value `go env` reports before the redirect, so a
// child `go build` stays on the warm build and module caches and still reads the
// user's `go env -w` settings.
var goDirs = []string{"GOCACHE", "GOENV", "GOMODCACHE", "GOPATH"}

// redirects maps each variable Environ points into root to its directory there.
//
// A unix socket path is capped near 104 bytes on macOS, and magus's longest one is
// 32 bytes below XDG_RUNTIME_DIR, so a root must be short: Main and Isolate make it
// with os.MkdirTemp("", "tk") rather than t.TempDir, whose path carries the test's
// name. TMPDIR is absent for the same reason: nesting it under root would push a
// socket a test makes with os.MkdirTemp past the cap. XDG_RUNTIME_DIR covers the one
// piece of shared state magus finds through TMPDIR, the socket directory's fallback.
var redirects = map[string]string{
	"HOME":            "home",
	"XDG_CACHE_HOME":  "cache",
	"XDG_CONFIG_HOME": "config",
	"XDG_DATA_HOME":   "data",
	"XDG_RUNTIME_DIR": "run",
	"XDG_STATE_HOME":  "state",
}

// Environ returns the environment a test, or a program run by a test or a generator,
// should see: os.Environ reduced to the sandbox's allowlist (internal/sandbox/env) plus
// the Go toolchain's settings, with every per-user location moved under root.
//
// Kept: the sandbox's DefaultAllow names (PATH, LANG, TMPDIR, ...), the Go settings
// above, and each keep entry: an exact name, or a prefix pattern such as "LOCKTEST_*" in
// internal/sandbox/env's syntax. Every other variable is dropped, so MAGUS_*
// configuration, BAGGAGE, tokens and GIT_DIR from a hook never reach the test. A keep
// entry cannot undo a redirect or a set value below. A malformed pattern is an error.
//
// Redirected: HOME and XDG_CACHE_HOME, XDG_CONFIG_HOME, XDG_DATA_HOME, XDG_RUNTIME_DIR
// and XDG_STATE_HOME, each to a directory under root that Environ creates. A user's
// magus config, auth token, job store and ~/.gitconfig are therefore out of reach.
//
// Pinned: GOCACHE, GOENV, GOMODCACHE and GOPATH as `go env` reports them (skipped when
// go is not on PATH), and mise's data, state and config directories, which its shims
// on PATH need to find installed tools and trusted configs once HOME has moved.
//
// Set: MAGUS_BROKER=off and MAGUS_SERVER_ENABLED=false, so no test claims capacity
// from or binds beside a broker or server the person running it has up (a test that
// wants a broker starts one and passes it with magus.WithBroker); and
// GIT_CONFIG_NOSYSTEM=1, so the machine's system gitconfig (credential helpers,
// signing) stays out. Git has no identity; a test that commits passes one.
//
// Environ reads the process environment and does not change it.
func Environ(root string, keep ...string) ([]string, error) {
	allow, err := env.Parse(keep)
	if err != nil {
		return nil, fmt.Errorf("testkit: keep: %w", err)
	}
	allow.Names = append(append(allow.Names, env.DefaultAllow()...), goSettings...)
	kept, _ := allow.Scrub(os.Environ())
	vars := make(map[string]string, len(kept)+len(redirects)+len(goDirs)+5)
	for _, kv := range kept {
		k, v, _ := strings.Cut(kv, "=")
		vars[k] = v
	}
	for name, dir := range redirects {
		path := filepath.Join(root, dir)
		if err := os.MkdirAll(path, 0o700); err != nil {
			return nil, fmt.Errorf("testkit: create %s: %w", name, err)
		}
		vars[name] = path
	}
	gov, err := goEnv(root)
	if err != nil {
		return nil, err
	}
	for k, v := range gov {
		vars[k] = v
	}
	for k, v := range miseDirs() {
		vars[k] = v
	}
	vars["MAGUS_BROKER"] = "off"
	vars["MAGUS_SERVER_ENABLED"] = "false"
	vars["GIT_CONFIG_NOSYSTEM"] = "1"

	out := make([]string, 0, len(vars))
	for k, v := range vars {
		out = append(out, k+"="+v)
	}
	slices.Sort(out)
	return out, nil
}

// goEnv reports goDirs as the go on PATH resolves them, asking go only when one is
// unset. It runs in dir, outside any module, so a go.mod's toolchain line cannot
// trigger a toolchain switch.
func goEnv(dir string) (map[string]string, error) {
	set := make(map[string]string, len(goDirs))
	for _, name := range goDirs {
		if v := os.Getenv(name); v != "" {
			set[name] = v
		}
	}
	if len(set) == len(goDirs) {
		return set, nil
	}
	cmd := exec.Command("go", append([]string{"env", "-json"}, goDirs...)...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if errors.Is(err, exec.ErrNotFound) {
		return set, nil
	}
	if err != nil {
		return nil, fmt.Errorf("testkit: go env: %w", err)
	}
	var dirs map[string]string
	if err := json.Unmarshal(out, &dirs); err != nil {
		return nil, fmt.Errorf("testkit: go env: %w", err)
	}
	return dirs, nil
}

// miseDirs resolves mise's directories the way mise does: its own variable, then the
// XDG base directory, then the default under HOME, on every platform.
func miseDirs() map[string]string {
	home := os.Getenv("HOME")
	dirs := make(map[string]string, 3)
	for name, base := range map[string]struct{ xdg, fallback string }{
		"MISE_CONFIG_DIR": {"XDG_CONFIG_HOME", ".config"},
		"MISE_DATA_DIR":   {"XDG_DATA_HOME", ".local/share"},
		"MISE_STATE_DIR":  {"XDG_STATE_HOME", ".local/state"},
	} {
		switch {
		case os.Getenv(name) != "":
			dirs[name] = os.Getenv(name)
		case os.Getenv(base.xdg) != "":
			dirs[name] = filepath.Join(os.Getenv(base.xdg), "mise")
		case home != "":
			dirs[name] = filepath.Join(home, base.fallback, "mise")
		}
	}
	return dirs
}

// Isolate replaces the process environment with Environ over a fresh short root for
// the rest of the test and returns that root. The original environment is restored and
// the root removed when the test ends. Like tb.Setenv it changes process-wide state, so
// it fails a test that is, or later becomes, parallel.
func Isolate(tb testing.TB) string {
	tb.Helper()
	root := shortRoot(tb)
	environ, err := Environ(root)
	if err != nil {
		tb.Fatal(err)
	}
	keep := make(map[string]bool, len(environ))
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		keep[k] = true
		tb.Setenv(k, v)
	}
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		if k == "" || keep[k] {
			continue
		}
		if err := os.Unsetenv(k); err != nil {
			tb.Fatalf("testkit: unset %s: %v", k, err)
		}
		tb.Cleanup(func() { _ = os.Setenv(k, v) })
	}
	return root
}

// shortRoot is a per-test root short enough for a socket under XDG_RUNTIME_DIR; see
// redirects.
func shortRoot(tb testing.TB) string {
	tb.Helper()
	root, err := os.MkdirTemp("", "tk")
	if err != nil {
		tb.Fatalf("testkit: %v", err)
	}
	tb.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

// IsolatedM is a testing.M whose Run first reduces the process environment to Environ
// over a temporary root, removed when Run returns.
//
// It satisfies testscript's TestingM, which is the reason it exists apart from Main:
// testscript.Main calls Run only in the top-level test binary, and a re-exec as one of
// the script's commands never does, so that command keeps the environment its script
// gave it.
//
// A helper process a test starts by re-running its own binary goes through Run again,
// so the variables that instruct it survive only if keep names them.
type IsolatedM struct {
	m    *testing.M
	keep []string
}

// Isolated wraps m; keep is passed to Environ. Nothing changes until Run.
func Isolated(m *testing.M, keep ...string) IsolatedM { return IsolatedM{m: m, keep: keep} }

// Run applies the environment, runs the tests, and returns m.Run's exit code. It
// returns 1 without running any test when the environment cannot be built.
func (i IsolatedM) Run() int {
	root, err := os.MkdirTemp("", "tk")
	if err != nil {
		fmt.Fprintln(os.Stderr, "testkit:", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(root) }()
	environ, err := Environ(root, i.keep...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	os.Clearenv()
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		if err := os.Setenv(k, v); err != nil {
			fmt.Fprintln(os.Stderr, "testkit:", err)
			return 1
		}
	}
	return i.m.Run()
}

// Main is a TestMain body: it runs m in the environment Environ(root, keep...) builds
// and exits with its code. A package using testscript passes Isolated(m, keep...) to
// testscript.Main instead.
func Main(m *testing.M, keep ...string) { os.Exit(Isolated(m, keep...).Run()) }
