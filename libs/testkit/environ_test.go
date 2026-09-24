package testkit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/sandbox/env"
	"github.com/rogpeppe/go-internal/testscript"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain is the testscript form of Main, so this package tests it the way cmd/magus
// uses it. The keep list is what TestIsolatedMKeepsAHelpersListedVariables instructs
// its helper with.
func TestMain(m *testing.M) {
	testscript.Main(Isolated(m, "TESTKIT_HELPER_*", "TESTKIT_EXACT"), map[string]func(){
		"testkit-environ": func() {
			for _, kv := range os.Environ() {
				fmt.Println(kv)
			}
		},
	})
}

func environMap(t *testing.T, environ []string) map[string]string {
	t.Helper()
	out := make(map[string]string, len(environ))
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		require.True(t, ok, "malformed entry %q", kv)
		_, dup := out[k]
		require.False(t, dup, "%s appears twice", k)
		out[k] = v
	}
	return out
}

// TestEnvironKeepsOnlyTheAllowlist: each dropped name is one that reached a test
// before and failed CI there (the merge queue's MAGUS_CACHE_DIR, an orchestrator's
// BAGGAGE), or a secret, or anything else nobody listed.
func TestEnvironKeepsOnlyTheAllowlist(t *testing.T) {
	for k, v := range map[string]string{
		"MAGUS_CACHE_DIR": "/scratch/magus", "MAGUS_LOG_LEVEL": "debug", "MAGUS_LEVEL": "2",
		"BAGGAGE": "magus.lease=x", "GITHUB_TOKEN": "ghp_x", "FOO": "bar", "GIT_DIR": "/repo/.git",
		"PATH": "/bin:/usr/bin", "GOFLAGS": "-mod=mod", "GOEXPERIMENT": "jsonv2", "GOTOOLCHAIN": "local",
	} {
		t.Setenv(k, v)
	}
	environ, err := Environ(t.TempDir())
	require.NoError(t, err)
	got := environMap(t, environ)

	for _, name := range []string{"MAGUS_CACHE_DIR", "MAGUS_LOG_LEVEL", "MAGUS_LEVEL", "BAGGAGE", "GITHUB_TOKEN", "FOO", "GIT_DIR"} {
		assert.NotContains(t, got, name)
	}
	assert.Equal(t, "/bin:/usr/bin", got["PATH"])
	assert.Equal(t, "-mod=mod", got["GOFLAGS"])
	assert.Equal(t, "jsonv2", got["GOEXPERIMENT"])
	assert.Equal(t, "local", got["GOTOOLCHAIN"])
	assert.Equal(t, "1", got["GIT_CONFIG_NOSYSTEM"])
}

// TestEnvironPinsTheBrokerAndServerOff: dropping the person's MAGUS_BROKER is not
// enough, since unset means the default; off is stated.
func TestEnvironPinsTheBrokerAndServerOff(t *testing.T) {
	t.Setenv("MAGUS_BROKER", "on")
	t.Setenv("MAGUS_SERVER_ENABLED", "true")
	t.Setenv("MAGUS_SERVER_ADDRESS", "unix:///run/user/1000/magus/server.sock")
	t.Setenv("MAGUS_PROC_SOCKET", "/run/user/1000/magus/magus-1-00.sock")
	environ, err := Environ(t.TempDir(), "MAGUS_*")
	require.NoError(t, err)
	got := environMap(t, environ)

	assert.Equal(t, "off", got["MAGUS_BROKER"], "a keep cannot turn the broker back on")
	assert.Equal(t, "false", got["MAGUS_SERVER_ENABLED"])
	environ, err = Environ(t.TempDir())
	require.NoError(t, err)
	got = environMap(t, environ)
	assert.NotContains(t, got, "MAGUS_SERVER_ADDRESS")
	assert.NotContains(t, got, "MAGUS_PROC_SOCKET")
}

// TestIsolateLeavesRoomForASocket: magus's longest socket name adds 32 bytes below
// XDG_RUNTIME_DIR, and macOS caps a unix socket path near 104.
func TestIsolateLeavesRoomForASocket(t *testing.T) {
	Isolate(t)
	sock := filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "magus", "magus-99999-0123abcd.sock")
	assert.Less(t, len(sock), 104, sock)
}

func TestEnvironKeepAddsToTheAllowlist(t *testing.T) {
	for k, v := range map[string]string{
		"LOCKTEST_PROJECT": "app", "LOCKTEST_HOLD_MS": "5", "LOCKTESTX": "no",
		"EXACT_NAME": "yes", "EXACT_NAME_TOO": "no", "FOO": "bar",
	} {
		t.Setenv(k, v)
	}
	environ, err := Environ(t.TempDir(), "LOCKTEST_*", "EXACT_NAME", "HOME")
	require.NoError(t, err)
	got := environMap(t, environ)

	assert.Equal(t, "app", got["LOCKTEST_PROJECT"], "a glob keeps its prefix")
	assert.Equal(t, "5", got["LOCKTEST_HOLD_MS"])
	assert.NotContains(t, got, "LOCKTESTX", "the glob's prefix includes its underscore")
	assert.Equal(t, "yes", got["EXACT_NAME"], "an exact name is kept")
	assert.NotContains(t, got, "EXACT_NAME_TOO", "an exact name is not a prefix")
	assert.NotContains(t, got, "FOO", "an unkept name is dropped")
	assert.Equal(t, "home", filepath.Base(got["HOME"]), "keep cannot undo a redirect")
}

func TestEnvironRejectsAMalformedKeepGlob(t *testing.T) {
	for _, keep := range []string{"*", "A*B*", "*_SUFFIX"} {
		_, err := Environ(t.TempDir(), keep)
		assert.ErrorIs(t, err, env.ErrInvalidGlob, keep)
	}
}

// TestEnvironMovesEveryUserLocationUnderRoot: an inherited cache or state dir is where
// the examples generator picked up a developer's daemon token and a queue candidate's
// run history.
func TestEnvironMovesEveryUserLocationUnderRoot(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/scratch/cache")
	t.Setenv("XDG_STATE_HOME", "/home/state")
	t.Setenv("TMPDIR", os.TempDir())
	root := t.TempDir()
	environ, err := Environ(root)
	require.NoError(t, err)
	got := environMap(t, environ)

	for _, name := range []string{"HOME", "XDG_CACHE_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_RUNTIME_DIR", "XDG_STATE_HOME"} {
		rel, err := filepath.Rel(root, got[name])
		require.NoError(t, err, name)
		assert.False(t, rel == "." || strings.HasPrefix(rel, ".."), "%s=%s is not under %s", name, got[name], root)
		assert.DirExists(t, got[name], name)
	}
	assert.Equal(t, os.TempDir(), got["TMPDIR"], "TMPDIR is inherited, not nested under root")
}

// TestEnvironPinsToolDirsBeforeTheRedirect: moving HOME must not move the Go caches or
// the directories mise's shims find tools in.
func TestEnvironPinsToolDirsBeforeTheRedirect(t *testing.T) {
	t.Setenv("HOME", "/users/me")
	for _, name := range []string{"MISE_CONFIG_DIR", "MISE_STATE_DIR", "XDG_CONFIG_HOME", "XDG_STATE_HOME"} {
		t.Setenv(name, "")
	}
	t.Setenv("MISE_DATA_DIR", "/opt/mise")
	t.Setenv("XDG_DATA_HOME", "/users/me/data")
	t.Setenv("GOCACHE", "/users/me/gocache")
	t.Setenv("GOENV", "/users/me/goenv")
	t.Setenv("GOMODCACHE", "/users/me/gomod")
	t.Setenv("GOPATH", "/users/me/go")
	environ, err := Environ(t.TempDir())
	require.NoError(t, err)
	got := environMap(t, environ)

	assert.Equal(t, "/users/me/gocache", got["GOCACHE"])
	assert.Equal(t, "/users/me/goenv", got["GOENV"])
	assert.Equal(t, "/users/me/gomod", got["GOMODCACHE"])
	assert.Equal(t, "/users/me/go", got["GOPATH"])
	assert.Equal(t, "/opt/mise", got["MISE_DATA_DIR"], "an explicit value wins")
	assert.Equal(t, filepath.Join("/users/me", ".config", "mise"), got["MISE_CONFIG_DIR"], "HOME default")
	assert.Equal(t, filepath.Join("/users/me", ".local", "state", "mise"), got["MISE_STATE_DIR"], "HOME default")
}

// TestEnvironAsksGoForUnsetDirs: with GOCACHE unset and HOME about to move, only go
// knows where the warm cache is.
func TestEnvironAsksGoForUnsetDirs(t *testing.T) {
	want := t.TempDir()
	t.Setenv("GOCACHE", "")
	t.Setenv("GOENV", filepath.Join(want, "goenv"))
	t.Setenv("XDG_CACHE_HOME", want)
	t.Setenv("HOME", want)
	environ, err := Environ(t.TempDir())
	require.NoError(t, err)
	got := environMap(t, environ)

	assert.True(t, strings.HasPrefix(got["GOCACHE"], want), "GOCACHE=%s resolved from the pre-redirect HOME", got["GOCACHE"])
	assert.Equal(t, filepath.Join(want, "goenv"), got["GOENV"])
}

func TestIsolateRestoresTheEnvironment(t *testing.T) {
	t.Setenv("FOO", "bar")
	home := os.Getenv("HOME")
	var root string
	t.Run("isolated", func(t *testing.T) {
		root = Isolate(t)
		_, ok := os.LookupEnv("FOO")
		assert.False(t, ok, "FOO survived Isolate")
		assert.Equal(t, filepath.Join(root, "home"), os.Getenv("HOME"))
		assert.Equal(t, "off", os.Getenv("MAGUS_BROKER"))
	})
	assert.Equal(t, "bar", os.Getenv("FOO"))
	assert.Equal(t, home, os.Getenv("HOME"))
	assert.NotEmpty(t, root)
}

func TestIsolateRefusesAParallelTest(t *testing.T) {
	t.Run("parallel", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() { Isolate(t) })
	})
}

// TestIsolatedMRanThisBinary: TestMain wraps m in Isolated, so this binary's own
// environment is the isolated one.
func TestIsolatedMRanThisBinary(t *testing.T) {
	assert.Equal(t, "off", os.Getenv("MAGUS_BROKER"))
	assert.Equal(t, "home", filepath.Base(os.Getenv("HOME")))
	assert.Equal(t, filepath.Dir(os.Getenv("HOME")), filepath.Dir(os.Getenv("XDG_STATE_HOME")))
}

// TestIsolatedMKeepsAHelpersListedVariables: a helper process re-runs this binary, so
// its TestMain isolates it again, and only the names TestMain keeps reach it.
func TestIsolatedMKeepsAHelpersListedVariables(t *testing.T) {
	if os.Getenv("TESTKIT_HELPER_RUN") == "1" {
		fmt.Printf("value=%s exact=%s cache=%s\n",
			os.Getenv("TESTKIT_HELPER_VALUE"), os.Getenv("TESTKIT_EXACT"), os.Getenv("MAGUS_CACHE_DIR"))
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestIsolatedMKeepsAHelpersListedVariables$")
	cmd.Env = append(os.Environ(),
		"TESTKIT_HELPER_RUN=1", "TESTKIT_HELPER_VALUE=v", "TESTKIT_EXACT=e", "MAGUS_CACHE_DIR=/scratch/magus")
	out, err := cmd.Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), "value=v exact=e cache=\n")
}

// TestIsolatedMLeavesAScriptCommandAlone: the re-exec'd command keeps the environment
// its script set, including a MAGUS_* variable Environ would have dropped.
func TestIsolatedMLeavesAScriptCommandAlone(t *testing.T) {
	testscript.Run(t, testscript.Params{Dir: filepath.Join("testdata", "script")})
}
