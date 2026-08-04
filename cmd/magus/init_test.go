package main

import (
	"context"
	"io"
	"log/slog"
	"os"
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
