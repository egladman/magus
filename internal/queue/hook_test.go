package queue

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	procrun "github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/internal/queue/types"
	"github.com/egladman/magus/internal/sandbox"
	sandboxenv "github.com/egladman/magus/internal/sandbox/env"
	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/egladman/magus/spells"
	magustypes "github.com/egladman/magus/types"
)

var (
	hookChange = types.Change{ID: "7", Head: strings.Repeat("a", 40)}
	hookUnits  = []string{"app"}
)

func init() { retryDelay = time.Millisecond }

// TestMain lets the test binary serve as the sandbox launcher, since a hook confined by
// the kernel re-executes it, and keeps the private temp dirs hook policies make out of
// the host's TMPDIR.
func TestMain(m *testing.M) {
	sandbox.MaybeLaunch()
	tmp, err := os.MkdirTemp("", "queue-test")
	if err == nil {
		err = os.Setenv("TMPDIR", tmp)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(tmp)
	os.Exit(code)
}

// script is a hook running body with sh, which reads the appended inputs as $1 onward.
func script(body string) Command { return Command{"sh", "-c", body, "hook"} }

// newBox makes a box's home and temporary directory as checkout does, and removes them
// even when a hook left them unwritable.
func newBox(t *testing.T) (home, tmp string) {
	t.Helper()
	box := t.TempDir()
	home, tmp = filepath.Join(box, "home"), filepath.Join(box, "tmp")
	for _, dir := range []string{home, tmp} {
		require.NoError(t, os.Mkdir(dir, 0o700))
	}
	t.Cleanup(func() { makeWritable(box) })
	return home, tmp
}

// boxed is cand in a box of its own, as checkout gives every candidate.
func boxed(t *testing.T, cand types.Candidate) types.Candidate {
	t.Helper()
	cand.Home, cand.TempDir = newBox(t)
	return cand
}

// boxedRegen is r in a box of its own, as a candidate's regeneration runs.
func boxedRegen(t *testing.T, r types.Regeneration) types.Regeneration {
	t.Helper()
	r.Home, r.TempDir = newBox(t)
	return r
}

// lines is what a hook log holds, one entry per line.
func lines(log *bytes.Buffer) []string { return strings.Split(strings.TrimSpace(log.String()), "\n") }

func TestParseCommandReadsLiteralWords(t *testing.T) {
	for line, want := range map[string]Command{
		"magus run ci --no-default-charms":         {"magus", "run", "ci", "--no-default-charms"},
		"magus --sandbox=required run generate:rw": {"magus", "--sandbox=required", "run", "generate:rw"},
		`'/opt/my magus' run "ci"`:                 {"/opt/my magus", "run", "ci"},
		`./gate.sh a\ b 'x; y' "\$HOME *"`:         {"./gate.sh", "a b", "x; y", "$HOME *"},
		`sh -c 'test -f ok || exit 3'`:             {"sh", "-c", "test -f ok || exit 3"},
		"  make   test  ":                          {"make", "test"},
		`tool a~b 'a*b' --pattern=\*.go`:           {"tool", "a~b", "a*b", "--pattern=*.go"},
	} {
		got, err := ParseCommand("--gate", line)
		require.NoError(t, err, line)
		assert.Equal(t, want, got, line)
	}
}

// No shell runs a hook, so whatever a shell would act on is refused rather than passed
// on as text the line does not read as.
func TestParseCommandRefusesShellSyntax(t *testing.T) {
	for line, why := range map[string]string{
		"":                             "it names no command",
		"  ":                           "it names no command",
		"magus run ci $PROJECTS":       "$PROJECTS is a variable",
		`magus run ci "${HOME}/x"`:     "$HOME is a variable",
		"magus run ci $(cat projects)": "$(...) is a command substitution",
		"magus run ci `cat projects`":  "$(...) is a command substitution",
		"echo $((1+1))":                "$((...)) is arithmetic",
		"magus run ci | tee log":       "| joins two commands",
		"make && magus run ci":         "&& joins two commands",
		"make || true":                 "|| joins two commands",
		"make; magus run ci":           "it holds more than one command",
		"make\nmagus run ci":           "it holds more than one command",
		"magus run ci > log":           "> redirects it",
		"magus run ci 2>&1":            ">& redirects it",
		"magus run ci &":               "& runs it in the background",
		"! magus run ci":               "! negates it",
		"GOFLAGS=-v magus run ci":      "GOFLAGS= sets a variable",
		"rm *.tmp":                     "* is a pattern",
		"ls file?":                     "? is a pattern",
		"ls [ab]":                      "[ is a pattern",
		"echo {a,b}":                   "{ is a pattern",
		"ls ~/x":                       "~ is a home directory",
		"magus run ci # all of it":     "# starts a comment",
		"{ make; }":                    "it is a compound command",
		"if true; then make; fi":       "it is a compound command",
		"magus run 'ci":                "it does not parse as sh words",
	} {
		_, err := ParseCommand("--gate", line)
		var diag *magustypes.DiagnosticError
		require.True(t, errors.As(err, &diag), "%q: %v", line, err)
		assert.Equal(t, magustypes.QueueHookNotACommand, diag.Code, line)
		assert.Contains(t, err.Error(), why, line)
		assert.Contains(t, err.Error(), `--gate `, line)
		assert.Contains(t, err.Error(), "put the line in a script and point --gate at it", line)
	}
}

// A hook gets exactly its words and the appended inputs: nothing splits, expands or
// globs them, and nothing reaches it through the environment.
func TestGateGetsExactlyItsWordsAndUnitsAndNoQueueEnvironment(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "x.go"), nil, 0o644))
	var log bytes.Buffer
	g := CommandGate(Command{"printf", `[%s]\n`, "*", "$HOME"}, HookEnv{}, NewHookLog(&log))
	res, err := g.Validate(context.Background(), boxed(t, types.Candidate{Commit: "s1", Change: "7", Dir: dir}), []string{"libs/a b", "$HOME", "*", "/"})
	require.NoError(t, err)
	assert.True(t, res.Green)
	assert.Equal(t, []string{"[s1 #7] [*]", "[s1 #7] [$HOME]", "[s1 #7] [libs/a b]", "[s1 #7] [$HOME]", "[s1 #7] [*]", "[s1 #7] [/]"}, lines(&log))

	log.Reset()
	_, err = CommandGate(script(`env`), HookEnv{}, NewHookLog(&log)).Validate(context.Background(), boxed(t, types.Candidate{Commit: "s1", Change: "7", Dir: dir}), hookUnits)
	require.NoError(t, err)
	assert.NotContains(t, log.String(), "MERGEQUEUE_")
}

func TestGateIsGreenOnExitZeroAndRedOtherwise(t *testing.T) {
	dir := t.TempDir()
	g := CommandGate(script(`test -f ok`), HookEnv{}, nil)
	cand := boxed(t, types.Candidate{Commit: "s1", Change: "7", Dir: dir})
	res, err := g.Validate(context.Background(), cand, hookUnits)
	require.NoError(t, err)
	assert.False(t, res.Green)
	assert.Equal(t, "the gate exited 1", res.Summary, "the hook line is the verdict's to record, never the prose's")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "ok"), nil, 0o644))
	res, err = g.Validate(context.Background(), cand, hookUnits)
	require.NoError(t, err)
	assert.True(t, res.Green)
}

// seenEnv reads the environment a hook wrote with `env -0 > seen` in dir.
func seenEnv(t *testing.T, dir string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "seen"))
	require.NoError(t, err)
	seen := map[string]string{}
	for kv := range strings.SplitSeq(strings.TrimSuffix(string(raw), "\x00"), "\x00") {
		name, value, _ := strings.Cut(kv, "=")
		seen[name] = value
	}
	return seen
}

// boxedNames are the variables boxEnv sets.
var boxedNames = []string{"HOME", "XDG_CACHE_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "TMPDIR",
	"MAGUS_CACHE_DIR", "MAGUS_CACHE_WRITE_ENABLED", "GOTOOLCHAIN", "MISE_DATA_DIR", "MISE_CONFIG_DIR", "MISE_TRUSTED_CONFIG_PATHS"}

// A hook runs the changes' code, so its environment is the sandbox's, built from the
// base's config: the names a sandboxed child gets and the base's passthrough, the mode,
// raised to best-effort, for a magus the hook runs, and over them the candidate's box.
// A credential, a GitHub Actions file command and anything else unnamed never reach it,
// and neither does any location of the queue's own caches, whatever the passthrough
// names: nothing inherited overrides the box.
func TestAHooksEnvironmentIsTheSandboxesInItsBox(t *testing.T) {
	for _, name := range []string{"MISE_GITHUB_TOKEN", "FOO_SECRET", "RANDOM_VAR", "MERGEQUEUE_TOKEN", "GITHUB_ENV"} {
		t.Setenv(name, "leaked")
	}
	runner := t.TempDir()
	for _, name := range []string{"GOCACHE", "GOMODCACHE", "GOPATH", "npm_config_cache", "XDG_RUNTIME_DIR", "XDG_CACHE_HOME", "MAGUS_CACHE_DIR", "TOOL_CACHE"} {
		t.Setenv(name, filepath.Join(runner, name))
	}
	t.Setenv("MAGUS_CACHE_WRITE_ENABLED", "false")
	t.Setenv("GOTOOLCHAIN", "auto")
	t.Setenv("MISE_DATA_DIR", "/runner/mise")
	t.Setenv("MISE_CONFIG_DIR", "/runner/mise-config")
	t.Setenv("MISE_TRUSTED_CONFIG_PATHS", "/work:/tmp")
	t.Setenv("PASSED", "p")
	t.Setenv("GLOB_X", "g")
	dir := t.TempDir()
	env := HookEnv{
		Sandbox: config.SandboxConfig{Env: config.SandboxEnv{Passthrough: []string{
			"PASSED", "GLOB_*", "GOCACHE", "GOMODCACHE", "GOPATH", "npm_config_cache", "XDG_*", "MAGUS_CACHE_DIR", "TOOL_CACHE", "HOME",
		}}},
		Spells: map[string]spells.Sandbox{"tool": {Allow: []spells.SandboxAllow{{Env: "TOOL_CACHE", Base: "xdgCache", Path: "tool", Mode: spells.SandboxAccessRW}}}},
		Fixed:  []string{"SET=s", "HOME=/fixed"},
	}
	cand := boxed(t, types.Candidate{Commit: "s", Dir: dir})
	_, err := CommandGate(script(`env -0 > seen`), env, nil).Validate(context.Background(), cand, hookUnits)
	require.NoError(t, err)
	seen := seenEnv(t, dir)

	for _, name := range []string{"MISE_GITHUB_TOKEN", "FOO_SECRET", "RANDOM_VAR", "MERGEQUEUE_TOKEN", "GITHUB_ENV", "GOCACHE", "GOMODCACHE", "GOPATH", "npm_config_cache", "XDG_RUNTIME_DIR", "TOOL_CACHE"} {
		assert.NotContains(t, seen, name, "%s reaches the hook", name)
	}
	home := cand.Home
	box := map[string]string{}
	for _, name := range boxedNames {
		box[name] = seen[name]
	}
	assert.Equal(t, map[string]string{
		"HOME":                      home,
		"XDG_CACHE_HOME":            filepath.Join(home, ".cache"),
		"XDG_CONFIG_HOME":           filepath.Join(home, ".config"),
		"XDG_DATA_HOME":             filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME":            filepath.Join(home, ".local", "state"),
		"TMPDIR":                    cand.TempDir,
		"MAGUS_CACHE_DIR":           filepath.Join(home, ".cache", "magus"),
		"MAGUS_CACHE_WRITE_ENABLED": "true",
		"GOTOOLCHAIN":               "local",
		"MISE_DATA_DIR":             "/runner/mise",
		"MISE_CONFIG_DIR":           "/runner/mise-config",
		"MISE_TRUSTED_CONFIG_PATHS": "/work:/tmp",
	}, box)
	assert.Equal(t, os.Getenv("PATH"), seen["PATH"])
	assert.Equal(t, string(magustypes.SandboxModeBestEffort), seen[procrun.SandboxEnvVar])
	assert.Equal(t, "p", seen["PASSED"])
	assert.Equal(t, "g", seen["GLOB_X"])
	assert.Equal(t, "s", seen["SET"])
	allowed := slices.Concat(sandboxenv.DefaultAllow(), boxedNames, []string{"PASSED", "GLOB_X", "SET"},
		// What magus gives every sandboxed child: itself and its mode.
		[]string{"MAGUS", "MAGUS_LEVEL", procrun.AncestorsEnvVar, procrun.SandboxEnvVar},
		[]string{"SHLVL", "_", "OLDPWD", "PWD"}) // what sh sets itself
	for name := range seen {
		assert.Contains(t, allowed, name, "%s reaches the hook", name)
	}
}

// Where the runner names no mise directory, a hook's is where the runner's mise looks by
// default, and a runner with no trust list hands none on.
func TestBoxEnvFindsTheRunnersMiseByItsDefaults(t *testing.T) {
	t.Setenv("MISE_DATA_DIR", "")
	t.Setenv("MISE_CONFIG_DIR", "")
	t.Setenv("MISE_TRUSTED_CONFIG_PATHS", "")
	t.Setenv("XDG_DATA_HOME", "/runner/data")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/runner/home")
	assert.Equal(t, []string{
		"HOME=/box/home",
		"XDG_CACHE_HOME=/box/home/.cache",
		"XDG_CONFIG_HOME=/box/home/.config",
		"XDG_DATA_HOME=/box/home/.local/share",
		"XDG_STATE_HOME=/box/home/.local/state",
		"TMPDIR=/box/tmp",
		"MAGUS_CACHE_DIR=/box/home/.cache/magus",
		"MAGUS_CACHE_WRITE_ENABLED=true",
		"GOTOOLCHAIN=local",
		"MISE_DATA_DIR=/runner/data/mise",
		"MISE_CONFIG_DIR=/runner/home/.config/mise",
	}, boxEnv("/box/home", "/box/tmp"))
}

// A base that requires the kernel sandbox keeps that mode for its hooks: where the
// kernel cannot confine them, none runs, and that is the machine's failure.
func TestARequiredBaseSandboxIsTheHooksToo(t *testing.T) {
	dir := t.TempDir()
	env := HookEnv{Sandbox: config.SandboxConfig{Mode: magustypes.SandboxModeRequired}}
	res, err := CommandGate(script(`echo "$MAGUS_SANDBOX" > seen`), env, nil).Validate(context.Background(), boxed(t, types.Candidate{Commit: "s", Dir: dir}), hookUnits)
	if abi, abiErr := sandbox.ABI(); abiErr != nil || abi < sandbox.RequiredABI {
		require.ErrorIs(t, err, magustypes.SandboxRequired)
		assert.NoFileExists(t, filepath.Join(dir, "seen"))
		return
	}
	require.NoError(t, err)
	assert.True(t, res.Green)
	seen, err := os.ReadFile(filepath.Join(dir, "seen"))
	require.NoError(t, err)
	assert.Equal(t, "required\n", string(seen))
}

// A target's own grant bounds the hook as a spell's does, since the gate's magus stacks
// that target's sandbox on the hook's: the root test target's read of /proc reaches the
// box's policy read-only, and adds no other rule.
func TestABoxedHooksPolicyCarriesATargetsGrantAndNothingMore(t *testing.T) {
	home, tmp := newBox(t)
	without := hookCommand{Dir: t.TempDir(), Home: home, TempDir: tmp, Env: boxEnv(home, tmp)}
	with := without
	with.Spells = map[string]spells.Sandbox{".:test": {Allow: []spells.SandboxAllow{{Path: "/proc", Mode: spells.SandboxAccessRO}}}}
	before, err := without.policy()
	require.NoError(t, err)
	after, err := with.policy()
	require.NoError(t, err)

	ctx, status := t.Context(), "/proc/1/status"
	assert.ErrorIs(t, before.CheckRead(ctx, status), filesystem.ErrDenied)
	assert.NoError(t, after.CheckRead(ctx, status))
	assert.ErrorIs(t, after.CheckWrite(ctx, status), filesystem.ErrDenied)
	assert.ErrorIs(t, after.CheckExec(ctx, status), filesystem.ErrDenied)
	added := slices.DeleteFunc(slices.Clone(after.FS.Rules), func(r filesystem.Rule) bool { return slices.Contains(before.FS.Rules, r) })
	dropped := slices.DeleteFunc(slices.Clone(before.FS.Rules), func(r filesystem.Rule) bool { return slices.Contains(after.FS.Rules, r) })
	assert.Equal(t, []filesystem.Rule{{Path: "/proc", Read: true}}, added)
	assert.Empty(t, dropped)
	assert.Empty(t, after.WritesOutside(with.Dir, home, tmp))
}

// goDecl stands in for the go spell's declaration: its toolchain read and run where the
// runner installed it, its caches written.
func goDecl(goroot string) map[string]spells.Sandbox {
	return map[string]spells.Sandbox{"go": {Allow: []spells.SandboxAllow{
		{Env: "MAGUS_TEST_GOROOT", Mode: spells.SandboxAccessRX},
		{Env: "GOCACHE", Base: "userCache", Path: "go-build", Mode: spells.SandboxAccessRWX},
		{Env: "GOMODCACHE", Base: "home", Path: "go/pkg/mod", Mode: spells.SandboxAccessRW},
	}}}
}

// A hook's policy is built from its box's environment: every cache a declaration grants
// resolves in its home, which it may write and run from, as go run runs what it caches in
// GOCACHE, while the runner's own caches are out of reach and the toolchain the runner
// installed is read and run where it is.
func TestABoxedHooksPolicyGrantsItsBoxAndNotTheRunnersCaches(t *testing.T) {
	runner := filesystem.ResolveRulePath(t.TempDir())
	goroot := filepath.Join(runner, "go")
	t.Setenv("GOCACHE", filepath.Join(runner, "gocache"))
	t.Setenv("GOMODCACHE", filepath.Join(runner, "gomod"))
	t.Setenv("MAGUS_TEST_GOROOT", goroot)
	home, tmp := newBox(t)
	c := hookCommand{Dir: t.TempDir(), Spells: goDecl(goroot), Home: home, TempDir: tmp, Env: boxEnv(home, tmp)}
	p, err := c.policy()
	require.NoError(t, err)

	ctx := t.Context()
	for _, path := range []string{
		filepath.Join(home, ".cache", "go-build", "29", "29d7-d", "magus-utils"),
		filepath.Join(home, "Library", "Caches", "go-build", "29", "29d7-d", "magus-utils"),
		filepath.Join(home, "go", "pkg", "mod", "cache", "x"),
		filepath.Join(tmp, "go-build1", "b001", "exe", "main"),
	} {
		assert.NoError(t, p.CheckWrite(ctx, path), path)
		assert.NoError(t, p.CheckExec(ctx, path), path)
	}
	assert.NoError(t, p.CheckExec(ctx, filepath.Join(goroot, "bin", "go")))
	for _, path := range []string{
		filepath.Join(runner, "gocache", "ab", "x-d"),
		filepath.Join(runner, "gomod", "cache", "x"),
		filepath.Join(goroot, "bin", "go"),
		filepath.Join(filepath.Dir(home), "beside"),
	} {
		assert.ErrorIs(t, p.CheckWrite(ctx, path), filesystem.ErrDenied, path)
	}
	assert.Empty(t, p.WritesOutside(c.Dir, home, tmp))
}

// A hook whose policy would still write outside its box, as a base's config granting the
// runner's own GOCACHE would, is the machine's error: another candidate's hook could
// plant there what this one's gate replays. It never runs.
func TestAHookIsRefusedWhenItsPolicyWouldWriteOutsideItsBox(t *testing.T) {
	gocache := filesystem.ResolveRulePath(t.TempDir())
	env := HookEnv{Sandbox: config.SandboxConfig{Allow: []spells.SandboxAllow{{Path: gocache, Mode: spells.SandboxAccessRW}}}}
	dir := t.TempDir()
	res, err := CommandGate(script(`touch ran`), env, nil).Validate(context.Background(), boxed(t, types.Candidate{Commit: "s", Dir: dir}), hookUnits)
	require.ErrorContains(t, err, "the sandbox grants a hook write on `"+gocache+"`, outside its candidate's box")
	assert.Equal(t, types.GateResult{}, res)
	assert.NoFileExists(t, filepath.Join(dir, "ran"))

	err = CommandRegenerate(script(`touch ran`), env, nil)(context.Background(), boxedRegen(t, types.Regeneration{Dir: dir, Change: hookChange, Paths: []string{"x"}, Units: hookUnits}))
	var refused *types.RefusedError
	require.False(t, errors.As(err, &refused), "the machine's, not the change's: %v", err)
	require.ErrorContains(t, err, "outside its candidate's box")
}

// Only a hook the change ran can have changed its box, so a box whose home or temporary
// directory is not the private directory the queue made, or that holds a link leading a
// grant out of it, makes that change red and the run goes on.
func TestABoxAHookChangedIsTheChangesRed(t *testing.T) {
	outside := t.TempDir()
	cacheGrant := map[string]spells.Sandbox{"tool": {Allow: []spells.SandboxAllow{{Base: "xdgCache", Path: "tool", Mode: spells.SandboxAccessRW}}}}
	for name, tc := range map[string]struct {
		change func(t *testing.T, home string)
		why    string
	}{
		"home is a link": {func(t *testing.T, home string) {
			require.NoError(t, os.Remove(home))
			require.NoError(t, os.Symlink(outside, home))
		}, "is a symbolic link"},
		"home is a file": {func(t *testing.T, home string) {
			require.NoError(t, os.Remove(home))
			require.NoError(t, os.WriteFile(home, nil, 0o600))
		}, "is not a directory"},
		"home is mode 000": {func(t *testing.T, home string) {
			require.NoError(t, os.Chmod(home, 0))
		}, "is mode 0000, not 0700"},
		"a link in home leads a grant out": {func(t *testing.T, home string) {
			require.NoError(t, os.Symlink(outside, filepath.Join(home, ".cache")))
		}, "found a link in its box leading a grant out of it, to `" + filepath.Join(filesystem.ResolveRulePath(outside), "tool") + "`"},
	} {
		t.Run(name, func(t *testing.T) {
			env := HookEnv{Spells: cacheGrant}
			dir := t.TempDir()
			cand := boxed(t, types.Candidate{Commit: "s", Dir: dir})
			tc.change(t, cand.Home)
			res, err := CommandGate(script(`touch ran`), env, nil).Validate(context.Background(), cand, hookUnits)
			require.NoError(t, err)
			assert.False(t, res.Green)
			assert.Contains(t, res.Summary, tc.why)
			assert.NoFileExists(t, filepath.Join(dir, "ran"))

			r := boxedRegen(t, types.Regeneration{Dir: dir, Change: hookChange, Paths: []string{"x"}, Units: hookUnits})
			tc.change(t, r.Home)
			err = CommandRegenerate(script(`touch ran`), env, nil)(context.Background(), r)
			var refused *types.RefusedError
			require.ErrorAs(t, err, &refused)
			assert.Contains(t, refused.Reason, tc.why)
		})
	}
}

// A hook builds a tool into its cache and runs it from there.
func TestAHookRunsWhatItBuildsInItsCache(t *testing.T) {
	dir := t.TempDir()
	body := `mkdir -p "$XDG_CACHE_HOME/go-build" && printf '#!/bin/sh\necho ran\n' > "$XDG_CACHE_HOME/go-build/tool" &&
		chmod +x "$XDG_CACHE_HOME/go-build/tool" && "$XDG_CACHE_HOME/go-build/tool" > seen`
	cand := boxed(t, types.Candidate{Commit: "s", Dir: dir})
	res, err := CommandGate(script(body), HookEnv{}, nil).Validate(context.Background(), cand, hookUnits)
	require.NoError(t, err)
	assert.True(t, res.Green, res.Summary)
	seen, err := os.ReadFile(filepath.Join(dir, "seen"))
	require.NoError(t, err)
	assert.Equal(t, "ran\n", string(seen))
	assert.FileExists(t, filepath.Join(cand.Home, ".cache", "go-build", "tool"))
}

// Two candidates of one validation keep separate caches: neither's hook finds the
// other's entries in its own, and where the kernel confines hooks, cannot read them.
func TestTwoCandidatesCannotSeeEachOthersCaches(t *testing.T) {
	first := boxed(t, types.Candidate{Commit: "a", Change: "1", Dir: t.TempDir()})
	res, err := CommandGate(script(`mkdir -p "$MAGUS_CACHE_DIR" && echo first > "$MAGUS_CACHE_DIR/entry"`), HookEnv{}, nil).
		Validate(context.Background(), first, hookUnits)
	require.NoError(t, err)
	require.True(t, res.Green, res.Summary)
	planted := filepath.Join(first.Home, ".cache", "magus", "entry")
	require.FileExists(t, planted)

	second := boxed(t, types.Candidate{Commit: "b", Change: "2", Dir: t.TempDir()})
	body := `ls -A "$MAGUS_CACHE_DIR" > own 2>/dev/null; echo "$MAGUS_CACHE_DIR" > dir; cat "$1" > read 2>/dev/null; true`
	res, err = CommandGate(Command{"sh", "-c", body, "hook", planted}, HookEnv{}, nil).Validate(context.Background(), second, nil)
	require.NoError(t, err)
	require.True(t, res.Green, res.Summary)
	own, err := os.ReadFile(filepath.Join(second.Dir, "own"))
	require.NoError(t, err)
	assert.Empty(t, string(own), "the second candidate's cache holds nothing the first wrote")
	cacheDir, err := os.ReadFile(filepath.Join(second.Dir, "dir"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(second.Home, ".cache", "magus")+"\n", string(cacheDir))
	if abi, err := sandbox.ABI(); err == nil && abi >= 1 {
		read, err := os.ReadFile(filepath.Join(second.Dir, "read"))
		require.NoError(t, err)
		assert.Empty(t, string(read), "landlock keeps the first candidate's box out of reach")
	}
}

// Where the kernel has landlock, a hook writes in its checkout and its box and nowhere
// else, and a process it starts is held to the same.
func TestAHookIsConfinedToItsBoxWhereTheKernelCan(t *testing.T) {
	if abi, err := sandbox.ABI(); err != nil || abi < 1 {
		t.Skip("no landlock on this host: best-effort confines the environment only")
	}
	dir, outside := t.TempDir(), t.TempDir()
	body := `touch in "$HOME/home-file" "$TMPDIR/tmp-file" && sh -c 'touch "$1/escaped"' child "$1"`
	cand := boxed(t, types.Candidate{Commit: "s", Dir: dir})
	res, err := CommandGate(Command{"sh", "-c", body, "hook", outside}, HookEnv{}, nil).Validate(context.Background(), cand, nil)
	require.NoError(t, err)
	assert.False(t, res.Green, "the write outside the grant fails")
	assert.FileExists(t, filepath.Join(dir, "in"))
	assert.FileExists(t, filepath.Join(cand.Home, "home-file"))
	assert.FileExists(t, filepath.Join(cand.TempDir, "tmp-file"))
	assert.NoFileExists(t, filepath.Join(outside, "escaped"))
}

// A target's read of /proc reaches a boxed hook's tree through the kernel, and stops at
// its landlock domain: the hook lists /proc and reads its own entries, and cannot read
// the environment of the process that started it, which sits outside that domain.
// Without the grant it cannot list /proc at all.
func TestAHooksProcGrantStopsAtItsLandlockDomainWhereTheKernelCan(t *testing.T) {
	if abi, err := sandbox.ABI(); err != nil || abi < 1 {
		t.Skip("no landlock on this host: best-effort confines the environment only")
	}
	body := `ls /proc >/dev/null && cat /proc/$$/status >/dev/null && touch listed
if cat "/proc/$1/environ" >/dev/null; then touch leaked; fi`
	proc := map[string]spells.Sandbox{".:test": {Allow: []spells.SandboxAllow{{Path: "/proc", Mode: spells.SandboxAccessRO}}}}
	for name, c := range map[string]struct {
		env    HookEnv
		listed bool
	}{"granted": {HookEnv{Spells: proc}, true}, "not granted": {HookEnv{}, false}} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			cand := boxed(t, types.Candidate{Commit: "s", Dir: dir})
			_, err := CommandGate(Command{"sh", "-c", body, "hook", strconv.Itoa(os.Getpid())}, c.env, nil).Validate(context.Background(), cand, nil)
			require.NoError(t, err)
			if c.listed {
				assert.FileExists(t, filepath.Join(dir, "listed"))
			} else {
				assert.NoFileExists(t, filepath.Join(dir, "listed"))
			}
			assert.NoFileExists(t, filepath.Join(dir, "leaked"))
		})
	}
}

// git needs nothing from the runner's home: a hook runs it in its checkout with its box's.
func TestAHookRunsGitWithItsBoxsHome(t *testing.T) {
	dir := t.TempDir()
	res, err := CommandGate(script(`git init -q . && git status --porcelain > seen`), HookEnv{}, nil).
		Validate(context.Background(), boxed(t, types.Candidate{Commit: "s", Dir: dir}), hookUnits)
	require.NoError(t, err)
	assert.True(t, res.Green, res.Summary)
	assert.FileExists(t, filepath.Join(dir, "seen"))
}

// A hook in a candidate's checkout reads the object store every candidate shares and
// writes none of it; its checkout's own git directory, where the index lives, stays its
// to write. Where the kernel has landlock, a write into the store fails.
func TestAHookCannotWriteTheSharedObjectStore(t *testing.T) {
	root, commit := gitRepo(t, map[string]string{"a.txt": "a\n"})
	drv := gitDriver(t, root)
	cand, err := checkout(t.Context(), drv, root, t.TempDir(), "candidate-1", commit)
	require.NoError(t, err)
	t.Cleanup(func() { _ = discard(context.Background(), drv, root, cand) })
	objects := filepath.Join(root, ".git", "objects")
	own := filepath.Join(root, ".git", "worktrees", "checkout")
	require.DirExists(t, own)

	p, err := hookCommand{Dir: cand.Dir, Home: cand.Home, TempDir: cand.TempDir, Env: boxEnv(cand.Home, cand.TempDir)}.policy()
	require.NoError(t, err)
	assert.ErrorIs(t, p.CheckWrite(t.Context(), filepath.Join(objects, "ab", "cd")), filesystem.ErrDenied)
	assert.NoError(t, p.CheckRead(t.Context(), filepath.Join(objects, "ab", "cd")))
	assert.NoError(t, p.CheckWrite(t.Context(), filepath.Join(own, "index")))
	assert.Empty(t, p.WritesOutside(cand.Dir, cand.Home, cand.TempDir), "the checkout's own git directory is its to write")

	if abi, err := sandbox.ABI(); err != nil || abi < 1 {
		t.Skip("no landlock on this host: best-effort confines the environment only")
	}
	res, err := CommandGate(script(fmt.Sprintf(`touch %q && touch %q`, filepath.Join(own, "probe"), filepath.Join(objects, "planted"))), HookEnv{}, nil).
		Validate(t.Context(), cand, hookUnits)
	require.NoError(t, err)
	assert.False(t, res.Green)
	assert.FileExists(t, filepath.Join(own, "probe"))
	assert.NoFileExists(t, filepath.Join(objects, "planted"))
}

// Every kind of hook takes the same HookEnv: a passthrough name and a fixed assignment
// reach a gate, a regeneration and a facts hook alike, over the queue's own value.
func TestEveryHookTakesItsHookEnv(t *testing.T) {
	t.Setenv("PASSED", "p")
	t.Setenv("FIXED", "queue")
	env := HookEnv{Sandbox: config.SandboxConfig{Env: config.SandboxEnv{Passthrough: []string{"PASSED", "FIXED"}}}, Fixed: []string{"FIXED=f"}}
	record := script(`echo "$PASSED $FIXED" > seen; echo '{"units": ["//..."]}'`)
	ctx := context.Background()

	gated := t.TempDir()
	_, err := CommandGate(record, env, nil).Validate(ctx, boxed(t, types.Candidate{Commit: "s", Dir: gated}), hookUnits)
	require.NoError(t, err)
	regenerated := t.TempDir()
	require.NoError(t, CommandRegenerate(record, env, nil)(ctx, boxedRegen(t, types.Regeneration{Dir: regenerated, Change: hookChange, Paths: []string{"x"}, Units: hookUnits})))
	asked := t.TempDir()
	_, err = CommandFacts(record, asked, env, nil).AllUnits(ctx)
	require.NoError(t, err)

	for _, dir := range []string{gated, regenerated, asked} {
		seen, err := os.ReadFile(filepath.Join(dir, "seen"))
		require.NoError(t, err)
		assert.Equal(t, "p f\n", string(seen), dir)
	}
}

// A hook is usually a nested magus, and landlock domains stack, so the hook gets the
// declarations of every spell the base loaded: a spell's toolchain variable reaches a
// gate, which may run the toolchain, and a facts hook, which runs in the base's own
// checkout with the queue's own caches, may write a spell's cache there.
func TestAHookGetsEverySpellTheBaseLoaded(t *testing.T) {
	tool, cache := filesystem.ResolveRulePath(t.TempDir()), filesystem.ResolveRulePath(t.TempDir())
	t.Setenv("MAGUS_TEST_SPELL_TOOL", tool)
	t.Setenv("MAGUS_TEST_SPELL_CACHE", cache)
	dir := t.TempDir()
	env := HookEnv{Spells: map[string]spells.Sandbox{"tool": {
		Allow: []spells.SandboxAllow{
			{Env: "MAGUS_TEST_SPELL_TOOL", Mode: spells.SandboxAccessRX},
			{Env: "MAGUS_TEST_SPELL_CACHE", Base: "userCache", Path: "tool", Mode: spells.SandboxAccessRW},
		},
		Env: spells.SandboxEnv{Passthrough: []string{"MAGUS_TEST_SPELL_TOOL", "MAGUS_TEST_SPELL_CACHE"}},
	}}}
	cand := boxed(t, types.Candidate{Commit: "s", Dir: dir})
	res, err := CommandGate(script(`echo "$MAGUS_TEST_SPELL_TOOL" > seen`), env, nil).Validate(context.Background(), cand, hookUnits)
	require.NoError(t, err)
	assert.True(t, res.Green)
	seen, err := os.ReadFile(filepath.Join(dir, "seen"))
	require.NoError(t, err)
	assert.Equal(t, tool+"\n", string(seen))
	gate, err := hookCommand{Dir: dir, Spells: env.Spells, Home: cand.Home, TempDir: cand.TempDir, Env: boxEnv(cand.Home, cand.TempDir)}.policy()
	require.NoError(t, err)
	assert.NoError(t, gate.CheckExec(context.Background(), filepath.Join(tool, "bin", "tool")))
	assert.ErrorIs(t, gate.CheckWrite(context.Background(), filepath.Join(cache, "x")), filesystem.ErrDenied, "the runner's cache is not the box's")

	p, err := hookCommand{Dir: dir, Spells: env.Spells}.policy()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(p.TempDir) })
	assert.NoError(t, p.CheckWrite(context.Background(), filepath.Join(cache, "x")))
	withoutSpells, err := hookCommand{Dir: dir}.policy()
	require.NoError(t, err)
	assert.ErrorIs(t, withoutSpells.CheckWrite(context.Background(), filepath.Join(cache, "x")), filesystem.ErrDenied)
}

func TestAPassthroughThatIsNoGlobIsAnError(t *testing.T) {
	env := HookEnv{Sandbox: config.SandboxConfig{Env: config.SandboxEnv{Passthrough: []string{"*"}}}}
	_, err := CommandGate(Command{"true"}, env, nil).Validate(context.Background(), boxed(t, types.Candidate{Commit: "s", Dir: t.TempDir()}), hookUnits)
	require.ErrorIs(t, err, magustypes.AllowlistUnresolved)
}

// A candidate's lines name its change, and a commit gated as it stands says it is the
// base, so the interleaved log of several gates reads on its own.
func TestGateTagsEveryOutputLineWithTheCommitAndWhatItHolds(t *testing.T) {
	var log bytes.Buffer
	g := CommandGate(script(`printf 'one\ntwo\n'; echo three >&2; printf 'no newline'`), HookEnv{}, NewHookLog(&log))
	_, err := g.Validate(context.Background(), boxed(t, types.Candidate{Commit: "0123456789abcdef", Change: "7", Dir: t.TempDir()}), hookUnits)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"[0123456789ab #7] one", "[0123456789ab #7] two", "[0123456789ab #7] three", "[0123456789ab #7] no newline"}, lines(&log))

	log.Reset()
	_, err = CommandGate(script(`echo one`), HookEnv{}, NewHookLog(&log)).Validate(context.Background(), boxed(t, types.Candidate{Commit: "fedcba9876543210", Dir: t.TempDir()}), hookUnits)
	require.NoError(t, err)
	assert.Equal(t, []string{"[fedcba987654 base] one"}, lines(&log))
}

func TestGateRunsATemporaryFailureAgainAndThenCallsItRed(t *testing.T) {
	g := CommandGate(script(`echo x >> tries; test "$(wc -l < tries)" -ge 3 || exit 75`), HookEnv{}, nil)
	res, err := g.Validate(context.Background(), boxed(t, types.Candidate{Commit: "s", Dir: t.TempDir()}), hookUnits)
	require.NoError(t, err)
	assert.True(t, res.Green)

	res, err = CommandGate(script(`exit 75`), HookEnv{}, nil).Validate(context.Background(), boxed(t, types.Candidate{Commit: "s", Dir: t.TempDir()}), hookUnits)
	require.NoError(t, err, "the change's processes chose the exit status, so it proves nothing about the machine")
	assert.False(t, res.Green)
	assert.Equal(t, "the gate exited 75 (temporary failure) 3 times", res.Summary)
}

// A change's own OOM kill, or a death by any other signal, is its red verdict. Before, it
// was the machine's failure, and a change dying that way stopped its partition every run.
func TestAGateKilledByASignalIsTheChangesRedVerdict(t *testing.T) {
	for body, why := range map[string]string{
		`kill -KILL $$`:                  "was killed (signal: killed)",
		`sh -c 'kill -KILL $$'; exit $?`: "exited 137",
		`exit 143`:                       "exited 143",
	} {
		res, err := CommandGate(script(body), HookEnv{}, nil).Validate(context.Background(), boxed(t, types.Candidate{Commit: "s", Dir: t.TempDir()}), hookUnits)
		require.NoError(t, err, body)
		assert.False(t, res.Green)
		assert.Contains(t, res.Summary, why, body)
	}
}

// Nothing a hook started outlives it: before, a background process kept running after
// the gate returned, into the next candidate and past the verdict it led to.
func TestAHooksProcessesDoNotOutliveIt(t *testing.T) {
	dir := t.TempDir()
	res, err := CommandGate(script(`(sleep 0.3; touch late) & echo started`), HookEnv{}, nil).Validate(context.Background(), boxed(t, types.Candidate{Commit: "s", Dir: dir}), hookUnits)
	require.NoError(t, err)
	assert.True(t, res.Green)
	time.Sleep(600 * time.Millisecond)
	assert.NoFileExists(t, filepath.Join(dir, "late"))
}

func TestARegenerationGetsItsUnitsAsArgumentsAndIsRefusedWhenItFails(t *testing.T) {
	regen := CommandRegenerate(script(`cat > got; printf '[%s]' "$@" > units`), HookEnv{}, nil)
	dir := t.TempDir()
	require.NoError(t, regen(context.Background(), boxedRegen(t, types.Regeneration{Dir: dir, Change: hookChange, Paths: []string{"app/gen/a", "app/gen/b"}, Units: []string{"app", "lib"}})))
	got, err := os.ReadFile(filepath.Join(dir, "got"))
	require.NoError(t, err)
	assert.Equal(t, "app/gen/a\napp/gen/b\n", string(got))
	units, err := os.ReadFile(filepath.Join(dir, "units"))
	require.NoError(t, err)
	assert.Equal(t, "[app][lib]", string(units))

	err = CommandRegenerate(script(`exit 3`), HookEnv{}, nil)(context.Background(), boxedRegen(t, types.Regeneration{Dir: t.TempDir(), Change: hookChange, Paths: []string{"x"}, Units: hookUnits}))
	var refused *types.RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, &types.RefusedError{Reason: "the regeneration exited 3", Paths: []string{"x"}}, refused)

	err = CommandRegenerate(script(`kill -KILL $$`), HookEnv{}, nil)(context.Background(), boxedRegen(t, types.Regeneration{Dir: t.TempDir(), Change: hookChange, Paths: []string{"x"}, Units: hookUnits}))
	require.ErrorAs(t, err, &refused)
	assert.Contains(t, refused.Reason, "was killed")
}

// A gate or a regeneration always runs in a box; one with none never runs with the
// queue's own home and caches instead.
func TestAHookWithNoBoxIsTheMachinesError(t *testing.T) {
	dir := t.TempDir()
	_, err := CommandGate(script(`touch ran`), HookEnv{}, nil).Validate(context.Background(), types.Candidate{Commit: "s", Dir: dir}, hookUnits)
	require.EqualError(t, err, "gate on `s`: the candidate has no box to run its hooks in")
	err = CommandRegenerate(script(`touch ran`), HookEnv{}, nil)(context.Background(), types.Regeneration{Dir: dir, Change: hookChange, Paths: []string{"x"}, Units: hookUnits})
	require.EqualError(t, err, "regenerate "+hookChange.Label()+": the candidate has no box to run its hooks in")
	assert.NoFileExists(t, filepath.Join(dir, "ran"))
}

// A hook that cannot start is a machine failure: a checkout gone, or a program nothing
// provides, which no change's code chose.
func TestAHookThatCannotStartIsTheMachinesFailure(t *testing.T) {
	_, err := CommandGate(Command{"true"}, HookEnv{}, nil).Validate(context.Background(), boxed(t, types.Candidate{Commit: "s", Dir: filepath.Join(t.TempDir(), "gone")}), hookUnits)
	require.ErrorContains(t, err, "gate on `s`")
	_, err = CommandGate(Command{"no-such-gate-program"}, HookEnv{}, nil).Validate(context.Background(), boxed(t, types.Candidate{Commit: "s", Dir: t.TempDir()}), hookUnits)
	require.ErrorContains(t, err, "gate on `s`")
}

// facts is a build tool's facts command: the queue appends the fact it asks for, as a
// person running `facts affected` would type it.
var facts = script(`case "$1" in
	affected) read p; printf '{"count": 1, "matrix": [], "affected": ["%s"], "unbounded_by": ""}' "${p%%/*}";;
	outputs) echo '{"outputs": ["gen/a", "elsewhere"], "updated": ["docs/b.md", "gen/a"], "maintained": [".gitattributes"]}';;
	generation) cat > asked; echo '{"units": ["app"], "code": ["app/gen.go"], "unbounded": ""}';;
	all) echo '{"units": ["//..."]}';;
	*) exit 9;;
	esac`)

func TestCommandFactsGetTheFactAskedForAsTheirArgument(t *testing.T) {
	// magus affected --plan prints a shard plan with affected and unbounded_by beside it.
	got, unboundedBy, err := CommandFacts(facts, t.TempDir(), HookEnv{}, nil).Affected(context.Background(), hookChange, []string{"app/main.go"})
	require.NoError(t, err)
	assert.Equal(t, []string{"app"}, got)
	assert.Empty(t, unboundedBy)
}

func TestCommandFactsWithoutASetAreUnboundedAndAFailureIsAnError(t *testing.T) {
	ctx := context.Background()
	_, unboundedBy, err := CommandFacts(script(`echo '{}'`), t.TempDir(), HookEnv{}, nil).Affected(ctx, hookChange, []string{"x"})
	require.NoError(t, err)
	assert.Equal(t, "the affected hook printed no affected set", unboundedBy)

	_, _, err = CommandFacts(script(`exit 3`), t.TempDir(), HookEnv{}, nil).Affected(ctx, hookChange, []string{"x"})
	require.EqualError(t, err, "affected hook: exited 3")

	got, unboundedBy, err := CommandFacts(script(`exit 3`), t.TempDir(), HookEnv{}, nil).Affected(ctx, hookChange, nil)
	require.NoError(t, err, "a change touching nothing is not asked about")
	assert.Equal(t, []string{}, got)
	assert.Empty(t, unboundedBy)
}

// A facts command that answers only "outputs" declares no update and maintains nothing.
func TestCommandFactsReadAnOutputsOnlyAnswer(t *testing.T) {
	writes, err := CommandFacts(script(`echo '{"outputs": ["gen/a"]}'`), t.TempDir(), HookEnv{}, nil).Classify(context.Background(), []string{"gen/a", ".gitattributes"})
	require.NoError(t, err)
	assert.Equal(t, map[string]types.Writes{"gen/a": {Output: true}}, writes)
}

// The auto_resolve fact is the build tool's classification of one merge, asked with the
// merge base's content and the merge; a command that cannot answer it settles nothing.
func TestCommandFactsAnswerAutoResolve(t *testing.T) {
	dir := t.TempDir()
	facts := script(`case "$1" in
	auto_resolve) cat > asked; echo '{"auto_resolve": true, "verdict": "CHANGELOG.md: prose (a glob)"}';;
	*) exit 9;;
	esac`)
	verdict, ok, err := CommandFacts(facts, dir, HookEnv{}, nil).AutoResolvable(context.Background(), "CHANGELOG.md", []byte("a\n"), []byte("a\nb\n"))
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "CHANGELOG.md: prose (a glob)", verdict)
	asked, err := os.ReadFile(filepath.Join(dir, "asked"))
	require.NoError(t, err)
	assert.JSONEq(t, `{"path": "CHANGELOG.md", "base": "a\n", "merged": "a\nb\n"}`, string(asked))

	verdict, ok, err = CommandFacts(script(`exit 9`), dir, HookEnv{}, nil).AutoResolvable(context.Background(), "x.go", nil, nil)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, "x.go: the facts command answered no auto_resolve (auto_resolve hook: exited 9)", verdict)
}

func TestCommandFactsAnswerWritesGenerationAndEveryUnit(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	f := CommandFacts(facts, dir, HookEnv{}, nil)
	writes, err := f.Classify(ctx, []string{"gen/a", "src/b", "docs/b.md", ".gitattributes"})
	require.NoError(t, err)
	assert.Equal(t, map[string]types.Writes{"gen/a": {Output: true, Updated: true}, "docs/b.md": {Updated: true}, ".gitattributes": {Maintained: true}}, writes,
		"only what was asked about")

	g, err := f.Generation(ctx, []string{"gen/a"}, []string{"app/gen.go", "docs/x.md"})
	require.NoError(t, err)
	assert.Equal(t, types.Generation{Units: []string{"app"}, Code: []string{"app/gen.go"}}, g)
	assert.False(t, regenerationProven(g))
	asked, err := os.ReadFile(filepath.Join(dir, "asked"))
	require.NoError(t, err)
	assert.JSONEq(t, `{"outputs": ["gen/a"], "changed": ["app/gen.go", "docs/x.md"]}`, string(asked))

	all, err := f.AllUnits(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"//..."}, all, "the build tool's own spelling of every unit")

	_, err = CommandFacts(script(`echo '{"units": []}'`), dir, HookEnv{}, nil).AllUnits(ctx)
	require.EqualError(t, err, "all hook named no unit", "no arguments would leave what runs to the tool's default")
}

// The queue appends units after the hook's own words with no "--" between, so a unit
// a hook could read as an option never reaches it.
func TestAUnitAHookCouldReadAsAnOptionIsNeverAppended(t *testing.T) {
	for _, unit := range []string{"-x", "--gate=sh", ""} {
		dir := t.TempDir()
		_, err := CommandGate(script(`touch ran`), HookEnv{}, nil).Validate(context.Background(), boxed(t, types.Candidate{Commit: "s", Dir: dir}), []string{"app", unit})
		require.ErrorContains(t, err, "which a hook would read as an option", "%q", unit)
		assert.NoFileExists(t, filepath.Join(dir, "ran"), "%q", unit)
	}
}

// Paths reach a hook's stdin one per line, and git allows a line break in a path, which
// would read as two paths: such a path is never written.
func TestAPathWithALineBreakNeverReachesAHooksStdin(t *testing.T) {
	ctx := context.Background()
	for _, broken := range []string{"gen/a\napp/main.go", "gen/a\r"} {
		dir := t.TempDir()
		err := CommandRegenerate(script(`cat > got`), HookEnv{}, nil)(ctx, boxedRegen(t, types.Regeneration{Dir: dir, Change: hookChange, Paths: []string{"gen/ok", broken}, Units: hookUnits}))
		var refused *types.RefusedError
		require.ErrorAs(t, err, &refused, "%q", broken)
		assert.Equal(t, []string{broken}, refused.Paths)
		assert.NoFileExists(t, filepath.Join(dir, "got"), "the regeneration never ran")

		got, unboundedBy, err := CommandFacts(script(`exit 9`), dir, HookEnv{}, nil).Affected(ctx, hookChange, []string{"app/x.go", broken})
		require.NoError(t, err, "the hook is not asked")
		assert.Nil(t, got)
		assert.Contains(t, unboundedBy, "holds a line break", "every unit is gated instead")

		writes, err := CommandFacts(script(`cat > asked; echo '{"outputs": ["gen/ok"]}'`), dir, HookEnv{}, nil).Classify(ctx, []string{"gen/ok", broken})
		require.NoError(t, err)
		assert.Equal(t, map[string]types.Writes{"gen/ok": {Output: true}}, writes, "left unclassified, which is source")
		asked, err := os.ReadFile(filepath.Join(dir, "asked"))
		require.NoError(t, err)
		assert.Equal(t, "gen/ok\n", string(asked))
	}
}
