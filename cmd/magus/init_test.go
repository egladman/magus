package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureStderr runs fn with os.Stderr redirected to a pipe and returns what it
// wrote there. Only interactive.Emit/fmt.Fprintln output is captured: the slog
// default handler holds the process's original stderr from package init.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	previous := os.Stderr
	read, write, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = write
	defer func() { os.Stderr = previous }()

	fn()

	require.NoError(t, write.Close())
	out, err := io.ReadAll(read)
	require.NoError(t, err)
	return string(out)
}

func TestWriteMagusfileStub(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, writeMagusfileStub(dir))
	data, err := os.ReadFile(filepath.Join(dir, "magusfile.buzz"))
	require.NoError(t, err, "expected magusfile.buzz")
	body := string(data)
	for _, want := range []string{
		`import "magus"`,
		"magus.project",
		`export fun preflight`,
		`export fun test`,
	} {
		assert.Contains(t, body, want, "magusfile.buzz missing %q", want)
	}
}

func TestInitSpellCmd(t *testing.T) {
	t.Run("scaffolds a parseable, contract-complete spell", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, initSpellCmd(context.Background(), []string{"--dir", dir, "acme"}))

		data, err := os.ReadFile(filepath.Join(dir, "acme", "spell.buzz"))
		require.NoError(t, err, "expected spells/acme/spell.buzz")
		body := string(data)

		// The generated spell must parse (embedded mode, as the engine loads it).
		_, perr := buzz.ParseEmbedded(body)
		require.NoError(t, perr, "scaffolded spell must parse")

		for _, want := range []string{
			// The teaching comment showing how to compose the op into a target is
			// the first thing a spell author copies, so it must be the shape the
			// magusfile checker accepts: a magus\Context first parameter (MGS1008)
			// and the ctx threaded into the op call.
			`export fun build(ctx: magus\Context, args: [str]) > void { acme.build(ctx); }`,
			`export fun mgs_getName() > str { return "acme"; }`,
			"mgs_listRequiredGlobs",
			"mgs_listTargets",
			`import "magus/spell"`,
			`import "magus/charm"`,
			`test "build op forks the expected command"`,
		} {
			assert.Contains(t, body, want, "scaffold missing %q", want)
		}
	})

	t.Run("rejects an invalid handle", func(t *testing.T) {
		dir := t.TempDir()
		err := initSpellCmd(context.Background(), []string{"--dir", dir, "1bad name"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a valid handle")
	})

	t.Run("refuses to overwrite without --force", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, initSpellCmd(context.Background(), []string{"--dir", dir, "acme"}))
		err := initSpellCmd(context.Background(), []string{"--dir", dir, "acme"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
		require.NoError(t, initSpellCmd(context.Background(), []string{"--dir", dir, "--force", "acme"}))
	})
}

// TestInitNextStepsAgentCommandsRun runs every `magus agent ...` line the init
// hint prints, in a scratch directory. The hint used to advertise `agent install
// <dir> --agents-md`, a flag no command ever defined, so the first thing a new
// user copied out of `magus init` died on "flag provided but not defined".
// Asserting the text alone would have kept passing; only executing it catches that.
func TestInitNextStepsAgentCommandsRun(t *testing.T) {
	hints := captureStderr(t, func() {
		printInitNextSteps(context.Background(), "magus.yaml", true, true)
	})

	var argvs [][]string
	for _, line := range strings.Split(hints, "\n") {
		// Strip the "hint: " prefix and any trailing "# what this does" comment.
		cmd, _, _ := strings.Cut(strings.TrimPrefix(strings.TrimSpace(line), "hint:"), "#")
		fields := strings.Fields(cmd)
		if len(fields) < 2 || fields[0] != "magus" || fields[1] != "agent" {
			continue
		}
		argvs = append(argvs, fields[2:])
	}
	require.NotEmpty(t, argvs, "init next steps should advertise the agent surface")

	// The installs log a line per written file; keep them out of the test output.
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	for _, argv := range argvs {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			t.Chdir(t.TempDir())
			assert.NoError(t, agentCmd(context.Background(), argv))
		})
	}
}

func TestMagusfilePresent(t *testing.T) {
	for _, name := range []string{"magusfile.buzz"} {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644))
		assert.True(t, magusfilePresent(dir), "magusfilePresent should detect %s", name)
	}
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "magusfiles"), 0o755))
	assert.True(t, magusfilePresent(dir), "magusfilePresent should detect magusfiles/ directory")
	assert.False(t, magusfilePresent(t.TempDir()), "magusfilePresent should be false for an empty directory")
}

// An existing magusfile must not be clobbered by a stub write.
func TestWriteMagusfileStubSkipsExisting(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(existing, []byte("// mine\n"), 0o644))
	require.NoError(t, writeMagusfileStub(dir))
	data, _ := os.ReadFile(existing)
	assert.Equal(t, "// mine\n", string(data), "existing magusfile.buzz was modified")
}

// captureLog redirects the default slog logger to buf for the life of the test,
// restoring the previous default on cleanup.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// TestInitCmdEmptyDirLoadsFreshWorkspace is the regression test for the ordering bug:
// `magus init --local` in an empty directory wrote magus.yaml and magusfile.buzz, then
// warned that it could not find them. installMergeDriverForInit must load the workspace
// it just wrote, not a stale pre-write lookup.
func TestInitCmdEmptyDirLoadsFreshWorkspace(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	buf := captureLog(t)

	require.NoError(t, initCmd(context.Background(), "", []string{"--local"}))

	assert.FileExists(t, filepath.Join(dir, "magus.yaml"))
	assert.FileExists(t, filepath.Join(dir, "magusfile.buzz"))
	assert.NotContains(t, buf.String(), "workspace load failed",
		"init must not warn that the workspace it just wrote could not be found")
}

// TestInitCmdGitDirWiresMergeDriver exercises the reported repro (`--vcs git`) against a
// real git repo whose magusfile already declares outputs, so installMergeDriverForInit
// takes the full path: fresh workspace load, glob collection, and driver install.
func TestInitCmdGitDirWiresMergeDriver(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	buf := captureLog(t)

	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	runGit("init")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "test")

	magusfile := `import "magus";
magus.project({"outputs": ["gen/**"]});
export fun build(ctx: magus\Context, args: [str]) > void {}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magusfile.buzz"), []byte(magusfile), 0o644))

	require.NoError(t, initCmd(context.Background(), "", []string{"--local", "--vcs", "git"}))

	assert.NotContains(t, buf.String(), "workspace load failed",
		"a real git repo with a fresh magusfile must load, not warn")
	data, err := os.ReadFile(filepath.Join(dir, ".gitattributes"))
	require.NoError(t, err, "expected .gitattributes written by the merge driver install")
	assert.Contains(t, string(data), "gen/**")
}

// TestInitCmdSecondRunGlobalConfigHintsLocal is the second half of the F10 repro: a bare
// `magus init` on a machine that already has a global config must not read as "bootstrap
// here failed" - the error should point at --local for a per-repo config.
func TestInitCmdSecondRunGlobalConfigHintsLocal(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())

	require.NoError(t, initCmd(context.Background(), "", nil), "first run must succeed")

	err := initCmd(context.Background(), "", nil)
	require.Error(t, err, "second run without --force must fail")
	assert.Contains(t, err.Error(), "already exists")
	assert.Contains(t, err.Error(), "--local", "error should point at --local for per-repo init")
}
